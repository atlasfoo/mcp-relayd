#!/usr/bin/env python3
"""Provision the same isolated, hash-locked bridge for devenv and CI."""

import argparse
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parent.parent
LOCK = ROOT / "tooling" / "mcp-proxy" / "requirements.lock"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--venv", type=Path, default=ROOT / ".devenv" / "mcp-proxy",
        help="dedicated environment to create/recreate (do not use a shared venv)",
    )
    parser.add_argument(
        "--github-actions", action="store_true",
        help="publish PATH and MCP_PROXY_TEST_COMMAND for subsequent Actions steps",
    )
    args = parser.parse_args()
    if sys.version_info[:2] != (3, 13):
        parser.error("run this script with Python 3.13 (devenv supplies it)")
    if args.github_actions and not all(
        os.environ.get(name) for name in ("GITHUB_PATH", "GITHUB_ENV")
    ):
        parser.error("--github-actions requires GITHUB_PATH and GITHUB_ENV")

    target = args.venv.resolve()
    # Recreate rather than trust a stale environment, changed interpreter, or
    # lock stamp. uv's artifact cache makes repeat shell entry inexpensive.
    subprocess.run([
        "uv", "--no-config", "--no-python-downloads", "venv",
        "--python", sys.executable, "--clear", str(target),
    ], check=True)
    bindir = target / ("Scripts" if os.name == "nt" else "bin")
    python = bindir / ("python.exe" if os.name == "nt" else "python")
    proxy = bindir / ("mcp-proxy.exe" if os.name == "nt" else "mcp-proxy")
    subprocess.run([
        "uv", "--no-config", "--no-python-downloads", "pip", "sync",
        "--python", str(python), "--require-hashes", "--only-binary", ":all:",
        "--index-url", "https://pypi.org/simple", str(LOCK),
    ], check=True)
    subprocess.run([
        "uv", "--no-config", "pip", "check", "--python", str(python),
    ], check=True)
    version = subprocess.check_output([str(proxy), "--version"], text=True).strip()
    if version != "mcp-proxy 0.12.0":
        raise RuntimeError(f"unexpected bridge version: {version}")

    if args.github_actions:
        with open(os.environ["GITHUB_PATH"], "a", encoding="utf-8") as output:
            output.write(f"{bindir}\n")
        with open(os.environ["GITHUB_ENV"], "a", encoding="utf-8") as output:
            output.write(f"MCP_PROXY_TEST_COMMAND={proxy}\n")
    print(f"{version} ready: {proxy}")


if __name__ == "__main__":
    main()
