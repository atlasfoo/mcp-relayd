---
type: log-entry
date: 2026-10-16
slug: isolate-and-simplify-pipeline-tooling
kind: work-closure
depth: design
adr: none
---

# Isolated application and pipeline validation

## What happened

Application checks no longer discover release tests: `tests/release` is a nested Go module with its own YAML dependency. `check`/Checks stay app-only; `just check-pipeline`, change selection and Pipeline Checks validate infrastructure independently. Trusted release operations share one stdlib Python program, isolated wrappers and direct internal calls. Historical PR/dev packaging paths and redundant tests were removed without changing daemon behavior or publication guarantees.

## Criteria delivered

None — design depth; done = plan scope + gate. All 18 tasks completed. Independent verification accepted closure with the explicit human waiver below; no product criteria or security checks were waived.

## Phases executed

| # | Theme | Notable result |
|---|---|---|
| 1 | Isolation and gates | Nested module, path/range selector, app-only Checks and independent Pipeline Checks. |
| 2 | Shared trusted tooling | `tooling/release.py`, isolated Python startup/environment, live wrappers retained. |
| 3 | Equivalent test consolidation | Net 186 LOC removed versus phase 2; act experiment not executed and prototype discarded. |
| 4 | Documentation and evidence | Final READMEs/workflow guide, positive/negative contract matrix and real six-target package repeatability. |

## Decisions and deviations

- **Explicit human waiver (2026-10-16):** accept increased test/infrastructure code to prioritize roadmap progress and faster app-only validation. Comparable executable tooling/tests/workflows increased **5114 → 5521 LOC (+407)**; new nested manifests add 8, for **+415 total**. Tooling alone decreased 997 → 830 and Go release tests 3659 → 3519; new Python tests add 600 and workflow adds 114. Docs decreased separately 642 → 519; that is not executable-code savings.
- Measured `check` 15.17 s and pipeline gate 91.87 s. Avoiding uncached release tests on app-only changes is estimated to save **80–90 s**, not a measured compiler or hosted-CI speedup. Warm-cache app build 0.24 s and six-target packaging 5.20 s are single samples without a comparable pre-plan benchmark.
- Act was **not adopted**: act/Docker unavailable, Podman without an active socket, no authorized provisioning, no smoke execution. No evidence of hosted scheduler, permissions/bypass, Skip CI, cross-run artifacts or cache behavior is inferred from local tests or prior hosted confirmation.

## ADR proposals & brain updates

No ADR proposed; existing [trusted publication ownership](../decisions/003-build-owned-versioning-and-trusted-publication.md) remains in force.

**Requested future work — act implementation:** the developer explicitly wants to revisit implementing act after advancing the solution roadmap, to replace redundant non-application pipeline test/fixture/orchestration code and reduce maintenance. This remains pending, not implemented or proven feasible. Require an authorized local runtime, real happy/API-failure smoke evidence, current pinned Actions compatibility, equivalent observable coverage and net code reduction before removing any existing tests. Never remove provenance, atomic-push, exact-byte recovery or hosted permission protections merely because act is introduced. Installation, engine activation, image downloads and remote writes require separate authorization.

Proposed `jaiba-init:update-brain` reconciliation: [Quality Gate](../identity/quality-gate.md), [architecture](../identity/architecture.md), [just](../references/just.md), [devenv](../references/devenv.md) and [Actions](../references/github-actions.md) still describe aspects of the former combined gate. Treat this closure's split-gate description as the approved current state until those concepts are updated. No act integration reference is adopted yet.

## Pointers

- Pre-plan baseline `f1a462c`; phase commits `0844fed` and `0f8edc4`. Phase 3 tests and phase 4 docs remain uncommitted; no additional commit was requested.
- Durable usage/evidence map: `README.md`, `tests/release/README.md`, `docs/actions-flow.md`, existing Go/Python tests. Machine-local reports: `/tmp/opencode/t018-final-evidence.md`, `/tmp/opencode/plan-final-validation.md`, `/tmp/opencode/phase4-controller-gate.log` (not durable repository artifacts).

## Suggested final commit

```text
chore(pipeline): isolate quality gates and simplify trusted release tooling
```

## Gate at close

Pass with the explicit LOC-objective waiver only. Root tests/fmt/lint/spelling/vet/check, nested uncached tests/fmt/lint/vet, actionlint, 25 Python tests, README spelling and local links pass. `devenv test` and app build pass. Two real six-target package sets and manifests match byte-for-byte/checksums; no crossed binaries executed or releases published. Formal verifier cleared summarize; hosted behavior and act smoke remain unverified, outside local evidence.
