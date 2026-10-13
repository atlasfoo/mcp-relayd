---
type: reference
title: "typos"
description: "English spelling and filename checks configured in .typos.toml."
tier: workflow
kind: tooling
role: "—"
resource: "CLI typos / URL: https://github.com/crate-ci/typos/blob/master/docs/reference.md"
tags: [workflow, tooling, quality]
updated: "2026-10-12"
---

# typos

## What it is

Standalone English spelling checker for development files.

## How the project uses it

`.typos.toml` records project vocabulary and exclusions. `spellcheck` runs `typos --force-exclude .`; Lefthook checks staged files.

## How to consult it

Read `.typos.toml`; run `spellcheck` inside `devenv shell`. [Official reference](https://github.com/crate-ci/typos/blob/master/docs/reference.md).

## Gotchas

README.md is now English but remains explicitly excluded in `.typos.toml`, alongside Spanish `docs/actions-flow.md`, JAIBA artifacts, generated files and environment/output directories. Explicit file arguments still honor exclusions through --force-exclude. The rewritten README was checked separately with `typos --isolated README.md` at closure; the general gate does not cover it. Removing that exclusion would be a separate tooling change, not part of this memory refresh.
