package app_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"mcp-relayd/internal/app"
	"mcp-relayd/internal/config"
)

type capturedRecord struct {
	Message     string
	Event       string
	Addr        string
	Server      string
	State       string
	Ready       int
	HasReady    bool
	Failed      int
	HasFailed   bool
	Disabled    string
	HasDisabled bool
	Attrs       map[string]any
}

type recordCapture struct {
	mu      sync.Mutex
	records []capturedRecord
}

func newRecordCapture() *recordCapture {
	return &recordCapture{}
}

func (c *recordCapture) capture(r slog.Record, baseAttrs []slog.Attr) {
	c.mu.Lock()
	defer c.mu.Unlock()

	attrs := make(map[string]any)
	for _, a := range baseAttrs {
		collectAttrs("", a, attrs)
	}
	r.Attrs(func(a slog.Attr) bool {
		collectAttrs("", a, attrs)
		return true
	})

	rec := capturedRecord{
		Message: r.Message,
		Attrs:   attrs,
	}
	if v, ok := attrs["event"].(string); ok {
		rec.Event = v
	}
	if v, ok := attrs["addr"].(string); ok {
		rec.Addr = v
	}
	if v, ok := attrs["server"].(string); ok {
		rec.Server = v
	}
	if v, ok := attrs["state"].(string); ok {
		rec.State = v
	}
	if v, ok := toInt(attrs["ready"]); ok {
		rec.Ready = v
		rec.HasReady = true
	}
	if v, ok := toInt(attrs["failed"]); ok {
		rec.Failed = v
		rec.HasFailed = true
	}
	if v, ok := attrs["disabled"].(string); ok {
		rec.Disabled = v
		rec.HasDisabled = true
	}

	c.records = append(c.records, rec)
}

func collectAttrs(prefix string, a slog.Attr, target map[string]any) {
	key := a.Key
	if prefix != "" {
		key = prefix + "." + key
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, sub := range a.Value.Group() {
			collectAttrs(key, sub, target)
		}
		return
	}
	target[key] = a.Value.Any()
}

func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int32:
		return int(val), true
	case int64:
		if val > int64(int(^uint(0)>>1)) || val < -int64(int(^uint(0)>>1))-1 {
			return 0, false
		}
		return int(val), true
	default:
		return 0, false
	}
}

func (c *recordCapture) Records() []capturedRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	res := make([]capturedRecord, len(c.records))
	copy(res, c.records)
	return res
}

func (c *recordCapture) Events(name string) []capturedRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	var matches []capturedRecord
	for _, r := range c.records {
		if r.Event == name {
			matches = append(matches, r)
		}
	}
	return matches
}

func (c *recordCapture) WaitForEvent(name string, timeout time.Duration) (capturedRecord, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for _, r := range c.records {
			if r.Event == name {
				c.mu.Unlock()
				return r, true
			}
		}
		c.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return capturedRecord{}, false
}

type captureHandler struct {
	capture *recordCapture
	attrs   []slog.Attr
}

func newCaptureHandler(capture *recordCapture) slog.Handler {
	return &captureHandler{capture: capture}
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.capture.capture(r, h.attrs)
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)
	return &captureHandler{
		capture: h.capture,
		attrs:   newAttrs,
	}
}

func (h *captureHandler) WithGroup(_ string) slog.Handler {
	return h
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate free port: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func createBlockingScript(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		path := filepath.Join(t.TempDir(), "block.bat")
		if err := os.WriteFile(path, []byte("@echo off\r\nping -n 30 127.0.0.1 >nul\r\n"), 0o600); err != nil {
			t.Fatalf("write blocking script: %v", err)
		}
		return path
	}
	path := filepath.Join(t.TempDir(), "block.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 30\n"), 0o600); err != nil {
		t.Fatalf("write blocking script: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil { // #nosec G302 -- The helper must be executable so the supervised proxy command can start.
		t.Fatalf("make blocking script executable: %v", err)
	}
	return path
}

func TestRunEmitsLifecycleEventsOnSuccess(t *testing.T) {
	capture := newRecordCapture()
	logger := slog.New(newCaptureHandler(capture))

	port := freePort(t)
	expectedAddr := fmt.Sprintf("127.0.0.1:%d", port)
	const sentinelSecret = "must-not-appear-in-events"

	cfg := config.Config{
		Gateway: config.Gateway{
			Host:            "127.0.0.1",
			Port:            port,
			ShutdownTimeout: 200 * time.Millisecond,
		},
		Proxy: config.Proxy{
			Command:        "nonexistent-mcp-proxy",
			StartupTimeout: 100 * time.Millisecond,
		},
		Servers: map[string]config.Server{
			"enabled-srv": {
				Command:   "nonexistent-server-cmd",
				Autostart: true,
				Env: map[string]string{
					"SECRET": sentinelSecret,
				},
			},
			"disabled-srv": {
				Command:   "nonexistent-server-cmd",
				Autostart: false,
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- app.Run(ctx, cfg, logger)
	}()

	// Wait briefly for startup_complete or deadline, then cancel to trigger shutdown.
	_, _ = capture.WaitForEvent("startup_complete", 2*time.Second)
	cancel()

	select {
	case err := <-runErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() unexpected error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not shut down within deadline")
	}

	records := capture.Records()

	// 1. gateway_bound event verification
	bounds := capture.Events("gateway_bound")
	if len(bounds) != 1 {
		t.Fatalf("expected 1 gateway_bound event, got %d", len(bounds))
	}
	if bounds[0].Message != "relay" {
		t.Errorf("gateway_bound message = %q, want %q", bounds[0].Message, "relay")
	}
	if bounds[0].Addr != expectedAddr {
		t.Errorf("gateway_bound addr = %q, want %q", bounds[0].Addr, expectedAddr)
	}

	// 2. startup_complete event verification
	startups := capture.Events("startup_complete")
	if len(startups) != 1 {
		t.Fatalf("expected 1 startup_complete event, got %d", len(startups))
	}
	if startups[0].Message != "relay" {
		t.Errorf("startup_complete message = %q, want %q", startups[0].Message, "relay")
	}
	if !startups[0].HasReady || startups[0].Ready != 0 {
		t.Errorf("startup_complete ready = %d (has=%v), want 0", startups[0].Ready, startups[0].HasReady)
	}
	if !startups[0].HasFailed || startups[0].Failed != 1 {
		t.Errorf("startup_complete failed = %d (has=%v), want 1", startups[0].Failed, startups[0].HasFailed)
	}
	if !startups[0].HasDisabled || startups[0].Disabled != "disabled-srv" {
		t.Errorf("startup_complete disabled = %q (has=%v), want %q", startups[0].Disabled, startups[0].HasDisabled, "disabled-srv")
	}

	// 3. shutdown_complete event verification
	shutdowns := capture.Events("shutdown_complete")
	if len(shutdowns) != 1 {
		t.Fatalf("expected 1 shutdown_complete event, got %d", len(shutdowns))
	}
	if shutdowns[0].Message != "relay" {
		t.Errorf("shutdown_complete message = %q, want %q", shutdowns[0].Message, "relay")
	}

	// 4. Ordering check: gateway_bound -> startup_complete -> shutdown_complete
	boundIdx, startupIdx, shutdownIdx := -1, -1, -1
	for i, r := range records {
		if r.Event == "gateway_bound" && boundIdx == -1 {
			boundIdx = i
		}
		if r.Event == "startup_complete" && startupIdx == -1 {
			startupIdx = i
		}
		if r.Event == "shutdown_complete" && shutdownIdx == -1 {
			shutdownIdx = i
		}
	}
	if boundIdx >= startupIdx || startupIdx >= shutdownIdx {
		t.Errorf("incorrect event ordering: gateway_bound index %d, startup_complete index %d, shutdown_complete index %d",
			boundIdx, startupIdx, shutdownIdx)
	}

	// 5. Redaction of configured environment values
	for _, rec := range records {
		if strings.Contains(rec.Message, sentinelSecret) {
			t.Errorf("record message %q leaks configured secret %q", rec.Message, sentinelSecret)
		}
		for k, v := range rec.Attrs {
			if strings.Contains(k, sentinelSecret) {
				t.Errorf("record attr key %q contains configured secret", k)
			}
			valStr := fmt.Sprintf("%v", v)
			if strings.Contains(valStr, sentinelSecret) {
				t.Errorf("record attr %q value %q leaks configured secret", k, valStr)
			}
		}
	}
}

func TestRunEmitsGatewayBoundOnlyAfterListenSucceeds(t *testing.T) {
	t.Run("listen succeeds", func(t *testing.T) {
		capture := newRecordCapture()
		logger := slog.New(newCaptureHandler(capture))

		port := freePort(t)
		expectedAddr := fmt.Sprintf("127.0.0.1:%d", port)

		cfg := config.Config{
			Gateway: config.Gateway{
				Host:            "127.0.0.1",
				Port:            port,
				ShutdownTimeout: 100 * time.Millisecond,
			},
			Proxy: config.Proxy{
				Command:        "nonexistent-mcp-proxy",
				StartupTimeout: 100 * time.Millisecond,
			},
			Servers: map[string]config.Server{
				"srv": {
					Command:   "nonexistent-cmd",
					Autostart: true,
				},
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		runErrCh := make(chan error, 1)
		go func() {
			runErrCh <- app.Run(ctx, cfg, logger)
		}()

		_, _ = capture.WaitForEvent("gateway_bound", 50*time.Millisecond)
		cancel()
		<-runErrCh

		bounds := capture.Events("gateway_bound")
		if len(bounds) != 1 {
			t.Fatalf("expected 1 gateway_bound event, got %d", len(bounds))
		}
		if bounds[0].Message != "relay" {
			t.Errorf("gateway_bound message = %q, want %q", bounds[0].Message, "relay")
		}
		if bounds[0].Addr != expectedAddr {
			t.Errorf("gateway_bound addr = %q, want %q", bounds[0].Addr, expectedAddr)
		}
	})

	t.Run("listen fails", func(t *testing.T) {
		capture := newRecordCapture()
		logger := slog.New(newCaptureHandler(capture))

		cfg := config.Config{
			Gateway: config.Gateway{
				Host:            "127.0.0.1",
				Port:            1, // Privileged port fails for non-root
				ShutdownTimeout: 100 * time.Millisecond,
			},
			Proxy: config.Proxy{
				Command:        "nonexistent-mcp-proxy",
				StartupTimeout: 100 * time.Millisecond,
			},
			Servers: map[string]config.Server{
				"srv": {
					Command:   "nonexistent-cmd",
					Autostart: true,
				},
			},
		}

		err := app.Run(context.Background(), cfg, logger)
		if err == nil {
			t.Fatal("expected Run() to fail with listen error, got nil")
		}

		bounds := capture.Events("gateway_bound")
		if len(bounds) > 0 {
			t.Fatalf("expected 0 gateway_bound events when listen fails, got %d", len(bounds))
		}
	})
}

func TestRunEmitsNoStartupSummaryWhenCanceledDuringStartup(t *testing.T) {
	capture := newRecordCapture()
	logger := slog.New(newCaptureHandler(capture))

	blockingScript := createBlockingScript(t)
	port := freePort(t)

	cfg := config.Config{
		Gateway: config.Gateway{
			Host:            "127.0.0.1",
			Port:            port,
			ShutdownTimeout: 200 * time.Millisecond,
		},
		Proxy: config.Proxy{
			Command:        blockingScript,
			StartupTimeout: 500 * time.Millisecond,
		},
		Servers: map[string]config.Server{
			"hanging-server": {
				Command:   "dummy-command",
				Autostart: true,
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- app.Run(ctx, cfg, logger)
	}()

	// Wait briefly so Listen succeeds and startup begins, then cancel while startup is still running.
	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case err := <-runErrCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not return within bounded deadline after cancellation")
	}

	startups := capture.Events("startup_complete")
	if len(startups) > 0 {
		t.Errorf("expected 0 startup_complete events when startup was canceled, got %d", len(startups))
	}

	shutdowns := capture.Events("shutdown_complete")
	if len(shutdowns) != 1 {
		t.Fatalf("expected 1 shutdown_complete event after shutdown finishes, got %d", len(shutdowns))
	}
	if shutdowns[0].Message != "relay" {
		t.Errorf("shutdown_complete message = %q, want %q", shutdowns[0].Message, "relay")
	}
}

func TestRunEmitsDisabledServersSummary(t *testing.T) {
	t.Run("empty string when all servers autostart", func(t *testing.T) {
		capture := newRecordCapture()
		logger := slog.New(newCaptureHandler(capture))

		port := freePort(t)
		cfg := config.Config{
			Gateway: config.Gateway{
				Host:            "127.0.0.1",
				Port:            port,
				ShutdownTimeout: 100 * time.Millisecond,
			},
			Proxy: config.Proxy{
				Command:        "nonexistent-mcp-proxy",
				StartupTimeout: 100 * time.Millisecond,
			},
			Servers: map[string]config.Server{
				"srv-1": {Command: "cmd1", Autostart: true},
				"srv-2": {Command: "cmd2", Autostart: true},
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		runErrCh := make(chan error, 1)
		go func() {
			runErrCh <- app.Run(ctx, cfg, logger)
		}()

		_, _ = capture.WaitForEvent("startup_complete", 2*time.Second)
		cancel()
		<-runErrCh

		startups := capture.Events("startup_complete")
		if len(startups) != 1 {
			t.Fatalf("expected 1 startup_complete event, got %d", len(startups))
		}
		if !startups[0].HasDisabled || startups[0].Disabled != "" {
			t.Errorf("startup_complete disabled = %q (has=%v), want empty string", startups[0].Disabled, startups[0].HasDisabled)
		}
	})

	t.Run("sorted comma-separated string when multiple servers disabled", func(t *testing.T) {
		capture := newRecordCapture()
		logger := slog.New(newCaptureHandler(capture))

		port := freePort(t)
		cfg := config.Config{
			Gateway: config.Gateway{
				Host:            "127.0.0.1",
				Port:            port,
				ShutdownTimeout: 100 * time.Millisecond,
			},
			Proxy: config.Proxy{
				Command:        "nonexistent-mcp-proxy",
				StartupTimeout: 100 * time.Millisecond,
			},
			Servers: map[string]config.Server{
				"srv-active": {Command: "cmd", Autostart: true},
				"srv-zebra":  {Command: "cmd", Autostart: false},
				"srv-alpha":  {Command: "cmd", Autostart: false},
				"srv-mango":  {Command: "cmd", Autostart: false},
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		runErrCh := make(chan error, 1)
		go func() {
			runErrCh <- app.Run(ctx, cfg, logger)
		}()

		_, _ = capture.WaitForEvent("startup_complete", 2*time.Second)
		cancel()
		<-runErrCh

		startups := capture.Events("startup_complete")
		if len(startups) != 1 {
			t.Fatalf("expected 1 startup_complete event, got %d", len(startups))
		}
		wantDisabled := "srv-alpha,srv-mango,srv-zebra"
		if !startups[0].HasDisabled || startups[0].Disabled != wantDisabled {
			t.Errorf("startup_complete disabled = %q (has=%v), want %q", startups[0].Disabled, startups[0].HasDisabled, wantDisabled)
		}
	})
}
