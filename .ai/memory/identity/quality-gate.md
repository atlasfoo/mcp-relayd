---
type: quality-gate
title: "Quality Gate"
description: "Uses the documented Go and devenv test, formatting, vet, and build commands."
tags: [identity, quality-gate, verification, go]
updated: "2026-10-03"
---

# Quality Gate

## Phase Gate

- **Tests (affected):** `go test ./...`
- **Linting:** `go vet ./...`
- **Type checking:** `go test ./...` (Go type-checks packages during testing).
- **Formatting:** `gofmt -l cmd` must produce no output. The `check` devenv script enforces this.

## Plan Gate

- **Full test suite:** `devenv test`
- **Build:** `go build -o bin/mcp-relayd ./cmd/mcp-relayd`
