"""Characterize the release Go module boundary and app/pipeline gates."""

from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[2]
RELEASE = ROOT / "tests" / "release"
RELEASE_MODULE_FILES = ("tests/release/go.mod", "tests/release/go.sum")
TEMP_DIR = "/tmp/opencode" if Path("/tmp/opencode").is_dir() else None


def run(*args, cwd=ROOT):
    return subprocess.run(args, cwd=cwd, text=True, capture_output=True)


def copy_versioned_paths(destination, paths):
    """Copy only tracked paths named by an explicit, non-secret allowlist."""
    for name in paths:
        source = ROOT / name
        target = destination / name
        if source.is_dir():
            tracked = subprocess.run(
                ["git", "ls-files", "--", name], cwd=ROOT, check=True,
                text=True, capture_output=True,
            ).stdout.splitlines()
            for relative in tracked:
                original = ROOT / relative
                copied = destination / relative
                copied.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(original, copied)
        elif source.is_file():
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, target)


class ModuleBoundaryTests(unittest.TestCase):
    def test_root_go_test_and_lint_package_listing_exclude_release(self):
        module = RELEASE / "go.mod"
        self.assertTrue(module.is_file(), "release suite must be a nested Go module")

        packages = run("go", "list", "-test", "./...")
        self.assertEqual(packages.returncode, 0, packages.stderr)
        self.assertNotIn("tests/release", packages.stdout)

        # The root lint gate uses the same ./... package scope as the Go test
        # gate; a nested go.mod makes the release tree invisible to that scope.

    def test_app_check_ignores_failure_sentinel_in_release_suite(self):
        with tempfile.TemporaryDirectory(dir=TEMP_DIR) as temporary:
            copy = Path(temporary) / "repo"
            copy.mkdir()
            copy_versioned_paths(copy, [
                "go.mod", "go.sum", ".golangci.yml", ".typos.toml",
                "cmd", "internal", "examples", *RELEASE_MODULE_FILES,
            ])
            sentinel = copy / "tests" / "release" / "pipeline_boundary_sentinel_test.go"
            sentinel.parent.mkdir(parents=True, exist_ok=True)
            sentinel.write_text(
                "package release\n\nimport \"testing\"\n\n"
                "func TestPipelineBoundarySentinel(t *testing.T) { t.Fatal(\"sentinel\") }\n",
                encoding="utf-8",
            )
            result = run("check", cwd=copy)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_explicit_release_suite_invocation_discovers_sentinel(self):
        with tempfile.TemporaryDirectory(dir=TEMP_DIR) as temporary:
            copy = Path(temporary) / "repo"
            copy.mkdir()
            copy_versioned_paths(copy, ["tests/release", *RELEASE_MODULE_FILES])
            release_copy = copy / "tests" / "release"
            self.assertTrue((release_copy / "go.mod").is_file())
            self.assertTrue((release_copy / "go.sum").is_file())
            sentinel = release_copy / "pipeline_boundary_sentinel_test.go"
            sentinel.write_text(
                "package release\n\nimport \"testing\"\n\n"
                "func TestPipelineBoundarySentinel(t *testing.T) { t.Fatal(\"sentinel\") }\n",
                encoding="utf-8",
            )
            result = run(
                "go", "test", "-count=1", "-run", "^TestPipelineBoundarySentinel$", "./...",
                cwd=release_copy,
            )
            self.assertNotEqual(result.returncode, 0, "explicit suite must execute sentinel")
            output = result.stdout + result.stderr
            self.assertIn("--- FAIL: TestPipelineBoundarySentinel", output)
            self.assertIn("sentinel", output)


if __name__ == "__main__":
    unittest.main()
