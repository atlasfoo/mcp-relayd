---
type: reference
title: "go-toml/v2"
description: "Strict TOML decoding for global user configuration with selective environment expansion."
tier: code-scope
kind: package
role: upstream
resource: "URL: https://github.com/pelletier/go-toml"
tags: [config, toml, go]
updated: "2026-11-02"
---

# go-toml/v2

## What it is

Go TOML decoder pinned in `go.mod` and `go.sum`.

## How the project uses it

`internal/config/config.go` rejects unknown keys, validates the entire configuration before effects and expands only `${VARIABLE}` values inside env. Arguments are arrays, never shell commands. Relative cwd is resolved from the selected config file.

## How to consult it

Consult upstream decoder documentation and `internal/config/config_test.go`. `examples/mcp-relayd.toml` is validated by a test. The default path is the user's home plus `.config/mcp-relayd.toml` on all supported platforms; `--config` explicitly overrides it.

## Gotchas

`Load` still errors when the file, home or an environment variable is missing. There is no cwd fallback. `mcp-relayd config` can write the example to the default path and refuses to overwrite an existing file. Diagnostics must not expose configured environment values.
