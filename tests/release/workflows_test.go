//go:build linux

package release_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type workflowContract struct {
	Name        string                 `yaml:"name"`
	On          map[string]any         `yaml:"on"`
	Permissions map[string]string      `yaml:"permissions"`
	Concurrency map[string]any         `yaml:"concurrency"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	If          string            `yaml:"if"`
	RunsOn      string            `yaml:"runs-on"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []workflowStep    `yaml:"steps"`
}

type workflowStep struct {
	ID   string            `yaml:"id"`
	If   string            `yaml:"if"`
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

func loadWorkflow(t *testing.T, name string) workflowContract {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name+".yml")) // #nosec G304 -- fixed workflow names supplied by tests.
	if err != nil {
		t.Fatalf("T-030 requires independent %s.yml: %v", name, err)
	}
	var workflow workflowContract
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatalf("%s YAML: %v", name, err)
	}
	if name == "build" {
		var tree yaml.Node
		if err := yaml.Unmarshal(data, &tree); err != nil {
			t.Fatal(err)
		}
		assertNoAppCredentials(t, &tree)
	}
	if len(workflow.Jobs) == 0 {
		t.Fatal("workflow has no jobs")
	}
	return workflow
}

func assertNoAppCredentials(t *testing.T, node *yaml.Node) {
	t.Helper()
	if node.Kind == yaml.ScalarNode && (strings.Contains(node.Value, "RELEASE_APP") || strings.Contains(node.Value, "create-github-app-token")) {
		t.Error("Build must not reference App credentials in any field")
	}
	for _, child := range node.Content {
		assertNoAppCredentials(t, child)
	}
}

func TestWorkflowsIndependentChain(t *testing.T) {
	for _, stage := range []struct{ file, name, predecessor string }{
		{"checks", "Checks", ""}, {"build", "Build", "Checks"}, {"release", "Release", "Build"},
	} {
		t.Run(stage.file, func(t *testing.T) {
			workflow := loadWorkflow(t, stage.file)
			if workflow.Name != stage.name {
				t.Errorf("name = %q, want %q", workflow.Name, stage.name)
			}
			if stage.predecessor == "" {
				push, ok := workflow.On["push"].(map[string]any)
				if !ok || !onlyStrings(push["branches"], "master") {
					t.Error("Checks must run on push to master")
				}
				if _, ok := workflow.On["pull_request"]; !ok || len(workflow.On) != 2 {
					t.Error("Checks must run on pull_request and push only")
				}
			} else {
				trigger, ok := workflow.On["workflow_run"].(map[string]any)
				if !ok || len(workflow.On) != 1 || !onlyStrings(trigger["workflows"], stage.predecessor) || !onlyStrings(trigger["types"], "completed") {
					t.Error("downstream trigger must be only predecessor workflow_run completed")
				}
				for name, job := range workflow.Jobs {
					requireGuard(t, name, job.If, "github.event.workflow_run.conclusion == 'success'")
					if stage.file == "release" {
						requireGuard(t, name, job.If, "github.event.workflow_run.event == 'workflow_run'", "github.event.workflow_run.head_branch == 'master'", "github.event.workflow_run.head_repository.full_name == github.repository")
					}
				}
			}
		})
	}
}

func onlyStrings(value any, expected string) bool {
	values, ok := value.([]any)
	return ok && len(values) == 1 && values[0] == expected
}

// A conjunction is intentional: an OR could bypass a security precondition.
func requireGuard(t *testing.T, label, guard string, clauses ...string) {
	t.Helper()
	guard = strings.Join(strings.Fields(strings.ReplaceAll(guard, "\"", "'")), " ")
	if strings.Contains(guard, "||") || strings.Contains(guard, "!") {
		t.Errorf("%s guard must be positive conjunction, got %q", label, guard)
	}
	for _, clause := range clauses {
		if !strings.Contains(guard, clause) {
			t.Errorf("%s guard lacks %s", label, clause)
		}
	}
}

func TestWorkflowsLockedEnvironmentAndReadOnlyPRCache(t *testing.T) {
	manual := regexp.MustCompile(`(?m)(\b(apt(-get)?|pip[0-9]*|npm|brew)\s+(install|add)|\b(go|uv)\s+(install|tool\s+install|pip\s+install)|\bnix\s+profile\s+install|\bcurl\b[^\n]*\|\s*(sh|bash)|\b(wget|curl)\b[^\n]*\b(go\.dev|github\.com/[^ ]+/releases))`)
	for _, name := range []string{"checks", "build", "release"} {
		t.Run(name, func(t *testing.T) {
			workflow := loadWorkflow(t, name)
			for id, job := range workflow.Jobs {
				if !strings.HasPrefix(job.RunsOn, "ubuntu-") {
					t.Errorf("%s must use a Linux runner, got %q", id, job.RunsOn)
				}
				var restore, save, recipe bool
				for _, step := range job.Steps {
					if manual.MatchString(executableLines(step.Run)) || strings.Contains(step.Uses, "/setup-go@") || strings.Contains(step.Uses, "/setup-python@") || regexp.MustCompile(`(?:^|[ /])[^\s]+\.sh\b`).MatchString(step.Run) {
						t.Errorf("%s installs ad hoc dependencies or invokes shared shell scripts", id)
					}
					if invokesRecipe(step.Run, "ci-"+name) {
						recipe = true
						if !strings.Contains(step.Run, "devenv shell") {
							t.Error("shared CI recipe must execute inside devenv")
						}
					}
					if strings.HasPrefix(step.Uses, "actions/cache/restore@") || strings.HasPrefix(step.Uses, "actions/cache/save@") {
						key := step.With["key"]
						for _, token := range []string{"runner.os", "runner.arch", "hashFiles(", "devenv.nix", "devenv.yaml", "devenv.lock"} {
							if !strings.Contains(key, token) {
								t.Errorf("Nix cache key lacks %s", token)
							}
						}
						if !strings.Contains(step.With["path"], "/nix") {
							t.Error("cache must include the Nix environment, not only Go outputs")
						}
						if strings.HasPrefix(step.Uses, "actions/cache/restore@") {
							restore = true
						} else {
							save = true
							switch name {
							case "checks":
								requireGuard(t, "cache save", step.If, "github.event_name == 'push'")
							case "release":
								requireGuard(t, "cache save", job.If+" && "+step.If, "github.event.workflow_run.event == 'workflow_run'", "github.event.workflow_run.head_repository.full_name == github.repository", "steps.provenance.outputs.event == 'push'", "steps.provenance.outputs.head_repository == github.repository")
							default:
								requireGuard(t, "cache save", job.If+" && "+step.If, "github.event.workflow_run.event == 'push'", "github.event.workflow_run.head_repository.full_name == github.repository")
							}
						}
					}
				}
				if !restore || !save || !recipe {
					t.Errorf("%s needs explicit Nix cache restore/save and just ci-%s", id, name)
				}
			}
		})
	}
}

func executableLines(script string) string {
	var lines []string
	for line := range strings.SplitSeq(script, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func invokesRecipe(script, recipe string) bool {
	// Trusted devenv runs from trusted/, while just executes in source/. Options
	// select both explicitly; accepting options is not accepting a source justfile.
	return regexp.MustCompile(`\bjust\s+(?:(?:--justfile|--working-directory)\s+(?:"[^"\n]+"|[^\s]+)\s+)*` + regexp.QuoteMeta(recipe) + `\b`).MatchString(executableLines(script))
}

func TestWorkflowsVerifiedSourceAndRunArtifacts(t *testing.T) {
	for _, name := range []string{"build", "release"} {
		t.Run(name, func(t *testing.T) {
			workflow := loadWorkflow(t, name)
			var api, provenance, download, trusted, source bool
			for _, job := range workflow.Jobs {
				if workflow.Permissions["contents"] == "write" || job.Permissions["contents"] == "write" {
					t.Error("GITHUB_TOKEN must not have contents write; publication belongs to the scoped App")
				}
				for _, step := range job.Steps {
					if step.ID == "provenance" {
						api = strings.Contains(executableLines(step.Run), "gh api") && strings.Contains(step.Run, "/actions/runs/")
						provenance = invokesRecipe(step.Run, "workflow-provenance") && strings.Contains(step.Run, "workflow-provenance "+name) && strings.Contains(step.Run, "devenv shell")
					}
					if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
						download = true
						if step.With["run-id"] != "${{ github.event.workflow_run.id }}" || step.With["github-token"] == "" || step.With["repository"] != "${{ github.repository }}" {
							t.Error("download must bind exact triggering run ID and current repository")
						}
					}
					if strings.HasPrefix(step.Uses, "actions/checkout@") {
						if step.With["persist-credentials"] != "false" {
							t.Error("checkout must not persist credentials")
						}
						switch step.With["ref"] {
						case "${{ github.workflow_sha }}":
							trusted = step.With["path"] == "trusted"
						case "${{ steps.provenance.outputs.source_sha }}":
							source = step.With["path"] == "source"
							if name == "release" {
								t.Error("privileged release must not execute candidate source checkout")
							}
						default:
							t.Error("checkout must explicitly select trusted workflow SHA or verified source SHA")
						}
					}
					if strings.Contains(step.Uses, "create-github-app-token") && name == "build" {
						t.Error("Build must never obtain the App token")
					}
					if name == "release" && strings.Contains(step.Run, "just ") && !strings.Contains(step.Run, "--justfile trusted/justfile") {
						t.Error("privileged recipes must use trusted/justfile, not downloaded/source code")
					}
				}
			}
			if !api || !provenance || !download || !trusted || (name == "build" && !source) {
				t.Errorf("missing API provenance, exact-run artifacts or explicit trusted/source checkout: api=%v provenance=%v download=%v trusted=%v source=%v", api, provenance, download, trusted, source)
			}
		})
	}
}

func TestWorkflowsMetadataClassificationAndSerializedRelease(t *testing.T) {
	checks := loadWorkflow(t, "checks")
	var metadata, testedCheckout bool
	for _, job := range checks.Jobs {
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/checkout@") {
				ref := step.With["ref"]
				testedCheckout = strings.Contains(ref, "github.event.pull_request.head.sha") && strings.Contains(ref, "github.sha") && step.With["persist-credentials"] == "false"
			}
			if strings.HasPrefix(step.Uses, "actions/upload-artifact@") && step.With["name"] == "checks-metadata" && step.With["path"] == "checks-metadata.json" {
				metadata = true
			}
			if strings.Contains(executableLines(step.Run), "cz bump") {
				t.Error("Checks must validate commits, not require a version bump")
			}
		}
	}
	if !metadata {
		t.Error("Checks must publish only its small checks-metadata.json provenance artifact")
	}
	if !testedCheckout {
		t.Error("Checks must explicitly checkout PR head SHA or push SHA, never implicit PR merge HEAD")
	}
	build := loadWorkflow(t, "build")
	var classified bool
	for _, job := range build.Jobs {
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/upload-artifact@") && strings.Contains(step.With["name"], "steps.provenance.outputs.artifact_class") && strings.Contains(step.With["name"], "github.run_id") {
				classified = true
			}
		}
	}
	if !classified {
		t.Error("Build artifacts must include verified dev/release classification and run ID")
	}
	release := loadWorkflow(t, "release")
	group, ok := release.Concurrency["group"].(string)
	if !ok || group == "" || strings.Contains(group, "run_id") || strings.Contains(group, "sha") || strings.Contains(group, "ref") || release.Concurrency["cancel-in-progress"] != false {
		t.Error("Release needs a stable repository-wide concurrency group and cancel-in-progress: false")
	}
	var app bool
	for _, job := range release.Jobs {
		for _, step := range job.Steps {
			if strings.HasPrefix(step.Uses, "actions/create-github-app-token@") {
				app = true
				if step.With["repositories"] != "${{ github.event.repository.name }}" || step.With["owner"] != "${{ github.repository_owner }}" {
					t.Error("App token must be scoped to this repository, not all installation repositories")
				}
			}
		}
	}
	if !app {
		t.Error("Release must obtain a repository-scoped App token")
	}
}

// Snapshots mirror the selected fields returned by the GitHub runs API.
// The future shared recipe is exercised, not a test-only provenance oracle.
func TestWorkflowProvenanceRejectsForgedChain(t *testing.T) {
	requireWorkflowRecipe(t, "workflow-provenance")
	for _, mode := range []string{"build", "release"} {
		for _, mutation := range []string{"valid", "no-bump", "failed-checks", "pending-checks", "foreign-repository", "wrong-workflow", "wrong-run", "different-sha", "pr-merge-sha", "missing-metadata", "failed-build", "wrong-checks-link", "wrong-build-link", "foreign-build", "forged-event", "pr", "non-master", "foreign-head", "checks-attempt", "build-attempt", "build-sha", "recorded-trigger"} {
			t.Run(mode+"/"+mutation, func(t *testing.T) {
				f := newFixture(t)
				checks := map[string]any{"id": 101, "path": ".github/workflows/checks.yml", "status": "completed", "conclusion": "success", "event": "push", "head_sha": strings.Repeat("a", 40), "head_branch": "master", "repository": map[string]any{"full_name": "owner/mcp-relayd"}, "head_repository": map[string]any{"full_name": "owner/mcp-relayd"}}
				build := map[string]any{"id": 202, "path": ".github/workflows/build.yml", "status": "completed", "conclusion": "success", "event": "workflow_run", "head_sha": strings.Repeat("b", 40), "repository": map[string]any{"full_name": "owner/mcp-relayd"}}
				metadata := map[string]any{"checks_run_id": 101, "build_run_id": 202, "source_sha": strings.Repeat("a", 40), "event": "push", "repository": "owner/mcp-relayd", "head_repository": "owner/mcp-relayd", "head_branch": "master", "artifact_class": "release", "eligible": true}
				snapshot := map[string]any{"repository": "owner/mcp-relayd", "trigger_run_id": 101, "checks": checks, "build": build, "metadata": metadata}
				if mode == "release" {
					snapshot["trigger_run_id"] = 202
					snapshot["require_chain_binding"] = true
				}
				checks["run_attempt"], build["run_attempt"] = 1, 1
				metadata["trigger_checks_run_id"], metadata["checks_run_attempt"], metadata["build_run_attempt"], metadata["build_head_sha"] = 101, 1, 1, strings.Repeat("b", 40)
				switch mutation {
				case "checks-attempt":
					metadata["checks_run_attempt"] = 2
				case "build-attempt":
					metadata["build_run_attempt"] = 2
				case "build-sha":
					metadata["build_head_sha"] = strings.Repeat("c", 40)
				case "recorded-trigger":
					metadata["trigger_checks_run_id"] = 999
				case "no-bump":
					metadata["artifact_class"], metadata["eligible"] = "dev", false
				case "failed-checks":
					checks["conclusion"] = "failure"
				case "pending-checks":
					checks["status"] = "in_progress"
				case "foreign-repository":
					checks["repository"] = map[string]any{"full_name": "attacker/other"}
				case "wrong-workflow":
					checks["path"] = ".github/workflows/other.yml"
				case "wrong-run":
					snapshot["trigger_run_id"] = 999
				case "different-sha":
					metadata["source_sha"] = strings.Repeat("c", 40)
				case "pr-merge-sha":
					checks["event"], metadata["event"] = "pull_request", "pull_request"
					checks["pull_requests"] = []any{map[string]any{"head": map[string]any{"sha": strings.Repeat("c", 40), "repo": map[string]any{"full_name": "owner/mcp-relayd"}}}}
					metadata["artifact_class"], metadata["eligible"] = "dev", false
				case "missing-metadata":
					delete(snapshot, "metadata")
				case "failed-build":
					build["conclusion"] = "failure"
				case "wrong-checks-link":
					metadata["checks_run_id"] = 999
				case "wrong-build-link":
					metadata["build_run_id"] = 999
				case "foreign-build":
					build["repository"] = map[string]any{"full_name": "attacker/other"}
				case "forged-event":
					metadata["event"] = "pull_request"
				case "pr":
					checks["event"], metadata["event"] = "pull_request", "pull_request"
					checks["head_sha"] = strings.Repeat("b", 40)
					checks["pull_requests"] = []any{map[string]any{"head": map[string]any{"sha": strings.Repeat("a", 40), "repo": map[string]any{"full_name": "owner/mcp-relayd"}}}}
					metadata["artifact_class"], metadata["eligible"] = "dev", false
				case "non-master":
					checks["head_branch"], metadata["head_branch"] = "topic", "topic"
					metadata["artifact_class"], metadata["eligible"] = "dev", false
				case "foreign-head":
					checks["head_repository"] = map[string]any{"full_name": "attacker/other"}
				}
				input := filepath.Join(t.TempDir(), "runs.json")
				data, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				write(t, input, string(data), 0o600)
				output, err := f.invoke(t, "workflow-provenance", mode, input)
				accepted := mutation == "valid" || mutation == "no-bump" || (mode == "build" && (mutation == "pr" || mutation == "non-master" || mutation == "failed-build" || mutation == "foreign-build" || mutation == "wrong-build-link" || mutation == "checks-attempt" || mutation == "build-attempt" || mutation == "build-sha" || mutation == "recorded-trigger"))
				if !accepted {
					if err == nil {
						t.Fatalf("accepted forged/ineligible chain: %s", output)
					}
					return
				}
				if err != nil {
					t.Fatalf("valid chain rejected: %v: %s", err, output)
				}
				var got struct {
					Source string `json:"source_sha"`
					Class  string `json:"artifact_class"`
				}
				decode(t, output, &got)
				wantClass := "release"
				if mutation == "pr" || mutation == "non-master" || mutation == "no-bump" {
					wantClass = "dev"
				}
				if got.Source != strings.Repeat("a", 40) || got.Class != wantClass {
					t.Errorf("verified provenance = %+v", got)
				}
			})
		}
	}
}

func TestWorkflowChecksNewCommitRangeAndSquashTitle(t *testing.T) {
	requireWorkflowRecipe(t, "ci-commits")
	for _, tc := range []struct {
		name, subject, title string
		valid                bool
	}{
		{"docs-no-bump", "docs: explain usage", "docs: explain usage", true},
		{"feature", "feat(api): add route", "feat(api): add route", true},
		{"bad-new-commit", "unstructured subject", "fix: close stream", false},
		{"bad-squash-title", "fix: close stream", "unstructured title", false},
		{"fake-merge", "Merge arbitrary text", "fix: close stream", false},
		{"fixup-not-conventional", "fixup! fix: close stream", "fix: close stream", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.git(t, "commit", "--allow-empty", "-m", "historical non-conventional subject")
			base := f.git(t, "rev-parse", "HEAD")
			f.git(t, "commit", "--allow-empty", "-m", tc.subject)
			head := f.git(t, "rev-parse", "HEAD")
			output, err := f.invoke(t, "ci-commits", base, head, tc.title)
			if !tc.valid {
				if err == nil {
					t.Fatalf("invalid new commit/title accepted: %s", output)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid new range rejected (historical commits must be excluded): %v: %s", err, output)
			}
			var got struct {
				Verified bool   `json:"verified"`
				Source   string `json:"source_sha"`
			}
			decode(t, output, &got)
			if !got.Verified || got.Source != head {
				t.Errorf("commit validation metadata = %+v, want verified SHA %s", got, head)
			}
		})
	}
}

func requireWorkflowRecipe(t *testing.T, recipe string) {
	t.Helper()
	f := newFixture(t)
	if output, err := f.command(t, "just", "--show", recipe); err != nil {
		t.Fatalf("T-030 requires shared recipe %s: %v: %s", recipe, err, output)
	}
}
