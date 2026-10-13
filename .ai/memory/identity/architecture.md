---
type: architecture
title: "Architecture"
description: "A Go daemon with strict TOML configuration, supervised external MCP bridges and a loopback HTTP gateway."
tags: [identity, architecture, go, devenv]
updated: "2026-10-12"
---

# Architecture

- **Architecture style:** One Go daemon supervises a separate external mcp-proxy process per enabled server; each bridge owns one stdio upstream shared by client sessions.
- **Primary language:** Go 1.25.0 minimum, declared in `go.mod`.
- **Primary framework:** Go standard library; no application framework is configured.
- **Persistence:** None.
- **Key packages:** `net/http`, `httputil.ReverseProxy` and `log/slog`; [go-toml/v2](../references/go-toml.md) for strict configuration and [Windows APIs](../references/windows-processes.md) through x/sys. YAML parsing is used by workflow contract tests, not runtime.

The small CLI entry point is `cmd/mcp-relayd/main.go`. `internal/config` loads global user configuration, `internal/process` handles lifecycle and process trees, `internal/gateway` validates local HTTP and forwards MCP routes, and `internal/app` composes signal-aware startup/shutdown. There is no automatic restart or lazy startup. Health and per-server routes remain separate.

[mcp-proxy](../references/mcp-proxy.md) remains an external Python runtime, not part of the Go binary. `devenv.nix` supplies the locked development/CI environment with `GOTOOLCHAIN=local`; [just](../references/just.md) shares build/release recipes across local use and GitHub Actions.

Distribution uses [Build-owned versioning and trusted publication](../decisions/003-build-owned-versioning-and-trusted-publication.md): Build prepares the local bump, verifies and uploads packages, then publishes Git refs in an isolated privileged job. A reusable Release publishes those packages without versioning or Git mutations. The [Actions reference](../references/github-actions.md) owns orchestration, provenance and shared-cache details.
