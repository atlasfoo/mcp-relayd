---
type: reference
title: "urfave/cli"
description: "urfave/cli v3 builds the process command tree without mutating package globals."
tier: code-scope
kind: package
role: upstream
resource: "URL: https://cli.urfave.org/v3/"
tags: [cli, go, urfave]
updated: "2026-11-02"
---

# urfave/cli

## What it is

The v3 command-line framework pinned in `go.mod` as `github.com/urfave/cli/v3` v3.14.0.

## How the project uses it

`internal/cli` builds a fresh command tree per invocation for `run`, `config`, help and version. `cmd/mcp-relayd` owns signals and `os.Exit`. The relay packages do not import this module. Shell completion stays off.

## How to consult it

Consult the v3 docs at the URL above, not v2 examples. The pin and checksum live in `go.mod` and `go.sum`. Instance fields `CustomHelpTemplate` and `CustomRootCommandHelpTemplate` carry help text; do not assign `HelpPrinter` or other package variables.

## Gotchas

Root help ignores `CustomHelpTemplate`. `--verbose` has no `-v` alias, because `-v` is a common version shorthand and version is printed by the command action from injected build metadata. Configuration loads only in the effective `run` action, never from a hook that also serves help or version.
