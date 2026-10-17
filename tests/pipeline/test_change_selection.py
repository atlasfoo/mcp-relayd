"""Behavioral contract for the pipeline change selector.

The selector API exercised here is
``tooling.change_selection.select_changed_paths(repo, base=None, head=None)``.
With neither endpoint it selects the local working tree; with endpoints it
selects the complete range. The all-zero base SHA denotes a conservative
first-push range. Results are sorted repository-relative paths.
"""

from __future__ import annotations

import os
import subprocess
import tempfile
import unittest
from pathlib import Path

from tooling.change_selection import is_pipeline_path, select_changed_paths


ZERO_SHA = "0" * 40
TEMP_DIR = "/tmp/opencode" if Path("/tmp/opencode").is_dir() else None


def run_git(repo: Path, *args: str) -> str:
    result = subprocess.run(
        ["git", "-C", str(repo), *args],
        check=True,
        capture_output=True,
        text=True,
        env={
            **os.environ,
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_TERMINAL_PROMPT": "0",
            "GIT_CONFIG_COUNT": "1",
            "GIT_CONFIG_KEY_0": "core.hooksPath",
            "GIT_CONFIG_VALUE_0": "/dev/null",
        },
    )
    return result.stdout.strip()


class ChangeSelectionTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temp_dir = tempfile.TemporaryDirectory(dir=TEMP_DIR)
        self.repo = Path(self.temp_dir.name)
        run_git(self.repo, "init", "-q")
        run_git(self.repo, "config", "user.name", "Pipeline Test")
        run_git(self.repo, "config", "user.email", "pipeline-test@example.invalid")
        self.write("seed.txt")
        run_git(self.repo, "add", "seed.txt")
        run_git(self.repo, "commit", "-qm", "seed")
        self.base = run_git(self.repo, "rev-parse", "HEAD")
        self.head = self.base

    def tearDown(self) -> None:
        self.temp_dir.cleanup()

    def write(self, path: str, content: str = "content\n") -> None:
        target = self.repo / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content, encoding="utf-8")

    def selected(self, base: str | None = None, head: str | None = None) -> list[str]:
        return select_changed_paths(str(self.repo), base, head)

    def test_runtime_go_and_docs_changes_are_excluded_but_pipeline_paths_included(self) -> None:
        paths = [
            "cmd/daemon/main.go",
            "internal/app/run.go",
            "examples/sample/config.toml",
            "go.mod",
            "go.sum",
            "docs/guide.md",
            "tests/release/README.md",
            ".github/workflows/check.yml",
            "justfile",
            "tooling/change_selection.py",
            "tooling/release.py",
            "tooling/mocks/helper.py",
            "tests/release/test_release.py",
            "tests/release/fixture.toml",
            "tests/pipeline/test_change_selection.py",
            "tests/release/go.mod",
            "tests/release/go.sum",
            "tests/release/.golangci.yml",
            ".cz.toml",
            "devenv.nix",
            "devenv.yaml",
            "devenv.lock",
        ]
        for path in paths:
            self.write(path)
        run_git(self.repo, "add", "--", *paths)

        self.assertEqual(self.selected(), sorted(set(paths) - {
            "cmd/daemon/main.go", "internal/app/run.go", "examples/sample/config.toml",
            "go.mod", "go.sum", "docs/guide.md", "tests/release/README.md",
        }))

    def test_mix_includes_pipeline_and_excludes_runtime(self) -> None:
        for path in ("cmd/daemon/main.go", "tooling/release.py", "docs/readme.md"):
            self.write(path)
        run_git(self.repo, "add", "--", "cmd/daemon/main.go", "tooling/release.py", "docs/readme.md")
        self.assertEqual(self.selected(), ["tooling/release.py"])

    def test_app_and_docs_only_changes_are_excluded(self) -> None:
        for path in ("cmd/daemon/main.go", "internal/app/run.go", "docs/readme.md"):
            self.write(path)
        run_git(self.repo, "add", "--", "cmd/daemon/main.go", "internal/app/run.go", "docs/readme.md")
        self.assertEqual(self.selected(), [])

    def test_commit_before_base_is_not_selected(self) -> None:
        self.write("tooling/old.py")
        run_git(self.repo, "add", "tooling/old.py")
        run_git(self.repo, "commit", "-qm", "before base")
        range_base = run_git(self.repo, "rev-parse", "HEAD")
        self.write("tooling/new.py")
        run_git(self.repo, "add", "tooling/new.py")
        run_git(self.repo, "commit", "-qm", "after base")
        range_head = run_git(self.repo, "rev-parse", "HEAD")
        self.assertEqual(self.selected(range_base, range_head), ["tooling/new.py"])

    def test_explicit_range_includes_first_commit_after_base_in_multicommit_range(self) -> None:
        range_base = self.base
        self.write("tooling/first.py")
        run_git(self.repo, "add", "tooling/first.py")
        run_git(self.repo, "commit", "-qm", "first in range")
        self.write("tooling/last.py")
        run_git(self.repo, "add", "tooling/last.py")
        run_git(self.repo, "commit", "-qm", "last in range")
        range_head = run_git(self.repo, "rev-parse", "HEAD")
        self.assertEqual(self.selected(range_base, range_head), ["tooling/first.py", "tooling/last.py"])

    def test_staged_unstaged_and_untracked_are_included(self) -> None:
        self.write("tooling/tracked.py")
        run_git(self.repo, "add", "tooling/tracked.py")
        run_git(self.repo, "commit", "-qm", "add tracked file")
        self.write("tooling/staged.py")
        run_git(self.repo, "add", "tooling/staged.py")
        self.write("tooling/tracked.py", "modified but unstaged\n")
        self.write("tooling/untracked.py")
        self.assertEqual(self.selected(), [
            "tooling/staged.py", "tooling/tracked.py", "tooling/untracked.py",
        ])

    def test_rename_and_delete_report_both_relevant_paths(self) -> None:
        self.write("tooling/original.py")
        self.write("app/original.py", "distinct rename source\n")
        run_git(self.repo, "add", "tooling/original.py", "app/original.py")
        run_git(self.repo, "commit", "-qm", "add rename sources")
        range_base = run_git(self.repo, "rev-parse", "HEAD")
        self.write("tooling/renamed.py", "distinct rename source\n")
        run_git(self.repo, "rm", "tooling/original.py", "app/original.py")
        run_git(self.repo, "add", "tooling/renamed.py")
        run_git(self.repo, "commit", "-qm", "rename across selector boundary")
        range_head = run_git(self.repo, "rev-parse", "HEAD")
        self.assertEqual(
            self.selected(range_base, range_head),
            ["app/original.py", "tooling/original.py", "tooling/renamed.py"],
        )

    def test_paths_with_spaces_are_preserved(self) -> None:
        self.write("tooling/a file with spaces.py")
        run_git(self.repo, "add", "--", "tooling/a file with spaces.py")
        self.assertEqual(self.selected(), ["tooling/a file with spaces.py"])

    def test_first_push_null_sha_conservatively_selects_pipeline_paths_only(self) -> None:
        self.write("tooling/new.py")
        self.write("cmd/new.go")
        run_git(self.repo, "add", "tooling/new.py", "cmd/new.go")
        run_git(self.repo, "commit", "-qm", "first push tree")
        self.head = run_git(self.repo, "rev-parse", "HEAD")
        self.assertEqual(self.selected(ZERO_SHA, self.head), ["tooling/new.py"])

    def test_invalid_base_fails_instead_of_returning_empty_selection(self) -> None:
        with self.assertRaises((ValueError, RuntimeError, subprocess.CalledProcessError)):
            select_changed_paths(str(self.repo), "not-a-commit", self.head)


class PipelineFilterYamlTests(unittest.TestCase):
    """Cross-check the selector against the workflow's deliberately simple lists."""

    def test_selector_path_table_matches_workflow_filters(self) -> None:
        import fnmatch
        import re

        # This is a bounded extractor, not a general YAML parser: require literal
        # single-quoted list entries and reject unsupported glob syntax. With
        # only ** wildcards, fnmatch has Actions' slash-crossing semantics.
        workflow = Path(__file__).resolve().parents[2] / ".github/workflows/pipeline-checks.yml"
        text = workflow.read_text(encoding="utf-8")
        filters = []
        for event in ("pull_request", "push"):
            section = re.search(r"(?ms)^  " + event + r":\n(.*?)(?=^  \w|^\S)", text)
            self.assertIsNotNone(section, event)
            paths = re.search(r"(?m)^    paths:\n((?:      - [^\n]+\n)+)", section[1])
            self.assertIsNotNone(paths, event)
            patterns = []
            for line in paths[1].splitlines():
                entry = re.fullmatch(r"      - '([^']+)'", line)
                self.assertIsNotNone(entry, line)
                pattern = entry[1]
                self.assertNotRegex(pattern.replace("**", ""), r"[*!?\[\]{}]", pattern)
                patterns.append(pattern)
            filters.append(patterns)
        self.assertEqual(filters[0], filters[1], "PR/push policy must be identical")

        cases = {
            "justfile": True, ".golangci.yml": True, ".cz.toml": True,
            "devenv.nix": True, "devenv.yaml": True, "devenv.lock": True,
            ".github/workflows/checks.yml": True,
            ".github/workflows/nested/anything.txt": True,
            "tooling/release.py": True, "tooling/nested/a file.py": True,
            "tooling/README.md": False, "tooling/script.sh": False,
            "cmd/daemon/main.go": False, "internal/app/run.go": False,
            "examples/config.toml": False, "go.mod": False, "go.sum": False,
            "README.md": False, "docs/guide.md": False,
        }
        for directory in ("tests/pipeline", "tests/release"):
            for prefix in (directory, directory + "/nested"):
                for suffix in ("go", "py", "sh", "toml", "json", "yaml", "yml", "lock", "mod", "sum"):
                    cases[f"{prefix}/input.{suffix}"] = True
                for name in ("go.mod", "go.sum", "justfile"):
                    cases[f"{prefix}/{name}"] = True
                for name in ("README.md", "guide.txt", "notjustfile", "input.py.bak"):
                    cases[f"{prefix}/{name}"] = False
        for path, expected in cases.items():
            with self.subTest(path=path):
                self.assertEqual(is_pipeline_path(path), expected, "selector policy")
                for patterns in filters:
                    self.assertEqual(any(fnmatch.fnmatchcase(path, pattern) for pattern in patterns), expected, "workflow policy")

        for changed, expected in ((["cmd/main.go", "README.md"], False),
                                  (["tests/release/README.md"], False),
                                  (["internal/app.go", "tooling/release.py"], True)):
            for patterns in filters:
                self.assertEqual(any(fnmatch.fnmatchcase(path, pattern) for path in changed for pattern in patterns), expected)


if __name__ == "__main__":
    unittest.main()
