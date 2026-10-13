---
type: quality-gate
title: "Quality Gate"
description: "Uses golangci-lint, typos, Go tests and devenv for the local quality and build gates."
tags: [identity, quality-gate, verification, go]
updated: "2026-10-12"
---

# Quality Gate

## Phase Gate

- **Tests (affected):** `go test ./...`
- **Linting and formatting:** `golangci-lint fmt --diff ./...` and `golangci-lint run ./...` (the `lint` script).
- **Spelling:** `typos --force-exclude .` (the `spellcheck` script).
- **Vet:** `go vet ./...`.
- **Type checking:** `go test ./...` (Go type-checks packages during testing).
- **Combined gate:** `check` inside `devenv shell` runs lint, spelling and Go tests without modifying files.

## Plan Gate

- **Full test suite:** `devenv test`
- **Build:** `go build -o bin/mcp-relayd ./cmd/mcp-relayd`

The devenv test task runs `check` and the uncached `TestRuntimeSmoke`, exercising
health and MCP through the real external bridge. Linux `go test ./...` includes
offline eligibility, packaging, workflow and publication contracts. Checks CI also
runs uncached process/gateway/integration tests and `actionlint -shellcheck= -pyflakes=`.
These CI-specific checks do not replace the phase/plan gates above. Native
macOS/Windows tests remain outside the approved scope; cross-compilation does not
verify native behavior. The developer confirmed hosted CORE-006–008 validation
on 2026-10-12, as recorded in the [work summary](../log/2026-10-04-core-daemon-and-github-releases.md).
This is human-reported evidence, not an independent agent inspection of hosted runs
or every cold/warm-cache scenario. The gate commands above are unchanged. Tool contracts live in
[golangci-lint](../references/golangci-lint.md),
[typos](../references/typos.md) and [devenv](../references/devenv.md).
