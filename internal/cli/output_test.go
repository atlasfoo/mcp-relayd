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
	"time"

	"mcp-relayd/internal/config"
)

const secretSentinel = "must-not-appear-in-errors"

func setHome(t *testing.T, home string) {
	t.Helper()
	if goRuntime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
		return
	}
	t.Setenv("HOME", home)
}

func writeConfigFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create config dir for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file %q: %v", path, err)
	}
}

func writeDefaultConfig(t *testing.T, home, content string) string {
	t.Helper()
	path := filepath.Join(home, ".config", "mcp-relayd.toml")
	writeConfigFile(t, path, content)
	return path
}

func injectRuntime(t *testing.T, fn RuntimeFunc) {
	t.Helper()
	prev := SetRuntime(fn)
	t.Cleanup(func() {
		SetRuntime(prev)
	})
}

func TestOutputNormalMode(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeDefaultConfig(t, home, "[gateway]\nport = 9876\n")

	injectRuntime(t, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
		logger.Info("gateway bound", "event", "gateway_bound", "addr", "127.0.0.1:9")
		logger.Info("server ready", "event", "state", "server", "alpha", "state", "READY")
		logger.Info("server start failed", "event", "start_failed", "server", "beta")
		logger.Info("server readiness failed", "event", "readiness_failed", "server", "beta")
		logger.Info("server exit failed", "event", "exit", "server", "beta", "state", "FAILED")
		logger.Info("server exit stopped", "event", "exit", "server", "beta", "state", "STOPPED")
		logger.Info("external line", "event", "external_output", "server", "alpha", "stream", "stdout", "line", "secret-external-payload")
		logger.Info("startup complete", "event", "startup_complete", "ready", 1, "failed", 1, "disabled", "gamma")
		logger.Info("shutdown complete", "event", "shutdown_complete")
		return nil
	})

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{"run"}, &stdout, &stderr, "v1.0.0", "testsha")
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}

	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}

	greeting := expectedGreeting(t)
	if !bytes.HasPrefix(stderr.Bytes(), greeting) {
		t.Fatalf("stderr does not begin with greeting; got:\n%s", stderr.String())
	}

	wantLocal := "Local: http://127.0.0.1:9/mcp/alpha"
	if !strings.Contains(stderr.String(), wantLocal) {
		t.Errorf("stderr missing ready server line: %q", wantLocal)
	}

	if strings.Contains(stderr.String(), "/mcp/beta") {
		t.Errorf("failed server beta listed as Local URL in stderr")
	}
	if strings.Contains(stderr.String(), "/mcp/gamma") {
		t.Errorf("disabled server gamma listed as Local URL in stderr")
	}

	for _, failureNotice := range []string{
		"server beta failed to start",
		"server beta failed to initialize",
		"server beta exited unexpectedly",
	} {
		if !strings.Contains(stderr.String(), failureNotice) {
			t.Errorf("stderr missing brief failure notice %q", failureNotice)
		}
	}

	if !strings.Contains(stderr.String(), "shutdown complete") {
		t.Errorf("stderr missing shutdown complete")
	}

	if strings.Contains(stderr.String(), "gateway listening on") {
		t.Errorf("stderr contains obsolete line gateway listening on: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "startup complete:") {
		t.Errorf("stderr contains obsolete line startup complete: %q", stderr.String())
	}

	if strings.Contains(stderr.String(), "all ready") {
		t.Errorf("stderr contains 'all ready': %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "STOPPED") || strings.Contains(stderr.String(), "stopped") {
		t.Errorf("stderr contains stopped exit line: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "external_output") || strings.Contains(stderr.String(), "secret-external-payload") {
		t.Errorf("stderr contains external output line: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "{") {
		t.Errorf("stderr contains JSON: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "\x1b") {
		t.Errorf("stderr contains ANSI escape codes: %q", stderr.String())
	}
}

func TestOutputVerboseMode(t *testing.T) {
	testCases := []struct {
		name string
		args []string
	}{
		{name: "verbose before run", args: []string{"--verbose", "run"}},
		{name: "verbose after run", args: []string{"run", "--verbose"}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			writeDefaultConfig(t, home, "[gateway]\nport = 9876\n")

			injectRuntime(t, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
				logger.Info("gateway bound", "event", "gateway_bound", "addr", "127.0.0.1:9")
				logger.Info("server ready", "event", "state", "server", "alpha", "state", "READY")
				logger.Info("server start failed", "event", "start_failed", "server", "beta")
				logger.Info("server readiness failed", "event", "readiness_failed", "server", "beta")
				logger.Info("server exit failed", "event", "exit", "server", "beta", "state", "FAILED")
				logger.Info("server exit stopped", "event", "exit", "server", "beta", "state", "STOPPED")
				logger.Info("external line", "event", "external_output", "server", "alpha", "stream", "stdout", "line", "external payload")
				logger.Info("startup complete", "event", "startup_complete", "ready", 1, "failed", 1, "disabled", "gamma")
				logger.Info("shutdown complete", "event", "shutdown_complete")
				return nil
			})

			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), tc.args, &stdout, &stderr, "v1.0.0", "testsha")
			if err != nil {
				t.Fatalf("Execute() error = %v, want nil", err)
			}

			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}

			if stderr.Len() == 0 {
				t.Fatal("stderr is empty, want JSON lines")
			}

			lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
			foundExternal := false
			for i, line := range lines {
				var record map[string]any
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatalf("stderr line %d is not valid JSON: %v (line: %q)", i, err, line)
				}
				if record["event"] == "external_output" {
					foundExternal = true
				}
			}

			if !foundExternal {
				t.Errorf("verbose mode missing external_output record in stderr")
			}

			humanPhrases := []string{
				"gateway listening on",
				"server alpha ready",
				"server beta failed to start",
				"server beta failed to initialize",
				"server beta exited unexpectedly",
				"startup complete: 1 ready, 1 failed, 1 disabled (gamma)",
				"shutdown complete",
			}
			for _, phrase := range humanPhrases {
				for _, line := range lines {
					if strings.TrimSpace(line) == phrase {
						t.Errorf("verbose mode printed human line %q", phrase)
					}
				}
			}

			stderrText := stderr.String()
			greeting := expectedGreeting(t)
			if strings.Contains(stderrText, string(greeting)) {
				t.Errorf("verbose mode stderr contains greeting")
			}
			if strings.Contains(stderrText, "Local:") {
				t.Errorf("verbose mode stderr contains Local:")
			}
		})
	}
}

func TestOutputVerboseHelpAndVersion(t *testing.T) {
	testCases := []struct {
		name       string
		args       []string
		wantStdout string
	}{
		{name: "verbose before help", args: []string{"--verbose", "--help"}, wantStdout: "mcp-relayd"},
		{name: "verbose after help", args: []string{"--help", "--verbose"}, wantStdout: "mcp-relayd"},
		{name: "run verbose help", args: []string{"run", "--verbose", "--help"}, wantStdout: "--config"},
		{name: "run help verbose", args: []string{"run", "--help", "--verbose"}, wantStdout: "--config"},
		{name: "verbose before version", args: []string{"--verbose", "--version"}, wantStdout: "mcp-relayd v1.0.0 testsha"},
		{name: "verbose after version", args: []string{"--version", "--verbose"}, wantStdout: "mcp-relayd v1.0.0 testsha"},
		{name: "run verbose version", args: []string{"run", "--verbose", "--version"}, wantStdout: "mcp-relayd v1.0.0 testsha"},
		{name: "run version verbose", args: []string{"run", "--version", "--verbose"}, wantStdout: "mcp-relayd v1.0.0 testsha"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtimeCalled := false
			injectRuntime(t, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
				runtimeCalled = true
				return nil
			})

			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), tc.args, &stdout, &stderr, "v1.0.0", "testsha")
			if err != nil {
				t.Fatalf("Execute() error = %v, want nil", err)
			}

			if runtimeCalled {
				t.Errorf("runtime was called for %v", tc.args)
			}

			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}

			greeting := expectedGreeting(t)
			if !bytes.HasPrefix(stdout.Bytes(), greeting) {
				t.Fatalf("stdout does not begin with greeting for %v; got:\n%s", tc.args, stdout.String())
			}

			afterGreeting := stdout.Bytes()[len(greeting):]
			if !strings.Contains(string(afterGreeting), tc.wantStdout) {
				t.Errorf("stdout after greeting = %q, want it to contain %q", string(afterGreeting), tc.wantStdout)
			}

			if strings.Contains(tc.name, "version") {
				wantVersion := "mcp-relayd v1.0.0 testsha\n"
				if string(afterGreeting) != wantVersion {
					t.Errorf("version output after greeting = %q, want %q", string(afterGreeting), wantVersion)
				}
			}
		})
	}
}

func TestOutputHelpAndVersion(t *testing.T) {
	testCases := []struct {
		name       string
		args       []string
		wantStdout string
		isVersion  bool
	}{
		{name: "root help no args", args: []string{}, wantStdout: "mcp-relayd run"},
		{name: "root help flag", args: []string{"--help"}, wantStdout: "mcp-relayd run"},
		{name: "run help flag", args: []string{"run", "--help"}, wantStdout: "--config"},
		{name: "root version flag", args: []string{"--version"}, wantStdout: "mcp-relayd v1.0.0 testsha", isVersion: true},
		{name: "run version flag", args: []string{"run", "--version"}, wantStdout: "mcp-relayd v1.0.0 testsha", isVersion: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtimeCalled := false
			injectRuntime(t, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
				runtimeCalled = true
				return nil
			})

			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), tc.args, &stdout, &stderr, "v1.0.0", "testsha")
			if err != nil {
				t.Fatalf("Execute() error = %v, want nil", err)
			}

			if runtimeCalled {
				t.Errorf("runtime was called for %v", tc.args)
			}

			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}

			greeting := expectedGreeting(t)
			if !bytes.HasPrefix(stdout.Bytes(), greeting) {
				t.Fatalf("stdout does not begin with greeting for %v; got:\n%s", tc.args, stdout.String())
			}

			afterGreeting := stdout.Bytes()[len(greeting):]
			if !strings.Contains(string(afterGreeting), tc.wantStdout) {
				t.Errorf("stdout after greeting = %q, want it to contain %q", string(afterGreeting), tc.wantStdout)
			}

			if tc.isVersion {
				wantVersion := "mcp-relayd v1.0.0 testsha\n"
				if string(afterGreeting) != wantVersion {
					t.Errorf("version output after greeting = %q, want %q", string(afterGreeting), wantVersion)
				}
			}
		})
	}
}

func TestOutputCancellationDuringStartup(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeDefaultConfig(t, home, "[gateway]\nport = 9876\n")

	started := make(chan struct{})
	observedCancel := make(chan struct{})

	injectRuntime(t, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
		close(started)
		logger.Info("gateway bound", "event", "gateway_bound", "addr", "127.0.0.1:9")
		select {
		case <-ctx.Done():
			close(observedCancel)
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("timed out waiting for ctx.Done()")
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var stdout, stderr bytes.Buffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- Execute(ctx, []string{"run"}, &stdout, &stderr, "v1.0.0", "testsha")
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not start in time")
	}

	cancel()

	select {
	case <-observedCancel:
	case <-time.After(2 * time.Second):
		t.Fatal("runtime did not observe cancellation")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Execute() error = %v, want nil on cancellation", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute() did not finish in time after cancellation")
	}

	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty", stdout.String())
	}

	if strings.Contains(stderr.String(), "startup complete") {
		t.Errorf("stderr unexpectedly contains startup complete: %q", stderr.String())
	}

	if strings.Contains(stderr.String(), "{") {
		t.Errorf("stderr unexpectedly contains JSON in normal mode: %q", stderr.String())
	}

	if !bytes.HasPrefix(stderr.Bytes(), expectedGreeting(t)) {
		t.Errorf("stderr missing greeting prefix: %q", stderr.String())
	}

	if strings.Contains(stderr.String(), "Local:") {
		t.Errorf("stderr unexpectedly contains Local: listing before startup completion: %q", stderr.String())
	}
}

func TestOutputInvalidConfig(t *testing.T) {
	testCases := []struct {
		name    string
		args    []string
		verbose bool
	}{
		{name: "normal mode", args: []string{"run"}, verbose: false},
		{name: "verbose before run", args: []string{"--verbose", "run"}, verbose: true},
		{name: "verbose after run", args: []string{"run", "--verbose"}, verbose: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			invalidContent := fmt.Sprintf("[gateway]\nport = 70000\nTOKEN = %q\n", secretSentinel)
			writeDefaultConfig(t, home, invalidContent)

			runtimeCalled := false
			injectRuntime(t, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
				runtimeCalled = true
				return nil
			})

			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), tc.args, &stdout, &stderr, "v1.0.0", "testsha")
			if err == nil {
				t.Fatal("Execute() error = nil, want non-nil error for invalid config")
			}

			if runtimeCalled {
				t.Errorf("runtime was called despite invalid config")
			}

			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}

			combined := stdout.String() + stderr.String() + err.Error()
			if strings.Contains(combined, secretSentinel) {
				t.Errorf("secret sentinel leaked in output or error: %q", combined)
			}

			stderrTrimmed := strings.TrimRight(stderr.String(), "\r\n")
			if stderrTrimmed == "" {
				t.Fatalf("stderr is empty, want exactly 1 formatted line")
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
}

func TestOutputBindFailure(t *testing.T) {
	testCases := []struct {
		name    string
		args    []string
		verbose bool
	}{
		{name: "normal mode", args: []string{"run"}, verbose: false},
		{name: "verbose before run", args: []string{"--verbose", "run"}, verbose: true},
		{name: "verbose after run", args: []string{"run", "--verbose"}, verbose: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			writeDefaultConfig(t, home, "[gateway]\nport = 9876\n")

			injectRuntime(t, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
				return fmt.Errorf("listen on local gateway: bind 127.0.0.1:9: address in use")
			})

			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), tc.args, &stdout, &stderr, "v1.0.0", "testsha")
			if err == nil {
				t.Fatal("Execute() error = nil, want non-nil error for bind failure")
			}

			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}

			combined := stdout.String() + stderr.String() + err.Error()
			if strings.Contains(combined, secretSentinel) {
				t.Errorf("secret sentinel leaked in output or error: %q", combined)
			}

			stderrTrimmed := strings.TrimRight(stderr.String(), "\r\n")
			if stderrTrimmed == "" {
				t.Fatalf("stderr is empty, want exactly 1 formatted line")
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
}

func TestOutputShutdownFailure(t *testing.T) {
	testCases := []struct {
		name    string
		args    []string
		verbose bool
	}{
		{name: "normal mode", args: []string{"run"}, verbose: false},
		{name: "verbose before run", args: []string{"--verbose", "run"}, verbose: true},
		{name: "verbose after run", args: []string{"run", "--verbose"}, verbose: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			writeDefaultConfig(t, home, "[gateway]\nport = 9876\n")

			injectRuntime(t, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
				return fmt.Errorf("shutdown proxy manager: timeout waiting for process exit")
			})

			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), tc.args, &stdout, &stderr, "v1.0.0", "testsha")
			if err == nil {
				t.Fatal("Execute() error = nil, want non-nil error for shutdown failure")
			}

			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}

			combined := stdout.String() + stderr.String() + err.Error()
			if strings.Contains(combined, secretSentinel) {
				t.Errorf("secret sentinel leaked in output or error: %q", combined)
			}

			stderrTrimmed := strings.TrimRight(stderr.String(), "\r\n")
			if stderrTrimmed == "" {
				t.Fatalf("stderr is empty, want exactly 1 formatted line")
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
}

func TestOutputUsageErrors(t *testing.T) {
	testCases := []struct {
		name string
		args []string
		bad  string
	}{
		{name: "unknown command", args: []string{"unknown-cmd"}, bad: "unknown-cmd"},
		{name: "unknown command verbose", args: []string{"--verbose", "unknown-cmd"}, bad: "unknown-cmd"},
		{name: "excess positional root", args: []string{"extra-arg"}, bad: "extra-arg"},
		{name: "excess positional run", args: []string{"run", "extra-arg"}, bad: "extra-arg"},
		{name: "invalid flag root", args: []string{"--invalid-flag"}, bad: "--invalid-flag"},
		{name: "invalid flag run", args: []string{"run", "--invalid-flag"}, bad: "--invalid-flag"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			runtimeCalled := false
			injectRuntime(t, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
				runtimeCalled = true
				return nil
			})

			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), tc.args, &stdout, &stderr, "v1.0.0", "testsha")
			if err == nil {
				t.Fatalf("Execute(%v) error = nil, want usage error", tc.args)
			}
			if !errors.Is(err, ErrUsage) {
				t.Errorf("Execute(%v) error = %v, want ErrUsage", tc.args, err)
			}

			if runtimeCalled {
				t.Errorf("runtime was called for %v, want no runtime execution", tc.args)
			}

			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}

			greeting := expectedGreeting(t)
			if strings.Contains(stderr.String(), string(greeting)) {
				t.Errorf("stderr unexpectedly contains greeting for %v", tc.args)
			}

			if strings.Contains(stderr.String(), tc.bad) {
				t.Errorf("stderr echoed input %q: %q", tc.bad, stderr.String())
			}

			wantStderr := "invalid command arguments; see --help\n"
			if stderr.String() != wantStderr {
				t.Errorf("stderr = %q, want exactly %q", stderr.String(), wantStderr)
			}
		})
	}
}
