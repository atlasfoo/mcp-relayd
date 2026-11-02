package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func greetingBytes(t *testing.T) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "internal", "cli", "greeting.txt")) // #nosec G304 -- path is the repository greeting, not caller input.
	if err != nil {
		t.Fatalf("read greeting: %v", err)
	}
	return content
}

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

func TestRunUsesExplicitConfigBeforeRunCommand(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeFile(t, filepath.Join(home, ".config", "mcp-relayd.toml"), "[gateway]\nport = 70000\n")
	explicitConfig := filepath.Join(t.TempDir(), "custom.toml")
	writeFile(t, explicitConfig, "[gateway]\nport = 0\n")

	var stdout, stderr bytes.Buffer
	err := run([]string{"--config", explicitConfig, "run"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "gateway.port") {
		t.Fatalf("run() error = %v, want validation error from explicit config", err)
	}
	if strings.Contains(err.Error(), "70000") {
		t.Errorf("run() error indicates global config was selected: %v", err)
	}
}

func TestRunHelpVariantsDoNotRequireConfig(t *testing.T) {
	testCases := []struct {
		name string
		args []string
	}{
		{name: "no arguments", args: []string{}},
		{name: "help flag", args: []string{"--help"}},
		{name: "run help flag", args: []string{"run", "--help"}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			setHome(t, t.TempDir())
			var stdout, stderr bytes.Buffer
			if err := run(tc.args, &stdout, &stderr); err != nil {
				t.Fatalf("run(%v) error = %v, want nil", tc.args, err)
			}
			if !strings.Contains(stdout.String(), "--config") || !strings.Contains(stdout.String(), "--help") {
				t.Errorf("run(%v) output = %q, want usage documenting --config and --help", tc.args, stdout.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("run(%v) wrote to stderr: %q", tc.args, stderr.String())
			}
		})
	}
}

func TestRunVersionVariantsOutputFormat(t *testing.T) {
	testCases := []struct {
		name string
		args []string
	}{
		{name: "version flag", args: []string{"--version"}},
		{name: "run version flag", args: []string{"run", "--version"}},
	}
	expected := fmt.Sprintf("mcp-relayd %s %s\n", version, sourceSHA)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			setHome(t, t.TempDir())
			var stdout, stderr bytes.Buffer
			if err := run(tc.args, &stdout, &stderr); err != nil {
				t.Fatalf("run(%v) error = %v, want nil", tc.args, err)
			}
			got := stdout.Bytes()
			greeting := greetingBytes(t)
			if !bytes.HasPrefix(got, greeting) || string(got[len(greeting):]) != expected {
				t.Errorf("run(%v) stdout = %q, want greeting plus exact %q", tc.args, stdout.String(), expected)
			}
			if stderr.Len() != 0 {
				t.Errorf("run(%v) wrote to stderr: %q", tc.args, stderr.String())
			}
		})
	}
}

func TestRunVerboseWithHelpAndVersion(t *testing.T) {
	expectedVersion := fmt.Sprintf("mcp-relayd %s %s\n", version, sourceSHA)
	testCases := []struct {
		name        string
		args        []string
		wantVersion bool
	}{
		{name: "verbose before help", args: []string{"--verbose", "--help"}, wantVersion: false},
		{name: "verbose after help", args: []string{"--help", "--verbose"}, wantVersion: false},
		{name: "run verbose help", args: []string{"run", "--verbose", "--help"}, wantVersion: false},
		{name: "run help verbose", args: []string{"run", "--help", "--verbose"}, wantVersion: false},
		{name: "verbose run help", args: []string{"--verbose", "run", "--help"}, wantVersion: false},
		{name: "verbose before version", args: []string{"--verbose", "--version"}, wantVersion: true},
		{name: "verbose after version", args: []string{"--version", "--verbose"}, wantVersion: true},
		{name: "run verbose version", args: []string{"run", "--verbose", "--version"}, wantVersion: true},
		{name: "run version verbose", args: []string{"run", "--version", "--verbose"}, wantVersion: true},
		{name: "verbose run version", args: []string{"--verbose", "run", "--version"}, wantVersion: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			setHome(t, t.TempDir())
			var stdout, stderr bytes.Buffer
			if err := run(tc.args, &stdout, &stderr); err != nil {
				t.Fatalf("run(%v) error = %v, want nil", tc.args, err)
			}
			if stderr.Len() != 0 {
				t.Errorf("run(%v) wrote to stderr: %q, want empty", tc.args, stderr.String())
			}
			if tc.wantVersion {
				got := stdout.Bytes()
				greeting := greetingBytes(t)
				if !bytes.HasPrefix(got, greeting) || string(got[len(greeting):]) != expectedVersion {
					t.Errorf("run(%v) stdout = %q, want greeting plus exact %q", tc.args, stdout.String(), expectedVersion)
				}
			} else if !strings.Contains(stdout.String(), "--config") || !strings.Contains(stdout.String(), "--help") {
				t.Errorf("run(%v) output = %q, want usage documenting --config and --help", tc.args, stdout.String())
			}
		})
	}
}

func TestRunInvalidInvocationsRejectAndDoNotStartProcess(t *testing.T) {
	const sentinel = "SECRET_SENTINEL_ARG_98765"
	home := t.TempDir()
	setHome(t, home)
	marker := filepath.Join(t.TempDir(), "started")
	command, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable(): %v", err)
	}
	configPath := filepath.Join(home, ".config", "mcp-relayd.toml")
	configText := "[gateway]\nport = 8765\n\n[servers.alpha]\ncommand = " + strconv.Quote(command) +
		"\nargs = [\"-test.run=TestCLIStartupSideEffectHelper\"]\n\n[servers.alpha.env]\n" +
		"MCP_RELAYD_TEST_MARKER = " + strconv.Quote(marker) + "\n"
	writeFile(t, configPath, configText)

	testCases := []struct {
		name string
		args []string
	}{
		{name: "unknown command", args: []string{"unknown-" + sentinel}},
		{name: "unknown command with run", args: []string{"bogus-" + sentinel, "run"}},
		{name: "literal completion bare", args: []string{"completion"}},
		{name: "literal completion with sentinel arg", args: []string{"completion", sentinel}},
		{name: "extra positional after run", args: []string{"run", "extra-positional-" + sentinel}},
		{name: "invalid flag leading", args: []string{"--invalid-flag-" + sentinel}},
		{name: "invalid flag after run", args: []string{"run", "--invalid-flag=" + sentinel}},
		{name: "unsupported short -v flag", args: []string{"-v"}},
		{name: "unsupported short -v flag after run", args: []string{"run", "-v"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(tc.args, &stdout, &stderr)
			if err == nil {
				t.Fatalf("run(%v) error = nil, want non-nil error", tc.args)
			}
			combined := stdout.String() + stderr.String() + err.Error()
			if strings.Contains(combined, sentinel) {
				t.Errorf("run(%v) echoed sentinel in output or error: %q", tc.args, combined)
			}
			if !strings.Contains(combined, "--help") {
				t.Errorf("run(%v) output and error did not point to --help: %q", tc.args, combined)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Errorf("run(%v) started configured process (marker stat err = %v)", tc.args, statErr)
			}
		})
	}
}

func TestRunSequentialInvocationsDoNotLeakState(t *testing.T) {
	t.Run("invalid call followed by valid help", func(t *testing.T) {
		setHome(t, t.TempDir())
		var out1, err1 bytes.Buffer
		if err := run([]string{"--invalid-flag"}, &out1, &err1); err == nil {
			t.Fatal("first run() error = nil, want invalid flag error")
		}

		var out2, err2 bytes.Buffer
		if err := run([]string{"--help"}, &out2, &err2); err != nil {
			t.Fatalf("second run(--help) error = %v, want nil", err)
		}
		if !strings.Contains(out2.String(), "--help") || !strings.Contains(out2.String(), "--config") {
			t.Errorf("second run(--help) output = %q, want usage", out2.String())
		}
		if err2.Len() != 0 {
			t.Errorf("second run(--help) wrote to stderr: %q", err2.String())
		}
	})

	t.Run("valid help followed by explicit config", func(t *testing.T) {
		home := t.TempDir()
		setHome(t, home)
		writeFile(t, filepath.Join(home, ".config", "mcp-relayd.toml"), "[gateway]\nport = 70000\n")
		explicitConfig := filepath.Join(t.TempDir(), "custom.toml")
		writeFile(t, explicitConfig, "[gateway]\nport = 0\n")

		var out1, err1 bytes.Buffer
		if err := run([]string{"--help"}, &out1, &err1); err != nil {
			t.Fatalf("first run(--help) error = %v, want nil", err)
		}

		var out2, err2 bytes.Buffer
		err := run([]string{"run", "--config", explicitConfig}, &out2, &err2)
		if err == nil || !strings.Contains(err.Error(), "gateway.port") {
			t.Fatalf("second run() error = %v, want validation error from explicit config", err)
		}
		if strings.Contains(err.Error(), "70000") {
			t.Errorf("second run() error indicates global config was selected: %v", err)
		}
	})

	t.Run("verbose help followed by explicit config", func(t *testing.T) {
		home := t.TempDir()
		setHome(t, home)
		writeFile(t, filepath.Join(home, ".config", "mcp-relayd.toml"), "[gateway]\nport = 70000\n")
		explicitConfig := filepath.Join(t.TempDir(), "custom.toml")
		writeFile(t, explicitConfig, "[gateway]\nport = 0\n")

		var out1, err1 bytes.Buffer
		if err := run([]string{"--verbose", "--help"}, &out1, &err1); err != nil {
			t.Fatalf("first run(--verbose, --help) error = %v, want nil", err)
		}

		var out2, err2 bytes.Buffer
		err := run([]string{"run", "--config", explicitConfig}, &out2, &err2)
		if err == nil || !strings.Contains(err.Error(), "gateway.port") {
			t.Fatalf("second run() error = %v, want validation error from explicit config", err)
		}
		if strings.Contains(err.Error(), "70000") {
			t.Errorf("second run() error indicates global config was selected: %v", err)
		}
	})
}

func TestProcessExit(t *testing.T) {
	ctx := context.Background()

	t.Run("help returns zero and empty stderr", func(t *testing.T) {
		setHome(t, t.TempDir())
		var stdout, stderr bytes.Buffer
		code := exitCode(ctx, []string{"--help"}, &stdout, &stderr)
		if code != 0 {
			t.Errorf("exitCode(--help) = %d, want 0", code)
		}
		if stderr.Len() != 0 {
			t.Errorf("exitCode(--help) stderr = %q, want empty", stderr.String())
		}
		if stdout.Len() == 0 {
			t.Errorf("exitCode(--help) stdout is empty, want non-empty")
		}
	})

	t.Run("invalid flag returns one without echoing sentinel or JSON", func(t *testing.T) {
		setHome(t, t.TempDir())
		const sentinel = "SECRET_SENTINEL_FLAG_12345"
		var stdout, stderr bytes.Buffer
		code := exitCode(ctx, []string{"--" + sentinel}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("exitCode(--%s) = %d, want 1", sentinel, code)
		}
		combined := stdout.String() + stderr.String()
		if strings.Contains(combined, sentinel) {
			t.Errorf("output echoed sentinel: %q", combined)
		}
		lines := strings.Split(strings.TrimRight(stderr.String(), "\r\n"), "\n")
		if len(lines) != 1 || lines[0] == "" {
			t.Fatalf("stderr lines = %v (raw %q), want exactly one non-empty line", lines, stderr.String())
		}
		if !strings.Contains(lines[0], "--help") {
			t.Errorf("stderr line %q does not contain --help", lines[0])
		}
		if strings.HasPrefix(strings.TrimSpace(lines[0]), "{") {
			t.Errorf("stderr line %q starts with {, want non-JSON", lines[0])
		}
	})

	t.Run("invalid config normal mode returns one with single non-json explanation", func(t *testing.T) {
		home := t.TempDir()
		setHome(t, home)
		const sentinel = "must-not-appear-in-errors"
		configPath := filepath.Join(home, ".config", "mcp-relayd.toml")
		configText := "[gateway]\nport = 70000\n\n[servers.alpha.env]\nTOKEN = \"" + sentinel + "\"\n"
		writeFile(t, configPath, configText)

		var stdout, stderr bytes.Buffer
		code := exitCode(ctx, []string{"run"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("exitCode(run) = %d, want 1", code)
		}
		combined := stdout.String() + stderr.String()
		if strings.Contains(combined, sentinel) {
			t.Errorf("output echoed sentinel %q: %q", sentinel, combined)
		}
		lines := strings.Split(strings.TrimRight(stderr.String(), "\r\n"), "\n")
		if len(lines) != 1 || lines[0] == "" {
			t.Fatalf("stderr lines = %v (raw %q), want exactly one line", lines, stderr.String())
		}
		if strings.HasPrefix(strings.TrimSpace(lines[0]), "{") {
			t.Errorf("stderr line %q is JSON, want non-JSON", lines[0])
		}
		if strings.Contains(stderr.String(), "daemon stopped with error") {
			t.Errorf("stderr contains second line/message %q", "daemon stopped with error")
		}
	})

	t.Run("invalid config verbose mode returns one with single json line", func(t *testing.T) {
		home := t.TempDir()
		setHome(t, home)
		const sentinel = "must-not-appear-in-errors"
		configPath := filepath.Join(home, ".config", "mcp-relayd.toml")
		configText := "[gateway]\nport = 70000\n\n[servers.alpha.env]\nTOKEN = \"" + sentinel + "\"\n"
		writeFile(t, configPath, configText)

		var stdout, stderr bytes.Buffer
		code := exitCode(ctx, []string{"--verbose", "run"}, &stdout, &stderr)
		if code != 1 {
			t.Errorf("exitCode(--verbose, run) = %d, want 1", code)
		}
		combined := stdout.String() + stderr.String()
		if strings.Contains(combined, sentinel) {
			t.Errorf("output echoed sentinel %q: %q", sentinel, combined)
		}
		lines := strings.Split(strings.TrimRight(stderr.String(), "\r\n"), "\n")
		if len(lines) != 1 || lines[0] == "" {
			t.Fatalf("stderr lines = %v (raw %q), want exactly one line", lines, stderr.String())
		}
		if !strings.HasPrefix(strings.TrimSpace(lines[0]), "{") {
			t.Errorf("stderr line %q is not JSON, want JSON line", lines[0])
		}
	})
}
