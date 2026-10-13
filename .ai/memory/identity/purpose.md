---
type: purpose
title: "Purpose"
description: "Simplifies local MCP administration and reduces duplicated process and initialization overhead for agent integrations."
tags: [identity, purpose, mcp, agents]
updated: "2026-10-12"
---

# Purpose

- **Business objective:** Provide one local relay for MCP servers so teams can administer them centrally, avoid running duplicate stdio processes for multiple agents or multiplexed sessions, and reduce initialization overhead in ACP-style integrations.
- **Position in the bigger picture:** A local integration component between agent clients and locally executed MCP servers.

## Relations

- [MCP Streamable HTTP](../references/mcp.md) defines the client-facing transport boundary; [mcp-proxy](../references/mcp-proxy.md) supplies the temporary external bridge.
