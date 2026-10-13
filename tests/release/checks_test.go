//go:build linux

package release_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestChecksSkipCIRequiresStructuralOwnBump(t *testing.T) {
	for _, scenario := range []string{"canonical", "forged-subject", "body-only-marker", "wrong-message", "wrong-author", "wrong-committer", "merge-parent", "extra-diff", "missing-changelog", "unchanged-version", "decreased-version", "message-version-mismatch", "config-change", "legacy-template", "pr", "foreign-repository", "topic-push"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			template := canonicalBumpTemplate
			if scenario == "legacy-template" {
				template = strings.TrimSuffix(template, " [skip ci]")
			}
			// Prepare the approved template in the isolated repo only. This lets
			// recognition regressions run independently of the root config red test.
			setBumpTemplate(t, f, template)
			f.git(t, "add", ".cz.toml")
			f.git(t, "commit", "--allow-empty", "-m", "chore: configure release message")
			f.git(t, "tag", "v0.1.0")
			f.git(t, "commit", "--allow-empty", "-m", "feat: add relay")
			base := f.git(t, "rev-parse", "HEAD")
			f.git(t, "config", "user.name", publicationBotName)
			f.git(t, "config", "user.email", publicationBotEmail)
			f.env = append(f.env, "RELEASE_BOT_NAME="+publicationBotName, "RELEASE_BOT_EMAIL="+publicationBotEmail)
			if output, err := f.command(t, "cz", "--config", ".cz.toml", "bump", "--yes"); err != nil {
				t.Fatalf("generate canonical fixture bump: %v: %s", err, output)
			}
			message := f.git(t, "show", "-s", "--format=%B", "HEAD")
			switch scenario {
			case "forged-subject":
				message = "docs: example bump [skip ci]"
			case "body-only-marker":
				message = "chore(release): bump version 0.1.0 → 0.2.0\n\n[skip ci]"
			case "wrong-message":
				message += " extra text"
			case "wrong-author":
				f.git(t, "commit", "--amend", "--no-edit", "--author", "Contributor <contributor@example.invalid>")
			case "wrong-committer":
				f.git(t, "config", "user.name", "Contributor")
				f.git(t, "config", "user.email", "contributor@example.invalid")
			case "merge-parent":
				tree := f.git(t, "rev-parse", "HEAD^{tree}")
				other := f.git(t, "commit-tree", tree, "-p", base, "-m", "docs: other parent")
				merge := f.git(t, "commit-tree", tree, "-p", base, "-p", other, "-m", message)
				f.git(t, "reset", "--hard", merge)
			case "extra-diff":
				write(t, filepath.Join(f.dir, "runtime.go"), "package runtime\n", 0o600)
			case "missing-changelog":
				f.git(t, "rm", "CHANGELOG.md")
			case "unchanged-version", "decreased-version", "message-version-mismatch":
				version := "0.1.0"
				if scenario == "decreased-version" {
					version = "0.0.9"
				}
				if scenario == "message-version-mismatch" {
					version = "0.3.0"
				}
				path := filepath.Join(f.dir, ".cz.toml")
				write(t, path, strings.ReplaceAll(read(t, path), `version = "0.2.0"`, `version = "`+version+`"`), 0o600)
				if scenario != "message-version-mismatch" {
					message = "chore(release): bump version 0.1.0 → " + version + " [skip ci]"
				}
			case "config-change":
				path := filepath.Join(f.dir, ".cz.toml")
				write(t, path, strings.ReplaceAll(read(t, path), "major_version_zero = true", "major_version_zero = false"), 0o600)
			}
			f.git(t, "add", ".")
			f.git(t, "commit", "--amend", "-m", message)
			head := f.git(t, "rev-parse", "HEAD")
			var recognition struct {
				Own bool `json:"own_bump"`
			}
			f.recipe(t, &recognition, "release-own-bump", head)
			structural := scenario == "canonical" || scenario == "pr" || scenario == "foreign-repository" || scenario == "topic-push"
			if recognition.Own != structural {
				t.Errorf("own_bump = %v, want %v", recognition.Own, structural)
			}
			event, repository, branch, title := "push", "owner/mcp-relayd", "master", ""
			switch scenario {
			case "pr":
				event, title = "pull_request", "docs: normal contributor PR"
			case "foreign-repository":
				repository = "contributor/fork"
			case "topic-push":
				branch = "topic"
			}
			f.env = append(f.env, "CHECKS_EVENT="+event, "CHECKS_SOURCE_SHA="+head, "CHECKS_BASE_SHA="+base, "CHECKS_PR_TITLE="+title, "CHECKS_HEAD_REPOSITORY="+repository, "CHECKS_HEAD_BRANCH="+branch, "GITHUB_REPOSITORY=owner/mcp-relayd", "GITHUB_RUN_ID=101")
			calls := spyChecksGate(t, &f)
			output, err := f.command(t, "just", "ci-checks")
			if err != nil {
				t.Fatalf("Checks should skip safely or validate normally: %v: %s", err, output)
			}
			wantCommands := [][]string{{"go", "test", "./..."}, {"golangci-lint", "fmt", "--diff", "./..."}, {"golangci-lint", "run", "./..."}, {"typos", "--force-exclude", "."}, {"go", "vet", "./..."}, {"check"}, {"go", "test", "./internal/process", "./internal/gateway", "./internal/integration", "-count=1"}, {"actionlint", "-shellcheck=", "-pyflakes="}}
			if scenario == "canonical" {
				wantCommands = nil
			}
			var commands [][]string
			for line := range strings.SplitSeq(strings.TrimSpace(read(t, calls)), "\n") {
				if line == "" {
					continue
				}
				var command []string
				decode(t, line, &command)
				commands = append(commands, command)
			}
			if !reflect.DeepEqual(commands, wantCommands) {
				t.Errorf("full Checks gate calls = %d, want %d: %v", len(commands), len(wantCommands), commands)
			}
			// A marker is not provenance. Native GitHub event suppression and
			// required-check Pending/App bypass need authorized hosted validation.
		})
	}
}

func spyChecksGate(t *testing.T, f *fixture) string {
	t.Helper()
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls.jsonl")
	write(t, calls, "", 0o600)
	f.env = append(f.env, "CHECKS_TEST_CALLS="+calls)
	for _, tool := range []string{"go", "golangci-lint", "typos", "check", "actionlint"} {
		write(t, filepath.Join(bin, tool), `#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
with open(os.environ["CHECKS_TEST_CALLS"], "a") as output:
    output.write(json.dumps([Path(sys.argv[0]).name, *sys.argv[1:]]) + "\n")
`, 0o700)
	}
	for i, value := range f.env {
		if path, ok := strings.CutPrefix(value, "PATH="); ok {
			f.env[i] = "PATH=" + bin + string(os.PathListSeparator) + path
		}
	}
	return calls
}

func TestChecksOrchestration(t *testing.T) {
	for _, scenario := range []string{"pr", "push", "first-push", "legacy-range", "bad-title", "ignored-title", "missing-title", "bad-commit", "failed-gate", "wrong-source", "missing-base"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			// An orphan history models a real pre-tooling commit, not a mock cz.
			f.git(t, "checkout", "--orphan", "legacy")
			f.git(t, "rm", "--cached", ".cz.toml", "justfile")
			f.git(t, "commit", "--allow-empty", "-m", "historical invalid subject")
			legacy := f.git(t, "rev-parse", "HEAD")
			f.git(t, "add", ".")
			f.git(t, "commit", "-m", "chore: introduce tooling")
			base := f.git(t, "rev-parse", "HEAD")
			subject := "docs: explain usage"
			if scenario == "bad-commit" {
				subject = "invalid new subject"
			}
			f.git(t, "commit", "--allow-empty", "-m", subject)
			head := f.git(t, "rev-parse", "HEAD")
			source := head
			event, title := "pull_request", "docs: literal $(touch injected); `touch injected`"
			if scenario == "push" || scenario == "first-push" {
				event, title = "push", ""
			}
			switch scenario {
			case "first-push":
				base = strings.Repeat("0", 40)
			case "legacy-range":
				base = legacy
			case "bad-title":
				title = "unstructured title"
			case "ignored-title":
				title = "Merge something"
			case "missing-title":
				title = ""
			case "wrong-source":
				source = base
			case "missing-base":
				base = strings.Repeat("f", 40)
			}
			f.env = append(f.env, "CHECKS_EVENT="+event, "CHECKS_SOURCE_SHA="+source, "CHECKS_BASE_SHA="+base, "CHECKS_PR_TITLE="+title, "CHECKS_HEAD_REPOSITORY=contributor/fork", "CHECKS_HEAD_BRANCH=topic", "GITHUB_REPOSITORY=owner/mcp-relayd", "GITHUB_RUN_ID=101")
			bin := t.TempDir()
			calls := filepath.Join(t.TempDir(), "calls.jsonl")
			f.env = append(f.env, "CHECKS_TEST_CALLS="+calls)
			if scenario == "failed-gate" {
				f.env = append(f.env, "CHECKS_TEST_FAIL=1")
			}
			// Only gate tools are doubled: real Git, just, Python and cz still run.
			for _, tool := range []string{"go", "golangci-lint", "typos", "check", "actionlint"} {
				write(t, filepath.Join(bin, tool), `#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
with open(os.environ["CHECKS_TEST_CALLS"], "a") as output:
    output.write(json.dumps([Path(sys.argv[0]).name, *sys.argv[1:]]) + "\n")
sys.exit(1 if os.environ.get("CHECKS_TEST_FAIL") else 0)
`, 0o700)
			}
			for i, value := range f.env {
				if path, ok := strings.CutPrefix(value, "PATH="); ok {
					f.env[i] = "PATH=" + bin + string(os.PathListSeparator) + path
				}
			}
			metadataPath := filepath.Join(f.dir, "checks-metadata.json")
			write(t, metadataPath, "stale metadata", 0o600)
			output, err := f.command(t, "just", "ci-checks")
			valid := scenario == "pr" || scenario == "push" || scenario == "first-push" || scenario == "legacy-range"
			if !valid {
				if err == nil {
					t.Fatalf("invalid Checks accepted: %s", output)
				}
				if _, statErr := os.Stat(metadataPath); !os.IsNotExist(statErr) {
					t.Fatal("failed Checks left success metadata")
				}
				return
			}
			if err != nil {
				t.Fatalf("Checks: %v: %s", err, output)
			}
			var got map[string]any
			decode(t, read(t, metadataPath), &got)
			want := map[string]any{"source_sha": head, "repository": "owner/mcp-relayd", "head_repository": "contributor/fork", "head_branch": "topic", "event": event, "checks_run_id": float64(101)}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("metadata = %v, want %v", got, want)
			}
			var commands [][]string
			for line := range strings.SplitSeq(strings.TrimSpace(read(t, calls)), "\n") {
				var command []string
				if err := json.Unmarshal([]byte(line), &command); err != nil {
					t.Fatal(err)
				}
				commands = append(commands, command)
			}
			wantCommands := [][]string{{"go", "test", "./..."}, {"golangci-lint", "fmt", "--diff", "./..."}, {"golangci-lint", "run", "./..."}, {"typos", "--force-exclude", "."}, {"go", "vet", "./..."}, {"check"}, {"go", "test", "./internal/process", "./internal/gateway", "./internal/integration", "-count=1"}, {"actionlint", "-shellcheck=", "-pyflakes="}}
			if !reflect.DeepEqual(commands, wantCommands) {
				t.Errorf("serial gate commands = %v", commands)
			}
			if _, err := os.Stat(filepath.Join(f.dir, "injected")); !os.IsNotExist(err) {
				t.Fatal("title executed as shell code")
			}
		})
	}
}

func TestChecksWorkflowMetadata(t *testing.T) {
	workflow := loadWorkflow(t, "checks")
	var checkout, metadata bool
	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/checkout@") {
				checkout = step.With["ref"] == "${{ github.event.pull_request.head.sha || github.sha }}" && step.With["persist-credentials"] == "false" && step.With["fetch-depth"] == "0"
			}
			if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
				metadata = step.With["name"] == "checks-metadata" && step.With["path"] == "checks-metadata.json" && step.With["if-no-files-found"] == "error"
			}
			if strings.Contains(step.Run, "${{") || strings.Contains(step.Run, "cz bump") {
				t.Error("Checks shell must not interpolate workflow data or gate version eligibility")
			}
		}
	}
	if !checkout || !metadata {
		t.Fatal("missing exact source checkout or success-only provenance artifact")
	}
}

func TestChecksCommitsRealMergeAndEmptyRange(t *testing.T) {
	f := newFixture(t)
	base := f.git(t, "rev-parse", "HEAD")
	f.git(t, "checkout", "-b", "topic")
	f.git(t, "commit", "--allow-empty", "-m", "docs: explain topic")
	f.git(t, "checkout", "master")
	f.git(t, "commit", "--allow-empty", "-m", "chore: update main")
	f.git(t, "merge", "--no-ff", "topic", "-m", "Merge pull request from topic")
	head := f.git(t, "rev-parse", "HEAD")
	for _, start := range []string{base, head} {
		output, err := f.invoke(t, "ci-commits", start, head, "")
		if err != nil {
			t.Fatalf("valid merge/empty range rejected: %v: %s", err, output)
		}
	}
}
