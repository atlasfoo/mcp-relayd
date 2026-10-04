---
type: scope
title: "Scope"
description: "Owns stdio-MCP relay and Streamable HTTP exposure; it does not implement the relayed MCP servers."
tags: [identity, scope, mcp, http]
updated: "2026-10-03"
---

# Scope

- **In scope:**
  - Run and relay local MCP servers that use stdio.
  - Expose a single Streamable HTTP process for agent clients.
  - Multiplex or share local MCP-server execution where appropriate to reduce system load and initialization overhead.
- **Out of scope:**
  - Implement the tools and business logic provided by relayed MCP servers.
  - Operate remote MCP infrastructure or a hosted control plane.
- **Cross-cutting packages:** None.

## Sub-units

Single unit.

## Relations

No concrete external surface is implemented or recorded yet.
