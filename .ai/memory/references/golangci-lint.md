---
type: reference
title: "golangci-lint"
description: "Go formatting and static analysis configured in .golangci.yml."
tier: workflow
kind: tooling
role: "—"
resource: "CLI golangci-lint / URL: https://golangci-lint.run/docs/configuration/file/"
tags: [workflow, tooling, quality]
updated: "2026-10-04"
---

# golangci-lint

## What it is

Go formatter and linter used by the development quality workflow.

## How the project uses it

`.golangci.yml` enables gofumpt and goimports plus the standard linter set and focused additional checks. `devenv.nix` supplies the CLI.

## How to consult it

Read `.golangci.yml`; use `fmt` to apply formatting and `lint` to check format and analysis inside `devenv shell`. [Official configuration](https://golangci-lint.run/docs/configuration/file/).

## Gotchas

Configuration is v2. Formatting fixes are separate from the non-mutating lint gate. Nix revisions are fixed by `devenv.lock`.

