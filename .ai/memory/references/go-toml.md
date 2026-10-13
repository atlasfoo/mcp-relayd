---
type: reference
title: "go-toml/v2"
description: "Strict TOML decoding for global user configuration with selective environment expansion."
tier: code-scope
kind: package
role: upstream
resource: "URL: https://github.com/pelletier/go-toml"
tags: [config, toml, go]
updated: "2026-10-07"
---

# go-toml/v2

## What it is

Go TOML decoder pinned in `go.mod` and `go.sum`.

## How the project uses it

`internal/config/config.go` rejects unknown keys, validates the entire configuration before effects and expands only `${VARIABLE}` values inside env. Arguments are arrays, never shell commands. Relative cwd is resolved from the selected config file.

## How to consult it

Consult upstream decoder documentation and `internal/config/config_test.go`. `examples/mcp-relayd.toml` is validated by a test. The default path is the user's home plus `.config/mcp-relayd.toml` on all supported platforms; `--config` explicitly overrides it.

## Gotchas

Missing config/home/environment variables are errors; there is no cwd fallback or generated empty configuration. Diagnostics must not expose configured environment values.
