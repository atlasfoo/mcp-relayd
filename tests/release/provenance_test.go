//go:build linux

package release_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func workflowNeeds(job workflowJob) []string {
	switch needs := job.Needs.(type) {
	case string:
		return []string{needs}
	case []any:
		var result []string
		for _, value := range needs {
			if name, ok := value.(string); ok {
				result = append(result, name)
			}
		}
		return result
	default:
		return nil
	}
}

func TestBuildReleaseCallRequiresSuccessfulProducerAndPush(t *testing.T) {
	build := loadWorkflow(t, "build")
	var producer, push, caller string
	for name, job := range build.Jobs {
		if job.Uses == "./.github/workflows/release.yml" {
			caller = name
		}
		for _, step := range job.Steps {
			if invokesRecipe(step.Run, "ci-build") {
				producer = name
			}
			if strings.Contains(step.Uses, "create-github-app-token") {
				push = name
			}
		}
	}
	if producer == "" || push == "" || caller == "" || producer == push {
		t.Fatalf("need separate packaging, privileged push and reusable Release jobs: producer=%q push=%q caller=%q", producer, push, caller)
	}
	for _, dependent := range []string{push, caller} {
		job := build.Jobs[dependent]
		dependencies := workflowNeeds(job)
		if !slices.Contains(dependencies, producer) {
			t.Errorf("%s must depend on successful artifact upload in %s", dependent, producer)
		}
		requireGuard(t, dependent, job.If, "needs."+producer+".result == 'success'", "needs."+producer+".outputs.eligible == 'true'")
	}
	producerJob := build.Jobs[producer]
	packageIndex, uploadIndex := -1, -1
	for index, step := range producerJob.Steps {
		if invokesRecipe(step.Run, "ci-build") {
			packageIndex = index
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			uploadIndex = index
			if packageIndex >= 0 {
				requireGuard(t, "eligible artifact upload", step.If, "steps."+producerJob.Steps[packageIndex].ID+".outputs.eligible == 'true'")
			}
			if strings.Contains(step.If, "always()") || strings.Contains(step.If, "failure()") {
				t.Error("failed compilation/verification must not upload publishable handoff")
			}
		}
	}
	if packageIndex < 0 || uploadIndex <= packageIndex {
		t.Error("all-target packaging/verification must finish before artifact upload")
	}
	job := build.Jobs[caller]
	if !slices.Contains(workflowNeeds(job), push) {
		t.Error("Release must depend on privileged push, not merely uploaded artifacts")
	}
	requireGuard(t, caller, job.If, "needs."+push+".result == 'success'")
	secrets, ok := job.Secrets.(map[string]any)
	if !ok || len(secrets) == 0 {
		t.Error("Release requires an explicit secret mapping, never secrets: inherit")
	}
	for name, value := range job.With {
		if strings.Contains(value, "secrets.") || strings.Contains(strings.ToLower(name), "token") {
			t.Error("credentials must not travel as reusable workflow inputs")
		}
	}
}

func TestBuildCredentialsIsolatedFromCompilationAndCommitizen(t *testing.T) {
	build := loadWorkflow(t, "build")
	var privileged bool
	for name, job := range build.Jobs {
		var compiles, obtainsToken bool
		for _, step := range job.Steps {
			compiles = compiles || invokesRecipe(step.Run, "ci-build") || strings.Contains(executableLines(step.Run), "cz bump") || strings.Contains(executableLines(step.Run), "go build")
			obtainsToken = obtainsToken || strings.Contains(step.Uses, "create-github-app-token")
		}
		if compiles {
			data, err := yaml.Marshal(job)
			if err != nil {
				t.Fatal(err)
			}
			var node yaml.Node
			if err := yaml.Unmarshal(data, &node); err != nil {
				t.Fatal(err)
			}
			assertNoAppCredentials(t, &node)
		}
		if !obtainsToken {
			continue
		}
		privileged = true
		if compiles {
			t.Errorf("%s executes compilation/Commitizen with App credentials", name)
		}
		var handoff, preflight bool
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/checkout@") && (step.With["ref"] != "${{ github.workflow_sha }}" || step.With["persist-credentials"] != "false") {
				t.Error("privileged push must checkout only trusted tooling without persisted credentials")
			}
			handoff = handoff || strings.HasPrefix(step.Uses, "actions/download-artifact@")
			preflight = preflight || step.ID == "preflight"
			if strings.Contains(step.Uses, "create-github-app-token") {
				if !preflight {
					t.Error("push token must follow credential-free handoff/ref verification")
				}
				requireGuard(t, "push token", step.If, "steps.preflight.outputs.ready == 'true'")
				if step.With["owner"] != "${{ github.repository_owner }}" || step.With["repositories"] != "${{ github.event.repository.name }}" || step.With["permission-contents"] != "write" {
					t.Error("push App token must have Contents write scoped only to caller repository")
				}
			}
		}
		if !handoff {
			t.Error("privileged push must consume uploaded Git objects/data, not candidate checkout")
		}
	}
	if !privileged {
		t.Error("Build lacks its isolated privileged push job")
	}
}

func TestWorkflowArtifactsBindProducerRunAndAttempt(t *testing.T) {
	build := loadWorkflow(t, "build")
	var uploaded, called bool
	for _, job := range build.Jobs {
		for _, step := range job.Steps {
			if !strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
				continue
			}
			uploaded = true
			if !strings.Contains(step.With["name"], "github.run_id") || !strings.Contains(step.With["name"], "github.run_attempt") || step.With["if-no-files-found"] != "error" {
				t.Error("producer artifact name must bind run ID and producing attempt; missing files must fail")
			}
		}
		if job.Uses != "./.github/workflows/release.yml" {
			continue
		}
		called = true
		var producerAttempt, artifact bool
		for key, value := range job.With {
			if strings.Contains(key, "attempt") {
				producerAttempt = strings.Contains(value, "needs.") && strings.Contains(value, ".outputs.") && !strings.Contains(value, "github.run_attempt")
			}
			artifact = artifact || (strings.Contains(key, "artifact") && strings.Contains(value, "needs.") && strings.Contains(value, ".outputs."))
		}
		if !producerAttempt || !artifact {
			t.Error("Release must receive preserved producer attempt and exact artifact output, not the rerun's current attempt")
		}
	}
	if !uploaded || !called {
		t.Error("need producer upload and reusable consumer")
	}
	release := loadWorkflow(t, "release")
	for _, job := range release.Jobs {
		for _, step := range job.Steps {
			if !strings.HasPrefix(step.Uses, "actions/download-artifact@") {
				continue
			}
			if step.With["pattern"] != "" || step.With["merge-multiple"] == "true" || (step.With["name"] == "" && step.With["artifact-ids"] == "") {
				t.Error("Release must download exact producer artifact, never merge a wildcard across attempts")
			}
			if step.With["run-id"] != "${{ github.run_id }}" && !strings.Contains(step.With["run-id"], "inputs.") {
				t.Error("reusable consumer must bind caller Build run, not triggering Checks run")
			}
		}
	}
}

func TestReusableReleaseObtainsPublicationTokenAfterPreflight(t *testing.T) {
	release := loadWorkflow(t, "release")
	var token bool
	for _, job := range release.Jobs {
		preflight := false
		for _, step := range job.Steps {
			preflight = preflight || step.ID == "preflight"
			if strings.Contains(step.Uses, "create-github-app-token") {
				token = true
				if !preflight {
					t.Error("publication token must follow credential-free artifact/ref preflight")
				}
				requireGuard(t, "publication token", step.If, "steps.preflight.outputs.ready == 'true'")
			}
			if regexpVersionWrite(executableLines(step.Run)) {
				t.Error("reusable Release must not bump, create tags or push Git")
			}
		}
	}
	if !token {
		t.Error("Release must obtain its own publication token")
	}
}

func regexpVersionWrite(script string) bool {
	return strings.Contains(script, "cz bump") || strings.Contains(script, "git push") || strings.Contains(script, "git tag ") || strings.Contains(script, "git commit")
}

func TestReusableReleaseProvenanceAcceptsOngoingCaller(t *testing.T) {
	f := newFixture(t)
	source, workflowSHA := strings.Repeat("a", 40), strings.Repeat("b", 40)
	snapshot := map[string]any{
		"repository": "owner/mcp-relayd", "trigger_run_id": 202, "require_chain_binding": true,
		"checks":   map[string]any{"id": 101, "run_attempt": 1, "path": ".github/workflows/checks.yml", "status": "completed", "conclusion": "success", "event": "push", "head_sha": source, "head_branch": "master", "repository": map[string]any{"full_name": "owner/mcp-relayd"}, "head_repository": map[string]any{"full_name": "owner/mcp-relayd"}},
		"build":    map[string]any{"id": 202, "run_attempt": 1, "path": ".github/workflows/build.yml", "status": "in_progress", "conclusion": nil, "event": "workflow_run", "head_sha": workflowSHA, "repository": map[string]any{"full_name": "owner/mcp-relayd"}},
		"metadata": map[string]any{"verified": true, "checks_run_id": 101, "trigger_checks_run_id": 101, "checks_run_attempt": 1, "build_run_id": 202, "build_run_attempt": 1, "build_head_sha": workflowSHA, "source_sha": source, "event": "push", "repository": "owner/mcp-relayd", "head_repository": "owner/mcp-relayd", "head_branch": "master", "artifact_class": "release", "eligible": true},
	}
	bindSuccessfulProducer(snapshot)
	input := filepath.Join(t.TempDir(), "ongoing-build.json")
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	write(t, input, string(data), 0o600)
	output, err := f.invoke(t, "workflow-provenance", "release", input)
	if err != nil {
		t.Fatalf("Release is part of ongoing caller Build; provenance must not demand completed overall run: %v: %s", err, output)
	}
}

// The whole Build may still be running, but its exact producing job must
// already have finished successfully in the retained producing attempt.
func bindSuccessfulProducer(snapshot map[string]any) {
	build := snapshot["build"].(map[string]any)
	build["head_branch"] = "master"
	build["head_repository"] = map[string]any{"full_name": "owner/mcp-relayd"}
	snapshot["producer"] = map[string]any{"name": "build", "run_id": 202, "run_attempt": 1, "head_sha": build["head_sha"], "status": "completed", "conclusion": "success"}
	snapshot["caller_workflow"] = "owner/mcp-relayd/.github/workflows/build.yml@refs/heads/master"
	snapshot["caller_sha"] = build["head_sha"]
	snapshot["caller_event"] = "workflow_run"
	snapshot["caller_checks_run_id"] = 101
}

func preparePushHandoff(t *testing.T, p *publicationFixture) string {
	t.Helper()
	p.env = append(p.env, "GITHUB_RUN_ID=202", "RELEASE_REMOTE="+p.origin)
	log := p.recordCZ(t)
	writePublicationJSON(t, p.input, map[string]any{"verified": true, "repository": "owner/mcp-relayd", "head_repository": "owner/mcp-relayd", "event": "push", "head_branch": "master", "checks_run_id": 101, "source_sha": p.source})
	logs, err := p.command(t, "just", "--justfile", p.trusted, "--working-directory", p.dir, "ci-build", p.input, p.assets, p.output)
	if err != nil {
		t.Fatalf("prepare inert handoff: %v: %s", err, logs)
	}
	decode(t, read(t, p.output), &p.metadata)
	writePublicationJSON(t, p.input, p.metadata)
	return log
}

func invokePush(t *testing.T, p *publicationFixture, preflight bool) (string, error) {
	t.Helper()
	f := p.fixture
	if preflight {
		f.env = append(append([]string(nil), f.env...), "RELEASE_PREFLIGHT_ONLY=1", "GH_TOKEN=")
	}
	return f.command(t, "just", "--justfile", p.trusted, "--working-directory", p.dir, "ci-push", p.input, p.assets, p.output)
}

func TestPushInertHandoffAtomicAndRetriedWithoutNewVersion(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	czLog := preparePushHandoff(t, p)
	p.recordGit(t, "")
	// Candidate files and hooks are deliberately hostile. The privileged
	// context consumes only uploaded objects and trusted recipes.
	write(t, filepath.Join(p.dir, "justfile"), "ci-push:\n    exit 97\n", 0o600)
	write(t, filepath.Join(p.dir, "devenv.nix"), "throw \"candidate environment executed\"\n", 0o600)
	write(t, filepath.Join(p.dir, ".git", "hooks", "pre-push"), "#!/bin/sh\nexit 97\n", 0o700)
	justPath, err := p.command(t, "sh", "-c", "command -v just")
	if err != nil {
		t.Fatal(err)
	}
	observer := t.TempDir()
	write(t, filepath.Join(observer, "just"), fmt.Sprintf(`#!/usr/bin/env python3
import os, sys
if 'ci-push' not in sys.argv and 'ci-build' not in sys.argv:
    assert not os.environ.get('GH_TOKEN'), 'App token reached nested tooling'
os.execv(%q, [%q, *sys.argv[1:]])
`, justPath, justPath), 0o700)
	p.prependPath(observer)
	beforeCZ := read(t, czLog)
	if logs, err := invokePush(t, p, true); err != nil {
		t.Fatalf("credential-free push preflight: %v: %s", err, logs)
	}
	p.assertUnpublished(t)
	if logs, err := invokePush(t, p, false); err != nil {
		t.Fatalf("atomic handoff push: %v: %s", err, logs)
	}
	bump := p.metadata["bump_sha"].(string)
	if p.remote(t, "master") != bump || p.remote(t, "v0.2.0") != bump {
		t.Fatal("handoff changed canonical commit/tag SHA")
	}
	refs := p.git(t, "--git-dir", p.origin, "show-ref")
	if logs, err := invokePush(t, p, false); err != nil {
		t.Fatalf("same handoff retry: %v: %s", err, logs)
	}
	if p.git(t, "--git-dir", p.origin, "show-ref") != refs || read(t, czLog) != beforeCZ {
		t.Fatal("push context ran Commitizen or changed version on retry")
	}
	pushes := read(t, filepath.Join(filepath.Dir(p.calls), "git.calls"))
	if strings.Count(pushes, "--atomic") != 1 || strings.Contains(pushes, "--force") {
		t.Fatalf("need exactly one non-force atomic push: %s", pushes)
	}
	// Rerunning the producer after a successful push must recover that bump,
	// not run Commitizen or manufacture a second version.
	p.git(t, "checkout", p.source, "--", "justfile")
	if err := os.Remove(filepath.Join(p.dir, "devenv.nix")); err != nil {
		t.Fatal(err)
	}
	p.git(t, "fetch", "origin", "master:refs/remotes/origin/master")
	p.env = append(p.env, "GH_TOKEN=")
	preparePushHandoff(t, p)
	if p.metadata["bump_sha"] != bump || p.metadata["version"] != "0.2.0" || read(t, czLog) != beforeCZ {
		t.Fatal("producer rerun did not recover exact published bump")
	}
}

func TestPushRejectsForgedHandoffAndRemoteRaces(t *testing.T) {
	for _, scenario := range []string{"checksum", "manifest", "extra-diff", "identity", "extra-ref", "stale-source", "stale-tag", "atomic-failure", "race-source", "race-tag"} {
		t.Run(scenario, func(t *testing.T) {
			p := newPublicationFixture(t, "feat: add relay")
			preparePushHandoff(t, p)
			skip := scenario == "stale-source" || scenario == "stale-tag"
			switch scenario {
			case "checksum":
				write(t, filepath.Join(p.assets, "bump.bundle"), "forged objects", 0o600)
			case "manifest":
				p.metadata["bump_sha"] = strings.Repeat("a", 40)
				writePublicationJSON(t, p.input, p.metadata)
			case "identity", "extra-diff":
				p.git(t, "checkout", "--detach", "v0.2.0")
				if scenario == "extra-diff" {
					write(t, filepath.Join(p.dir, "extra.txt"), "forged extra diff", 0o600)
					p.git(t, "add", "extra.txt")
					p.git(t, "-c", "user.name="+publicationBotName, "-c", "user.email="+publicationBotEmail, "commit", "--amend", "--reset-author", "--no-edit")
				} else {
					p.git(t, "commit", "--amend", "--reset-author", "--no-edit")
				}
				p.git(t, "tag", "-f", "v0.2.0")
				p.metadata["bump_sha"] = p.git(t, "rev-parse", "HEAD")
				p.git(t, "checkout", "--detach", p.source)
				rewritePushBundle(t, p, false)
			case "extra-ref":
				rewritePushBundle(t, p, true)
			case "stale-source":
				p.git(t, "commit", "--allow-empty", "-m", "fix: newer source")
				p.git(t, "push", "origin", "HEAD:master")
			case "stale-tag":
				p.git(t, "--git-dir", p.origin, "update-ref", "refs/tags/v0.1.1", p.source)
			case "atomic-failure":
				// A server-side failure must leave BOTH refs unchanged.
				write(t, filepath.Join(p.origin, "hooks", "pre-receive"), "#!/bin/sh\nexit 1\n", 0o700)
			case "race-source":
				p.recordGit(t, "source")
			case "race-tag":
				p.recordGit(t, "tag")
			}
			logs, err := invokePush(t, p, false)
			if skip {
				if err != nil {
					t.Fatalf("stale refs should skip: %v: %s", err, logs)
				}
				var result map[string]any
				decode(t, read(t, p.output), &result)
				if result["pushed"] != false || result["ready"] != false {
					t.Fatal("stale refs authorize Release")
				}
			} else if err == nil {
				t.Fatalf("unsafe handoff/push accepted: %s", logs)
			}
			if scenario != "stale-source" && scenario != "race-source" && p.remote(t, "master") != p.source {
				t.Fatal("failed atomic push moved master")
			}
			if scenario != "race-tag" && p.git(t, "--git-dir", p.origin, "tag", "--list", "v0.2.0") != "" {
				t.Fatal("failed atomic push created tag")
			}
		})
	}
}

func rewritePushBundle(t *testing.T, p *publicationFixture, extra bool) {
	t.Helper()
	bundle := filepath.Join(p.assets, "bump.bundle")
	if err := os.Remove(bundle); err != nil {
		t.Fatal(err)
	}
	args := []string{"bundle", "create", bundle, "refs/tags/v0.2.0"}
	if extra {
		p.git(t, "tag", "extra", "v0.2.0")
		args = append(args, "refs/tags/extra")
	}
	p.git(t, append(args, "^"+p.source)...)
	sum := sha256.Sum256([]byte(read(t, bundle)))
	p.metadata["bundle_sha256"] = fmt.Sprintf("%x", sum)
	var manifest map[string]any
	decode(t, read(t, filepath.Join(p.assets, "manifest.json")), &manifest)
	for _, key := range []string{"bump_sha", "bundle_sha256"} {
		manifest[key] = p.metadata[key]
	}
	writePublicationJSON(t, filepath.Join(p.assets, "manifest.json"), manifest)
	writePublicationJSON(t, p.input, p.metadata)
}

func TestHandoffAPIBindsExactArtifactAndRetainedSuccessfulProducer(t *testing.T) {
	for _, mutation := range []string{"valid-rerun", "wrong-id", "wrong-name", "expired", "wrong-run", "wrong-sha", "failed-producer", "duplicate-producer"} {
		t.Run(mutation, func(t *testing.T) {
			p := newPublicationFixture(t, "feat: add relay")
			source, workflowSHA := p.source, strings.Repeat("b", 40)
			metadata := map[string]any{"verified": true, "checks_run_id": 101, "trigger_checks_run_id": 101, "checks_run_attempt": 1, "build_run_id": 202, "build_run_attempt": 1, "build_head_sha": workflowSHA, "source_sha": source, "event": "push", "repository": "owner/mcp-relayd", "head_repository": "owner/mcp-relayd", "head_branch": "master", "artifact_class": "release", "eligible": true}
			checks := map[string]any{"id": 101, "run_attempt": 1, "path": ".github/workflows/checks.yml", "status": "completed", "conclusion": "success", "event": "push", "head_sha": source, "head_branch": "master", "repository": map[string]any{"full_name": "owner/mcp-relayd"}, "head_repository": map[string]any{"full_name": "owner/mcp-relayd"}}
			build := map[string]any{"id": 202, "run_attempt": 2, "path": ".github/workflows/build.yml", "status": "in_progress", "conclusion": nil, "event": "workflow_run", "head_sha": workflowSHA, "head_branch": "master", "repository": map[string]any{"full_name": "owner/mcp-relayd"}, "head_repository": map[string]any{"full_name": "owner/mcp-relayd"}}
			producer := map[string]any{"id": 404, "name": "build", "run_id": 202, "run_attempt": 1, "head_sha": workflowSHA, "status": "completed", "conclusion": "success"}
			artifactRun := map[string]any{"id": 202, "head_sha": workflowSHA}
			artifact := map[string]any{"id": 505, "name": "release-202-1", "expired": false, "workflow_run": artifactRun}
			jobs := []any{producer}
			switch mutation {
			case "wrong-id":
				artifact["id"] = 999
			case "wrong-name":
				artifact["name"] = "release-202-2"
			case "expired":
				artifact["expired"] = true
			case "wrong-run":
				artifactRun["id"] = 999
			case "wrong-sha":
				artifactRun["head_sha"] = strings.Repeat("c", 40)
			case "failed-producer":
				producer["conclusion"] = "failure"
			case "duplicate-producer":
				jobs = append(jobs, producer)
			}
			state := p.state(t)
			state["actions"] = map[string]any{
				"repos/owner/mcp-relayd/actions/runs/202":                              build,
				"repos/owner/mcp-relayd/actions/runs/101":                              checks,
				"repos/owner/mcp-relayd/actions/runs/202/attempts/1/jobs?per_page=100": []any{map[string]any{"jobs": jobs}},
				"repos/owner/mcp-relayd/actions/artifacts/505":                         artifact,
			}
			writePublicationJSON(t, p.api, state)
			writePublicationJSON(t, filepath.Join(p.assets, "build-metadata.json"), metadata)
			event := filepath.Join(t.TempDir(), "event.json")
			writePublicationJSON(t, event, map[string]any{"workflow_run": map[string]any{"id": 101}})
			p.env = append(p.env, "GITHUB_RUN_ID=202", "GITHUB_WORKFLOW_REF=owner/mcp-relayd/.github/workflows/build.yml@refs/heads/master", "GITHUB_SHA="+workflowSHA, "GITHUB_EVENT_NAME=workflow_run", "GITHUB_EVENT_PATH="+event)
			logs, err := p.command(t, "just", "--justfile", p.trusted, "workflow-handoff", p.assets, "1", "505", p.input)
			if err == nil {
				logs, err = p.command(t, "just", "--justfile", p.trusted, "workflow-provenance", "release", p.input, p.output)
			}
			if mutation == "valid-rerun" {
				if err != nil {
					t.Fatalf("retained producer rejected by API handoff: %v: %s", err, logs)
				}
			} else if err == nil {
				t.Fatalf("forged API producer/artifact accepted: %s", logs)
			}
		})
	}
}
