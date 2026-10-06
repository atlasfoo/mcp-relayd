package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"mcp-relayd/internal/config"
)

var version = "dev"

const usage = `Usage: mcp-relayd run [--config PATH]
       mcp-relayd --help
       mcp-relayd --version

Options:
  --config PATH  Override the global configuration file.
                 Default: ~/.config/mcp-relayd.toml
                 Windows: %USERPROFILE%\.config\mcp-relayd.toml
  --help         Show this help without loading configuration.
  --version      Show the build version without loading configuration.

This build validates configuration; daemon runtime is not yet available.
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "mcp-relayd: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
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
		_, err := fmt.Fprintf(stdout, "mcp-relayd %s\n", version)
		return err
	}
	if !isRun {
		return errors.New("expected run command; see --help")
	}
	if _, err := config.Load(*configPath); err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	_, err := io.WriteString(stdout, "Configuration valid. Daemon runtime is not yet available in this build.\n")
	return err
}
