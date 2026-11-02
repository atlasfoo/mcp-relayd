---
type: decision
id: ADR-004
title: "urfave/cli v3 and controlled process output"
description: "Use urfave/cli v3 for the process CLI, with instance-local help and JSON logs only under --verbose."
status: accepted
date: "2026-11-02"
tags: [architecture, cli, logging]
updated: "2026-11-02"
---

# ADR-004: urfave/cli v3 and controlled process output

## Context

The process entry printed JSON logs on every run, including external bridge output, so gateway startup and MCP failures were hard to see. The developer chose a full CLI framework without a TUI or a second configuration library. The choice is recorded in the [basic-cli closure](../log/2026-10-28-basic-cli.md).

## Decision

Build the process command tree with [urfave/cli v3](../references/urfave-cli.md), pinned in `go.mod`. Each invocation gets a new command tree. Help and version printers stay on that instance; package globals such as `HelpPrinter` are not assigned. The relay core does not import the CLI package.

Normal `run` output is brief human text on stderr, including the embedded greeting and ready-server URLs. `--verbose` replaces that presentation with JSON logs. Help, version and `config` do not load configuration in order to print themselves, and they do not start the relay.

## Alternatives Considered

- *Keep the hand-rolled parser* — rejected; flags, help and future subcommands were already awkward to extend.
- *A TUI or Viper* — rejected; plain text must work in pipes and services, and TOML loading stays in `internal/config`.
- *Mutating urfave package globals* — rejected; tests invoke the CLI repeatedly and must not share printer state.

## Consequences

- *Positive:* help, version and `run` have one command tree; diagnostic JSON is opt-in; new subcommands can join `internal/cli` without touching the relay core.
- *Negative / Risks:* urfave v3 help templates differ between root and subcommands, so a banner has to be set on both instance fields.
- *Follow-ups:* future product commands stay out until they are implemented. Do not announce them in help or the README before that.
