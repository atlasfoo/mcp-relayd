---
type: log-entry
date: "2026-10-12"
slug: core-release-memory-refresh
kind: brain-change
adr: "ADR-002, ADR-003"
---

# Reconcile memory after core daemon and release closure

## What happened

Applied the [work-closure proposals](2026-10-04-core-daemon-and-github-releases.md) after showing the concrete changes and receiving the developer's explicit approval. Reconciled the initial-project status, implemented transport relations and the workflow/versioning/cache architecture. Added two accepted ADRs and regenerated the memory index; source code, README, Git index and prior logs were untouched by this refresh.

## Decisions and deviations

- [Project](../identity/project.md) moves from greenfield to delivered initial core/distribution; [purpose](../identity/purpose.md) links implemented surfaces rather than claiming none exist. [Architecture](../identity/architecture.md) records the new Build/Release ownership boundary.
- [Actions](../references/github-actions.md), [App](../references/github-app.md) and [Releases](../references/github-releases.md) replace independent workflow chaining and Release-owned bump/push with early Build eligibility, local versioning, post-upload isolated refs publication and reusable publication-only Release. [devenv](../references/devenv.md) reflects shared `nix-env-v2` with Checks as sole writer.
- [Commitizen](../references/commitizen.md) points to the live version file instead of freezing a stale numeric version and records canonical Skip CI and retry semantics. [just](../references/just.md) records `ci-push`, exact handoffs and mutation boundaries. [typos](../references/typos.md) records that the English README is still excluded from the general gate and was checked separately.
- [Quality gate](../identity/quality-gate.md) commands remain unchanged; hosted CORE-006–008 validation is attributed to the developer's 2026-10-12 confirmation, not independent agent inspection or exhaustive cache-case verification. Native macOS/Windows tests remain excluded, and the existing Windows containment caveat remains intact.
- Accepted [ADR-002](../decisions/002-locked-external-mcp-bridge.md) and [ADR-003](../decisions/003-build-owned-versioning-and-trusted-publication.md), dated today. ADR-001 was not rewritten or superseded. Scope, conventions and unrelated reference concepts were left unchanged.

## Pointers

- [Regenerated index](../index.md); [previous core/release brain update](2026-10-07-core-and-release-brain-update.md).
- Ground truth: `.github/workflows/{checks,build,release}.yml`, `justfile`, `.cz.toml`, `.typos.toml`, `docs/actions-flow.md`, README and the linked closure. No secrets or hosted credentials were read.
- Verification: the six Phase Gate commands passed serially inside devenv; memory audit checked 23 indexed concepts, 98 local links, exact index descriptions, no canonical placeholders and an empty `.ai/work/`. Evidence: `/tmp/opencode/mcp-relayd-memory-refresh-gate.log`. No commit or push was performed.
