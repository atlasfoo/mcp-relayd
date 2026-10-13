---
type: reference
title: "Release GitHub App"
description: "Repository-scoped GitHub App token authenticates atomic version/tag pushes and publication."
tier: workflow
kind: external-api
role: "—"
resource: "URL: https://github.com/actions/create-github-app-token"
tags: [github, app, auth, release]
updated: "2026-10-12"
---

# Release GitHub App

## What it is

A separately administered GitHub App identity for release mutations, not daemon authentication.

## How the project uses it

The isolated `push` job in `build.yml` obtains a repository-limited Contents write token after verified package upload and provenance/artifact/ref preflight, then atomically publishes only the validated bump/tag objects. The reusable `release.yml` obtains its own repository-limited token after validating the already published refs and exact artifact; it publishes assets without Git mutations. Compilation and Commitizen never receive these write tokens. Configuration is `vars.RELEASE_APP_CLIENT_ID`, `secrets.RELEASE_APP_PRIVATE_KEY` and public `vars.RELEASE_BOT_SLUG`; the reusable call explicitly passes the required private-key secret rather than using `secrets: inherit`. Actual App slug and GitHub bot user ID validate the bot identity.

## How to consult it

Consult the pinned token action documentation above, the workflows and `docs/actions-flow.md` App prerequisites. The administrator installs the App and configures the master protection/ruleset bypass in GitHub; these settings are not created by repository code.

## Gotchas

No App token is passed to Commitizen or used to execute candidate tooling/binaries. Git credentials stay in environment, not remote URLs or persisted config. No-bump/stale preflight omissions do not request write tokens. Public bot slug is needed for own-bump suppression and retry recognition. `[skip ci]` can leave required checks Pending; the App's bypass remains an external prerequisite. Hosted CORE-006–008 validation was confirmed by the developer on 2026-10-12 in the [work summary](../log/2026-10-04-core-daemon-and-github-releases.md), not independently inspected by the agent. Never copy secrets into documentation or logs.
