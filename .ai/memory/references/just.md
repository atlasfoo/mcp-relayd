---
type: reference
title: "just"
description: "Versioned justfile shares eligibility, packaging, verification and CI publication recipes."
tier: workflow
kind: tooling
role: "—"
resource: "CLI just / URL: https://just.systems/man/en/"
tags: [tooling, build, release, ci]
updated: "2026-10-12"
---

# just

## What it is

Recipe runner supplied by devenv. The versioned justfile holds self-contained Python recipes instead of shared release shell scripts.

## How the project uses it

CI calls `ci-checks`, `ci-build`, `ci-push` and `ci-release` from devenv. Build handles early eligibility, local versioning and package preparation; `ci-push` validates the inert Git bundle and publishes refs atomically only after upload; `ci-release` publishes assets without Commitizen or Git mutations. `workflow-handoff` and `workflow-provenance` bind caller, exact artifact and successful producing attempt. Other recipes classify versions, cross-build/package six targets, verify archives and recognize own bumps. Packaging uses Python standard-library tar/zip/gzip/hashlib with normalized metadata and CGO disabled.

## How to consult it

Run `just --list` inside `devenv shell`, then read `justfile`, `docs/actions-flow.md` and `tests/release/README.md` contracts. Select trusted recipes with `--justfile` and a separate `--working-directory` for candidate git data/build sources.

## Gotchas

`ci-push` and `ci-release` are remote mutation operations, not casual local build commands. `RELEASE_PREFLIGHT_ONLY=1` enables their non-publishing validation paths, which may fetch/read remote data but do not publish refs or assets. Candidate/preflight/verify recipes are read-only except requested JSON outputs; `release-preflight` uses already fetched refs. Build/Release do not load candidate justfile/devenv with publication privileges. Local six-target builds are not proof of native macOS/Windows operation.
