package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"testing"

	"mcp-relayd/internal/config"
)

func expectedGreeting(t *testing.T) []byte {
	t.Helper()
	root := findModuleRoot(t)
	cliGreeting := filepath.Join(root, "internal", "cli", "greeting.txt")
	if data, err := os.ReadFile(cliGreeting); err == nil { // #nosec G304 -- test reads static greeting file
		return data
	}
	rootGreeting := filepath.Join(root, "greeting.txt")
	data, err := os.ReadFile(rootGreeting) // #nosec G304 -- test reads static greeting file
	if err != nil {
		t.Fatalf("read greeting file: %v", err)
	}
	return data
}

func expectedExampleConfig(t *testing.T) []byte {
	t.Helper()
	root := findModuleRoot(t)
	examplePath := filepath.Join(root, "examples", "mcp-relayd.toml")
	data, err := os.ReadFile(examplePath) // #nosec G304 -- test reads static example configuration file
	if err != nil {
		t.Fatalf("read example configuration file %q: %v", examplePath, err)
	}
	return data
}

func TestConfigCommandSuccess(t *testing.T) {
	testCases := []struct {
		name       string
		args       []string
		customPath string
	}{
		{
			name: "default destination creates parent and writes example",
			args: []string{"config"},
		},
		{
			name:       "config flag after config does not redirect write",
			args:       nil, // populated dynamically with customPath
			customPath: "redirect-after.toml",
		},
		{
			name:       "config flag before config does not redirect write",
			args:       nil, // populated dynamically with customPath
			customPath: "redirect-before.toml",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)

			configDir := filepath.Join(home, ".config")
			if _, err := os.Stat(configDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected .config directory to not exist yet, got err = %v", err)
			}

			var otherPath string
			args := tc.args
			if tc.customPath != "" {
				otherPath = filepath.Join(t.TempDir(), tc.customPath)
				if tc.name == "config flag after config does not redirect write" {
					args = []string{"config", "--config", otherPath}
				} else {
					args = []string{"--config", otherPath, "config"}
				}
			}

			runtimeCalled := false
			injectRuntime(t, func(_ context.Context, _ config.Config, _ *slog.Logger) error {
				runtimeCalled = true
				return nil
			})

			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), args, &stdout, &stderr, "v1.0.0", "testsha")
			if err != nil {
				t.Fatalf("Execute(%v) error = %v, want nil", args, err)
			}

			if runtimeCalled {
				t.Errorf("runtime was called for %v, want no process started", args)
			}

			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}

			wantGreeting := expectedGreeting(t)
			if !bytes.HasPrefix(stdout.Bytes(), wantGreeting) {
				t.Errorf("stdout does not start with greeting bytes:\ngot:\n%s", stdout.String())
			}

			destPath := filepath.Join(home, ".config", "mcp-relayd.toml")
			writtenBytes, readErr := os.ReadFile(destPath) // #nosec G304 -- test verifies created configuration file
			if readErr != nil {
				t.Fatalf("read created config %q: %v", destPath, readErr)
			}

			wantConfig := expectedExampleConfig(t)
			if !bytes.Equal(writtenBytes, wantConfig) {
				t.Errorf("written config at %q does not match examples/mcp-relayd.toml exactly", destPath)
			}

			if otherPath != "" {
				if _, statErr := os.Stat(otherPath); !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("--config destination %q was unexpectedly written to (err = %v)", otherPath, statErr)
				}
			}
		})
	}
}

func TestConfigCommandFileExists(t *testing.T) {
	testCases := []struct {
		name    string
		args    []string
		verbose bool
	}{
		{name: "normal mode", args: []string{"config"}, verbose: false},
		{name: "verbose before config", args: []string{"--verbose", "config"}, verbose: true},
		{name: "verbose after config", args: []string{"config", "--verbose"}, verbose: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)

			targetPath := filepath.Join(home, ".config", "mcp-relayd.toml")
			existingContent := fmt.Sprintf("[gateway]\nport = 5555\nsentinel = %q\n", secretSentinel)
			writeConfigFile(t, targetPath, existingContent)

			runtimeCalled := false
			injectRuntime(t, func(_ context.Context, _ config.Config, _ *slog.Logger) error {
				runtimeCalled = true
				return nil
			})

			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), tc.args, &stdout, &stderr, "v1.0.0", "testsha")
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want error when target file already exists", tc.args)
			}
			if errors.Is(err, ErrUsage) {
				t.Fatalf("Execute(%v) returned ErrUsage, want operational config error: %v", tc.args, err)
			}

			if runtimeCalled {
				t.Errorf("runtime was called for %v, want no process started", tc.args)
			}

			afterBytes, readErr := os.ReadFile(targetPath) // #nosec G304 -- test verifies existing configuration file
			if readErr != nil {
				t.Fatalf("read config %q: %v", targetPath, readErr)
			}
			if string(afterBytes) != existingContent {
				t.Errorf("existing config file was modified: got %q, want %q", string(afterBytes), existingContent)
			}

			stderrTrimmed := strings.TrimRight(stderr.String(), "\r\n")
			if stderrTrimmed == "" {
				t.Fatalf("stderr is empty, want exactly 1 formatted line")
			}
			lines := strings.Split(stderrTrimmed, "\n")
			if len(lines) != 1 {
				t.Fatalf("stderr lines count = %d (%q), want exactly 1 line", len(lines), stderr.String())
			}

			if strings.Contains(stderr.String(), secretSentinel) {
				t.Errorf("stderr contains secret sentinel from file body: %q", stderr.String())
			}
			if strings.Contains(stderr.String(), "[gateway]") || strings.Contains(stderr.String(), "port = 5555") {
				t.Errorf("stderr contains existing file body: %q", stderr.String())
			}
			if strings.Contains(stderr.String(), "mcp-proxy") {
				t.Errorf("stderr contains example config file body: %q", stderr.String())
			}

			if !tc.verbose {
				if strings.HasPrefix(strings.TrimSpace(lines[0]), "{") {
					t.Errorf("stderr is JSON in normal mode: %q", lines[0])
				}
			} else {
				var rec map[string]any
				if jsonErr := json.Unmarshal([]byte(lines[0]), &rec); jsonErr != nil {
					t.Errorf("stderr is not valid JSON in verbose mode: %v (line: %q)", jsonErr, lines[0])
				}
			}
		})
	}
}

func TestConfigCommandUnwritable(t *testing.T) {
	testCases := []struct {
		name    string
		args    []string
		verbose bool
	}{
		{name: "normal mode", args: []string{"config"}, verbose: false},
		{name: "verbose before config", args: []string{"--verbose", "config"}, verbose: true},
		{name: "verbose after config", args: []string{"config", "--verbose"}, verbose: true},
	}

	for _, tc := range testCases {
		t.Run("parent path blocked by file - "+tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)

			blockedPath := filepath.Join(home, ".config")
			if err := os.WriteFile(blockedPath, []byte("blocking-file"), 0o600); err != nil {
				t.Fatalf("create blocking file %q: %v", blockedPath, err)
			}

			runtimeCalled := false
			injectRuntime(t, func(_ context.Context, _ config.Config, _ *slog.Logger) error {
				runtimeCalled = true
				return nil
			})

			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), tc.args, &stdout, &stderr, "v1.0.0", "testsha")
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want error when parent directory cannot be created", tc.args)
			}
			if errors.Is(err, ErrUsage) {
				t.Fatalf("Execute(%v) returned ErrUsage, want operational unwritable error: %v", tc.args, err)
			}

			if runtimeCalled {
				t.Errorf("runtime was called for %v, want no process started", tc.args)
			}

			stderrTrimmed := strings.TrimRight(stderr.String(), "\r\n")
			if stderrTrimmed == "" {
				t.Fatalf("stderr is empty for %v, want exactly 1 formatted explanation line", tc.args)
			}
			lines := strings.Split(stderrTrimmed, "\n")
			if len(lines) != 1 {
				t.Fatalf("stderr lines count = %d (%q), want exactly 1 line", len(lines), stderr.String())
			}

			if !tc.verbose {
				if strings.HasPrefix(strings.TrimSpace(lines[0]), "{") {
					t.Errorf("stderr is JSON in normal mode: %q", lines[0])
				}
			} else {
				var rec map[string]any
				if jsonErr := json.Unmarshal([]byte(lines[0]), &rec); jsonErr != nil {
					t.Errorf("stderr is not valid JSON in verbose mode: %v (line: %q)", jsonErr, lines[0])
				}
			}
		})
	}

	t.Run("read-only parent directory", func(t *testing.T) {
		if goRuntime.GOOS == "windows" {
			t.Skip("skipping chmod-based read-only test on Windows")
		}

		home := t.TempDir()
		setHome(t, home)

		configDir := filepath.Join(home, ".config")
		if err := os.MkdirAll(configDir, 0o500); err != nil {
			t.Fatalf("create read-only dir %q: %v", configDir, err)
		}
		if err := os.Chmod(configDir, 0o500); err != nil { // #nosec G302 -- Test sets directory read-only to verify unwritable path error.
			t.Fatalf("chmod 0500 on %q: %v", configDir, err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(configDir, 0o700) // #nosec G302 -- Restore write permissions for temp directory cleanup.
		})

		runtimeCalled := false
		injectRuntime(t, func(_ context.Context, _ config.Config, _ *slog.Logger) error {
			runtimeCalled = true
			return nil
		})

		var stdout, stderr bytes.Buffer
		err := Execute(context.Background(), []string{"config"}, &stdout, &stderr, "v1.0.0", "testsha")
		if err == nil {
			t.Fatal("Execute(config) error = nil, want error when destination file cannot be written")
		}
		if errors.Is(err, ErrUsage) {
			t.Fatalf("Execute(config) returned ErrUsage, want operational unwritable error: %v", err)
		}

		if runtimeCalled {
			t.Error("runtime was called, want no process started")
		}

		stderrTrimmed := strings.TrimRight(stderr.String(), "\r\n")
		if stderrTrimmed == "" {
			t.Fatal("stderr is empty, want exactly 1 formatted explanation line")
		}
		lines := strings.Split(stderrTrimmed, "\n")
		if len(lines) != 1 {
			t.Fatalf("stderr lines count = %d (%q), want exactly 1 line", len(lines), stderr.String())
		}
		if strings.HasPrefix(strings.TrimSpace(lines[0]), "{") {
			t.Errorf("stderr is JSON in normal mode: %q", lines[0])
		}
	})
}
