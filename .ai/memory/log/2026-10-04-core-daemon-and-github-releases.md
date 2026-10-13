---
type: log-entry
date: "2026-10-12"
slug: core-daemon-and-github-releases
kind: work-closure
depth: spec
adr: "proposed: locked external bridge; Build-owned versioning and trusted publication"
---

# Local MCP daemon and conditional GitHub distribution

## What happened

Delivered strict global TOML configuration, supervised stdio MCP servers, shared per-server Streamable HTTP routes, health, redacted JSON logs and bounded shutdown. Added six-target distribution and conditional GitHub publication. The English README now leads with usage, the external bridge prerequisite and complete TOML schema, ending with Go build and Nix/devenv development instructions.

## Criteria delivered

- `CORE-001` — Declarative configuration and CLI startup — delivered.
- `CORE-002` — Isolated per-server lifecycle — delivered.
- `CORE-003` — Shared HTTP transport and independent routes — delivered.
- `CORE-004` — Loopback exposure and HTTP validation — delivered.
- `CORE-005` — Structured logs and bounded process-tree shutdown — delivered; Linux procfs ESRCH test race corrected.
- `CORE-006` — Checks allow changes without a version bump — delivered; local contracts plus developer-confirmed GitHub execution.
- `CORE-007` — Six-target builds and traceable archives — delivered; local contracts plus developer-confirmed GitHub execution.
- `CORE-008` — Conditional GitHub App publication and recovery — delivered; local contracts plus developer-confirmed GitHub execution.

## Phases executed

| # | Theme | Notable result |
|---|---|---|
| 1 | Configuration and CLI | Strict TOML, global home-relative config and explicit override |
| 2 | Process lifecycle | Isolated supervision and bounded shutdown |
| 3 | HTTP gateway | Shared upstreams, separate routes, health and Host/Origin checks |
| 4 | Real bridge integration | Locked mcp-proxy/MCP SDK and real runtime smoke |
| 5 | Checks/build/release | Provenance, six archives, checksums and recoverable publication |
| 6 | Build-owned versioning | Early eligibility; local bump, verified upload, atomic push, reusable Release |
| Close | User documentation | English usage-first README and development setup |

## Decisions and deviations

- The temporary external bridge remains required: mcp-proxy 0.12.0 and MCP SDK 1.30.0, not bundled in Go binaries.
- Approved amendments moved versioning into Build and made Release publication-only; no increment means no packages or Release call. The canonical bump uses `[skip ci]`. Checks is the sole shared NAR-cache writer; PRs, Build and Release only restore.
- Native macOS/Windows execution was explicitly excluded; cross-compilation is not runtime verification. The Windows Job Object assignment window remains a known containment caveat.
- Hosted CORE-006–008 validation was confirmed by the developer on 2026-10-12. The agent verified offline contracts, not hosted runs independently; no run URLs were supplied. No Quality Gate checks were waived.

## ADR proposals & brain updates

- **Proposed `type: decision` — Locked external bridge:** incompatible unconstrained SDK upgrades motivate retaining the tested dependency closure rather than an unpinned bridge; reproducibility adds a separate Python runtime dependency.
- **Proposed `type: decision` — Build-owned versioning and trusted publication:** prepare the bump locally, verify/upload packages before atomic refs publication, then call a publication-only Release. This replaces Release-owned versioning and separates candidate execution from write credentials, at the cost of explicit provenance/handoff contracts.
- **Proposed `type: architecture` / `type: reference` updates:** reconcile the older pipeline and cache descriptions in [architecture](../identity/architecture.md), [Actions](../references/github-actions.md), [App](../references/github-app.md), [Releases](../references/github-releases.md) and [Commitizen](../references/commitizen.md) through `jaiba-init:update-brain`; these concepts were not rewritten at closure.

## Pointers

- Related work: [quality automation](2026-10-03-go-quality-automation.md), [core/release brain update](2026-10-07-core-and-release-brain-update.md).
- Contracts: [quality gate](../identity/quality-gate.md), [mcp-proxy](../references/mcp-proxy.md), [MCP](../references/mcp.md), [devenv](../references/devenv.md).
- Checkpoints: `7d2ed3e`, `9b76433`; cache fix `fba32d7` ([PR #2](https://github.com/atlasfoo/mcp-relayd/pull/2)); pipeline change `15685b5` ([PR #3](https://github.com/atlasfoo/mcp-relayd/pull/3)). Closure made no commit, push or branch change.

## Suggested final commit

```text
feat(relay): add local MCP daemon and conditional GitHub releases
```

## Gate at close

Pass — all phase commands, uncached runtime/release tests, `devenv test`, Go build, actionlint, direct English README spelling and diff checks. Verifier: `ses_ecc7bb4f8ffevfbK8qZ8udG9P3`; README executor: `ses_ecc835359ffez2yPSdNQE7ppF7`. Local logs: `/tmp/opencode/mcp-relayd-close-{plan,build,verify}-gate.log`. All 47 tasks completed; 8/8 criteria delivered with the hosted-evidence attribution above. The developer explicitly authorized archiving and executive-memory cleanup after reviewing the closing summary.
