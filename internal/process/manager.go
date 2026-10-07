// Package process supervises one proxy process per enabled upstream server.
package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"mcp-relayd/internal/config"
)

// State describes the lifecycle of an upstream's supervised proxy.
type State string

const (
	StateStopped      State = "STOPPED"
	StateStarting     State = "STARTING"
	StateInitializing State = "INITIALIZING"
	StateReady        State = "READY"
	StateFailed       State = "FAILED"
	StateStopping     State = "STOPPING"
)

// Adapter launches a proxy without interpreting the upstream arguments in a shell.
// Start must honor ctx, which bounds startup rather than the process lifetime.
type Adapter interface {
	Start(ctx context.Context, name string, proxy config.Proxy, server config.Server) (Process, error)
}

// Process owns readiness, exit observation, and termination of a proxy tree.
// Done reports termination once. A nil Stop result confirms tree termination;
// an error leaves termination unconfirmed until Done reports it.
type Process interface {
	WaitReady(ctx context.Context) error
	Addr() string
	Done() <-chan error
	Stop(ctx context.Context) error
}

type managedServer struct {
	config        config.Server
	state         State
	child         Process
	launchDone    chan struct{}
	cancelStartup context.CancelFunc
	stopObserver  chan struct{}
	observerOnce  sync.Once
	termination   chan struct{}
}

// Manager isolates server failures and never automatically restarts a proxy.
type Manager struct {
	mu              sync.RWMutex
	proxy           config.Proxy
	shutdownTimeout time.Duration
	adapter         Adapter
	logger          *slog.Logger
	servers         map[string]*managedServer
	started         bool
	stopping        bool
}

// NewManager constructs a stopped manager from validated configuration.
func NewManager(cfg config.Config, adapter Adapter, loggers ...*slog.Logger) *Manager {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	servers := make(map[string]*managedServer, len(cfg.Servers))
	for name, server := range cfg.Servers {
		servers[name] = &managedServer{config: server, state: StateStopped, termination: make(chan struct{}, 1)}
	}
	return &Manager{proxy: cfg.Proxy, shutdownTimeout: cfg.Gateway.ShutdownTimeout, adapter: adapter, logger: logger, servers: servers}
}

func (m *Manager) logServer(name string, server *managedServer, event, failure string) {
	attrs := []any{"server", name, "state", string(server.state), "event", event}
	if child, ok := server.child.(interface{ PID() int }); ok {
		attrs = append(attrs, "proxy_pid", child.PID())
	} else {
		attrs = append(attrs, "proxy_pid", 0)
	}
	if failure != "" {
		attrs = append(attrs, "error", failure)
	}
	m.logger.Info("proxy lifecycle", attrs...)
}

// Start waits for enabled servers to initialize concurrently. Individual launch,
// readiness, and exit failures remain local to their server. Repeated calls do
// not launch additional processes.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started || m.stopping {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	var workers sync.WaitGroup
	for name, server := range m.servers {
		if !server.config.Autostart {
			continue
		}
		startupCtx, cancel := context.WithTimeout(ctx, m.proxy.StartupTimeout)
		server.cancelStartup = cancel
		server.launchDone = make(chan struct{})
		server.stopObserver = make(chan struct{})
		server.state = StateStarting
		m.logServer(name, server, "state", "")
		workers.Go(func() {
			defer cancel()
			m.startServer(startupCtx, name, server)
		})
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) startServer(ctx context.Context, name string, server *managedServer) {
	child, err := m.adapter.Start(ctx, name, m.proxy, server.config)
	if err != nil {
		child = nil
	}
	m.mu.Lock()
	server.child = child
	if err != nil || child == nil {
		if server.state != StateStopping {
			server.state = StateFailed
		}
	} else if server.state == StateStarting {
		server.state = StateInitializing
	}
	if err != nil || child == nil {
		m.logServer(name, server, "start_failed", "proxy startup failed")
	} else {
		m.logServer(name, server, "state", "")
	}
	close(server.launchDone)
	m.mu.Unlock()
	if child == nil {
		return
	}
	go m.observeExit(server, child)
	err = child.WaitReady(ctx)
	m.mu.Lock()
	if server.state != StateInitializing && (err == nil || server.state != StateFailed) {
		m.mu.Unlock()
		return
	}
	if err != nil {
		server.state = StateFailed
	} else {
		server.state = StateReady
	}
	if err != nil {
		m.logServer(name, server, "readiness_failed", "proxy readiness failed")
	} else {
		m.logServer(name, server, "state", "")
	}
	m.mu.Unlock()
	if err != nil {
		// The startup context may already be expired. Give cleanup its own
		// bounded budget and retain the child when termination is unconfirmed.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), m.shutdownTimeout)
		defer cancel()
		_ = m.terminate(cleanupCtx, name, server, true)
	}
}

func (m *Manager) observeExit(server *managedServer, child Process) {
	select {
	case <-child.Done():
		m.mu.Lock()
		server.cancelStartup()
		if server.state == StateStopping || server.state == StateStopped {
			server.state = StateStopped
		} else {
			server.state = StateFailed
		}
		m.logServer(serverName(m.servers, server), server, "exit", "proxy process exited")
		m.mu.Unlock()
	case <-server.stopObserver:
	}
}

func serverName(servers map[string]*managedServer, target *managedServer) string {
	for name, server := range servers {
		if server == target {
			return name
		}
	}
	return ""
}

// State returns STOPPED for an unknown or disabled server.
func (m *Manager) State(name string) State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if server, ok := m.servers[name]; ok {
		return server.state
	}
	return StateStopped
}

// Addr returns the private address only while the server is READY.
func (m *Manager) Addr(name string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if server, ok := m.servers[name]; ok && server.state == StateReady {
		return server.child.Addr()
	}
	return ""
}

// ProxyPID returns the proxy PID when the child supports reporting it, or zero
// for unknown, stopped, or not yet launched servers.
func (m *Manager) ProxyPID(name string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	server, ok := m.servers[name]
	if !ok || server.state == StateStopped || server.child == nil {
		return 0
	}
	if child, ok := server.child.(interface{ PID() int }); ok {
		return child.PID()
	}
	return 0
}

// Stop cancels startup and terminates active proxies concurrently under one
// caller deadline. A failed termination remains STOPPING until exit is confirmed.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.stopping = true
	results := make(chan error, len(m.servers))
	count := 0
	for name, server := range m.servers {
		if server.launchDone == nil || server.state == StateStopped {
			continue
		}
		server.cancelStartup()
		server.state = StateStopping
		m.logServer(name, server, "state", "")
		count++
		go func() { results <- m.stopServer(ctx, name, server) }()
	}
	m.mu.Unlock()
	var failures []error
	for range count {
		select {
		case err := <-results:
			if err != nil {
				failures = append(failures, err)
			}
		case <-ctx.Done():
			return errors.Join(append(failures, ctx.Err())...)
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) stopServer(ctx context.Context, name string, server *managedServer) error {
	select {
	case <-server.launchDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	return m.terminate(ctx, name, server, false)
}

func (m *Manager) terminate(ctx context.Context, name string, server *managedServer, preserveFailure bool) error {
	// Readiness cleanup and daemon shutdown can overlap. Serialize Stop calls
	// without making the shutdown caller wait beyond its own deadline.
	select {
	case server.termination <- struct{}{}:
		defer func() { <-server.termination }()
	case <-ctx.Done():
		return ctx.Err()
	}
	m.mu.Lock()
	child := server.child
	if child == nil {
		if !preserveFailure || server.state == StateStopping {
			server.state = StateStopped
		}
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	err := child.Stop(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		server.child = nil
		if !preserveFailure || server.state == StateStopping {
			server.state = StateStopped
		}
		server.observerOnce.Do(func() { close(server.stopObserver) })
		m.logServer(name, server, "stopped", "")
		return nil
	}
	m.logServer(name, server, "stop_failed", "proxy shutdown failed")
	// Do not return the child error verbatim: it can contain configured values
	// or protocol bodies that are not safe to expose through manager errors.
	return fmt.Errorf("stop server %q: proxy shutdown failed", name)
}
