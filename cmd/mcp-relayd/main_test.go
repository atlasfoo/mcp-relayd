package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRunUsesGlobalConfigInsteadOfWorkingDirectory(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	globalConfig := filepath.Join(home, ".config", "mcp-relayd.toml")
	writeFile(t, globalConfig, "[gateway]\nport = 70000\n")

	workingDir := t.TempDir()
	writeFile(t, filepath.Join(workingDir, "mcp-relayd.toml"), "[gateway]\nport = 8765\n")
	changeWorkingDirectory(t, workingDir)

	var stdout, stderr bytes.Buffer
	err := run([]string{"run"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "gateway.port") {
		t.Fatalf("run() error = %v, want validation error from global config", err)
	}
	if strings.Contains(err.Error(), "8765") {
		t.Errorf("run() error indicates cwd config was selected: %v", err)
	}
}

func TestRunUsesExplicitConfigOverride(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeFile(t, filepath.Join(home, ".config", "mcp-relayd.toml"), "[gateway]\nport = 70000\n")
	explicitConfig := filepath.Join(t.TempDir(), "custom.toml")
	writeFile(t, explicitConfig, "[gateway]\nport = 0\n")

	var stdout, stderr bytes.Buffer
	err := run([]string{"run", "--config", explicitConfig}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "gateway.port") {
		t.Fatalf("run() error = %v, want validation error from explicit config", err)
	}
	if strings.Contains(err.Error(), "70000") {
		t.Errorf("run() error indicates global config was selected: %v", err)
	}
}

func TestRunHelpDoesNotRequireConfig(t *testing.T) {
	setHome(t, t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := run([]string{"run", "--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(--help) error = %v, want nil", err)
	}
	if !strings.Contains(stdout.String(), "--config") || !strings.Contains(stdout.String(), "--help") {
		t.Errorf("run(--help) output = %q, want usage documenting --config and --help", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("run(--help) wrote to stderr: %q", stderr.String())
	}
}

func TestRunVersionDoesNotRequireConfig(t *testing.T) {
	setHome(t, t.TempDir())
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run(--version) error = %v, want nil", err)
	}
	if !strings.Contains(stdout.String(), "mcp-relayd") {
		t.Errorf("run(--version) output = %q, want product name", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("run(--version) wrote to stderr: %q", stderr.String())
	}
}

func TestRunMissingGlobalConfigDoesNotFallBackToWorkingDirectory(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	workingDir := t.TempDir()
	writeFile(t, filepath.Join(workingDir, "mcp-relayd.toml"), "[gateway]\nport = 8765\n")
	changeWorkingDirectory(t, workingDir)

	var stdout, stderr bytes.Buffer
	err := run([]string{"run"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want missing global configuration error")
	}
	expectedPath := filepath.Join(home, ".config", "mcp-relayd.toml")
	if !strings.Contains(err.Error(), expectedPath) {
		t.Errorf("run() error = %v, want expected path %q", err, expectedPath)
	}
}

func TestRunInvalidConfigDoesNotStartConfiguredCommand(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	marker := filepath.Join(t.TempDir(), "started")
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable(): %v", err)
	}
	configPath := filepath.Join(home, ".config", "mcp-relayd.toml")
	configText := "[gateway]\nport = 70000\n\n[servers.alpha]\ncommand = " + strconv.Quote(command) +
		"\nargs = [\"-test.run=TestCLIStartupSideEffectHelper\"]\n\n[servers.alpha.env]\n" +
		"MCP_RELAYD_TEST_MARKER = " + strconv.Quote(marker) +
		"\nTOKEN = \"must-not-appear-in-errors\"\n"
	writeFile(t, configPath, configText)

	var stdout, stderr bytes.Buffer
	err = run([]string{"run"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "gateway.port") {
		t.Fatalf("run() error = %v, want validation error", err)
	}
	output := err.Error() + stdout.String() + stderr.String()
	if strings.Contains(output, "must-not-appear-in-errors") {
		t.Errorf("run() exposed a configured environment value: %q", output)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Errorf("configured command ran before validation completed (marker stat error = %v)", statErr)
	}
}

func TestCLIStartupSideEffectHelper(t *testing.T) {
	marker := os.Getenv("MCP_RELAYD_TEST_MARKER")
	if marker == "" {
		return
	}
	// #nosec G703 -- The parent test supplies a path under its own TempDir.
	if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
		t.Fatalf("write startup marker: %v", err)
	}
}

func setHome(t *testing.T, home string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
		return
	}
	t.Setenv("HOME", home)
}

func changeWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd(): %v", err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatalf("os.Chdir(%q): %v", directory, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create directory for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}
