---
type: reference
title: "mcp-proxy"
description: "External mcp-proxy 0.12.0 bridge with a hash-locked MCP 1.30.0 SDK dependency closure."
tier: code-scope
kind: package
role: upstream
resource: "URL: https://github.com/sparfenyuk/mcp-proxy/tree/v0.12.0"
tags: [mcp, bridge, python, runtime]
updated: "2026-10-07"
---

# mcp-proxy

## What it is

External Python stdio-to-Streamable-HTTP bridge. It owns upstream negotiation and stdio; Go supervises and routes to it.

## How the project uses it

`internal/process/adapter.go` starts one bridge per enabled definition on a private loopback port and checks `/status` readiness. `internal/gateway` forwards to its `/mcp` endpoint. The binary does not bundle or install the bridge.

## How to consult it

The versioned upstream source above defines the CLI and listener contracts. `tooling/mcp-proxy/requirements.lock` fixes the dependency closure; `scripts/provision-mcp-proxy.py` provisions Python 3.13 with uv, hash validation and wheels. `devenv.nix` invokes it on shell entry. `internal/integration` exercises the real bridge against a controlled stdio fixture.

## Gotchas

The tested pair is mcp-proxy 0.12.0 and MCP SDK 1.30.0; unconstrained SDK upgrades are not safe. Native macOS/Windows behavior remains unverified. Arbitrary server-to-client features and universal MCP compatibility are not guaranteed.
