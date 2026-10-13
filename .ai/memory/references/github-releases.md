---
type: reference
title: "GitHub Releases"
description: "Publication-only reusable Release attaches six verified archives and SHA256SUMS after Build publishes the bump/tag."
tier: workflow
kind: external-api
role: "—"
resource: "URL: https://docs.github.com/en/rest/releases/releases"
tags: [github, release, artifacts, checksums]
updated: "2026-10-12"
---

# GitHub Releases

## What it is

GitHub's release and asset REST API provides public distribution of the already verified Go packages.

## How the project uses it

Build owns version eligibility, local Commitizen bump/tag creation and package upload; its isolated `just ci-push` job atomically publishes verified refs without force. Only then does Build invoke reusable Release. `just ci-release` verifies the exact artifact, manifest and six archives, caller/producer provenance and already published bump/tag refs. It does not run Commitizen, create commits/tags or push Git. A draft gets six archives and SHA256SUMS; incremental changelog notes come from the validated bump. It is published only after complete asset verification.

## How to consult it

Consult the Releases API above, [assets API](https://docs.github.com/en/rest/releases/assets), README and just recipes. `tests/release/publication_test.go` uses local bare Git origins and a strict offline gh API adapter, not real credentials.

## Gotchas

Eligibility requires feat/fix plus a successful increasing Commitizen candidate and is evaluated before packaging; no increment means no Release call. Binaries report the checked source SHA, not the version-only bump SHA. Stale candidates require a new Checks/Build cycle. Retry the same Build run to recover partial publication: retained successful producers may use their original attempt/artifact, and a producer rerun recovers the existing bump instead of incrementing again. Recovery verifies expected parent, bot identity, canonical message, version-only changes and asset bytes; conflicts fail rather than overwrite. Release executes within Build, so its caller may still be in progress. Hosted CORE-006–008 passed according to the developer's 2026-10-12 [confirmation](../log/2026-10-04-core-daemon-and-github-releases.md); offline fixtures alone are not hosted evidence.
