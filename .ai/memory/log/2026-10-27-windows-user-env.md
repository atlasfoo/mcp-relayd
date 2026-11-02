---
type: log-entry
date: 2026-10-28
slug: windows-user-env
kind: work-closure
depth: design
adr: none
---

# Windows user-directory environment baseline

## What happened

Fixed issue #5 by passing available Windows user-directory variables to supervised MCP processes without inheriting the full parent environment. HOME is derived from the effective USERPROFILE only when configuration does not explicitly define HOME and the inherited value is absent or empty. Linux and Darwin retain the PATH-only baseline plus configured values.

## Criteria delivered

None — design depth; done = plan scope + gate. All six scope deliverables were verified as met.

## Phases executed

| # | Theme | Notable result |
|---|---|---|
| 1 | Windows baseline | Linux-testable environment policy covers copying, omission, HOME derivation, case-insensitive overrides and isolation. |
| 2 | Documentation | README describes Windows variables and HOME precedence. |

## Decisions and deviations

Explicitly configured HOME, including an empty value, blocks derivation. Only existing baseline variables are copied; unrelated parent values remain isolated. No scope deviations or waived checks.

## ADR proposals & brain updates

No ADR proposed; all decisions were tactical. No new external integration. The bridge and process-containment contracts remain unchanged: [ADR-002](../decisions/002-locked-external-mcp-bridge.md), [mcp-proxy](../references/mcp-proxy.md), [Windows process containment](../references/windows-processes.md).

## Pointers

- [Issue #5](https://github.com/atlasfoo/mcp-relayd/issues/5).
- `internal/process/adapter.go`, `internal/process/adapter_test.go`, `README.md`.
- [Architecture](../identity/architecture.md), [scope](../identity/scope.md), [conventions](../identity/conventions.md), [quality gate](../identity/quality-gate.md).

## Suggested final commit

```text
fix(process): inherit Windows user directory environment
```

## Gate at close

Pass — phase gates passed during execution; final `devenv test` and `devenv shell -- go build -o bin/mcp-relayd ./cmd/mcp-relayd` passed. The verify subagent (`ses_e7a46a754ffeFCiZEnx7PmwMSI`) reran `go test ./...` through devenv and verified all six scope targets. On 2026-10-28 the developer also reported successful manual Windows testing; native Windows execution was not independently performed by the agent and is outside the approved gate.
