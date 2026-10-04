# AGENTS.md — JAIBA project marker

This repository is **JAIBA-instrumented** (Joint-operations Artificial
Intelligence Behavioral Architecture). This file is deliberately minimal:
behavior does not live per-repo.

1. **Behavior** — follow the **JAIBA Behavioral Contract** installed globally
   in your agent configuration (file `jaiba-contract.md` in the agent's global
   config folder). It defines the brain map, the routing rule, and the
   behavioral rules.

   *If you cannot find the global contract*, say so before doing substantive
   work and route the developer to `jaiba-doctor` (checks presence/drift) or
   `jaiba-configure` (reinstalls it). Do not improvise the missing rules.

   *If the JAIBA workflow/meta skills (`conduct`, `ask`, `fast`,
   `jaiba-doctor`, `jaiba-init`, `create-knowledge`, …) aren't available to
   you specifically*, say so before doing substantive work and route the
   developer to `jaiba-configure` to install them for this agent.

2. **Project facts** — identity, stack, scope, the Quality Gate, decisions in
   force, and external surfaces live in the constitutive memory under
   `.ai/memory/`. Active work: `.ai/work/`.

Anything project-specific a maintainer wants agents to know belongs in the
constitutive memory, not appended here.
