---
type: reference
title: "Lefthook"
description: "Local Git hooks enforce Go formatting, spelling and Conventional Commits."
tier: workflow
kind: tooling
role: "—"
resource: "CLI lefthook / URL: https://lefthook.dev/configuration/"
tags: [workflow, tooling, quality]
updated: "2026-10-04"
---

# Lefthook

## What it is

Git hook runner for contributor quality checks.

## How the project uses it

`lefthook.yml` configures pre-commit formatting/spelling, Commitizen commit-msg validation and the pre-push `check` command.

## How to consult it

Read `lefthook.yml`; run `lefthook install` inside `devenv shell` once per clone. [Official configuration](https://lefthook.dev/configuration/).

## Gotchas

The formatter uses stage_fixed, which stages the entire affected Go file. Hooks require the devenv tools and scripts on PATH.

