// Package app composes the daemon's process manager and public gateway.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"mcp-relayd/internal/config"
	"mcp-relayd/internal/gateway"
	"mcp-relayd/internal/process"
)

// Run starts the configured proxies and HTTP gateway until ctx is canceled.
// The shutdown budget is shared by HTTP draining and all supervised processes.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	manager := process.NewManager(cfg, process.NewExecAdapter(logger), logger)
	server, err := gateway.NewServer(cfg.Gateway, gateway.NewManagerRegistry(manager, cfg))
	if err != nil {
		return fmt.Errorf("configure gateway: %w", err)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen on local gateway: %w", err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	startErr := manager.Start(ctx)
	if startErr != nil && !errors.Is(startErr, context.Canceled) && !errors.Is(startErr, context.DeadlineExceeded) {
		return errors.Join(fmt.Errorf("start proxy manager: %w", startErr), shutdown(server, manager, cfg.Gateway.ShutdownTimeout))
	}
	if ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case err := <-serveErr:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				startErr = fmt.Errorf("serve local gateway: %w", err)
			}
		}
	}
	stopErr := shutdown(server, manager, cfg.Gateway.ShutdownTimeout)
	if startErr != nil && !errors.Is(startErr, context.Canceled) && !errors.Is(startErr, context.DeadlineExceeded) {
		return errors.Join(startErr, stopErr)
	}
	return stopErr
}

func shutdown(server *http.Server, manager *process.Manager, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Keep enough of the single global budget for process-tree termination after
	// draining client connections. Shutdown closes listeners and waits only for
	// this drain window; Close then force-closes any streams that did not finish.
	processBudget := min(3*time.Second, timeout-timeout/4)
	drainCtx, cancelDrain := context.WithTimeout(ctx, timeout-processBudget)
	drainErr := server.Shutdown(drainCtx)
	cancelDrain()
	closeErr := server.Close()
	if errors.Is(drainErr, context.DeadlineExceeded) && ctx.Err() == nil {
		// An expired drain window is the reason to force-close residual streams,
		// not a failed daemon shutdown, provided process cleanup still succeeds.
		drainErr = nil
	}
	if errors.Is(closeErr, net.ErrClosed) {
		closeErr = nil
	}
	processErr := manager.Stop(ctx)
	return errors.Join(drainErr, closeErr, processErr)
}
