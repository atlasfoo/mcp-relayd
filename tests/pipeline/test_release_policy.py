"""Direct contracts for the stdlib helpers to be extracted into tooling/release.py.

Minimal API: semver_key(str) -> comparable key; latest_tag(iterable[str]) ->
str; validate_commitizen_config(dict) -> version str; clean_environment(dict)
-> new dict; require_metadata_match(expected, actual, keys) -> None.
Invalid policy inputs raise ValueError. No Git repositories or CLI dispatch here.
"""

import copy
import importlib.util
import os
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[2]
STANDARD = {
    "name": "cz_conventional_commits",
    "version_provider": "commitizen",
    "version_scheme": "semver2",
    "tag_format": "v$version",
    "major_version_zero": True,
    "update_changelog_on_bump": True,
    "changelog_file": "CHANGELOG.md",
    "bump_message": "chore(release): bump version $current_version → $new_version [skip ci]",
    "version": "0.1.0",
}


class ReleasePolicyTests(unittest.TestCase):
    def setUp(self):
        path = ROOT / "tooling/release.py"
        self.assertTrue(path.is_file(), "shared release implementation has not been extracted")
        spec = importlib.util.spec_from_file_location("release_policy_under_test", path)
        self.release = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.release)

    def test_semver_precedence_and_unbounded_numeric_components(self):
        versions = [
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta",
            "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11",
            "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0",
            "999999999999999999999999999999.0.0",
        ]
        for before, after in zip(versions, versions[1:]):
            with self.subTest(before=before, after=after):
                self.assertLess(self.release.semver_key(before), self.release.semver_key(after))

    def test_build_metadata_does_not_change_precedence(self):
        for version in ("0.0.0", "1.2.3-rc.1", "1.2.3-0", "1.2.3-01a"):
            with self.subTest(version=version):
                self.assertEqual(
                    self.release.semver_key(version),
                    self.release.semver_key(version + "+build.001"),
                )

    def test_invalid_candidate_version_fails_closed(self):
        invalid = [
            "", "v1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03",
            "1.2.3-01", "1.2.3-rc.00", "1.2.3-", "1.2.3-a..b", "1.2.3+",
            "1.2.3+a..b", "1.2.3\n", " 1.2.3", "1.2.3-β", "1.2.3-١",
        ]
        for value in invalid:
            with self.subTest(value=value), self.assertRaises(ValueError):
                self.release.semver_key(value)

    def test_invalid_historical_tags_are_ignored_not_candidate_errors(self):
        invalid = ["release-9.0.0", "9.0.0", "v99.0.0-01", "v01.0.0", "v2.0.0\n"]
        self.assertEqual(self.release.latest_tag(invalid), "")
        self.assertEqual(self.release.latest_tag(iter([
            *invalid, "v1.9.0", "v1.10.0-rc.2", "v1.10.0-rc.11",
        ])), "v1.10.0-rc.11")
        self.assertEqual(self.release.latest_tag(["v1.10.0-rc.11", "v1.10.0"]), "v1.10.0")

    def test_equal_precedence_tags_have_deterministic_lexical_tiebreak(self):
        tags = ["v1.2.3+aaa", "v1.2.3+zzz", "v1.2.3"]
        self.assertEqual(self.release.latest_tag(tags), "v1.2.3+zzz")
        self.assertEqual(self.release.latest_tag(reversed(tags)), "v1.2.3+zzz")

    def test_standard_commitizen_config_returns_valid_version_without_mutation(self):
        config = {"tool": {"commitizen": dict(STANDARD, version="1.2.3-rc.1+001")}}
        before = copy.deepcopy(config)
        self.assertEqual(self.release.validate_commitizen_config(config), "1.2.3-rc.1+001")
        self.assertEqual(config, before)

    def test_commitizen_allowlist_rejects_extensions_and_type_coercion(self):
        configs = []
        for key, value in [
            ("name", "candidate_plugin"), ("version_provider", "scm"),
            ("tag_format", "$version"), ("major_version_zero", 1),
            ("update_changelog_on_bump", 1), ("changelog_file", "../CHANGELOG.md"),
            ("bump_message", "chore: bump $new_version"),
            ("bump_message", "chore(release): bump version $current_version → $new_version"),
            ("bump_message", "chore(release): arbitrary $new_version [skip ci]"),
            ("pre_bump_hooks", ["candidate-hook"]), ("version", "1.2.3-01"),
        ]:
            configs.append({"tool": {"commitizen": dict(STANDARD, **{key: value})}})
        configs.extend([
            {"tool": {"commitizen": dict(STANDARD)}, "extra": {}},
            {"tool": {"commitizen": dict(STANDARD), "plugin": {}}},
            {"tool": {"commitizen": {k: v for k, v in STANDARD.items() if k != "name"}}},
            {},
        ])
        for config in configs:
            with self.subTest(config=config), self.assertRaises(ValueError):
                self.release.validate_commitizen_config(config)

    def test_environment_removes_credentials_and_injected_git_python_settings(self):
        original = {
            "PATH": "/trusted/bin", "HOME": "/fixture/home", "GITHUB_RUN_ID": "42",
            "RELEASE_BOT_NAME": "fixture[bot]", "RELEASE_BOT_EMAIL": "fixture@example.invalid",
            "GH_TOKEN": "fixture-only", "GITHUB_TOKEN": "fixture-only",
            "RELEASE_READ_TOKEN": "fixture-only", "RELEASE_GIT_TOKEN": "fixture-only",
            "GIT_CONFIG_PARAMETERS": "candidate-config", "GIT_CONFIG_COUNT": "100",
            "GIT_CONFIG_KEY_0": "credential.helper", "GIT_CONFIG_VALUE_0": "candidate-helper",
            "GIT_CONFIG_KEY_99": "core.hooksPath", "GIT_CONFIG_VALUE_99": "/candidate/hooks",
            "PYTHONPATH": "/candidate/python", "PYTHONHOME": "/candidate/python",
        }
        before = dict(original)
        ambient = dict(os.environ)
        clean = self.release.clean_environment(original)
        self.assertEqual(original, before)
        self.assertEqual(dict(os.environ), ambient)
        self.assertIsNot(clean, original)
        for key in ("GH_TOKEN", "GITHUB_TOKEN", "RELEASE_READ_TOKEN", "RELEASE_GIT_TOKEN",
                    "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_KEY_99", "GIT_CONFIG_VALUE_99", "PYTHONHOME"):
            self.assertNotIn(key, clean)
        expected = dict(
            PATH="/trusted/bin", HOME="/fixture/home", GITHUB_RUN_ID="42",
            RELEASE_BOT_NAME="fixture[bot]", RELEASE_BOT_EMAIL="fixture@example.invalid",
            GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null", GIT_TERMINAL_PROMPT="0",
            GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="core.hooksPath", GIT_CONFIG_VALUE_0="/dev/null",
            PYTHONPATH="", PYTHONSAFEPATH="1",
        )
        for key, value in expected.items():
            self.assertEqual(clean.get(key), value, key)
        self.assertEqual(self.release.clean_environment(clean), clean)

    def test_metadata_comparison_checks_selected_fields_and_preserves_inputs(self):
        expected = {"checks_run_id": 42, "eligible": True, "source_sha": "a" * 40}
        actual = dict(expected, unrelated="allowed")
        before = copy.deepcopy((expected, actual))
        self.assertIsNone(self.release.require_metadata_match(expected, actual, tuple(expected)))
        self.assertEqual((expected, actual), before)

    def test_metadata_mismatch_missing_fields_and_equal_but_different_types_fail(self):
        for expected, actual in [(1, True), (True, 1), (42, 42.0), (42, "42"), ("a", "b")]:
            with self.subTest(expected=expected, actual=actual), self.assertRaises(ValueError):
                self.release.require_metadata_match({"field": expected}, {"field": actual}, ["field"])
        for expected, actual in [({"field": 42}, {}), ({}, {"field": 42}), ({}, {})]:
            with self.subTest(expected=expected, actual=actual), self.assertRaises(ValueError):
                self.release.require_metadata_match(expected, actual, ["field"])


if __name__ == "__main__":
    unittest.main()
