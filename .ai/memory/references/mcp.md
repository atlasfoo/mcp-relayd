---
type: reference
title: "MCP Streamable HTTP"
description: "MCP transport contract for per-server loopback HTTP routes and shared stdio upstreams."
tier: code-scope
kind: external-api
role: upstream
resource: "URL: https://modelcontextprotocol.io/specification/2025-06-18/basic/transports"
tags: [mcp, transport, http]
updated: "2026-10-07"
---

# MCP Streamable HTTP

## What it is

Model Context Protocol transport specification used by clients and the external bridge, not an external hosted service.

## How the project uses it

The gateway exposes `/mcp/{name}`, forwarding POST/GET/DELETE, session headers, status codes, queries and streaming to each private bridge. Host/Origin are validated before forwarding; absent Origin is permitted for CLI clients. Health is separate.

## How to consult it

Consult the transport specification above, `internal/gateway` contracts and `internal/integration/proxy_test.go`. The local stdio fixture is in `internal/integration/testdata/stdio-mcp`.

## Gotchas

Support is bounded by the pinned bridge. Sampling, elicitation, roots, arbitrary server-to-client notifications and complete serverInfo preservation are not guaranteed. Session cancellation does not terminate the shared upstream.
