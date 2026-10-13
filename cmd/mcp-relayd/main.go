package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"mcp-relayd/internal/app"
	"mcp-relayd/internal/config"
)

var version = "dev"

var sourceSHA = "unknown"

const usage = `Usage: mcp-relayd run [--config PATH]
       mcp-relayd --help
       mcp-relayd --version

Options:
  --config PATH  Override the global configuration file.
                 Default: ~/.config/mcp-relayd.toml
                 Windows: %USERPROFILE%\.config\mcp-relayd.toml
  --help         Show this help without loading configuration.
  --version      Show the build version without loading configuration.

`

func main() {
	os.Exit(mainExit())
}

func mainExit() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("daemon stopped with error", "error", err)
		return 1
	}
	return 0
}

func run(args []string, stdout, stderr io.Writer) error {
	return runContext(context.Background(), args, stdout, stderr)
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		_, err := io.WriteString(stdout, usage)
		return err
	}
	isRun := args[0] == "run"
	if isRun {
		args = args[1:]
	}
	flags := flag.NewFlagSet("mcp-relayd", flag.ContinueOnError)
	// Flag errors can contain user-supplied values. Emit a fixed diagnostic instead.
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "Configuration path")
	help := flags.Bool("help", false, "Show help")
	showVersion := flags.Bool("version", false, "Show version")
	if err := flags.Parse(args); err != nil {
		_, _ = io.WriteString(stderr, usage)
		return errors.New("invalid command arguments; see --help")
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected command or positional argument; see --help")
	}
	if *help {
		_, err := io.WriteString(stdout, usage)
		return err
	}
	if *showVersion {
		_, err := fmt.Fprintf(stdout, "mcp-relayd %s %s\n", version, sourceSHA)
		return err
	}
	if !isRun {
		return errors.New("expected run command; see --help")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	return app.Run(ctx, cfg, logger)
}
