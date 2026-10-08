//go:build linux

package release_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	dir string
	env []string
}

type candidate struct {
	Eligible bool   `json:"eligible"`
	Source   string `json:"source_sha"`
	LastTag  string `json:"last_tag"`
	Version  string `json:"version"`
	Reason   string `json:"reason"`
}

func TestReleaseCandidateHistory(t *testing.T) {
	for _, tc := range []struct {
		name     string
		tag      bool
		messages []string
		version  string
	}{
		{"feature", true, []string{"feat: add relay"}, "0.2.0"},
		{"scoped-fix", true, []string{"fix(http): close stream"}, "0.1.1"},
		{"breaking-feature", true, []string{"feat(api)!: change routing"}, "0.2.0"},
		{"breaking-fix", true, []string{"fix!: change routing"}, "0.2.0"},
		{"first-feature", false, []string{"feat: add relay"}, "0.2.0"},
		{"first-fix", false, []string{"fix: close stream"}, "0.1.1"},
		{"docs-and-tooling", true, []string{"docs: explain usage", "chore: update tooling"}, ""},
		{"first-docs", false, []string{"docs: explain usage"}, ""},
		{"other-breaking-type", true, []string{"refactor!: change routing\n\nBREAKING CHANGE: new interface"}, ""},
		{"performance", true, []string{"perf: speed up routing"}, ""},
		{"merge-subject", true, []string{"Merge branch 'feat: add relay'"}, ""},
		{"body-only-feature", true, []string{"docs: explain usage\n\nfeat: example subject"}, ""},
		{"lookalike-type", true, []string{"feature: add relay", "fixup: close stream"}, ""},
		{"published-feature-only", true, []string{"docs: explain usage"}, ""},
		{"empty-range", true, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			// A published feature must not make subsequent documentation eligible.
			f.git(t, "commit", "--allow-empty", "-m", "feat: published baseline")
			if tc.tag {
				f.git(t, "tag", "v0.1.0")
			} else {
				// Keep an untagged baseline without an old feature for bootstrap tests.
				f.git(t, "reset", "--hard", "HEAD~1")
			}
			for _, message := range tc.messages {
				f.git(t, "commit", "--allow-empty", "-m", message)
			}
			want := candidate{Eligible: tc.version != "", Source: f.git(t, "rev-parse", "HEAD"), Version: tc.version, Reason: "no-feat-fix"}
			if tc.tag {
				want.LastTag = "v0.1.0"
			}
			if want.Eligible {
				want.Reason = "eligible"
			}
			var got candidate
			f.recipe(t, &got, "release-candidate")
			if got != want {
				t.Errorf("candidate = %+v, want %+v", got, want)
			}
		})
	}
}

func TestReleaseCandidateCommitizenContract(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dryCode  int
		next     string
		nextCode int
		reason   string
	}{
		{"success", 0, "0.2.0", 0, "eligible"},
		{"no-increment", 21, "0.2.0", 0, "no-increment"},
		{"unchanged-version", 0, "0.1.0", 0, "no-increment"},
		{"decreased-version", 0, "0.0.9", 0, "error"},
		{"configuration-error", 1, "0.2.0", 0, "error"},
		{"missing-version", 3, "0.2.0", 0, "error"},
		{"invalid-commit", 14, "0.2.0", 0, "error"},
		{"unknown-error", 99, "0.2.0", 0, "error"},
		{"get-next-error", 0, "0.2.0", 1, "error"},
		{"malformed-version", 0, "version: 0.2.0", 0, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.git(t, "tag", "v0.1.0")
			f.git(t, "commit", "--allow-empty", "-m", "feat: add relay")
			log := f.mockCZ(t, tc.dryCode, tc.next, tc.nextCode)
			output, err := f.invoke(t, "release-candidate")
			if tc.reason == "error" {
				if err == nil {
					t.Fatalf("real Commitizen error was hidden: %s", output)
				}
			} else {
				if err != nil {
					t.Fatalf("recipe: %v: %s", err, output)
				}
				var got candidate
				decode(t, output, &got)
				if got.Reason != tc.reason || got.Eligible != (tc.reason == "eligible") {
					t.Errorf("candidate = %+v, want %s", got, tc.reason)
				}
				if got.Eligible && got.Version != tc.next {
					t.Errorf("version = %q, want structured get-next %q", got.Version, tc.next)
				}
			}
			calls := read(t, log)
			if !strings.Contains(calls, "--dry-run") || !strings.Contains(calls, "--yes") {
				t.Errorf("missing explicit noninteractive dry-run: %s", calls)
			}
			if tc.dryCode == 0 && !strings.Contains(calls, "--get-next") {
				t.Errorf("missing structured get-next: %s", calls)
			}
		})
	}
}

func TestReleaseCandidateBootstrapArguments(t *testing.T) {
	f := newFixture(t)
	f.git(t, "commit", "--allow-empty", "-m", "fix: close stream")
	log := f.mockCZ(t, 0, "0.1.1", 0)
	var got candidate
	f.recipe(t, &got, "release-candidate")
	if !got.Eligible || got.LastTag != "" || got.Version != "0.1.1" {
		t.Errorf("bootstrap candidate = %+v", got)
	}
	for line := range strings.SplitSeq(strings.TrimSpace(read(t, log)), "\n") {
		if !strings.Contains(line, "--yes") {
			t.Errorf("bootstrap call could prompt: %s", line)
		}
	}
}

func TestReleaseCandidateSkipsCommitizenWithoutFeature(t *testing.T) {
	f := newFixture(t)
	f.git(t, "commit", "--allow-empty", "-m", "feat: already published")
	f.git(t, "tag", "v0.1.0")
	f.git(t, "commit", "--allow-empty", "-m", "docs: explain usage")
	log := f.mockCZ(t, 99, "invalid", 99)
	var got candidate
	f.recipe(t, &got, "release-candidate")
	if got.Eligible || got.Reason != "no-feat-fix" || read(t, log) != "" {
		t.Errorf("non-version changes must not need working Commitizen: %+v, calls %q", got, read(t, log))
	}
}

func TestReleasePreflightStaleCandidates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tag    bool
		reason string
	}{
		{"current", true, "current"},
		{"stale-source", true, "stale-source"},
		{"stale-tag", true, "stale-tag"},
		{"current-bootstrap", false, "current"},
		{"stale-first-tag", false, "stale-tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			lastTag := ""
			if tc.tag {
				lastTag = "v0.1.0"
				f.git(t, "tag", lastTag)
			}
			f.git(t, "commit", "--allow-empty", "-m", "feat: add relay")
			source := f.git(t, "rev-parse", "HEAD")
			f.git(t, "update-ref", "refs/remotes/origin/master", source)
			if tc.reason == "stale-source" {
				f.git(t, "commit", "--allow-empty", "-m", "fix: newer change")
				f.git(t, "update-ref", "refs/remotes/origin/master", f.git(t, "rev-parse", "HEAD"))
				f.git(t, "checkout", "--detach", source)
			}
			if tc.reason == "stale-tag" {
				f.git(t, "tag", "v0.2.0")
			}
			log := f.mockCZ(t, 99, "invalid", 99)
			var got struct {
				Publish bool   `json:"publish"`
				Reason  string `json:"reason"`
			}
			f.recipe(t, &got, "release-preflight", source, lastTag)
			if got.Publish != (tc.reason == "current") || got.Reason != tc.reason {
				t.Errorf("preflight = %+v, want %s", got, tc.reason)
			}
			if calls := read(t, log); calls != "" {
				t.Errorf("preflight must not invoke Commitizen: %s", calls)
			}
		})
	}
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{dir: t.TempDir()}
	home := t.TempDir()
	// Deliberately do not inherit App, GitHub or other credential variables.
	f.env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "XDG_CONFIG_HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C.UTF-8"}
	for _, name := range []string{".cz.toml", "justfile"} {
		data, err := os.ReadFile(filepath.Join("../..", name)) // #nosec G304 -- Names are fixed repository tooling files, not user input.
		if err != nil {
			t.Fatalf("release contract requires root %s: %v", name, err)
		}
		write(t, filepath.Join(f.dir, name), string(data), 0o600)
	}
	f.git(t, "init", "--initial-branch=master")
	f.git(t, "config", "user.name", "Release Test")
	f.git(t, "config", "user.email", "release-test@example.invalid")
	f.git(t, "config", "core.hooksPath", "/dev/null")
	f.git(t, "add", ".")
	f.git(t, "commit", "-m", "chore: initialize fixture")
	return f
}

func (f fixture) command(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- Executables and arguments are controlled tooling fixtures, never user input.
	cmd.Dir, cmd.Env = f.dir, f.env
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func (f fixture) git(t *testing.T, args ...string) string {
	t.Helper()
	output, err := f.command(t, "git", args...)
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return output
}

func (f fixture) snapshot(t *testing.T) []string {
	t.Helper()
	return []string{f.git(t, "rev-parse", "HEAD"), f.git(t, "show-ref"), f.git(t, "status", "--porcelain"), f.git(t, "diff", "HEAD"), f.git(t, "log", "--format=%H"), f.git(t, "remote", "-v")}
}

func (f fixture) invoke(t *testing.T, recipe string, args ...string) (string, error) {
	t.Helper()
	before := f.snapshot(t)
	defer func() {
		if after := f.snapshot(t); !reflect.DeepEqual(before, after) {
			t.Errorf("read-only recipe mutated repository: before %q, after %q", before, after)
		}
	}()
	outputPath := filepath.Join(t.TempDir(), "decision.json")
	commandArgs := append([]string{"--justfile", filepath.Join(f.dir, "justfile"), recipe}, args...)
	commandArgs = append(commandArgs, outputPath)
	logs, err := f.command(t, "just", commandArgs...)
	if err != nil {
		return logs, err
	}
	return read(t, outputPath), nil
}

func (f fixture) recipe(t *testing.T, result any, name string, args ...string) {
	t.Helper()
	output, err := f.invoke(t, name, args...)
	if err != nil {
		t.Fatalf("%s: %v: %s", name, err, output)
	}
	decode(t, output, result)
}

func decode(t *testing.T, output string, result any) {
	t.Helper()
	if err := json.Unmarshal([]byte(output), result); err != nil {
		t.Fatalf("invalid decision JSON %q: %v", output, err)
	}
}

func (f *fixture) mockCZ(t *testing.T, dryCode int, next string, nextCode int) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	write(t, log, "", 0o600)
	script := fmt.Sprintf(`#!/usr/bin/env bash
set -eu
printf '%%s\n' "$*" >> %q
if [[ "${1:-}" == --config ]]; then shift 2; fi
[[ "${1:-}" == bump ]] || exit 90
[[ " $* " == *" --yes "* ]] || exit 91
if [[ " $* " == *" --get-next "* && " $* " == *" --dry-run "* ]]; then
  echo 'get-next cannot substitute for a separate dry-run' >&2
  exit 93
fi
if [[ " $* " == *" --get-next "* ]]; then
  printf '%%s\n' %q
  exit %d
fi
if [[ " $* " == *" --dry-run "* ]]; then
  # Deliberately misleading human-readable output must not be parsed.
  echo 'bump: version 0.1.0 -> 9.9.9'
  echo 'fixture dry-run diagnostic' >&2
  exit %d
fi
echo 'mutating Commitizen invocation forbidden' >&2
exit 92
`, log, next, nextCode, dryCode)
	write(t, filepath.Join(dir, "cz"), script, 0o700)
	for i, entry := range f.env {
		if path, ok := strings.CutPrefix(entry, "PATH="); ok {
			f.env[i] = "PATH=" + dir + ":" + path
		}
	}
	return log
}

func write(t *testing.T, path, text string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), mode); err != nil { // #nosec G703 -- All destinations are controlled files in t.TempDir, never external input.
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- Paths are repository fixtures or files inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
