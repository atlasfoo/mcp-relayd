package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"mcp-relayd/internal/cli"
)

var version = "dev"

var sourceSHA = "unknown"

func main() {
	os.Exit(mainExit())
}

func mainExit() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return exitCode(ctx, os.Args[1:], os.Stdout, os.Stderr)
}

func exitCode(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if err := runContext(ctx, args, stdout, stderr); err != nil {
		// Usage text and operational failures are already on stderr.
		// A second line would duplicate that diagnostic.
		if !errors.Is(err, cli.ErrUsage) && !cli.Presented(err) {
			slog.New(slog.NewJSONHandler(stderr, nil)).Error("daemon stopped with error", "error", err)
		}
		return 1
	}
	return 0
}

func run(args []string, stdout, stderr io.Writer) error {
	return runContext(context.Background(), args, stdout, stderr)
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return cli.Execute(ctx, args, stdout, stderr, version, sourceSHA)
}
