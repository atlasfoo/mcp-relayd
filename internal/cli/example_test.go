package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedExampleMatchesSource(t *testing.T) {
	root := findModuleRoot(t)
	sourcePath := filepath.Join(root, "examples", "mcp-relayd.toml")
	expected, err := os.ReadFile(sourcePath) // #nosec G304 -- test reads static example configuration file
	if err != nil {
		t.Fatalf("read example configuration file %q: %v", sourcePath, err)
	}
	if !bytes.Equal(exampleToml, expected) {
		t.Errorf("embedded example in internal/cli does not match %q", sourcePath)
	}
}
