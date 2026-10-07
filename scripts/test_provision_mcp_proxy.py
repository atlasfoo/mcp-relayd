"""Entrypoint contract tests; no downloads or runtime credentials required."""

from contextlib import redirect_stderr, redirect_stdout
import io
import os
from pathlib import Path
import runpy
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch


MAIN = runpy.run_path(str(Path(__file__).with_name("provision-mcp-proxy.py")))["main"]


class ProvisionTests(unittest.TestCase):
    def invoke(self, args):
        with patch.object(sys, "argv", ["provision-mcp-proxy.py", *args]), \
                patch.object(sys, "version_info", (3, 13)), \
                redirect_stdout(io.StringIO()):
            MAIN()

    def test_locked_install_and_actions_exports(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path_file, env_file = root / "path", root / "env"
            target = root / "bridge"
            with patch.dict(os.environ, {
                "GITHUB_PATH": str(path_file), "GITHUB_ENV": str(env_file),
            }), patch.object(subprocess, "run") as run, \
                    patch.object(subprocess, "check_output", return_value="mcp-proxy 0.12.0\n"):
                self.invoke(["--venv", str(target), "--github-actions"])
            commands = [call.args[0] for call in run.call_args_list]
            self.assertEqual(len(commands), 3)
            self.assertIn("--clear", commands[0])
            self.assertIn("--no-python-downloads", commands[0])
            self.assertIn("--require-hashes", commands[1])
            self.assertIn("--only-binary", commands[1])
            self.assertTrue(commands[1][-1].endswith("requirements.lock"))
            self.assertIn("check", commands[2])
            bindir = target / ("Scripts" if os.name == "nt" else "bin")
            proxy = bindir / ("mcp-proxy.exe" if os.name == "nt" else "mcp-proxy")
            self.assertEqual(path_file.read_text(), f"{bindir}\n")
            self.assertEqual(env_file.read_text(), f"MCP_PROXY_TEST_COMMAND={proxy}\n")

    def test_failed_install_does_not_publish_actions_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path_file, env_file = root / "path", root / "env"
            with patch.dict(os.environ, {
                "GITHUB_PATH": str(path_file), "GITHUB_ENV": str(env_file),
            }), patch.object(subprocess, "run", side_effect=[
                None, subprocess.CalledProcessError(1, "uv"),
            ]), self.assertRaises(subprocess.CalledProcessError):
                self.invoke(["--venv", str(root / "bridge"), "--github-actions"])
            self.assertFalse(path_file.exists())
            self.assertFalse(env_file.exists())

    def test_missing_actions_context_fails_before_provisioning(self):
        with patch.dict(os.environ, {}, clear=True), \
                patch.object(subprocess, "run") as run, \
                redirect_stderr(io.StringIO()), self.assertRaises(SystemExit):
            self.invoke(["--github-actions"])
        run.assert_not_called()


if __name__ == "__main__":
    unittest.main()
