"""Behavioral trust-boundary characterization; all credentials are fixture data."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
TOKENS = ("GH_TOKEN", "GITHUB_TOKEN", "RELEASE_READ_TOKEN", "RELEASE_GIT_TOKEN")
TEMP_DIR = "/tmp/opencode" if Path("/tmp/opencode").is_dir() else None


class ReleaseWrapperTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(dir=TEMP_DIR)
        self.addCleanup(self.temporary.cleanup)
        self.base = Path(self.temporary.name)
        self.trusted = self.base / "trusted tooling"
        self.candidate = self.base / "hostile candidate"
        self.bin = self.base / "fixture-bin"
        for directory in (self.trusted, self.candidate, self.bin):
            directory.mkdir()
        shutil.copy2(ROOT / "justfile", self.trusted / "justfile")
        # Before extraction the inline wrapper remains executable. After extraction
        # only the trusted implementation is copied, never candidate artifacts.
        if (ROOT / "tooling/release.py").is_file():
            (self.trusted / "tooling").mkdir()
            shutil.copy2(ROOT / "tooling/release.py", self.trusted / "tooling/release.py")
        self.env = {
            "PATH": str(self.bin) + os.pathsep + os.environ["PATH"],
            "HOME": str(self.base), "LANG": "C.UTF-8",
            "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": "/dev/null",
            "GIT_TERMINAL_PROMPT": "0", "GIT_AUTHOR_NAME": "Fixture",
            "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_NAME": "Fixture",
            "GIT_COMMITTER_EMAIL": "fixture@example.invalid",
        }
        self.git("init", "-q")
        shutil.copy2(ROOT / ".cz.toml", self.candidate / ".cz.toml")
        self.git("add", ".cz.toml")
        self.git("-c", "core.hooksPath=/dev/null", "commit", "-qm", "feat: fixture release")
        self.source = self.git("rev-parse", "HEAD").stdout.strip()
        self.marker = self.base / "candidate-executed"
        self.calls = self.base / "cz-calls.jsonl"
        # This mock is isolated itself, so a marker can only originate in tooling
        # under test, not in the test double's Python startup.
        mock = self.bin / "cz"
        mock.write_text(
            f"#!{sys.executable} -I\n"
            "import json, os, sys\n"
            f"with open({str(self.calls)!r}, 'a') as stream:\n"
            f"    stream.write(json.dumps({{key: os.environ[key] for key in {TOKENS!r} if key in os.environ}}) + '\\n')\n"
            "if '--get-next' in sys.argv: print('0.2.0')\n",
            encoding="utf-8",
        )
        mock.chmod(0o700)

    def git(self, *args):
        return subprocess.run(
            ["git", *args], cwd=self.candidate, env=self.env,
            check=True, capture_output=True, text=True, timeout=20,
        )

    def invoke(self):
        output = self.base / "decision with spaces.json"
        result = subprocess.run(
            ["just", "--justfile", str(self.trusted / "justfile"),
             "--working-directory", str(self.candidate), "release-candidate", str(output)],
            cwd=self.candidate, env=self.env, capture_output=True, text=True, timeout=30,
        )
        self.assertFalse(self.marker.exists(), "candidate-controlled code was executed")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        decision = json.loads(output.read_text(encoding="utf-8"))
        self.assertEqual(decision["source_sha"], self.source, "wrapper lost the candidate cwd")
        self.assertIs(decision["eligible"], True)
        self.assertEqual(decision["version"], "0.2.0")
        calls = [json.loads(line) for line in self.calls.read_text().splitlines()]
        self.assertEqual(len(calls), 2, "both Commitizen policy probes must execute")
        return calls

    def test_trusted_wrapper_ignores_candidate_justfile_modules_path_and_artifacts(self):
        poison = f"open({str(self.marker)!r}, 'a').write('candidate code ran\\n')\n"
        for relative in (
            "release.py", "tooling/release.py", "artifacts/release.py",
            "json.py", "subprocess.py", "pathlib.py", "sitecustomize.py", "usercustomize.py",
        ):
            path = self.candidate / relative
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(poison, encoding="utf-8")
        (self.candidate / "justfile").write_text(
            "release-candidate output:\n    #!/bin/sh\n"
            f"    touch '{self.marker}'\n    exit 93\n", encoding="utf-8",
        )
        shadow = self.bin / "release.py"
        shadow.write_text("#!/bin/sh\n" + f"touch '{self.marker}'\nexit 94\n", encoding="utf-8")
        shadow.chmod(0o700)
        self.env.update(PYTHONPATH=str(self.candidate), PYTHONSAFEPATH="0")
        self.env.update({key: "fixture-only-not-a-real-token" for key in TOKENS})
        self.assertEqual(self.invoke(), [{}, {}], "credentials reached Commitizen")

if __name__ == "__main__":
    unittest.main()
