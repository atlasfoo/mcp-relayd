package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mcp-relayd/internal/config"
)

func TestExecAdapterPropagatesProxyAndServerConfiguration(t *testing.T) {
	t.Setenv("MCP_RELAYD_PARENT_ONLY_SENTINEL", "must-not-leak")
	workDir := filepath.Join(t.TempDir(), "cwd with spaces")
	if err := os.Mkdir(workDir, 0o700); err != nil {
		t.Fatal(err)
	}
	server := config.Server{
		Command: "upstream command",
		Args:    []string{"argument with spaces", "$(do-not-run)", "semicolon;argument"},
		CWD:     workDir,
		Env: map[string]string{
			"MCP_TOKEN": "value with spaces=and-equals",
			"MCP_MODE":  "test",
		},
	}
	proxy := config.Proxy{Command: "mcp-proxy-test", StartupTimeout: time.Second}
	var logs synchronizedBuffer
	adapter := newAdapterUnderTest(t, slog.New(slog.NewJSONHandler(&logs, nil)))
	var observedAddr string
	tree := newAdapterTestProcessTree()
	adapter.start = func(cmd *exec.Cmd, grace time.Duration) (processTree, error) {
		t.Helper()
		if grace <= 0 {
			t.Errorf("process-tree grace = %s, want positive duration", grace)
		}
		assertProxyCommand(t, cmd, proxy, server)
		if cmd.Stdout == nil || cmd.Stderr == nil {
			t.Error("adapter did not attach stdout and stderr capture writers")
		} else {
			if _, err := fmt.Fprintln(cmd.Stdout, "fixture stdout line"); err != nil {
				t.Errorf("write fixture stdout: %v", err)
			}
			if _, err := fmt.Fprintln(cmd.Stderr, "fixture stderr line"); err != nil {
				t.Errorf("write fixture stderr: %v", err)
			}
		}
		return tree, nil
	}
	adapter.probe = func(_ context.Context, addr string) error {
		observedAddr = addr
		return nil
	}

	child, err := adapter.Start(context.Background(), "workspace", proxy, server)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	readyCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := child.WaitReady(readyCtx); err != nil {
		t.Fatalf("WaitReady() error = %v", err)
	}
	if got := child.Addr(); got != observedAddr {
		t.Fatalf("Addr() = %q, readiness probed %q", got, observedAddr)
	}
	host, portText, found := strings.Cut(observedAddr, ":")
	if !found || host != "127.0.0.1" {
		t.Fatalf("readiness address = %q, want loopback host and a reserved port", observedAddr)
	}
	port, parseErr := strconv.Atoi(portText)
	if parseErr != nil || port < 1 || port > 65535 {
		t.Fatalf("readiness port = %q, want reserved TCP port", portText)
	}
	stopAdapterProcess(t, child)
	if err := tree.assertStopped(t); err != nil {
		t.Fatal(err)
	}
	waitForAdapterLogs(t, &logs, `"server":"workspace"`, `"stream":"stdout"`, `"msg":"fixture stdout line"`, `"stream":"stderr"`, `"msg":"fixture stderr line"`)
}

func TestExecAdapterReadinessPollingStopsWhenContextIsCanceled(t *testing.T) {
	proxy := config.Proxy{Command: "mcp-proxy-test", StartupTimeout: time.Second}
	server := config.Server{Command: "upstream"}
	adapter := newAdapterUnderTest(t, slog.New(slog.NewTextHandler(&synchronizedBuffer{}, nil)))
	tree := newAdapterTestProcessTree()
	adapter.start = func(_ *exec.Cmd, _ time.Duration) (processTree, error) {
		return tree, nil
	}
	probeStarted := make(chan struct{})
	probeCanceled := make(chan struct{})
	var once sync.Once
	adapter.probe = func(ctx context.Context, _ string) error {
		once.Do(func() { close(probeStarted) })
		<-ctx.Done()
		close(probeCanceled)
		return ctx.Err()
	}

	child, err := adapter.Start(context.Background(), "slow", proxy, server)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	readyDone := make(chan error, 1)
	go func() { readyDone <- child.WaitReady(ctx) }()
	select {
	case <-probeStarted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("readiness polling did not start")
	}
	cancel()
	select {
	case err := <-readyDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitReady() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitReady() did not return after context cancellation")
	}
	select {
	case <-probeCanceled:
	case <-time.After(time.Second):
		t.Fatal("readiness probe did not receive context cancellation")
	}
	stopAdapterProcess(t, child)
	if err := tree.assertStopped(t); err != nil {
		t.Fatal(err)
	}
}

func newAdapterUnderTest(t *testing.T, logger *slog.Logger) *execAdapter {
	t.Helper()
	adapter, ok := NewExecAdapter(logger).(*execAdapter)
	if !ok {
		t.Fatal("NewExecAdapter() did not return the process exec adapter")
	}
	return adapter
}

func assertProxyCommand(t *testing.T, cmd *exec.Cmd, proxy config.Proxy, server config.Server) {
	t.Helper()
	if cmd.Path != proxy.Command {
		t.Errorf("proxy command path = %q, want %q", cmd.Path, proxy.Command)
	}
	if cmd.Dir != server.CWD {
		t.Errorf("proxy cwd = %q, want %q", cmd.Dir, server.CWD)
	}
	if len(cmd.Args) < 8 {
		t.Errorf("proxy argv = %#v, too short for private listener and upstream command", cmd.Args)
		return
	}
	wantPrefix := []string{proxy.Command, "--host", "127.0.0.1", "--port", cmd.Args[4], "--pass-environment", "--", server.Command}
	if len(cmd.Args) < len(wantPrefix) || !slices.Equal(cmd.Args[:len(wantPrefix)], wantPrefix) {
		t.Errorf("proxy argv prefix = %#v, want %#v", cmd.Args, wantPrefix)
	}
	wantArgs := append(slices.Clone(wantPrefix), server.Args...)
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Errorf("proxy argv = %#v, want %#v", cmd.Args, wantArgs)
	}
	gotEnv := make(map[string]string, len(cmd.Env))
	for _, entry := range cmd.Env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			t.Errorf("proxy environment entry %q has no '='", entry)
			continue
		}
		gotEnv[key] = value
	}
	for key, want := range server.Env {
		if got := gotEnv[key]; got != want {
			t.Errorf("proxy environment %s = %q, want %q", key, got, want)
		}
	}
	if _, leaked := gotEnv["MCP_RELAYD_PARENT_ONLY_SENTINEL"]; leaked {
		t.Error("proxy environment leaked a parent-only variable")
	}
	for key, secret := range server.Env {
		if key == "MCP_TOKEN" && slices.Contains(cmd.Args, secret) {
			t.Error("configured environment value was passed in proxy argv")
		}
	}
}

func waitForAdapterLogs(t *testing.T, logs *synchronizedBuffer, fields ...string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		output := logs.String()
		allPresent := true
		for _, field := range fields {
			if !strings.Contains(output, field) {
				allPresent = false
				break
			}
		}
		if allPresent {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("captured logs missing expected server-tagged stdout/stderr fields: %s", logs.String())
}

func stopAdapterProcess(t *testing.T, child Process) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := child.Stop(ctx); err != nil {
		t.Errorf("Stop() error = %v", err)
	}
}

type adapterTestProcessTree struct {
	done chan error
	one  sync.Once
}

func newAdapterTestProcessTree() *adapterTestProcessTree {
	return &adapterTestProcessTree{done: make(chan error, 1)}
}

func (tree *adapterTestProcessTree) PID() int           { return 1234 }
func (tree *adapterTestProcessTree) Done() <-chan error { return tree.done }

func (tree *adapterTestProcessTree) Stop(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	tree.one.Do(func() { close(tree.done) })
	return nil
}

func (tree *adapterTestProcessTree) assertStopped(t *testing.T) error {
	t.Helper()
	select {
	case <-tree.done:
		return nil
	case <-time.After(time.Second):
		return fmt.Errorf("process tree was not stopped")
	}
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}
