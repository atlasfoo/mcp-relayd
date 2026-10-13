package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"mcp-relayd/internal/config"
)

const (
	processGrace  = time.Second
	probeInterval = 50 * time.Millisecond
	maxLogLine    = 64 * 1024
)

type execAdapter struct {
	logger *slog.Logger
	start  func(*exec.Cmd, time.Duration) (processTree, error)
	probe  func(context.Context, string) error
}

// NewExecAdapter creates the adapter for the external mcp-proxy executable.
func NewExecAdapter(logger *slog.Logger) Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	return &execAdapter{logger: logger, start: startProcessTree, probe: func(ctx context.Context, addr string) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/status", nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("proxy status is %d", response.StatusCode)
		}
		return nil
	}}
}

// Start launches a private proxy without giving the startup context ownership
// of its lifetime. The manager separately stops the process on startup failure.
func (adapter *execAdapter) Start(ctx context.Context, name string, proxy config.Proxy, server config.Server) (Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("allocate private proxy address: %w", err)
	}
	addr := listener.Addr().String()
	_, port, err := net.SplitHostPort(addr)
	closeErr := listener.Close()
	if err != nil || closeErr != nil {
		return nil, fmt.Errorf("release private proxy address: %w", errors.Join(err, closeErr))
	}
	args := []string{"--host", "127.0.0.1", "--port", port, "--pass-environment", "--", server.Command}
	args = append(args, server.Args...)
	// Configuration intentionally selects a local executable, with no shell.
	//nolint:noctx // Startup cancellation is separate from process lifetime; processTree owns termination.
	cmd := exec.Command(proxy.Command, args...) // #nosec G204 -- User-selected proxy and upstream are the purpose of this adapter.
	cmd.Dir = server.CWD
	cmd.Env = isolatedEnvironment(server.Env)
	stdout := newLogWriter(adapter.logger, name, "stdout", server.Env)
	stderr := newLogWriter(adapter.logger, name, "stderr", server.Env)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	tree, err := adapter.start(cmd, processGrace)
	if err != nil {
		stdout.flush()
		stderr.flush()
		return nil, fmt.Errorf("launch proxy for server %q: %w", name, err)
	}
	child := &execProcess{tree: tree, addr: addr, probe: adapter.probe, startupTimeout: proxy.StartupTimeout, exited: make(chan struct{}), done: make(chan error, 1)}
	go func() {
		child.exitErr = <-tree.Done()
		stdout.flush()
		stderr.flush()
		close(child.exited)
		child.done <- child.exitErr
		close(child.done)
	}()
	if err := ctx.Err(); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*processGrace)
		defer cancel()
		return nil, errors.Join(err, child.Stop(cleanupCtx))
	}
	return child, nil
}

func isolatedEnvironment(configured map[string]string) []string {
	environment := make(map[string]string, len(configured)+3)
	baseline := []string{"PATH"}
	if runtime.GOOS == "windows" {
		baseline = append(baseline, "SystemRoot", "WINDIR")
	}
	for _, key := range baseline {
		if value, exists := os.LookupEnv(key); exists {
			environment[key] = value
		}
	}
	for key, value := range configured {
		if runtime.GOOS == "windows" {
			for existing := range environment {
				if strings.EqualFold(key, existing) {
					delete(environment, existing)
				}
			}
		}
		environment[key] = value
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+environment[key])
	}
	return result
}

type execProcess struct {
	tree           processTree
	addr           string
	probe          func(context.Context, string) error
	startupTimeout time.Duration
	exited         chan struct{}
	done           chan error
	exitErr        error // Written before exited is closed, read only after receiving exited.
}

func (child *execProcess) Addr() string { return child.addr }
func (child *execProcess) PID() int {
	select {
	case <-child.exited:
		return 0
	default:
		return child.tree.PID()
	}
}

func (child *execProcess) Done() <-chan error             { return child.done }
func (child *execProcess) Stop(ctx context.Context) error { return child.tree.Stop(ctx) }

func (child *execProcess) WaitReady(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, child.startupTimeout)
	defer cancel()
	go func() {
		select {
		case <-child.exited:
			cancel()
		case <-probeCtx.Done():
		}
	}()
	for {
		if err := child.readinessError(probeCtx); err != nil {
			return err
		}
		err := child.probe(probeCtx, child.addr)
		if readinessErr := child.readinessError(probeCtx); readinessErr != nil {
			return readinessErr
		}
		if err == nil {
			return nil
		}
		timer := time.NewTimer(probeInterval)
		select {
		case <-probeCtx.Done():
			timer.Stop()
			return child.readinessError(probeCtx)
		case <-timer.C:
		}
	}
}

func (child *execProcess) readinessError(ctx context.Context) error {
	select {
	case <-child.exited:
		if child.exitErr != nil {
			return fmt.Errorf("proxy exited before readiness: %w", child.exitErr)
		}
		return errors.New("proxy exited before readiness")
	default:
		return ctx.Err()
	}
}

// lineLogWriter bounds unterminated lines and redacts configured environment
// values before handing captured child output to the structured logger.
type lineLogWriter struct {
	mu      sync.Mutex
	logger  *slog.Logger
	secrets []string
	pending string
	discard bool
}

func newLogWriter(logger *slog.Logger, server, stream string, environment map[string]string) *lineLogWriter {
	secrets := make([]string, 0, len(environment))
	for _, value := range environment {
		if value != "" {
			secrets = append(secrets, value)
		}
	}
	// Redact longer values first so an overlapping shorter value cannot expose
	// the remaining part of a longer secret.
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	return &lineLogWriter{logger: logger.With("server", server, "event", "external_output", "stream", stream), secrets: secrets}
}

func (writer *lineLogWriter) Write(data []byte) (int, error) {
	total := len(data)
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for len(data) > 0 {
		if writer.discard {
			newline := slices.Index(data, byte('\n'))
			if newline < 0 {
				break
			}
			data = data[newline+1:]
			writer.discard = false
			continue
		}
		count := min(len(data), maxLogLine-len(writer.pending))
		writer.pending += string(data[:count])
		data = data[count:]
		for {
			line, rest, found := strings.Cut(writer.pending, "\n")
			if !found {
				break
			}
			writer.log(line)
			writer.pending = rest
		}
		if len(writer.pending) == maxLogLine {
			// Suppress oversized lines instead of splitting across secret values.
			writer.pending = ""
			writer.discard = true
			writer.logger.Info("[oversized output omitted]")
		}
	}
	return total, nil
}

func (writer *lineLogWriter) log(line string) {
	for _, secret := range writer.secrets {
		line = strings.ReplaceAll(line, secret, "[redacted]")
	}
	writer.logger.Info(strings.TrimSuffix(line, "\r"))
}

func (writer *lineLogWriter) flush() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.pending != "" {
		writer.log(writer.pending)
		writer.pending = ""
	}
}
