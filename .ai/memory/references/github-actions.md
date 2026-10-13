---
type: reference
title: "GitHub Actions"
description: "Linux-only Checks and Build with reusable Release, exact producer provenance and a shared single-writer NAR cache."
tier: workflow
kind: tooling
role: "—"
resource: "URL: https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows"
tags: [ci, github, nix, provenance]
updated: "2026-10-12"
---

# GitHub Actions

## What it is

Hosted CI orchestration for checks, six-target cross-builds and conditional publication.

## How the project uses it

Three files under `.github/workflows` implement Checks → Build → reusable Release. Checks runs PR heads and normal master pushes. Build uses `workflow_run`, allowing its runner only after successful Checks from an own-repository master push; PRs run Checks only. Build evaluates feat/fix history and an increasing Commitizen candidate before preparing a local bump/tag and six packages. No increment is a successful omission without packages or Release.

After verified packages and the Git-object bundle are uploaded, a separate privileged Build job revalidates provenance and remote refs, then pushes the bump/tag atomically. Only a successful push invokes Release through `workflow_call` inside the same Build run. Release validates and publishes existing packages; it does not version or mutate Git. API data binds source SHA, bump SHA, caller, successful producer, artifact ID and producing attempt, including retained successful jobs during retries. Tooling comes from the trusted workflow SHA, not candidate artifacts. One caller-level concurrency group serializes publication without cancellation; Release does not reacquire that lock.

## How to consult it

Read the workflows, `docs/actions-flow.md` and `justfile`. `tests/release/workflows_test.go` parses real YAML; provenance and publication tests exercise offline contracts. `actionlint -shellcheck= -pyflakes=` checks workflow syntax from the declarative devenv environment. Official semantics: [reusable workflows](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows) and [skipping runs](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/skip-workflow-runs).

## Gotchas

Workflows must exist on the default branch before chaining works. Build's event is workflow_run, not push; the original origin is verified against Checks API. Checks, Build and Release restore `nix-env-v2` keys based on runner OS/architecture and the three devenv configuration/lock files; Build/Release hash trusted files. Only successful master Checks saves a missing cache key; PRs, Build and Release never export/save. Runner-owned cache directories are prepared after restore and before Nix installation. This is a NAR cache, not a shared live store, and Python bridge provisioning remains separate.

The canonical bump carries `[skip ci]`; GitHub may leave required checks Pending, so App bypass is an external prerequisite. Structural own-bump recognition is a safe fallback, not authorization based on message text alone. Only Linux runners are in scope. Hosted CORE-006–008 passed according to the developer's 2026-10-12 confirmation in the [work summary](../log/2026-10-04-core-daemon-and-github-releases.md); the agent did not independently inspect hosted runs or every cache scenario.
