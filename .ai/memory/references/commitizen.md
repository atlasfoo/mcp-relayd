---
type: reference
title: "Commitizen"
description: "Conventional Commit validation, SemVer versions and changelog generation configured in .cz.toml."
tier: workflow
kind: tooling
role: "—"
resource: "CLI cz / URL: https://commitizen-tools.github.io/commitizen/commands/bump/"
tags: [workflow, tooling, quality]
updated: "2026-10-12"
---

# Commitizen

## What it is

Conventional Commit validator and history-driven version/changelog tool.

## How the project uses it

`.cz.toml` owns the current project version, SemVer 2, v-prefixed tags, pre-1.0 semantics and automatic CHANGELOG.md updates; read the file rather than treating this memory as a version register. The canonical bump message is `chore(release): bump version $current_version → $new_version [skip ci]`. `pkgs.commitizen` supplies the CLI through devenv. Build uses strict standard configuration to evaluate eligibility and prepare a local version-only bump/tag before compilation; refs are published only after verified packages are uploaded. Release never invokes Commitizen.

## How to consult it

Read `.cz.toml`; run `cz version --project` or `cz bump --dry-run` inside `devenv shell`. [Official bump documentation](https://commitizen-tools.github.io/commitizen/commands/bump/).

## Gotchas

A real cz bump changes files, creates a commit and a local tag. It does not publish them. The CI eligibility rule requires feat/fix since the version boundary and an increasing candidate; no-increment is a successful omission, while actual errors fail. First release handles missing version tags explicitly. Retry recovers a valid existing bump instead of incrementing again. The Skip CI marker is not provenance proof and may leave required checks Pending; App bypass is configured externally.
