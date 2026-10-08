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
