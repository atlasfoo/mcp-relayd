---
type: convention
title: "Conventions"
description: "Uses TDD and project-local Go tooling as the source of style and planning conventions."
tags: [identity, convention, planning, style, go]
updated: "2026-10-03"
---

# Conventions

## Planning conventions

- **TDD mode:** `enabled`
- **Atomicity granularity:** One coherent user-visible or infrastructure change per plan; one independently verifiable task per task entry.
- **Phase structure:** Group tasks by architectural cohesion. Each phase must leave the repository buildable and reversible.
- **Git strategy:** Per-task conventional commits when the task is independently reviewable; otherwise one conventional commit at plan close.
- **Definition of ready:** A plan and task list exist, open questions are resolved, and a human has explicitly approved the plan.

## Style and syntax

Project-local Go tooling and its configuration define formatting and static-analysis rules. Until dedicated style configuration is added, use `gofmt` and the checks in `devenv.nix`.
