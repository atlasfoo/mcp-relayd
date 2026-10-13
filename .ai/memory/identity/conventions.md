---
type: convention
title: "Conventions"
description: "Uses TDD, golangci-lint formatting and Nix-managed commit and quality tooling."
tags: [identity, convention, planning, style, go]
updated: "2026-10-04"
---

# Conventions

## Planning conventions

- **TDD mode:** `enabled`
- **Atomicity granularity:** One coherent user-visible or infrastructure change per plan; one independently verifiable task per task entry.
- **Phase structure:** Group tasks by architectural cohesion. Each phase must leave the repository buildable and reversible.
- **Git strategy:** Per-task conventional commits when the task is independently reviewable; otherwise one conventional commit at plan close.
- **Definition of ready:** A plan and task list exist, open questions are resolved, and a human has explicitly approved the plan.

## Style and syntax

`.golangci.yml` is the formatting and static-analysis authority:
gofumpt and goimports format Go code through `fmt`, while `lint` checks
without changing files. [golangci-lint](../references/golangci-lint.md)
documents the integration.

[Lefthook](../references/lefthook.md) enforces formatting and spelling at
pre-commit, [Commitizen](../references/commitizen.md) validates Conventional
Commits at commit-msg, and pre-push runs `check`.
Commitizen owns SemVer and changelog generation through `.cz.toml`.
All quality tools, including Commitizen, are supplied by
[devenv](../references/devenv.md); no manual uv installation is required.
[typos](../references/typos.md) checks English code and filenames.
