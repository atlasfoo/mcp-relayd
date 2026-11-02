package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	urfavecli "github.com/urfave/cli/v3"

	"mcp-relayd/internal/app"
	"mcp-relayd/internal/config"
)

// RuntimeFunc starts the configured relay until ctx is canceled.
type RuntimeFunc func(ctx context.Context, cfg config.Config, logger *slog.Logger) error

// runtime is the daemon starter. Production leaves the default, app.Run.
// Tests may replace it for one test and must restore the previous value.
var runtime RuntimeFunc = app.Run

// SetRuntime replaces the daemon starter and returns the previous function.
// A nil next restores app.Run. Production does not call SetRuntime.
func SetRuntime(next RuntimeFunc) RuntimeFunc {
	if next == nil {
		next = app.Run
	}
	previous := runtime
	runtime = next
	return previous
}

func newRun(stdout, stderr io.Writer, version, sourceSHA string) *urfavecli.Command {
	cmd := &urfavecli.Command{
		Name:      "run",
		Usage:     "Run the relay in the foreground",
		UsageText: runUsageText,
		Writer:    stdout,
		ErrWriter: stderr,
		Action:    runAction(version, sourceSHA),
	}
	prepareCommand(cmd)
	return cmd
}

func runAction(version, sourceSHA string) urfavecli.ActionFunc {
	return func(ctx context.Context, cmd *urfavecli.Command) error {
		if cmd.NArg() != 0 {
			return presentUsage(cmd)
		}
		if cmd.Bool("version") {
			return printVersion(cmd.Root().Writer, version, sourceSHA)
		}
		// Configuration is loaded only for an actual run, never from a hook
		// that also serves help or version. --verbose selects the presenter;
		// it does not load configuration by itself.
		cfg, err := config.Load(cmd.String("config"))
		if err != nil {
			return fmt.Errorf("load configuration: %w", err)
		}
		verbose := cmd.Bool("verbose")
		handler := NewPresenter(cmd.Root().ErrWriter, verbose)
		if !verbose {
			// Printed on the first record, not before runtime: a bind or
			// shutdown error that never logs must stay one stderr line.
			handler = withGreeting(handler, greetingTxt)
		}
		logger := slog.New(handler)
		return runtime(ctx, cfg, logger)
	}
}
