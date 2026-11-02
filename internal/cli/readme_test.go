package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find module root containing go.mod")
		}
		dir = parent
	}
}

func TestREADMECLIContract(t *testing.T) {
	root := findModuleRoot(t)
	readmePath := filepath.Join(root, "README.md")
	contentBytes, err := os.ReadFile(readmePath) // #nosec G304 -- README path is the repository file found by walking to go.mod, not caller input.
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readme := string(contentBytes)

	// Required English statements / case-sensitive substrings:
	requiredSubstrings := []string{
		"mcp-relayd run",
		"--config",
		"--help",
		"--version",
		"--verbose",
		"mcp-relayd config",
		"scripts/install.sh",
		"scripts/install.ps1",
		"greeting",
		"Local:",
	}
	for _, sub := range requiredSubstrings {
		if !strings.Contains(readme, sub) {
			t.Errorf("README.md missing required substring: %q", sub)
		}
	}

	// Sentence requirement: JSON diagnostic logs appear only with --verbose.
	if (!strings.Contains(readme, "JSON") || !strings.Contains(readme, "diagnostic logs") || !strings.Contains(readme, "verbose")) ||
		(!strings.Contains(readme, "only with `--verbose`") && !strings.Contains(readme, "only with --verbose")) {
		t.Errorf("README.md missing sentence documenting that JSON diagnostic logs appear only with --verbose")
	}

	// Obsolete claims that must be absent:
	if strings.Contains(readme, "does not generate a configuration file") {
		t.Errorf("README.md contains obsolete claim: does not generate a configuration file")
	}
	if strings.Contains(readme, "written as structured JSON logs to stderr") ||
		strings.Contains(readme, "Startup and bridge errors are written as structured JSON logs to stderr") {
		t.Errorf("README.md contains obsolete claim: startup and bridge errors are written as structured JSON logs to stderr")
	}
}
