package process_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"mcp-relayd/internal/config"
	"mcp-relayd/internal/process"
)

func TestManagerLifecycleLogsJSONWithoutConfiguredValues(t *testing.T) {
	const (
		longSecret  = "shared-sensitive-value"
		shortSecret = "sensitive"
	)
	logs := &loggingBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	failedReady := newLoggingProcess()
	failedReady.readyErr = errors.New("readiness failed: " + longSecret + " MCP body")
	normalExit := newLoggingProcess()
	adapter := &loggingAdapter{starts: map[string]loggingStart{
		"readiness": {child: failedReady},
		"exit":      {child: normalExit},
	}}
	cfg := loggingConfig(map[string]config.Server{
		"readiness": {Command: "configured-command", Autostart: true, Env: map[string]string{"TOKEN": longSecret, "PREFIX": shortSecret}},
		"exit":      {Command: "configured-command", Autostart: true, Env: map[string]string{"TOKEN": longSecret}},
	})
	manager := process.NewManager(cfg, adapter, logger)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForLogState(t, manager, "readiness", process.StateFailed)
	waitForLoggingStop(t, failedReady)
	waitForLogState(t, manager, "readiness", process.StateFailed)
	normalExit.signalExit(errors.New("normal process exit: " + longSecret))
	waitForLogState(t, manager, "exit", process.StateFailed)
	if err := manager.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	// Stop is a no-op for the failed readiness server; make a second manager
	// exercise an unsuccessful forced stop while the server remains STOPPING.
	forcedStop := newLoggingProcess()
	forcedStop.stopErr = errors.New("forced cleanup failed: " + shortSecret)
	cleanupManager := process.NewManager(loggingConfig(map[string]config.Server{
		"cleanup": {Command: "configured-command", Autostart: true, Env: map[string]string{"TOKEN": longSecret}},
	}), &loggingAdapter{starts: map[string]loggingStart{"cleanup": {child: forcedStop}}}, logger)
	if err := cleanupManager.Start(context.Background()); err != nil {
		t.Fatalf("cleanup manager Start() error = %v", err)
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := cleanupManager.Stop(stopCtx); err == nil {
		t.Fatal("Stop() error = nil, want forced cleanup failure")
	}
	waitForLogState(t, cleanupManager, "cleanup", process.StateStopping)

	entries := logs.entries(t)
	assertLogEvent(t, entries, "readiness", "FAILED", "readiness_failed")
	assertLogEvent(t, entries, "exit", "FAILED", "exit")
	assertLogEvent(t, entries, "cleanup", "STOPPING", "stop_failed")
	for _, entry := range entries {
		for _, key := range []string{"state", "server", "event", "proxy_pid"} {
			if _, ok := entry[key]; !ok {
				t.Errorf("log entry missing %q: %#v", key, entry)
			}
		}
	}
	output := logs.String()
	if !strings.Contains(output, `"proxy_pid":4321`) {
		t.Error("lifecycle logs did not include the proxy PID")
	}
	for _, forbidden := range []string{longSecret, shortSecret, "configured-command", "MCP body", "TOML"} {
		if strings.Contains(output, forbidden) {
			t.Errorf("logs contain forbidden configured or protocol value %q", forbidden)
		}
	}
}

func TestManagerLifecycleLogsIsolateConcurrentServers(t *testing.T) {
	logs := &loggingBuffer{}
	children := map[string]*loggingProcess{"alpha": newLoggingProcess(), "beta": newLoggingProcess()}
	adapter := &loggingAdapter{starts: map[string]loggingStart{"alpha": {child: children["alpha"]}, "beta": {child: children["beta"]}}}
	manager := process.NewManager(loggingConfig(map[string]config.Server{
		"alpha": {Command: "alpha", Autostart: true},
		"beta":  {Command: "beta", Autostart: true},
	}), adapter, slog.New(slog.NewJSONHandler(logs, nil)))
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	children["alpha"].signalExit(nil)
	waitForLogState(t, manager, "alpha", process.StateFailed)
	if got := manager.State("beta"); got != process.StateReady {
		t.Fatalf("beta state = %v, want READY after alpha exit", got)
	}
	for _, entry := range logs.entries(t) {
		name, _ := entry["server"].(string)
		state, _ := entry["state"].(string)
		if name == "alpha" && state == string(process.StateFailed) && manager.State("beta") != process.StateReady {
			t.Fatal("alpha lifecycle event was not isolated from beta")
		}
	}
}

func TestManagerStartupErrorDoesNotExposeConfiguredValues(t *testing.T) {
	const secret = "configured-test-value"
	logs := &loggingBuffer{}
	manager := process.NewManager(loggingConfig(map[string]config.Server{
		"startup": {Command: "configured-command", Autostart: true, Env: map[string]string{"TOKEN": secret}},
	}), &loggingAdapter{starts: map[string]loggingStart{
		"startup": {err: errors.New("launch failure: " + secret + " MCP body")},
	}}, slog.New(slog.NewJSONHandler(logs, nil)))
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForLogState(t, manager, "startup", process.StateFailed)
	output := logs.String()
	for _, forbidden := range []string{secret, "MCP body", "configured-command", "launch failure"} {
		if strings.Contains(output, forbidden) {
			t.Errorf("startup log contains sensitive detail %q", forbidden)
		}
	}
	assertLogEvent(t, logs.entries(t), "startup", "FAILED", "start_failed")
}

func assertLogEvent(t *testing.T, entries []map[string]any, server, state, event string) {
	t.Helper()
	for _, entry := range entries {
		if entry["server"] == server && entry["state"] == state && entry["event"] == event {
			return
		}
	}
	t.Errorf("missing lifecycle log server=%q state=%q event=%q in %#v", server, state, event, entries)
}

func waitForLogState(t *testing.T, manager *process.Manager, name string, state process.State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if manager.State(name) == state {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("server %q state = %v, want %v", name, manager.State(name), state)
}

func waitForLoggingStop(t *testing.T, child *loggingProcess) {
	t.Helper()
	select {
	case <-child.stopEntered:
	case <-time.After(time.Second):
		t.Fatal("readiness-failed process was not stopped")
	}
}

func loggingConfig(servers map[string]config.Server) config.Config {
	return config.Config{Gateway: config.Gateway{ShutdownTimeout: time.Second}, Proxy: config.Proxy{Command: "mcp-proxy", StartupTimeout: time.Second}, Servers: servers}
}

type (
	loggingAdapter struct{ starts map[string]loggingStart }
	loggingStart   struct {
		child *loggingProcess
		err   error
	}
)

func (adapter *loggingAdapter) Start(_ context.Context, name string, _ config.Proxy, _ config.Server) (process.Process, error) {
	start, ok := adapter.starts[name]
	if !ok {
		return nil, errors.New("unexpected server")
	}
	return start.child, start.err
}

type loggingProcess struct {
	readyErr    error
	stopErr     error
	stopEntered chan struct{}
	exit        chan error
	once        sync.Once
}

func (*loggingProcess) PID() int { return 4321 }

func newLoggingProcess() *loggingProcess {
	return &loggingProcess{stopEntered: make(chan struct{}), exit: make(chan error, 1)}
}
func (*loggingProcess) Addr() string                          { return "127.0.0.1:12345" }
func (child *loggingProcess) WaitReady(context.Context) error { return child.readyErr }
func (child *loggingProcess) Done() <-chan error              { return child.exit }
func (child *loggingProcess) Stop(context.Context) error {
	child.once.Do(func() { close(child.stopEntered) })
	return child.stopErr
}
func (child *loggingProcess) signalExit(err error) { child.exit <- err }

type loggingBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *loggingBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *loggingBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func (buffer *loggingBuffer) entries(t *testing.T) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buffer.String()), "\n")
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("invalid JSON log record %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}
