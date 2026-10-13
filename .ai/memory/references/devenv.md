---
type: reference
title: "devenv"
description: "Nix-managed Go toolchain, quality scripts and the full local test gate."
tier: workflow
kind: tooling
role: "—"
resource: "CLI devenv / URL: https://devenv.sh/tasks/"
tags: [workflow, tooling, quality]
updated: "2026-10-12"
---

# devenv

## What it is

Nix development environment and task runner.

## How the project uses it

`devenv.nix`, `devenv.yaml` and `devenv.lock` define Go, quality tools, just, gh, actionlint and Python/uv plus run/build/fmt/lint/spellcheck/check scripts. GOTOOLCHAIN=local uses the Nix toolchain. Pinned inputs supply the CI environment; workflows bootstrap pinned Nix/devenv and restore the shared `nix-env-v2` NAR cache. Only successful master Checks writes a missing key; PRs, Build and reusable Release are readers. Keys bind runner OS/architecture and the three environment files, with trusted files used by Build/Release. Bridge provisioning is separate from the NAR cache.

## How to consult it

Read the three devenv files and README. Run `devenv shell` for scripts or `devenv test` for the complete gate. [Official tasks documentation](https://devenv.sh/tasks/).

## Gotchas

The mcp-relayd:quality task is enabled only while testing and runs before devenv:enterTest. It runs `check` and real health/MCP `TestRuntimeSmoke`. Shell entry recreates the hash-locked [bridge](mcp-proxy.md) environment; concurrent shell provisioning can interfere, so verification is serialized. Hosted CORE-006–008 validation was confirmed by the developer on 2026-10-12 in the [work summary](../log/2026-10-04-core-daemon-and-github-releases.md); this does not constitute independent agent inspection of every cold/warm-cache case. Nix/devenv supply Python/uv automatically; a Go-only build without devenv does not require the bridge.
