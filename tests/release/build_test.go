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

func TestBuildWorkflowTrustedEnvironmentBoundary(t *testing.T) {
	workflow := loadWorkflow(t, "build")
	var provenance, source, packaging int
	provenance, source, packaging = -1, -1, -1
	for _, job := range workflow.Jobs {
		for i, step := range job.Steps {
			switch step.ID {
			case "provenance":
				provenance = i
				if !strings.Contains(step.Run, "cd trusted\n") || !strings.Contains(step.Run, "--justfile justfile workflow-provenance build") {
					t.Fatal("provenance must execute trusted recipes inside trusted devenv")
				}
			case "package":
				packaging = i
				if !strings.Contains(step.Run, "cd trusted\n") || !strings.Contains(step.Run, `--working-directory "$GITHUB_WORKSPACE/source" ci-build`) || !strings.Contains(step.Run, `--justfile "$CI_JUSTFILE"`) {
					t.Fatal("candidate must not select its own justfile/devenv")
				}
			}
			if strings.HasPrefix(step.Uses, "actions/checkout@") && step.With["path"] == "source" {
				source = i
				if step.With["repository"] != "${{ steps.provenance.outputs.head_repository }}" || step.With["ref"] != "${{ steps.provenance.outputs.source_sha }}" {
					t.Fatal("source checkout must bind verified repository and SHA")
				}
			}
			if strings.HasPrefix(step.Uses, "actions/cache/") {
				key := step.With["key"]
				if !strings.HasPrefix(key, "build-nix-") || !strings.Contains(key, "trusted/devenv.nix") || !strings.Contains(key, "trusted/devenv.yaml") || !strings.Contains(key, "trusted/devenv.lock") {
					t.Fatal("Build cache must not share Checks namespace or hash candidate inputs")
				}
			}
		}
	}
	if provenance < 0 || source <= provenance || packaging <= source {
		t.Fatal("required order: verify Checks, checkout source, package")
	}
}

func TestCIBuildClassificationAndTrustedRecipes(t *testing.T) {
	for _, scenario := range []string{"master-feature", "master-docs", "pr", "fork", "topic", "wrong-source", "candidate-error", "package-error"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			trusted := filepath.Join(t.TempDir(), "justfile")
			write(t, trusted, read(t, filepath.Join(f.dir, "justfile")), 0o600)
			// Candidate recipes cannot delegate back to this worktree's justfile.
			write(t, filepath.Join(f.dir, "justfile"), "ci-build:\n    exit 97\n", 0o600)
			f.git(t, "add", ".")
			f.git(t, "commit", "-m", "chore: replace candidate recipes")
			f.git(t, "tag", "v0.1.0")
			message := "feat: add relay"
			if scenario == "master-docs" {
				message = "docs: explain usage"
			}
			f.git(t, "commit", "--allow-empty", "-m", message)
			head := f.git(t, "rev-parse", "HEAD")
			metadata := map[string]any{"verified": true, "repository": "owner/mcp-relayd", "head_repository": "owner/mcp-relayd", "event": "push", "head_branch": "master", "checks_run_id": 101, "source_sha": head}
			switch scenario {
			case "pr":
				metadata["event"] = "pull_request"
			case "fork":
				metadata["event"], metadata["head_repository"] = "pull_request", "fork/mcp-relayd"
			case "topic":
				metadata["head_branch"] = "topic"
			case "wrong-source":
				metadata["source_sha"] = strings.Repeat("a", 40)
			}
			// Non-master candidates must not invoke cz, even if source has a feature.
			czCode := 99
			if scenario == "master-feature" || scenario == "package-error" {
				czCode = 0
			}
			czLog := f.mockCZ(t, czCode, "0.2.0", 0)
			goPath := installMockGo(t, &f, "linux", "amd64")
			if scenario == "package-error" {
				write(t, goPath, "#!/bin/sh\nexit 98\n", 0o700)
			}
			f.env = append(f.env, "CI_JUSTFILE="+trusted, "GITHUB_RUN_ID=202")
			directory := t.TempDir()
			input, output, assets := filepath.Join(directory, "provenance.json"), filepath.Join(directory, "output.json"), filepath.Join(directory, "assets")
			data, err := json.Marshal(metadata)
			if err != nil {
				t.Fatal(err)
			}
			write(t, input, string(data), 0o600)
			before := f.snapshot(t)
			logs, err := f.command(t, "just", "--justfile", trusted, "--working-directory", f.dir, "ci-build", input, assets, output)
			if after := f.snapshot(t); !reflect.DeepEqual(before, after) {
				t.Fatal("ci-build mutated candidate repository")
			}
			if scenario == "wrong-source" || scenario == "candidate-error" || scenario == "package-error" {
				if err == nil {
					t.Fatalf("accepted %s: %s", scenario, logs)
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatal("failed build wrote completion metadata")
				}
				return
			}
			if err != nil {
				t.Fatalf("ci-build: %v: %s", err, logs)
			}
			var got struct {
				Source   string `json:"source_sha"`
				Version  string `json:"version"`
				Class    string `json:"artifact_class"`
				Eligible bool   `json:"eligible"`
				Build    int    `json:"build_run_id"`
				Checks   int    `json:"checks_run_id"`
			}
			decode(t, read(t, output), &got)
			eligible := scenario == "master-feature"
			if got.Source != head || got.Eligible != eligible || got.Build != 202 || got.Checks != 101 {
				t.Fatalf("wrong metadata: %+v", got)
			}
			if eligible {
				if got.Version != "0.2.0" || got.Class != "release" {
					t.Fatalf("release classification: %+v", got)
				}
			} else if got.Class != "dev" || got.Version != "0.0.0-dev.202+"+head[:12] || read(t, czLog) != "" {
				t.Fatalf("development classification: %+v, cz calls %q", got, read(t, czLog))
			}
			var manifest struct {
				Source string           `json:"source_sha"`
				Build  int              `json:"build_run_id"`
				Checks int              `json:"checks_run_id"`
				Assets []map[string]any `json:"assets"`
			}
			decode(t, read(t, filepath.Join(assets, "manifest.json")), &manifest)
			if len(manifest.Assets) != 6 || manifest.Source != head || manifest.Build != 202 || manifest.Checks != 101 || read(t, output) != read(t, filepath.Join(assets, "build-metadata.json")) {
				t.Fatal("incomplete manifest/chain metadata")
			}
		})
	}
}

func TestBuildProvenanceRealPRHeadAndSafeOutputs(t *testing.T) {
	for _, scenario := range []string{"fork-pr", "api-id", "foreign-id", "bad-sha", "output-injection", "wrong-repo", "ambiguous-pr", "missing-pr"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			head := strings.Repeat("a", 40)
			prRepo := map[string]any{"id": 17, "name": "mcp-relayd"}
			checks := map[string]any{"id": 101, "path": ".github/workflows/checks.yml", "status": "completed", "conclusion": "success", "event": "pull_request", "head_sha": strings.Repeat("b", 40), "head_branch": "topic", "repository": map[string]any{"full_name": "owner/mcp-relayd"}, "head_repository": map[string]any{"full_name": "fork/mcp-relayd", "id": 17}, "pull_requests": []any{map[string]any{"head": map[string]any{"sha": head, "repo": prRepo}}}}
			metadata := map[string]any{"repository": "owner/mcp-relayd", "head_repository": "fork/mcp-relayd", "head_branch": "topic", "source_sha": head, "checks_run_id": 101, "event": "pull_request"}
			switch scenario {
			case "fork-pr":
				prRepo["full_name"] = "fork/mcp-relayd"
			case "foreign-id":
				prRepo["id"] = 18
			case "bad-sha":
				metadata["source_sha"] = checks["head_sha"]
			case "output-injection":
				checks["head_branch"], metadata["head_branch"] = "topic\neligible=true", "topic\neligible=true"
			case "wrong-repo":
				prRepo["full_name"] = "attacker/other"
			case "ambiguous-pr":
				checks["pull_requests"] = []any{map[string]any{}, map[string]any{}}
			case "missing-pr":
				checks["pull_requests"] = []any{}
			}
			snapshot := map[string]any{"repository": "owner/mcp-relayd", "trigger_run_id": 101, "checks": checks, "metadata": metadata}
			data, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			input := filepath.Join(t.TempDir(), "api.json")
			write(t, input, string(data), 0o600)
			output, err := f.invoke(t, "workflow-provenance", "build", input)
			if scenario != "fork-pr" && scenario != "api-id" {
				if err == nil {
					t.Fatalf("accepted %s: %s", scenario, output)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid fork rejected: %v: %s", err, output)
			}
			var got struct {
				Source string `json:"source_sha"`
				Class  string `json:"artifact_class"`
			}
			decode(t, output, &got)
			if got.Source != head || got.Class != "dev" {
				t.Fatalf("wrong fork provenance: %+v", got)
			}
		})
	}
}
