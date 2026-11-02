---
type: log-entry
date: 2026-11-02
slug: urfave-cli-brain-update
kind: brain-change
adr: ADR-004
---

# Recorded urfave/cli and controlled output

## What happened

Enacted the proposal in [basic-cli](2026-10-28-basic-cli.md). Added [ADR-004](../decisions/004-urfave-cli-and-controlled-output.md) and [urfave/cli](../references/urfave-cli.md). `identity/architecture.md` now names urfave/cli v3 as the process CLI framework and `internal/cli` as the command tree; the relay core still has no application framework. The go-toml gotcha now says `Load` still errors when the file is missing, while `config` can write the example. `index.md` lists the new decision and reference.

## Decisions and deviations

Provenance is the basic-cli closure, which proposed the decision and the missing reference. The developer asked to update the brain on 2026-11-02. The stale scaffold sentence in the product roadmap was left untouched.

## Pointers

- [ADR-004](../decisions/004-urfave-cli-and-controlled-output.md)
- [basic-cli closure](2026-10-28-basic-cli.md), criteria CLI-001 through CLI-007
