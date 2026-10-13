---
type: decision
id: ADR-002
title: "Locked external MCP bridge"
description: "Use a separately provisioned, hash-locked mcp-proxy bridge until a native bridge is implemented."
status: accepted
date: "2026-10-12"
tags: [architecture, mcp, bridge, reproducibility]
updated: "2026-10-12"
---

# ADR-002: Locked external MCP bridge

## Context

The initial daemon needs stdio negotiation and Streamable HTTP while Go owns configuration, supervision and routing. Real integration established a tested mcp-proxy 0.12.0 / MCP SDK 1.30.0 pair; an unconstrained incompatible SDK failed to import. The [work closure](../log/2026-10-04-core-daemon-and-github-releases.md) proposed making this runtime boundary explicit, and the developer accepted the proposal during this memory refresh.

## Decision

Retain one external [mcp-proxy](../references/mcp-proxy.md) bridge per enabled server, with one shared stdio upstream. Provision the tested Python dependency closure from the hash lock rather than upgrading it implicitly. The daemon neither bundles nor installs the bridge; Nix/devenv provide the isolated development/CI environment. A native bridge remains future work.

## Alternatives Considered

- Unpinned proxy/SDK installation — not chosen because compatible resolution cannot be assumed and would invalidate the tested contract.
- Native Go bridge in the initial core — deferred by the approved scope; the external bridge permits delivery without implementing stdio negotiation in Go now.

## Consequences

- *Positive:* reproducible integration and a clear ownership boundary for stdio and transport negotiation.
- *Negative / Risks:* Python and the bridge remain runtime prerequisites; capabilities and shared-state constraints depend on the tested proxy.
- *Follow-ups:* exercise real-proxy contracts before changing the lock; treat a native bridge as a separate planned change. Native macOS/Windows behavior remains outside the verified scope.
