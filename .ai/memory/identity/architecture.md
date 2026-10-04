---
type: architecture
title: "Architecture"
description: "A single Go executable, currently using the standard library and a devenv-managed Go toolchain."
tags: [identity, architecture, go, devenv]
updated: "2026-10-03"
---

# Architecture

- **Architecture style:** Single-process relay; the current codebase is a minimal executable entry point.
- **Primary language:** Go 1.25.0 minimum, declared in `go.mod`.
- **Primary framework:** Go standard library; no application framework is configured.
- **Persistence:** None.
- **Key packages:** Go standard library (`fmt`); application dependencies have not been added yet.

The executable entry point is `cmd/mcp-relayd/main.go`. Future application logic belongs under `internal/`, as documented in the README. `devenv.nix` supplies the local Go toolchain and sets `GOTOOLCHAIN=local`.
