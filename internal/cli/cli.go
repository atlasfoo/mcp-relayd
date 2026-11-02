// Package cli builds a fresh command tree for each invocation.
// It does not mutate urfave/cli package globals or run package init side effects.
package cli

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	urfavecli "github.com/urfave/cli/v3"
)

// greetingTxt is the project ASCII art, compiled into the binary.
// The installed command must not look for this file on disk.
//
//go:embed greeting.txt
var greetingTxt []byte

const (
	name = "mcp-relayd"

	// usageText is the only usage diagnostic. It must not include caller input.
	usageText = "invalid command arguments; see --help"

	rootUsageText = `mcp-relayd run [--config PATH] [--verbose]
   mcp-relayd --help
   mcp-relayd --version`

	runUsageText = "mcp-relayd run [--config PATH] [--verbose] [--help]"
)

// ErrUsage is a command-line usage failure already written to stderr.
// The message is fixed and never includes the rejected argument or flag value.
var ErrUsage = errors.New(usageText)

// stoppedMessage is the verbose failure record. It matches the wording previously
// written by mainExit so verbose mode does not invent a second diagnostic.
const stoppedMessage = "daemon stopped with error"

// presentedError is an operational failure already written to stderr.
// Its Error text is the original safe load, bind, or shutdown error.
type presentedError struct {
	error
}

func (e presentedError) Unwrap() error { return e.error }

// Presented reports whether Execute already wrote err to stderr.
// main must not append another log line for it.
func Presented(err error) bool {
	var target presentedError
	return errors.As(err, &target)
}

// Execute parses args and runs the matching command.
// args excludes the program name. A new command tree is built on every call.
// A clean cancellation or clean shutdown returns nil. Any other operational
// failure is written once to stderr and returned as a presented error.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, version, sourceSHA string) error {
	root := newRoot(stdout, stderr, version, sourceSHA)
	err := root.Run(ctx, append([]string{name}, args...))
	if err == nil || errors.Is(err, ErrUsage) || Presented(err) {
		return err
	}
	if cleanCancellation(ctx, err) {
		return nil
	}
	return presentOperational(stderr, root.Bool("verbose"), err)
}

func newRoot(stdout, stderr io.Writer, version, sourceSHA string) *urfavecli.Command {
	cmd := &urfavecli.Command{
		Name:      name,
		Usage:     "Expose local stdio MCP servers through one Streamable HTTP process",
		UsageText: rootUsageText,
		Flags:     newPersistentFlags(),
		Commands: []*urfavecli.Command{
			newRun(stdout, stderr, version, sourceSHA),
			newConfig(stdout, stderr, version, sourceSHA),
		},
		Action:    rootAction(version, sourceSHA),
		Writer:    stdout,
		ErrWriter: stderr,
	}
	prepareCommand(cmd)
	return cmd
}

func rootAction(version, sourceSHA string) urfavecli.ActionFunc {
	return func(_ context.Context, cmd *urfavecli.Command) error {
		if cmd.NArg() != 0 {
			return presentUsage(cmd)
		}
		if cmd.Bool("version") {
			return printVersion(cmd.Root().Writer, version, sourceSHA)
		}
		// The framework handles --help before this action. No arguments and no
		// --version means the caller asked for help and must not load configuration.
		return urfavecli.ShowRootCommandHelp(cmd)
	}
}

func printVersion(w io.Writer, version, sourceSHA string) error {
	if _, err := w.Write(greetingTxt); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "%s %s %s\n", name, version, sourceSHA)
	return err
}

// greetingTemplatePrefix is the embedded art as literal template text.
// text/template treats "{{" as an action, so any occurrence is escaped
// and the rendered help still begins with the file bytes.
func greetingTemplatePrefix() string {
	return strings.ReplaceAll(string(greetingTxt), "{{", `{{"{{"}}`)
}

// newPersistentFlags returns flags that parse before or after a subcommand.
// Each call allocates new flag values so one invocation cannot leak into the next.
// They stay non-local: a local flag would not be visible on both sides of run.
// --verbose and --version have no short aliases, so -v is neither of them.
func newPersistentFlags() []urfavecli.Flag {
	return []urfavecli.Flag{
		&urfavecli.StringFlag{
			Name:        "config",
			Usage:       "Override the global configuration file",
			HideDefault: true,
		},
		&urfavecli.BoolFlag{
			Name:        "verbose",
			Usage:       "Select verbose diagnostic logging",
			HideDefault: true,
		},
		&urfavecli.BoolFlag{
			Name:        "version",
			Usage:       "Show the build version without loading configuration",
			HideDefault: true,
		},
	}
}

// prepareCommand configures exit and usage handling on this command instance.
// Shell completion stays off so "completion" is not a registered command.
// HideVersion suppresses the framework -v/--version flag; version output is
// printed by the command action from the injected build metadata.
func prepareCommand(cmd *urfavecli.Command) {
	cmd.EnableShellCompletion = false
	cmd.HideHelpCommand = true
	cmd.HideVersion = true
	cmd.Suggest = false
	cmd.OnUsageError = onUsageError
	cmd.ExitErrHandler = discardExit
	// Instance fields only. Root help is rendered from CustomRootCommandHelpTemplate;
	// subcommand help uses CustomHelpTemplate. Neither path may assign a urfave package global.
	prefix := greetingTemplatePrefix()
	cmd.CustomHelpTemplate = prefix + urfavecli.CommandHelpTemplate
	cmd.CustomRootCommandHelpTemplate = prefix + urfavecli.RootCommandHelpTemplate
}

// onUsageError drops the parser error. Its text can repeat the rejected flag
// or value, and the framework must not print that text itself.
func onUsageError(_ context.Context, cmd *urfavecli.Command, _ error, _ bool) error {
	return presentUsage(cmd)
}

func presentUsage(cmd *urfavecli.Command) error {
	_, _ = fmt.Fprintln(errWriter(cmd), usageText)
	return ErrUsage
}

func errWriter(cmd *urfavecli.Command) io.Writer {
	if cmd.ErrWriter != nil {
		return cmd.ErrWriter
	}
	return cmd.Root().ErrWriter
}

// discardExit leaves process termination to main. It does not print: usage
// text and operational failures are already written by Execute.
func discardExit(context.Context, *urfavecli.Command, error) {}

// presentOperational writes one safe explanation. Normal mode is a single
// non-JSON line; verbose mode is one JSON line with the existing wording.
// The returned error keeps the original text and does not write it again.
func presentOperational(w io.Writer, verbose bool, err error) error {
	if w == nil {
		w = io.Discard
	}
	if verbose {
		slog.New(NewPresenter(w, true)).Error(stoppedMessage, "error", err)
	} else {
		_, _ = fmt.Fprintln(w, humanFailureLine(err))
	}
	return presentedError{error: err}
}

func humanFailureLine(err error) string {
	line := strings.Join(strings.Fields(err.Error()), " ")
	switch {
	case line == "":
		return stoppedMessage
	case strings.HasPrefix(line, "{"):
		return stoppedMessage + ": " + line
	default:
		return line
	}
}

// cleanCancellation is a run-context cancel whose text is only the cancel.
// A wrapped bind or shutdown failure stays an error. Logged MCP failures are
// not errors: a nil runtime result is already a clean shutdown.
func cleanCancellation(ctx context.Context, err error) bool {
	if ctx == nil || ctx.Err() == nil {
		return false
	}
	return errors.Is(err, context.Canceled) && err.Error() == context.Canceled.Error()
}
