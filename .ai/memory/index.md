---
type: index
title: "mcp-relayd — memory index"
description: "Entry point to this repository's constitutive memory."
tags: [index]
updated: "2026-10-03"
---

# mcp-relayd — Memory Index

## `project`

- [project.md](identity/project.md) — A greenfield Go relay that exposes locally run stdio MCP servers through one Streamable HTTP process.

## `architecture`

- [architecture.md](identity/architecture.md) — A single Go executable, currently using the standard library and a devenv-managed Go toolchain.

## `purpose`

- [purpose.md](identity/purpose.md) — Simplifies local MCP administration and reduces duplicated process and initialization overhead for agent integrations.

## `scope`

- [scope.md](identity/scope.md) — Owns stdio-MCP relay and Streamable HTTP exposure; it does not implement the relayed MCP servers.

## `quality-gate`

- [quality-gate.md](identity/quality-gate.md) — Uses the documented Go and devenv test, formatting, vet, and build commands.

## `convention`

- [conventions.md](identity/conventions.md) — Uses TDD and project-local Go tooling as the source of style and planning conventions.

## `decision`

- [ADR-001 — Adoption of the JAIBA brain structure](decisions/001-jaiba-brain-adoption.md) — The project keeps agent-facing memory in .ai/ under the JAIBA framework.

## `log-entry`

[`log/`](log/) holds append-only dated records of closed work and brain changes.
