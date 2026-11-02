package cli

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"

	urfavecli "github.com/urfave/cli/v3"

	"mcp-relayd/internal/config"
)

// exampleToml is the reference configuration template compiled into the binary.
//
//go:embed example.toml
var exampleToml []byte

const configUsageText = "mcp-relayd config [--verbose] [--help]"

func newConfig(stdout, stderr io.Writer, version, sourceSHA string) *urfavecli.Command {
	cmd := &urfavecli.Command{
		Name:      "config",
		Usage:     "Generate the default configuration file",
		UsageText: configUsageText,
		Writer:    stdout,
		ErrWriter: stderr,
		Action:    configAction(version, sourceSHA),
	}
	prepareCommand(cmd)
	return cmd
}

func configAction(version, sourceSHA string) urfavecli.ActionFunc {
	return func(_ context.Context, cmd *urfavecli.Command) error {
		if cmd.NArg() != 0 {
			return presentUsage(cmd)
		}
		if cmd.Bool("version") {
			return printVersion(cmd.Root().Writer, version, sourceSHA)
		}

		destPath, err := config.DefaultPath()
		if err != nil {
			return err
		}

		parentDir := filepath.Dir(destPath)
		if err := os.MkdirAll(parentDir, 0o700); err != nil {
			return fmt.Errorf("create configuration directory %q: %w", parentDir, err)
		}

		f, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- destPath is the fixed default path from config.DefaultPath.
		if err != nil {
			return fmt.Errorf("create configuration file %q: %w", destPath, err)
		}
		defer func() {
			_ = f.Close()
		}()

		if _, err := f.Write(exampleToml); err != nil {
			return fmt.Errorf("write configuration file %q: %w", destPath, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close configuration file %q: %w", destPath, err)
		}

		w := cmd.Root().Writer
		if w == nil {
			w = io.Discard
		}
		if _, err := w.Write(greetingTxt); err != nil {
			return err
		}
		return nil
	}
}
