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
	"sort"
	"strings"
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
	logger.Info("relay", slog.String("event", "gateway_bound"), slog.String("addr", listener.Addr().String()))

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()

	startErr := manager.Start(ctx)
	if startErr != nil && !errors.Is(startErr, context.Canceled) && !errors.Is(startErr, context.DeadlineExceeded) {
		return errors.Join(fmt.Errorf("start proxy manager: %w", startErr), shutdown(server, manager, cfg.Gateway.ShutdownTimeout, logger))
	}
	if ctx.Err() == nil {
		var readyCount, failedCount int
		var disabled []string
		for name, srv := range cfg.Servers {
			if !srv.Autostart {
				disabled = append(disabled, name)
			}
			switch manager.State(name) {
			case process.StateReady:
				readyCount++
			case process.StateFailed:
				failedCount++
			}
		}
		sort.Strings(disabled)
		logger.Info("relay",
			slog.String("event", "startup_complete"),
			slog.Int("ready", readyCount),
			slog.Int("failed", failedCount),
			slog.String("disabled", strings.Join(disabled, ",")),
		)

		select {
		case <-ctx.Done():
		case err := <-serveErr:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				startErr = fmt.Errorf("serve local gateway: %w", err)
			}
		}
	}
	stopErr := shutdown(server, manager, cfg.Gateway.ShutdownTimeout, logger)
	if startErr != nil && !errors.Is(startErr, context.Canceled) && !errors.Is(startErr, context.DeadlineExceeded) {
		return errors.Join(startErr, stopErr)
	}
	return stopErr
}

func shutdown(server *http.Server, manager *process.Manager, timeout time.Duration, logger *slog.Logger) error {
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
	if logger != nil {
		logger.Info("relay", slog.String("event", "shutdown_complete"))
	}
	return errors.Join(drainErr, closeErr, processErr)
}
