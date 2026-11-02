package cli

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"mcp-relayd/internal/config"
)

func TestGreetingInformativeCommands(t *testing.T) {
	testCases := []struct {
		name       string
		args       []string
		wantStdout string
		isVersion  bool
	}{
		{name: "root help default", args: []string{}, wantStdout: "mcp-relayd run"},
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
				t.Fatalf("Execute(%v) error = %v, want nil", tc.args, err)
			}

			if runtimeCalled {
				t.Errorf("runtime was called for %v, want no runtime execution", tc.args)
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

func TestGreetingRunNormalMode(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	writeDefaultConfig(t, home, "[gateway]\nhost = \"127.0.0.1\"\nport = 8080\n")

	injectRuntime(t, func(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
		logger.Info("gateway bound", "event", "gateway_bound", "addr", "127.0.0.1:8080")
		logger.Info("server ready", "event", "state", "server", "alpha", "state", "READY")
		logger.Info("server start failed", "event", "start_failed", "server", "beta")
		logger.Info("server readiness failed", "event", "readiness_failed", "server", "beta")
		logger.Info("server exit failed", "event", "exit", "server", "beta", "state", "FAILED")
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

	wantReadyURL := "Local: http://127.0.0.1:8080/mcp/alpha"
	if !strings.Contains(stderr.String(), wantReadyURL) {
		t.Errorf("stderr missing ready server line %q; got:\n%s", wantReadyURL, stderr.String())
	}

	if strings.Contains(stderr.String(), "/mcp/beta") {
		t.Errorf("failed server beta unexpectedly listed as Local URL in stderr: %q", stderr.String())
	}

	if strings.Contains(stderr.String(), "/mcp/gamma") {
		t.Errorf("disabled server gamma unexpectedly listed as Local URL in stderr: %q", stderr.String())
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

	if strings.Contains(stderr.String(), "gateway listening on") {
		t.Errorf("stderr contains obsolete line gateway listening on: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "startup complete:") {
		t.Errorf("stderr contains obsolete line startup complete: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "shutdown complete") {
		t.Errorf("stderr missing shutdown complete")
	}

	if strings.Contains(stderr.String(), "{") {
		t.Errorf("stderr contains JSON: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "\x1b") {
		t.Errorf("stderr contains ANSI escape codes: %q", stderr.String())
	}
}

func TestGreetingRunVerboseMode(t *testing.T) {
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
				logger.Info("gateway bound", "event", "gateway_bound", "addr", "127.0.0.1:9876")
				logger.Info("server ready", "event", "state", "server", "alpha", "state", "READY")
				logger.Info("startup complete", "event", "startup_complete", "ready", 1, "failed", 0, "disabled", "")
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

			stderrText := stderr.String()
			greeting := expectedGreeting(t)
			if strings.Contains(stderrText, string(greeting)) {
				t.Errorf("verbose stderr contains greeting")
			}
			if strings.Contains(stderrText, "Local:") {
				t.Errorf("verbose stderr contains Local:")
			}
		})
	}
}

func TestGreetingUsageErrors(t *testing.T) {
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
