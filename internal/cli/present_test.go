package cli

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

type chunkRecorder struct {
	mu     sync.Mutex
	chunks []string
}

func (r *chunkRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chunks = append(r.chunks, string(p))
	return len(p), nil
}

func TestPresenter_NormalMode_ExactLines(t *testing.T) {
	cases := []struct {
		name     string
		logFn    func(l *slog.Logger)
		expected string
	}{
		{
			name: "gateway_bound",
			logFn: func(l *slog.Logger) {
				l.Info("", "event", "gateway_bound", "addr", "127.0.0.1:8080")
			},
			expected: "",
		},
		{
			name: "state READY with inherited server",
			logFn: func(l *slog.Logger) {
				l.With("server", "alpha").Info("", "event", "state", "state", "READY")
			},
			expected: "server alpha ready\n",
		},
		{
			name: "start_failed",
			logFn: func(l *slog.Logger) {
				l.With("server", "beta").Info("", "event", "start_failed")
			},
			expected: "server beta failed to start\n",
		},
		{
			name: "readiness_failed",
			logFn: func(l *slog.Logger) {
				l.With("server", "gamma").Info("", "event", "readiness_failed")
			},
			expected: "server gamma failed to initialize\n",
		},
		{
			name: "exit with FAILED state",
			logFn: func(l *slog.Logger) {
				l.With("server", "delta").Info("", "event", "exit", "state", "FAILED")
			},
			expected: "server delta exited unexpectedly\n",
		},
		{
			name: "exit with STOPPING state produces no output",
			logFn: func(l *slog.Logger) {
				l.With("server", "delta").Info("", "event", "exit", "state", "STOPPING")
			},
			expected: "",
		},
		{
			name: "exit with STOPPED state produces no output",
			logFn: func(l *slog.Logger) {
				l.With("server", "delta").Info("", "event", "exit", "state", "STOPPED")
			},
			expected: "",
		},
		{
			name: "startup_complete with disabled servers",
			logFn: func(l *slog.Logger) {
				l.Info("", "event", "startup_complete", "ready", 1, "failed", 1, "disabled", "gamma")
			},
			expected: "",
		},
		{
			name: "startup_complete with none disabled",
			logFn: func(l *slog.Logger) {
				l.Info("", "event", "startup_complete", "ready", 1, "failed", 0, "disabled", "")
			},
			expected: "",
		},
		{
			name: "shutdown_complete",
			logFn: func(l *slog.Logger) {
				l.Info("", "event", "shutdown_complete")
			},
			expected: "shutdown complete\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := NewPresenter(&buf, false)
			logger := slog.New(h)

			tc.logFn(logger)

			got := buf.String()
			if got != tc.expected {
				t.Fatalf("output mismatch: got %q, want %q", got, tc.expected)
			}
			if strings.Contains(got, "\x1b") {
				t.Fatalf("output contains ANSI escape sequences: %q", got)
			}
			if strings.HasPrefix(got, "{") {
				t.Fatalf("output looks like JSON in normal mode: %q", got)
			}
		})
	}
}

func TestPresenter_NormalMode_LocalListing(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewPresenter(&buf, false))

	logger.Info("", "event", "gateway_bound", "addr", "127.0.0.1:8080")
	logger.Info("", "event", "state", "server", "alpha", "state", "READY")
	logger.Info("", "event", "state", "server", "dropped", "state", "READY")
	logger.Info("", "event", "exit", "server", "dropped", "state", "FAILED")
	logger.Info("", "event", "start_failed", "server", "beta")
	logger.Info("", "event", "startup_complete", "ready", 1, "failed", 1, "disabled", "gamma")

	got := buf.String()
	if !strings.Contains(got, "Local: http://127.0.0.1:8080/mcp/alpha\n") {
		t.Fatalf("missing ready URL: %q", got)
	}
	if strings.Contains(got, "/mcp/beta") || strings.Contains(got, "/mcp/gamma") || strings.Contains(got, "/mcp/dropped") {
		t.Fatalf("listed a server that is not ready: %q", got)
	}
	if strings.Contains(got, "gateway listening on") || strings.Contains(got, "startup complete:") {
		t.Fatalf("obsolete summary in %q", got)
	}
	if !strings.Contains(got, "server alpha ready\n") {
		t.Fatalf("missing ready line: %q", got)
	}
	if !strings.Contains(got, "server beta failed to start\n") {
		t.Fatalf("missing failure line: %q", got)
	}
	if !strings.Contains(got, "server dropped exited unexpectedly\n") {
		t.Fatalf("missing post-READY failure line: %q", got)
	}
}

func TestPresenter_NormalMode_FailedStartupMustNotSayAllReady(t *testing.T) {
	var buf bytes.Buffer
	h := NewPresenter(&buf, false)
	logger := slog.New(h)

	logger.Info("", "event", "startup_complete", "ready", 1, "failed", 1, "disabled", "gamma")
	got := buf.String()
	if strings.Contains(got, "all ready") {
		t.Fatalf("output must not contain 'all ready' when failed > 0: %q", got)
	}
}

func TestPresenter_NormalMode_SuppressedEvents(t *testing.T) {
	suppressed := []struct {
		name  string
		logFn func(l *slog.Logger)
	}{
		{
			name: "external_output",
			logFn: func(l *slog.Logger) {
				l.Info("", "event", "external_output", "server", "alpha", "data", "log line")
			},
		},
		{
			name: "state STARTING",
			logFn: func(l *slog.Logger) {
				l.Info("", "event", "state", "server", "alpha", "state", "STARTING")
			},
		},
		{
			name: "state INITIALIZING",
			logFn: func(l *slog.Logger) {
				l.Info("", "event", "state", "server", "alpha", "state", "INITIALIZING")
			},
		},
		{
			name: "state STOPPING",
			logFn: func(l *slog.Logger) {
				l.Info("", "event", "state", "server", "alpha", "state", "STOPPING")
			},
		},
		{
			name: "unknown event",
			logFn: func(l *slog.Logger) {
				l.Info("", "event", "custom_unknown_event", "server", "alpha")
			},
		},
	}

	for _, tc := range suppressed {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := NewPresenter(&buf, false)
			logger := slog.New(h)

			tc.logFn(logger)

			if buf.Len() != 0 {
				t.Fatalf("expected no output for %s, got %q", tc.name, buf.String())
			}
		})
	}
}

func TestPresenter_NormalMode_DeduplicateFailures(t *testing.T) {
	var buf bytes.Buffer
	h := NewPresenter(&buf, false)
	logger := slog.New(h)

	srvA := logger.With("server", "srv-a")
	srvB := logger.With("server", "srv-b")

	// First failure on srv-a emits a line
	srvA.Info("", "event", "start_failed")
	wantA1 := "server srv-a failed to start\n"
	if buf.String() != wantA1 {
		t.Fatalf("first failure: got %q, want %q", buf.String(), wantA1)
	}

	// Repeated failures without state change are suppressed
	srvA.Info("", "event", "start_failed")
	srvA.Info("", "event", "start_failed")
	if buf.String() != wantA1 {
		t.Fatalf("repeats should be suppressed: got %q, want %q", buf.String(), wantA1)
	}

	// srv-b failing is a different server and must emit
	srvB.Info("", "event", "start_failed")
	wantB := wantA1 + "server srv-b failed to start\n"
	if buf.String() != wantB {
		t.Fatalf("different server: got %q, want %q", buf.String(), wantB)
	}

	// Intervening state change on srv-a
	srvA.Info("", "event", "state", "state", "STARTING")
	if buf.String() != wantB {
		t.Fatalf("intervening state STARTING should produce no output: got %q", buf.String())
	}

	// Now a new failure on srv-a must be emitted again
	srvA.Info("", "event", "start_failed")
	wantA2 := wantB + "server srv-a failed to start\n"
	if buf.String() != wantA2 {
		t.Fatalf("failure after state change should emit: got %q, want %q", buf.String(), wantA2)
	}
}

func TestPresenter_WithAttrsAndGroup(t *testing.T) {
	t.Run("normal mode honors WithAttrs and WithGroup", func(t *testing.T) {
		var buf bytes.Buffer
		h := NewPresenter(&buf, false)

		// Pattern required: slog.New(handler).With(...).WithGroup(...).Info(...)
		slog.New(h).With("server", "alpha").WithGroup("details").Info("", "event", "state", "state", "READY")

		got := buf.String()
		want := "server alpha ready\n"
		if got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("verbose mode preserves event and server with group", func(t *testing.T) {
		var buf bytes.Buffer
		h := NewPresenter(&buf, true)

		// Pattern required: slog.New(handler).With(...).WithGroup(...).Info(...)
		slog.New(h).With("server", "beta").WithGroup("meta").Info("", "event", "state", "state", "STARTING")

		line := buf.String()
		if !strings.HasSuffix(line, "\n") {
			t.Fatalf("expected newline termination, got %q", line)
		}

		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("verbose output is not valid JSON (%v): %q", err, line)
		}

		// Check server preservation (top-level or grouped)
		serverVal := raw["server"]
		if meta, ok := raw["meta"].(map[string]any); ok && serverVal == nil {
			serverVal = meta["server"]
		}
		if serverVal != "beta" {
			t.Fatalf("server attr not preserved in JSON: %#v", raw)
		}

		// Check event preservation (top-level or grouped)
		eventVal := raw["event"]
		if meta, ok := raw["meta"].(map[string]any); ok && eventVal == nil {
			eventVal = meta["event"]
		}
		if eventVal != "state" {
			t.Fatalf("event attr not preserved in JSON: %#v", raw)
		}
	})
}

func TestPresenter_ConcurrentWrites(t *testing.T) {
	rec := &chunkRecorder{}
	h := NewPresenter(rec, false)
	logger := slog.New(h)

	const goroutines = 20
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				logger.Info("", "event", "shutdown_complete")
			}
		}()
	}
	wg.Wait()

	rec.mu.Lock()
	defer rec.mu.Unlock()

	expectedCount := goroutines * iterations
	if len(rec.chunks) != expectedCount {
		t.Fatalf("expected %d atomic writes, got %d", expectedCount, len(rec.chunks))
	}

	for i, chunk := range rec.chunks {
		if !strings.HasSuffix(chunk, "\n") {
			t.Errorf("chunk %d does not end in newline: %q", i, chunk)
		}
		if chunk != "shutdown complete\n" {
			t.Errorf("chunk %d has corrupt or interleaved content: %q", i, chunk)
		}
	}
}

func TestPresenter_VerboseMode_JSONOnly(t *testing.T) {
	var buf bytes.Buffer
	h := NewPresenter(&buf, true)
	logger := slog.New(h)

	// Emit multiple event types including intermediate and external_output
	logger.Info("", "event", "gateway_bound", "addr", "127.0.0.1:8080")
	logger.With("server", "srv-1").Info("", "event", "state", "state", "STARTING")
	logger.With("server", "srv-1").Info("", "event", "state", "state", "INITIALIZING")
	logger.With("server", "srv-1").Info("", "event", "state", "state", "READY")
	logger.With("server", "srv-1").Info("", "event", "external_output", "stream", "stdout", "data", "hello from server")
	logger.With("server", "srv-1").Info("", "event", "state", "state", "STOPPING")
	logger.Info("", "event", "shutdown_complete")

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 7 {
		t.Fatalf("expected 7 JSON lines, got %d: %q", len(lines), buf.String())
	}

	humanPhrases := []string{
		"gateway listening on",
		"ready",
		"failed to start",
		"failed to initialize",
		"exited unexpectedly",
		"startup complete:",
		"shutdown complete",
	}

	for i, line := range lines {
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Errorf("line %d is not a JSON object: %q", i, line)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Errorf("line %d failed to unmarshal JSON: %v", i, err)
		}
		for _, phrase := range humanPhrases {
			// Plain human phrases must not appear outside JSON keys/values
			if line == phrase {
				t.Errorf("line %d matched human phrase %q", i, phrase)
			}
		}
	}
}

func TestPresenter_DebugOmitted(t *testing.T) {
	t.Run("normal mode omits debug", func(t *testing.T) {
		var buf bytes.Buffer
		h := NewPresenter(&buf, false)
		logger := slog.New(h)

		logger.Debug("debug message", "event", "gateway_bound", "addr", "127.0.0.1:8080")
		if buf.Len() != 0 {
			t.Fatalf("expected no output for debug in normal mode, got %q", buf.String())
		}
	})

	t.Run("verbose mode omits debug", func(t *testing.T) {
		var buf bytes.Buffer
		h := NewPresenter(&buf, true)
		logger := slog.New(h)

		logger.Debug("debug message", "event", "gateway_bound", "addr", "127.0.0.1:8080")
		if buf.Len() != 0 {
			t.Fatalf("expected no output for debug in verbose mode, got %q", buf.String())
		}
	})
}
