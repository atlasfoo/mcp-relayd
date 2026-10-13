---
type: index
title: "mcp-relayd — memory index"
description: "Entry point to this repository's constitutive memory."
tags: [index]
updated: "2026-10-12"
---

# mcp-relayd — Memory Index

## `project`

- [mcp-relayd](identity/project.md) — A Go relay that exposes locally run stdio MCP servers through one Streamable HTTP process.

## `architecture`

- [Architecture](identity/architecture.md) — A Go daemon with strict TOML configuration, supervised external MCP bridges and a loopback HTTP gateway.

## `purpose`

- [Purpose](identity/purpose.md) — Simplifies local MCP administration and reduces duplicated process and initialization overhead for agent integrations.

## `scope`

- [Scope](identity/scope.md) — Owns stdio-MCP relay and Streamable HTTP exposure; it does not implement the relayed MCP servers.

## `quality-gate`

- [Quality Gate](identity/quality-gate.md) — Uses golangci-lint, typos, Go tests and devenv for the local quality and build gates.

## `convention`

- [Conventions](identity/conventions.md) — Uses TDD, golangci-lint formatting and Nix-managed commit and quality tooling.

## `decision`

- [ADR-001 — Adoption of the JAIBA brain structure](decisions/001-jaiba-brain-adoption.md) — The project keeps agent-facing memory in .ai/ under the JAIBA framework.
- [ADR-002 — Locked external MCP bridge](decisions/002-locked-external-mcp-bridge.md) — Use a separately provisioned, hash-locked mcp-proxy bridge until a native bridge is implemented.
- [ADR-003 — Build-owned versioning and trusted publication](decisions/003-build-owned-versioning-and-trusted-publication.md) — Build prepares the version and publishes verified refs after upload; reusable Release only publishes assets.

## `reference`

- [Commitizen](references/commitizen.md) — Conventional Commit validation, SemVer versions and changelog generation configured in .cz.toml.
- [devenv](references/devenv.md) — Nix-managed Go toolchain, quality scripts and the full local test gate.
- [GitHub Actions](references/github-actions.md) — Linux-only Checks and Build with reusable Release, exact producer provenance and a shared single-writer NAR cache.
- [Release GitHub App](references/github-app.md) — Repository-scoped GitHub App token authenticates atomic version/tag pushes and publication.
- [GitHub Releases](references/github-releases.md) — Publication-only reusable Release attaches six verified archives and SHA256SUMS after Build publishes the bump/tag.
- [go-toml/v2](references/go-toml.md) — Strict TOML decoding for global user configuration with selective environment expansion.
- [golangci-lint](references/golangci-lint.md) — Go formatting and static analysis configured in .golangci.yml.
- [just](references/just.md) — Versioned justfile shares eligibility, packaging, verification and CI publication recipes.
- [Lefthook](references/lefthook.md) — Local Git hooks enforce Go formatting, spelling and Conventional Commits.
- [mcp-proxy](references/mcp-proxy.md) — External mcp-proxy 0.12.0 bridge with a hash-locked MCP 1.30.0 SDK dependency closure.
- [MCP Streamable HTTP](references/mcp.md) — MCP transport contract for per-server loopback HTTP routes and shared stdio upstreams.
- [Product roadmap](references/product-roadmap.md) — Vendored product vision defining the daemon phases and six distribution targets.
- [typos](references/typos.md) — English spelling and filename checks configured in .typos.toml.
- [Windows process containment](references/windows-processes.md) — Windows console groups and Job Objects contain supervised bridge descendants through x/sys.

## `log-entry`

[`log/`](log/) holds append-only dated records of closed work and brain changes.
