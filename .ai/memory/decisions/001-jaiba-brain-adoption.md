---
type: decision
id: ADR-001
title: "Adoption of the JAIBA brain structure"
description: The project keeps agent-facing memory in .ai/ under the JAIBA framework.
status: accepted
date: "2026-10-03"
tags: [meta, memory, tooling]
updated: "2026-10-03"
---

# ADR-001: Adoption of the JAIBA brain structure

## Context

The project needs persistent context for human and AI contributors while its relay architecture and quality tooling are established.

## Decision

Adopt the JAIBA `.ai/` brain: `AGENTS.md` for agent behavior, `.ai/memory/` for constitutive project facts and decisions, and `.ai/work/` for gitignored executive work artifacts.

## Alternatives Considered

- *No structured memory* — rejected because contributors would repeatedly reconstruct the project context.
- *A single monolithic context file* — rejected because independent project facts and decisions would become difficult to maintain.

## Consequences

- *Positive:* project facts and future decisions remain discoverable across sessions.
- *Negative / Risks:* the memory must be updated when project identity, scope, integrations, or the quality gate changes.
- *Follow-ups:* use `jaiba-init:update-brain` after major milestones.
