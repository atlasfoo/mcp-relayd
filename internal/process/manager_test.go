package process_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"mcp-relayd/internal/config"
	"mcp-relayd/internal/process"
)

func TestManagerStartsOnlyAutostartServersAndTracksReadiness(t *testing.T) {
	ready := newFakeProcess("127.0.0.1:41001")
	blocked := newFakeProcess("127.0.0.1:41002")
	blocked.readiness = make(chan struct{})
	adapter := newFakeAdapter(map[string]fakeStart{
		"ready":        {process: ready},
		"initializing": {process: blocked},
	})
	manager := process.NewManager(testConfig(map[string]config.Server{
		"ready":        {Command: "one", Autostart: true},
		"initializing": {Command: "two", Autostart: true},
		"disabled":     {Command: "three", Autostart: false},
	}), adapter)

	startDone := make(chan error, 1)
	go func() { startDone <- manager.Start(context.Background()) }()

	waitForState(t, manager, "initializing", process.StateInitializing)
	if got := manager.State("disabled"); got != process.StateStopped {
		t.Fatalf("disabled server state = %v, want STOPPED", got)
	}
	if got := adapter.startCount("disabled"); got != 0 {
		t.Fatalf("disabled server start count = %d, want 0", got)
	}

	close(blocked.readiness)
	waitForState(t, manager, "initializing", process.StateReady)
	waitForState(t, manager, "ready", process.StateReady)
	select {
	case err := <-startDone:
		if err != nil {
			t.Fatalf("Start() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start() did not return after all enabled servers became ready")
	}
}

func TestManagerIsolatesFailuresAndDoesNotRestartExitedProcesses(t *testing.T) {
	exited := newFakeProcess("127.0.0.1:42001")
	healthy := newFakeProcess("127.0.0.1:42002")
	adapter := newFakeAdapter(map[string]fakeStart{
		"broken":  {startErr: errors.New("proxy launch failed")},
		"exited":  {process: exited},
		"healthy": {process: healthy},
	})
	manager := process.NewManager(testConfig(map[string]config.Server{
		"broken":  {Command: "broken", Autostart: true},
		"exited":  {Command: "exited", Autostart: true},
		"healthy": {Command: "healthy", Autostart: true},
	}), adapter)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v; per-server launch failures must not prevent startup", err)
	}
	waitForState(t, manager, "broken", process.StateFailed)
	waitForState(t, manager, "healthy", process.StateReady)
	waitForState(t, manager, "exited", process.StateReady)

	exited.signalExit(errors.New("upstream exited"))
	waitForState(t, manager, "exited", process.StateFailed)
	if got := adapter.startCount("exited"); got != 1 {
		t.Fatalf("exited server start count = %d, want 1 (no automatic restart)", got)
	}
	if got := manager.State("healthy"); got != process.StateReady {
		t.Fatalf("healthy server state after peer failure = %v, want READY", got)
	}
}

func TestManagerBoundsReadinessByProxyStartupTimeout(t *testing.T) {
	blocked := newFakeProcess("127.0.0.1:43001")
	blocked.readiness = make(chan struct{})
	adapter := newFakeAdapter(map[string]fakeStart{"slow": {process: blocked}})
	cfg := testConfig(map[string]config.Server{
		"slow": {Command: "slow", Autostart: true},
	})
	cfg.Proxy.StartupTimeout = 35 * time.Millisecond
	manager := process.NewManager(cfg, adapter)

	start := time.Now()
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v; readiness failure is isolated to its server", err)
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("Start() took %s, want startup timeout to bound readiness", elapsed)
	}
	if got := manager.State("slow"); got != process.StateFailed {
		t.Fatalf("slow server state = %v, want FAILED after readiness timeout", got)
	}
	if !blocked.readinessContextExpired() {
		t.Fatal("readiness wait did not receive the bounded startup context")
	}
}

func TestManagerStopsChildAfterReadinessFailure(t *testing.T) {
	child := newFakeProcess("127.0.0.1:43501")
	child.readyErr = errors.New("upstream initialization failed")
	adapter := newFakeAdapter(map[string]fakeStart{"failed": {process: child}})
	cfg := testConfig(map[string]config.Server{
		"failed": {Command: "failed", Autostart: true},
	})
	cfg.Gateway.ShutdownTimeout = 100 * time.Millisecond
	manager := process.NewManager(cfg, adapter)

	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v; readiness failure is isolated to its server", err)
	}
	if got := manager.State("failed"); got != process.StateFailed {
		t.Fatalf("failed server state = %v, want FAILED", got)
	}
	if got := adapter.startCount("failed"); got != 1 {
		t.Fatalf("failed server start count = %d, want 1 (no automatic restart)", got)
	}

	select {
	case <-child.stopEntered:
	case <-time.After(time.Second):
		t.Fatal("manager did not stop the child after readiness failed")
	}
	readyCtx := <-child.readyContexts
	stopCtx := <-child.stopContexts
	if stopCtx == readyCtx {
		t.Fatal("child cleanup reused the readiness context instead of a fresh shutdown context")
	}
	deadline, ok := stopCtx.Deadline()
	if !ok {
		t.Fatal("child cleanup context has no bounded shutdown deadline")
	}
	if remaining := time.Until(deadline); remaining <= 0 || remaining > cfg.Gateway.ShutdownTimeout {
		t.Fatalf("child cleanup deadline has %s remaining, want a positive duration no greater than %s", remaining, cfg.Gateway.ShutdownTimeout)
	}
}

func TestManagerBoundsStopAndExposesStoppingState(t *testing.T) {
	first := newFakeProcess("127.0.0.1:44001")
	second := newFakeProcess("127.0.0.1:44002")
	first.blockStop = true
	second.blockStop = true
	adapter := newFakeAdapter(map[string]fakeStart{
		"first":  {process: first},
		"second": {process: second},
	})
	manager := process.NewManager(testConfig(map[string]config.Server{
		"first":  {Command: "first", Autostart: true},
		"second": {Command: "second", Autostart: true},
	}), adapter)
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	stopDone := make(chan error, 1)
	go func() { stopDone <- manager.Stop(ctx) }()
	waitForState(t, manager, "first", process.StateStopping)
	waitForState(t, manager, "second", process.StateStopping)

	select {
	case <-stopDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Stop() exceeded the caller's bounded shutdown deadline")
	}
	if got := manager.State("first"); got == process.StateStopped {
		t.Fatal("first server reported STOPPED without process termination confirmation")
	}
	if got := manager.State("second"); got == process.StateStopped {
		t.Fatal("second server reported STOPPED without process termination confirmation")
	}
}

func testConfig(servers map[string]config.Server) config.Config {
	return config.Config{
		Gateway: config.Gateway{ShutdownTimeout: time.Second},
		Proxy:   config.Proxy{Command: "mcp-proxy", StartupTimeout: time.Second},
		Servers: servers,
	}
}

func waitForState(t *testing.T, manager *process.Manager, name string, want process.State) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := manager.State(name); got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("server %q state = %v, want %v", name, manager.State(name), want)
}

type fakeStart struct {
	process  *fakeProcess
	startErr error
}

type fakeAdapter struct {
	mu     sync.Mutex
	starts map[string]fakeStart
	calls  map[string]int
}

func newFakeAdapter(starts map[string]fakeStart) *fakeAdapter {
	return &fakeAdapter{starts: starts, calls: make(map[string]int)}
}

func (a *fakeAdapter) Start(_ context.Context, name string, proxy config.Proxy, server config.Server) (process.Process, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls[name]++
	if proxy.StartupTimeout <= 0 {
		return nil, fmt.Errorf("adapter received no startup timeout for %q", name)
	}
	if server.Command == "" {
		return nil, fmt.Errorf("adapter received no command for %q", name)
	}
	result, ok := a.starts[name]
	if !ok {
		return nil, fmt.Errorf("unexpected start for %q", name)
	}
	return result.process, result.startErr
}

func (a *fakeAdapter) startCount(name string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls[name]
}

type fakeProcess struct {
	addr          string
	readiness     chan struct{}
	readyErr      error
	readyContexts chan context.Context
	readyContext  chan error
	stopEntered   chan struct{}
	stopContexts  chan context.Context
	stopErr       error
	blockStop     bool
	exitCh        chan error
	stopOnce      sync.Once
}

func newFakeProcess(addr string) *fakeProcess {
	return &fakeProcess{
		addr:          addr,
		readyContexts: make(chan context.Context, 1),
		readyContext:  make(chan error, 1),
		stopEntered:   make(chan struct{}),
		stopContexts:  make(chan context.Context, 1),
		exitCh:        make(chan error, 1),
	}
}

func (p *fakeProcess) WaitReady(ctx context.Context) error {
	p.readyContexts <- ctx
	if p.readiness != nil {
		select {
		case <-p.readiness:
			p.readyContext <- nil
			return p.readyErr
		case <-ctx.Done():
			p.readyContext <- ctx.Err()
			return ctx.Err()
		}
	}
	p.readyContext <- nil
	return p.readyErr
}

func (p *fakeProcess) Addr() string { return p.addr }

func (p *fakeProcess) Done() <-chan error { return p.exitCh }

func (p *fakeProcess) Stop(ctx context.Context) error {
	p.stopContexts <- ctx
	p.stopOnce.Do(func() { close(p.stopEntered) })
	if p.blockStop {
		<-ctx.Done()
		return ctx.Err()
	}
	return p.stopErr
}

func (p *fakeProcess) signalExit(err error) { p.exitCh <- err }

func (p *fakeProcess) readinessContextExpired() bool {
	select {
	case err := <-p.readyContext:
		return errors.Is(err, context.DeadlineExceeded)
	default:
		return false
	}
}
