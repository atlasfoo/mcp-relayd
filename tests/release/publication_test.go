//go:build linux

package release_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	publicationBotName  = "relay-release[bot]"
	publicationBotEmail = "123+relay-release[bot]@users.noreply.github.com"
	publicationMessage  = "chore(release): bump version 0.1.0 → 0.2.0"
)

type publicationFixture struct {
	fixture
	origin, trusted, assets, input, output, api, calls, source, gh string
	metadata                                                       map[string]any
}

func requirePublicationRecipe(t *testing.T, f fixture, name string) {
	t.Helper()
	if logs, err := f.command(t, "just", "--show", name); err != nil {
		t.Fatalf("publication contract requires recipe %s before exercising rejection: %v: %s", name, err, logs)
	}
}

func newPublicationFixture(t *testing.T, message string) *publicationFixture {
	t.Helper()
	f := newFixture(t)
	requirePublicationRecipe(t, f, "ci-release")
	p := &publicationFixture{fixture: f}
	p.trusted = filepath.Join(t.TempDir(), "justfile")
	write(t, p.trusted, read(t, filepath.Join(f.dir, "justfile")), 0o600)
	f.git(t, "tag", "v0.1.0")
	f.git(t, "commit", "--allow-empty", "-m", message)
	p.source = f.git(t, "rev-parse", "HEAD")
	p.origin = filepath.Join(t.TempDir(), "origin.git")
	f.git(t, "init", "--bare", p.origin)
	f.git(t, "remote", "add", "origin", p.origin)
	f.git(t, "push", "--atomic", "origin", "master", "v0.1.0")
	p.assets = t.TempDir()
	installMockGo(t, &p.fixture, "linux", "amd64")
	invokePackaging(t, p.fixture, "release-package", "0.2.0", p.source, "202", p.assets)
	p.metadata = map[string]any{"verified": true, "repository": "owner/mcp-relayd", "head_repository": "owner/mcp-relayd", "event": "push", "head_branch": "master", "checks_run_id": 101, "build_run_id": 202, "source_sha": p.source, "version": "0.2.0", "last_tag": "v0.1.0", "eligible": true, "artifact_class": "release"}
	var manifest map[string]any
	decode(t, read(t, filepath.Join(p.assets, "manifest.json")), &manifest)
	maps.Copy(manifest, p.metadata)
	writePublicationJSON(t, filepath.Join(p.assets, "manifest.json"), manifest)
	p.input, p.output = filepath.Join(t.TempDir(), "provenance.json"), filepath.Join(t.TempDir(), "result.json")
	p.api, p.calls = filepath.Join(t.TempDir(), "api.json"), filepath.Join(t.TempDir(), "calls.jsonl")
	write(t, p.calls, "", 0o600)
	writePublicationJSON(t, p.api, map[string]any{"release": nil, "uploads": 0, "creates": 0, "publishes": 0, "fail_upload": 0})
	p.installGH(t)
	p.env = append(p.env, "CI_JUSTFILE="+p.trusted, "GITHUB_REPOSITORY=owner/mcp-relayd", "GITHUB_RUN_ID=303", "GH_TOKEN=offline-placeholder", "RELEASE_BOT_NAME="+publicationBotName, "RELEASE_BOT_EMAIL="+publicationBotEmail)
	return p
}

func writePublicationJSON(t *testing.T, path string, value any) {
	t.Helper()
	// Use the existing fixture JSON decoder's inverse without shell interpolation.
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, string(data)+"\n", 0o600)
}

func (p *publicationFixture) run(t *testing.T) (string, error) {
	t.Helper()
	writePublicationJSON(t, p.input, p.metadata)
	return p.command(t, "just", "--justfile", p.trusted, "--working-directory", p.dir, "ci-release", p.input, p.assets, p.output)
}

func (p *publicationFixture) state(t *testing.T) map[string]any {
	t.Helper()
	var state map[string]any
	decode(t, read(t, p.api), &state)
	return state
}

func (p *publicationFixture) remote(t *testing.T, ref string) string {
	t.Helper()
	sha := p.git(t, "--git-dir", p.origin, "rev-parse", ref)
	// Publication uses a disposable clone: fetch objects for assertions without
	// changing the fixture HEAD, local refs or FETCH_HEAD.
	p.git(t, "fetch", "--no-write-fetch-head", "--no-tags", "origin", sha)
	return sha
}

func (p *publicationFixture) assertUnpublished(t *testing.T) {
	t.Helper()
	if p.remote(t, "master") != p.source || p.state(t)["publishes"] != float64(0) {
		t.Fatal("rejection changed remote master or published a release")
	}
	if refs := p.git(t, "--git-dir", p.origin, "tag", "--list", "v0.2.0"); refs != "" {
		t.Fatal("rejection pushed candidate tag")
	}
}

func TestPublicationBumpAtomicPushCompleteAssetsAndIdempotence(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	p.recordGit(t, "")
	czLog := p.recordCZ(t)
	logs, err := p.run(t)
	if err != nil {
		t.Fatalf("publication: %v: %s", err, logs)
	}
	bump := p.remote(t, "master")
	if bump == p.source || p.remote(t, "v0.2.0^{commit}") != bump || p.git(t, "show", "-s", "--format=%P", bump) != p.source {
		t.Fatal("master/tag must point to one bump whose sole parent is tested source")
	}
	if got := p.git(t, "show", "-s", "--format=%an|%ae|%cn|%ce|%s", bump); got != publicationBotName+"|"+publicationBotEmail+"|"+publicationBotName+"|"+publicationBotEmail+"|"+publicationMessage {
		t.Fatalf("wrong App identity/message: %s", got)
	}
	if got := p.git(t, "diff", "--name-only", p.source, bump); got != ".cz.toml\nCHANGELOG.md" {
		t.Fatalf("bump touched non-version files: %s", got)
	}
	if config := p.git(t, "show", bump+":.cz.toml"); !strings.Contains(config, `version = "0.2.0"`) {
		t.Fatal("bump version differs from compiled version")
	}
	wantConfig := strings.ReplaceAll(p.git(t, "show", p.source+":.cz.toml"), `version = "0.1.0"`, `version = "0.2.0"`)
	if p.git(t, "show", bump+":.cz.toml") != wantConfig {
		t.Fatal("bump changed Commitizen configuration beyond the version")
	}
	assertPublicationComplete(t, p)
	mutations := 0
	for line := range strings.SplitSeq(strings.TrimSpace(read(t, czLog)), "\n") {
		if strings.Contains(line, "bump") && !strings.Contains(line, "--dry-run") && !strings.Contains(line, "--get-next") {
			mutations++
			if !strings.Contains(line, "--yes") {
				t.Fatalf("Commitizen bump may prompt: %s", line)
			}
		}
	}
	if mutations != 1 {
		t.Fatalf("expected one real Commitizen bump, calls: %s", read(t, czLog))
	}
	before, refs := read(t, p.api), p.git(t, "--git-dir", p.origin, "show-ref")
	if logs, err := p.run(t); err != nil {
		t.Fatalf("completed retry: %v: %s", err, logs)
	}
	if read(t, p.api) != before || p.git(t, "--git-dir", p.origin, "show-ref") != refs {
		t.Fatal("completed retry duplicated bump/tag/release/assets")
	}
	pushes := read(t, filepath.Join(filepath.Dir(p.calls), "git.calls"))
	if !strings.Contains(pushes, "--atomic") || strings.Contains(pushes, "--force") || strings.Contains(pushes, "+refs/") {
		t.Fatalf("publication needs atomic non-force push: %s", pushes)
	}
}

func assertPublicationComplete(t *testing.T, p *publicationFixture) {
	t.Helper()
	state := p.state(t)
	release, ok := state["release"].(map[string]any)
	if !ok || release["draft"] != false || release["tag_name"] != "v0.2.0" || state["creates"] != float64(1) || state["publishes"] != float64(1) {
		t.Fatalf("not exactly one complete publication: %+v", state)
	}
	assets := release["assets"].([]any)
	if len(assets) != 7 {
		t.Fatalf("release must contain six archives and SHA256SUMS: %+v", assets)
	}
	checksums := ""
	for _, item := range assets {
		asset := item.(map[string]any)
		name := asset["name"].(string)
		if name == "SHA256SUMS" {
			checksums = asset["content"].(string)
			continue
		}
		local := read(t, filepath.Join(p.assets, filepath.Base(name)))
		if asset["hex"] != fmt.Sprintf("%x", []byte(local)) {
			t.Fatalf("uploaded bytes differ for %s", name)
		}
	}
	for _, target := range releaseTargets {
		name := fmt.Sprintf("mcp-relayd_0.2.0_%s_%s.%s", target.goos, target.goarch, target.suffix)
		sum := sha256.Sum256([]byte(read(t, filepath.Join(p.assets, name))))
		if !strings.Contains(checksums, fmt.Sprintf("%x  %s", sum, name)) {
			t.Fatalf("missing correct checksum for %s", name)
		}
	}
	if body, ok := release["body"].(string); !ok || !strings.Contains(body, "add relay") {
		t.Fatal("release notes must include incremental changelog")
	}
}

func TestPublicationPushThenUploadFailureRecoversSameDraft(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	state := p.state(t)
	state["fail_upload"] = 3
	writePublicationJSON(t, p.api, state)
	if logs, err := p.run(t); err == nil {
		t.Fatalf("upload failure hidden: %s", logs)
	}
	bump, state := p.remote(t, "master"), p.state(t)
	if bump == p.source || p.remote(t, "v0.2.0^{commit}") != bump || state["publishes"] != float64(0) {
		t.Fatal("fixture must reach successful push and incomplete draft before failure")
	}
	release := state["release"].(map[string]any)
	if release["draft"] != true || len(release["assets"].([]any)) != 2 {
		t.Fatalf("expected recoverable partial draft: %+v", release)
	}
	state["fail_upload"] = 0
	writePublicationJSON(t, p.api, state)
	if logs, err := p.run(t); err != nil {
		t.Fatalf("partial retry: %v: %s", err, logs)
	}
	assertPublicationComplete(t, p)
	if p.remote(t, "master") != bump || p.state(t)["uploads"] != float64(7) {
		t.Fatal("retry created a second bump or reuploaded existing assets")
	}
}

func TestPublicationRecoversExpectedPushedBumpBeforeDraftCreation(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	p.seedBump(t, "expected")
	bump := p.remote(t, "master")
	if logs, err := p.run(t); err != nil {
		t.Fatalf("recover pushed bump without draft: %v: %s", err, logs)
	}
	assertPublicationComplete(t, p)
	if p.remote(t, "master") != bump {
		t.Fatal("recovery created another bump")
	}
}

func TestPublicationRejectsArtifactAndProvenanceMismatch(t *testing.T) {
	for _, scenario := range []string{"sha", "version", "run", "checks", "tamper", "missing-asset", "duplicate-asset", "unverified", "foreign", "pull-request"} {
		t.Run(scenario, func(t *testing.T) {
			p := newPublicationFixture(t, "feat: add relay")
			path := filepath.Join(p.assets, "manifest.json")
			var manifest map[string]any
			decode(t, read(t, path), &manifest)
			switch scenario {
			case "sha":
				manifest["source_sha"] = strings.Repeat("a", 40)
			case "version":
				manifest["version"] = "0.3.0"
			case "run":
				manifest["run_id"], manifest["build_run_id"] = "999", 999
			case "checks":
				manifest["checks_run_id"] = 999
			case "tamper":
				write(t, filepath.Join(p.assets, "mcp-relayd_0.2.0_linux_amd64.tar.gz"), "tampered", 0o600)
			case "missing-asset":
				if err := os.Remove(filepath.Join(p.assets, "mcp-relayd_0.2.0_linux_amd64.tar.gz")); err != nil {
					t.Fatal(err)
				}
			case "duplicate-asset":
				assets := manifest["assets"].([]any)
				assets[1] = assets[0]
			case "unverified":
				p.metadata["verified"] = false
			case "foreign":
				p.metadata["head_repository"] = "foreign/mcp-relayd"
			case "pull-request":
				p.metadata["event"] = "pull_request"
			}
			writePublicationJSON(t, path, manifest)
			before := p.snapshot(t)
			if logs, err := p.run(t); err == nil {
				t.Fatalf("accepted %s: %s", scenario, logs)
			}
			p.assertUnpublished(t)
			if !reflect.DeepEqual(before, p.snapshot(t)) || p.state(t)["creates"] != float64(0) {
				t.Fatal("invalid input mutated local history or created a draft")
			}
		})
	}
}

func TestPublicationSkipsWithoutAppConfiguration(t *testing.T) {
	for _, scenario := range []string{"docs", "no-increment", "stale-source", "stale-tag"} {
		t.Run(scenario, func(t *testing.T) {
			message := "feat: add relay"
			if scenario == "docs" {
				message = "docs: explain usage"
			}
			p := newPublicationFixture(t, message)
			if scenario == "docs" || scenario == "no-increment" {
				p.metadata["eligible"], p.metadata["artifact_class"] = false, "dev"
				p.metadata["reason"] = "no-feat-fix"
				if scenario == "no-increment" {
					p.metadata["reason"] = "no-increment"
				}
				var manifest map[string]any
				path := filepath.Join(p.assets, "manifest.json")
				decode(t, read(t, path), &manifest)
				maps.Copy(manifest, p.metadata)
				writePublicationJSON(t, path, manifest)
			}
			if scenario == "stale-source" {
				p.git(t, "commit", "--allow-empty", "-m", "fix: newer source")
				p.git(t, "push", "origin", "master")
				p.git(t, "checkout", "--detach", p.source)
			}
			if scenario == "stale-tag" {
				p.git(t, "tag", "v0.1.1")
				p.git(t, "push", "origin", "v0.1.1")
			}
			p.withoutApp()
			before := p.git(t, "--git-dir", p.origin, "show-ref")
			if logs, err := p.run(t); err != nil {
				t.Fatalf("skip needs no App configuration: %v: %s", err, logs)
			}
			var result struct {
				Published bool   `json:"published"`
				Reason    string `json:"reason"`
			}
			decode(t, read(t, p.output), &result)
			expected := scenario
			if scenario == "docs" {
				expected = "no-feat-fix"
			}
			if result.Published || result.Reason != expected || read(t, p.calls) != "" || before != p.git(t, "--git-dir", p.origin, "show-ref") {
				t.Fatalf("skip changed publication state: %+v", result)
			}
		})
	}
}

func (p *publicationFixture) withoutApp() {
	env := make([]string, 0, len(p.env))
	for _, entry := range p.env {
		if !strings.HasPrefix(entry, "GH_TOKEN=") && !strings.HasPrefix(entry, "RELEASE_BOT_") {
			env = append(env, entry)
		}
	}
	p.env = env
}

func TestPublicationEligibleRequiresApp(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	p.withoutApp()
	if logs, err := p.run(t); err == nil {
		t.Fatalf("eligible publication accepted missing App: %s", logs)
	}
	p.assertUnpublished(t)
}

func TestPublicationPreflightNeedsNoAppAndDoesNotMutate(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	p.withoutApp()
	p.env = append(p.env, "RELEASE_PREFLIGHT_ONLY=1")
	before := p.snapshot(t)
	if logs, err := p.run(t); err != nil {
		t.Fatalf("preflight: %v: %s", err, logs)
	}
	var result struct {
		Ready bool `json:"ready"`
	}
	decode(t, read(t, p.output), &result)
	if !result.Ready || !reflect.DeepEqual(before, p.snapshot(t)) || read(t, p.calls) != "" {
		t.Fatal("preflight needs App credentials or mutated publication state")
	}
	p.assertUnpublished(t)
}

func TestPublicationOwnBumpBuildIsDevelopmentWithoutPrivateApp(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	p.seedBump(t, "expected")
	p.metadata["source_sha"] = p.git(t, "rev-parse", "HEAD")
	p.withoutApp()
	p.env = append(p.env, "RELEASE_BOT_NAME="+publicationBotName, "RELEASE_BOT_EMAIL="+publicationBotEmail)
	writePublicationJSON(t, p.input, p.metadata)
	if logs, err := p.command(t, "just", "--justfile", p.trusted, "--working-directory", p.dir, "ci-build", p.input, t.TempDir(), p.output); err != nil {
		t.Fatalf("own bump Build: %v: %s", err, logs)
	}
	var result struct {
		Eligible bool   `json:"eligible"`
		Class    string `json:"artifact_class"`
		Reason   string `json:"reason"`
	}
	decode(t, read(t, p.output), &result)
	if result.Eligible || result.Class != "dev" || result.Reason != "own-bump" || read(t, p.calls) != "" {
		t.Fatalf("own bump started a publication cycle: %+v", result)
	}
}

func TestPublicationAtomicPushRaces(t *testing.T) {
	for _, race := range []string{"master", "tag"} {
		t.Run(race, func(t *testing.T) {
			p := newPublicationFixture(t, "feat: add relay")
			p.recordGit(t, race)
			if logs, err := p.run(t); err == nil {
				t.Fatalf("concurrent %s update hidden: %s", race, logs)
			}
			if p.state(t)["creates"] != float64(0) || p.state(t)["publishes"] != float64(0) {
				t.Fatal("failed atomic push reached publication API")
			}
			if race == "tag" && p.remote(t, "master") != p.source {
				t.Fatal("tag race partially pushed master")
			}
			if race == "master" && p.git(t, "--git-dir", p.origin, "tag", "--list", "v0.2.0") != "" {
				t.Fatal("master race partially pushed tag")
			}
		})
	}
}

func TestPublicationExistingTagMustBeExactExpectedBump(t *testing.T) {
	for _, scenario := range []string{"source-tag", "wrong-parent", "wrong-identity", "wrong-message", "code-change", "wrong-version", "config-change"} {
		t.Run(scenario, func(t *testing.T) {
			p := newPublicationFixture(t, "feat: add relay")
			p.seedBump(t, scenario)
			before := p.git(t, "--git-dir", p.origin, "show-ref")
			if logs, err := p.run(t); err == nil {
				t.Fatalf("recovered conflicting tag %s: %s", scenario, logs)
			}
			if before != p.git(t, "--git-dir", p.origin, "show-ref") || p.state(t)["creates"] != float64(0) {
				t.Fatal("tag conflict was overwritten or published")
			}
		})
	}
}

func (p *publicationFixture) seedBump(t *testing.T, scenario string) {
	t.Helper()
	if scenario != "source-tag" {
		if scenario == "wrong-parent" {
			p.git(t, "commit", "--allow-empty", "-m", "docs: unrelated parent")
		}
		config := strings.ReplaceAll(read(t, filepath.Join(p.dir, ".cz.toml")), `version = "0.1.0"`, `version = "0.2.0"`)
		if scenario == "wrong-version" {
			config = strings.ReplaceAll(config, `version = "0.2.0"`, `version = "0.3.0"`)
		}
		if scenario == "config-change" {
			config += "\npre_bump_hooks = [\"exit 98\"]\n"
		}
		write(t, filepath.Join(p.dir, ".cz.toml"), config, 0o600)
		write(t, filepath.Join(p.dir, "CHANGELOG.md"), "## 0.2.0\n\n- add relay\n", 0o600)
		if scenario == "code-change" {
			write(t, filepath.Join(p.dir, "code.go"), "package unexpected\n", 0o600)
		}
		p.git(t, "config", "user.name", publicationBotName)
		p.git(t, "config", "user.email", publicationBotEmail)
		if scenario == "wrong-identity" {
			p.git(t, "config", "user.email", "other@example.invalid")
		}
		message := publicationMessage
		if scenario == "wrong-message" {
			message = "chore(release): arbitrary lookalike"
		}
		p.git(t, "add", ".")
		p.git(t, "commit", "-m", message)
	}
	p.git(t, "tag", "v0.2.0")
	p.git(t, "push", "--atomic", "origin", "master", "v0.2.0")
}

func TestPublicationOwnBumpDetectionIsNotSubjectOnly(t *testing.T) {
	for _, scenario := range []string{"expected", "wrong-identity", "wrong-message", "code-change", "wrong-version", "config-change"} {
		t.Run(scenario, func(t *testing.T) {
			p := newPublicationFixture(t, "feat: add relay")
			requirePublicationRecipe(t, p.fixture, "release-own-bump")
			p.seedBump(t, scenario)
			var result struct {
				Own bool `json:"own_bump"`
			}
			p.recipe(t, &result, "release-own-bump", p.git(t, "rev-parse", "HEAD"))
			if result.Own != (scenario == "expected") {
				t.Fatalf("own bump detection %s = %v", scenario, result.Own)
			}
		})
	}
}

func TestPublicationConflictingExistingAssetNeverOverwrites(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	state := p.state(t)
	state["fail_upload"] = 3
	writePublicationJSON(t, p.api, state)
	if logs, err := p.run(t); err == nil {
		t.Fatalf("expected partial upload failure: %s", logs)
	}
	state = p.state(t)
	state["fail_upload"] = 0
	asset := state["release"].(map[string]any)["assets"].([]any)[0].(map[string]any)
	asset["hex"], asset["size"], asset["digest"] = "626164", 3, "sha256:"+strings.Repeat("0", 64)
	writePublicationJSON(t, p.api, state)
	before := read(t, p.api)
	if logs, err := p.run(t); err == nil {
		t.Fatalf("conflicting asset accepted: %s", logs)
	}
	if before != read(t, p.api) {
		t.Fatal("conflicting asset overwritten or draft published")
	}
}

func TestPublicationNeverExecutesCandidateConfiguration(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	marker := filepath.Join(t.TempDir(), "executed")
	config := read(t, filepath.Join(p.dir, ".cz.toml")) + fmt.Sprintf("\npre_bump_hooks = [%q]\npost_bump_hooks = [%q]\n", "touch "+marker, "touch "+marker)
	write(t, filepath.Join(p.dir, ".cz.toml"), config, 0o600)
	write(t, filepath.Join(p.dir, "justfile"), "ci-release:\n    exit 98\n", 0o600)
	write(t, filepath.Join(p.dir, "devenv.nix"), "throw \"candidate environment must not be evaluated\"\n", 0o600)
	p.git(t, "add", ".")
	p.git(t, "commit", "-m", "feat: hostile release configuration")
	p.source = p.git(t, "rev-parse", "HEAD")
	p.git(t, "push", "origin", "master")
	p.metadata["source_sha"] = p.source
	var manifest map[string]any
	decode(t, read(t, filepath.Join(p.assets, "manifest.json")), &manifest)
	manifest["source_sha"] = p.source
	writePublicationJSON(t, filepath.Join(p.assets, "manifest.json"), manifest)
	// A verifier may reject customization, or safely strip it. Neither may execute it.
	logs, err := p.run(t)
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatal("candidate Commitizen hooks executed in privileged publication")
	}
	if err == nil {
		assertPublicationComplete(t, p)
	} else {
		p.assertUnpublished(t)
		if strings.Contains(logs, "exit 98") {
			t.Fatal("publication delegated to candidate justfile")
		}
	}
}

func TestPublicationNeverExecutesDownloadedBinaries(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	marker := filepath.Join(t.TempDir(), "binary-executed")
	// Valid archive shape/checksums, hostile executable payload. Verification must
	// inspect only archive metadata/bytes, never invoke the contained executable.
	script := `import hashlib, io, json, sys, tarfile, zipfile
from pathlib import Path
directory, marker = Path(sys.argv[1]), sys.argv[2]
manifest_path = directory / 'manifest.json'
manifest = json.loads(manifest_path.read_text())
payload = ('#!/bin/sh\ntouch "' + marker + '"\n').encode()
for asset in manifest['assets']:
    path = directory / asset['name']
    if path.name.endswith('.zip'):
        with zipfile.ZipFile(path, 'w') as archive:
            member = zipfile.ZipInfo('mcp-relayd.exe')
            member.create_system = 3
            member.external_attr = 0o100755 << 16
            archive.writestr(member, payload)
    else:
        with tarfile.open(path, 'w:gz') as archive:
            member = tarfile.TarInfo('mcp-relayd')
            member.mode, member.size = 0o755, len(payload)
            archive.addfile(member, io.BytesIO(payload))
    asset['sha256'] = hashlib.sha256(path.read_bytes()).hexdigest()
manifest_path.write_text(json.dumps(manifest))
`
	if logs, err := p.command(t, "python3", "-c", script, p.assets, marker); err != nil {
		t.Fatalf("prepare hostile payload: %v: %s", err, logs)
	}
	logs, err := p.run(t)
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatal("downloaded binary executed during privileged publication")
	}
	if err != nil {
		t.Fatalf("publication must treat valid archives as data: %v: %s", err, logs)
	}
	assertPublicationComplete(t, p)
}

func TestPublicationExplicitConfigIgnoresAlternativePluginConfiguration(t *testing.T) {
	p := newPublicationFixture(t, "feat: add relay")
	// .cz.toml is the only accepted input, even when another default discovery
	// location names an executable/custom plugin.
	write(t, filepath.Join(p.dir, "pyproject.toml"), "[tool.commitizen]\nname = \"cz_nonexistent_candidate_plugin\"\nversion = \"99.0.0\"\n", 0o600)
	p.git(t, "add", "pyproject.toml")
	p.git(t, "commit", "-m", "feat: alternative config")
	p.source = p.git(t, "rev-parse", "HEAD")
	p.git(t, "push", "origin", "master")
	p.metadata["source_sha"] = p.source
	var manifest map[string]any
	path := filepath.Join(p.assets, "manifest.json")
	decode(t, read(t, path), &manifest)
	manifest["source_sha"] = p.source
	writePublicationJSON(t, path, manifest)
	if logs, err := p.run(t); err != nil {
		t.Fatalf("explicit standard config: %v: %s", err, logs)
	}
	assertPublicationComplete(t, p)
}

func (p *publicationFixture) recordGit(t *testing.T, race string) {
	t.Helper()
	gitPath, err := p.command(t, "sh", "-c", "command -v git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(filepath.Dir(p.calls), "git.calls")
	write(t, log, "", 0o600)
	// Race refs immediately before the real push, not before preflight.
	script := fmt.Sprintf(`#!/usr/bin/env python3
import os, subprocess, sys
from pathlib import Path
real, origin, source, race, log = %q, %q, %q, %q, %q
args = sys.argv[1:]
if 'push' in args:
    with open(log, 'a', encoding='utf-8') as output:
        output.write(' '.join(args) + '\n')
    if race and not Path(log + '.raced').exists():
        Path(log + '.raced').touch()
        if race == 'tag':
            subprocess.run([real, '--git-dir', origin, 'update-ref', 'refs/tags/v0.2.0', source], check=True)
        else:
            tree = subprocess.check_output([real, '--git-dir', origin, 'rev-parse', source + '^{tree}'], text=True).strip()
            env = dict(os.environ, GIT_AUTHOR_NAME='Concurrent', GIT_AUTHOR_EMAIL='race@example.invalid', GIT_COMMITTER_NAME='Concurrent', GIT_COMMITTER_EMAIL='race@example.invalid')
            commit = subprocess.check_output([real, '--git-dir', origin, 'commit-tree', tree, '-p', source, '-m', 'fix: concurrent update'], env=env, text=True).strip()
            subprocess.run([real, '--git-dir', origin, 'update-ref', 'refs/heads/master', commit], check=True)
os.execv(real, [real, *args])
`, gitPath, p.origin, p.source, race, log)
	write(t, filepath.Join(dir, "git"), script, 0o700)
	p.prependPath(dir)
}

func (p *publicationFixture) prependPath(dir string) {
	for i, entry := range p.env {
		if path, ok := strings.CutPrefix(entry, "PATH="); ok {
			p.env[i] = "PATH=" + dir + ":" + path
		}
	}
}

func (p *publicationFixture) recordCZ(t *testing.T) string {
	t.Helper()
	czPath, err := p.command(t, "sh", "-c", "command -v cz")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "cz.calls")
	write(t, log, "", 0o600)
	write(t, filepath.Join(dir, "cz"), fmt.Sprintf(`#!/usr/bin/env python3
import os, sys
with open(%q, 'a', encoding='utf-8') as output:
    output.write(' '.join(sys.argv[1:]) + '\n')
os.execv(%q, [%q, *sys.argv[1:]])
`, log, czPath, czPath), 0o700)
	p.prependPath(dir)
	return log
}

func TestPublicationOfflineAPIAdapter(t *testing.T) {
	// Exercise the adapter even while the implementation recipes are intentionally red.
	p := &publicationFixture{fixture: newFixture(t), api: filepath.Join(t.TempDir(), "state.json"), calls: filepath.Join(t.TempDir(), "calls")}
	write(t, p.calls, "", 0o600)
	writePublicationJSON(t, p.api, map[string]any{"release": nil, "uploads": 0, "creates": 0, "publishes": 0, "fail_upload": 0})
	p.env = append(p.env, "GH_TOKEN=offline-placeholder")
	p.installGH(t)
	endpoint := "repos/owner/mcp-relayd/releases"
	if _, err := p.command(t, p.gh, "api", endpoint+"/tags/v0.2.0"); err == nil {
		t.Fatal("adapter must return 404 before draft creation")
	}
	if logs, err := p.command(t, p.gh, "api", endpoint, "-X", "POST", "-f", "tag_name=v0.2.0", "-F", "draft=true", "-f", "body=add relay"); err != nil {
		t.Fatalf("adapter create: %v: %s", err, logs)
	}
	if _, err := p.command(t, p.gh, "api", endpoint+"/17", "-X", "PATCH", "-F", "draft=false"); err == nil {
		t.Fatal("adapter must refuse incomplete publication")
	}
	file := filepath.Join(t.TempDir(), "bytes")
	write(t, file, "offline bytes", 0o600)
	for _, target := range releaseTargets {
		name := fmt.Sprintf("mcp-relayd_0.2.0_%s_%s.%s", target.goos, target.goarch, target.suffix)
		if logs, err := p.command(t, p.gh, "api", "https://uploads.github.com/"+endpoint+"/17/assets?name="+name, "-X", "POST", "--input", file); err != nil {
			t.Fatalf("adapter upload: %v: %s", err, logs)
		}
	}
	if logs, err := p.command(t, p.gh, "api", endpoint+"/17/assets?name=SHA256SUMS", "-X", "POST", "--input", file); err != nil {
		t.Fatalf("adapter checksum upload: %v: %s", err, logs)
	}
	if _, err := p.command(t, p.gh, "api", endpoint+"/17/assets?name=SHA256SUMS", "-X", "POST", "--input", file); err == nil {
		t.Fatal("adapter must reject duplicate uploads")
	}
	if logs, err := p.command(t, p.gh, "api", endpoint+"/assets/100", "-H", "Accept: application/octet-stream"); err != nil || logs != "offline bytes" {
		t.Fatalf("adapter download: %v: %s", err, logs)
	}
	if logs, err := p.command(t, p.gh, "api", endpoint+"/17", "-X", "PATCH", "-F", "draft=false"); err != nil {
		t.Fatalf("adapter publish: %v: %s", err, logs)
	}
	if p.state(t)["publishes"] != float64(1) {
		t.Fatal("adapter failed to persist publication")
	}
}

func (p *publicationFixture) installGH(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	// Strict offline API: unsupported commands fail; no fallback to the real gh.
	script := fmt.Sprintf(`#!/usr/bin/env python3
import hashlib, json, os, sys
from pathlib import Path
from urllib.parse import urlparse, parse_qs
state_path, calls_path = Path(%q), Path(%q)
args = sys.argv[1:]
with calls_path.open('a', encoding='utf-8') as log:
    log.write(json.dumps(args) + '\n')
def fail(message, code=1):
    print(message, file=sys.stderr)
    sys.exit(code)
if os.environ.get('GH_TOKEN') != 'offline-placeholder':
    fail('offline App token required')
if not args or args.pop(0) != 'api':
    fail('only gh api is supported')
method, endpoint, fields, input_path, headers = 'GET', None, {}, None, []
while args:
    arg = args.pop(0)
    if arg in ('--method', '-X'):
        method = args.pop(0)
    elif arg in ('--field', '-F', '--raw-field', '-f'):
        key, value = args.pop(0).split('=', 1)
        if arg in ('--field', '-F'):
            if value.startswith('@'):
                value = Path(value[1:]).read_text()
            elif value in ('true', 'false'):
                value = value == 'true'
        fields[key] = value
    elif arg == '--input':
        input_path = args.pop(0)
    elif arg in ('--header', '-H'):
        headers.append(args.pop(0))
    elif arg == '--paginate':
        pass
    elif arg.startswith('-') or endpoint is not None:
        fail('unsupported API argument: ' + arg)
    else:
        endpoint = arg
if not endpoint:
    fail('missing endpoint')
parsed = urlparse(endpoint)
if parsed.netloc and parsed.netloc not in ('api.github.com', 'uploads.github.com'):
    fail('foreign host')
path = parsed.path.lstrip('/')
prefix = 'repos/owner/mcp-relayd/releases'
if not path.startswith(prefix):
    fail('foreign repository or unsupported API')
suffix = path[len(prefix):]
state = json.loads(state_path.read_text())
release = state['release']
def save():
    state_path.write_text(json.dumps(state, sort_keys=True) + '\n')
def emit(value):
    print(json.dumps(value))
if input_path and method in ('POST', 'PATCH') and not suffix.endswith('/assets'):
    fields.update(json.loads(Path(input_path).read_text()))
if method == 'GET' and suffix == '/tags/v0.2.0':
    if release is None or release['draft']:
        fail('HTTP 404: Not Found')
    emit(release)
elif method == 'GET' and suffix == '':
    emit([release] if release else [])
elif method == 'POST' and suffix == '':
    if release is not None or fields.get('draft') is not True or fields.get('tag_name') != 'v0.2.0':
        fail('duplicate release or non-draft creation')
    release = dict(fields, id=17, assets=[], upload_url='https://uploads.github.com/' + prefix + '/17/assets{?name,label}')
    state['release'], state['creates'] = release, state['creates'] + 1
    save()
    emit(release)
elif method == 'GET' and suffix == '/17':
    emit(release)
elif method == 'GET' and suffix == '/17/assets':
    emit(release['assets'])
elif method == 'POST' and suffix == '/17/assets':
    name = parse_qs(parsed.query).get('name', [fields.get('name')])[0]
    if not release or not release['draft'] or not name or not input_path:
        fail('upload requires draft, name and --input file')
    if any(asset['name'] == name for asset in release['assets']):
        fail('HTTP 422: duplicate asset')
    if state['fail_upload'] == state['uploads'] + 1:
        fail('HTTP 503: simulated upload failure')
    data = Path(input_path).read_bytes()
    digest = hashlib.sha256(data).hexdigest()
    asset = dict(id=100 + state['uploads'], name=name, size=len(data), digest='sha256:' + digest, hex=data.hex(), content=data.decode('utf-8') if name == 'SHA256SUMS' else '', url='https://api.github.com/' + prefix + '/assets/' + str(100 + state['uploads']))
    release['assets'].append(asset)
    state['uploads'] += 1
    save()
    emit(asset)
elif method == 'GET' and suffix.startswith('/assets/'):
    asset = next((a for a in release['assets'] if str(a['id']) == suffix.rsplit('/', 1)[1]), None)
    if asset is None:
        fail('HTTP 404: asset missing')
    if any('application/octet-stream' in h for h in headers):
        sys.stdout.buffer.write(bytes.fromhex(asset['hex']))
    else:
        emit(asset)
elif method == 'PATCH' and suffix == '/17':
    if fields.get('draft') is not False or not release['draft'] or len(release['assets']) != 7:
        fail('cannot publish incomplete or already published release')
    expected = {'mcp-relayd_0.2.0_' + system + '_' + arch + ('.zip' if system == 'windows' else '.tar.gz') for system in ('linux', 'darwin', 'windows') for arch in ('amd64', 'arm64')} | {'SHA256SUMS'}
    if {a['name'] for a in release['assets']} != expected:
        fail('unexpected publication assets')
    release['draft'] = False
    state['publishes'] += 1
    save()
    emit(release)
else:
    fail('unsupported API operation: ' + method + ' ' + suffix)
`, p.api, p.calls)
	p.gh = filepath.Join(dir, "gh")
	write(t, p.gh, script, 0o700)
	p.prependPath(dir)
}
