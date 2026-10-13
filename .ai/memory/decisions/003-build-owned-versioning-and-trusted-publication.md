---
type: decision
id: ADR-003
title: "Build-owned versioning and trusted publication"
description: "Build prepares the version and publishes verified refs after upload; reusable Release only publishes assets."
status: accepted
date: "2026-10-12"
tags: [architecture, ci, release, provenance, security]
updated: "2026-10-12"
---

# ADR-003: Build-owned versioning and trusted publication

## Context

The approved workflow amendment moved versioning from Release into Build to avoid unnecessary compilation and publication when no new version exists. Version refs must not be consumed before verified packages are uploaded, and write credentials must remain separate from candidate execution. The [work closure](../log/2026-10-04-core-daemon-and-github-releases.md) proposed recording this boundary; the developer accepted it during this refresh. This entry records that acceptance now, not a backdated decision.

## Decision

After successful own-master Checks, Build verifies provenance and evaluates feat/fix history plus an increasing [Commitizen](../references/commitizen.md) candidate. An ineligible result succeeds without bump, packages or Release. For an eligible result, Build prepares/reuses the local bump/tag, builds and verifies six packages, then uploads the exact artifact and inert Git bundle. A separate privileged Build job revalidates provenance and refs before requesting a repository-scoped App token and pushing verified objects atomically.

Only a successful push calls reusable [Release](../references/github-releases.md) within the same Build run. Release validates the producer/artifact and published refs, then publishes or recovers identical assets without Commitizen or Git writes. Trusted workflow tooling is isolated from candidate source; credentials are not available during compilation/versioning. One caller-level lock serializes publication without cancellation. The canonical bump uses `[skip ci]`, with App bypass configured externally.

## Alternatives Considered

- Release-owned versioning after unconditional builds — replaced by the approved amendment because ineligible changes should stop before packaging.
- Publishing version refs before package verification/upload — not chosen because a pre-upload failure must not consume a remote version.
- Independent workflow_run Release — replaced by conditional workflow_call so omitted Builds do not start a separate publication run.

## Consequences

- *Positive:* early omission, explicit version ownership, immutable package provenance and isolated write credentials.
- *Negative / Risks:* handoff/run/attempt verification and partial-publication recovery are more complex; native Skip CI can leave required checks Pending.
- *Follow-ups:* preserve exact-byte retry and stale-ref contracts, maintain [App prerequisites](../references/github-app.md), and keep [Actions](../references/github-actions.md) and cache documentation aligned. Reduce duplicated orchestration/tests only as separately scoped work, without removing provenance guards.
