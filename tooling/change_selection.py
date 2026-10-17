"""Select pipeline inputs from Git without importing code from the source tree."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path, PurePosixPath
import subprocess
import sys


ZERO_SHA = "0" * 40
SHARED_PATHS = {"justfile", ".golangci.yml", ".cz.toml", "devenv.nix", "devenv.yaml", "devenv.lock"}
TEST_SUFFIXES = {".go", ".py", ".sh", ".toml", ".json", ".yaml", ".yml", ".lock", ".mod", ".sum"}


def is_pipeline_path(path: str) -> bool:
    """Keep executable/configuration inputs, not documentation or runtime Go."""
    name = PurePosixPath(path)
    if path in SHARED_PATHS or path.startswith(".github/workflows/"):
        return True
    if path.startswith("tooling/"):
        return name.suffix == ".py"
    if path.startswith(("tests/pipeline/", "tests/release/")):
        return name.suffix in TEST_SUFFIXES or name.name in {"go.mod", "go.sum", "justfile"}
    return False


def git(repo: str, *args: str) -> bytes:
    return subprocess.run(
        ["git", "-C", str(repo), *args], check=True, capture_output=True,
    ).stdout


def changed_groups(data: bytes) -> list[list[str]]:
    """Parse --name-status -z, retaining both endpoints of renames."""
    fields = iter(data.split(b"\0")[:-1])
    groups = []
    for status in fields:
        paths = [os.fsdecode(next(fields))]
        if status.startswith((b"R", b"C")):
            paths.append(os.fsdecode(next(fields)))
        groups.append(paths)
    return groups


def select_changed_paths(repo: str, base: str | None = None, head: str | None = None) -> list[str]:
    if (base is None) != (head is None):
        raise ValueError("BASE and HEAD must be supplied together")
    if base is not None:
        resolved_head = git(repo, "rev-parse", "--verify", "--end-of-options", head + "^{commit}").decode().strip()
        if base == ZERO_SHA:
            # No predecessor exists: every input in the requested tree is relevant.
            groups = [[os.fsdecode(path)] for path in git(repo, "ls-tree", "-r", "--name-only", "-z", resolved_head).split(b"\0") if path]
        else:
            resolved_base = git(repo, "rev-parse", "--verify", "--end-of-options", base + "^{commit}").decode().strip()
            groups = changed_groups(git(repo, "diff", "--name-status", "-z", "--find-renames", resolved_base, resolved_head, "--"))
    else:
        groups = changed_groups(git(repo, "diff", "--name-status", "-z", "--find-renames", "--"))
        groups += changed_groups(git(repo, "diff", "--cached", "--name-status", "-z", "--find-renames", "--"))
        groups += [[os.fsdecode(path)] for path in git(repo, "ls-files", "--others", "--exclude-standard", "-z").split(b"\0") if path]
    return sorted({path for group in groups if any(map(is_pipeline_path, group)) for path in group})


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("base", nargs="?")
    parser.add_argument("head", nargs="?")
    args = parser.parse_args()
    mode = "local (staged, unstaged, untracked)" if args.base is None else f"range {args.base}..{args.head}"
    if args.base == ZERO_SHA:
        mode += " (first push: all pipeline inputs in requested HEAD)"
    print(f"check-changes: {mode}", flush=True)
    try:
        paths = select_changed_paths(".", args.base, args.head)
        for path in paths:
            print(f"pipeline path: {json.dumps(path, ensure_ascii=True)}", flush=True)
        print("pipeline: " + ("run" if paths else "skip (no relevant paths)"), flush=True)
        subprocess.run(["check"], check=True)
        if paths:
            trusted = Path(__file__).resolve().parents[1] / "justfile"
            subprocess.run(["just", "--justfile", str(trusted), "--working-directory", os.getcwd(), "check-pipeline"], check=True)
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"check-changes: {mode}: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
