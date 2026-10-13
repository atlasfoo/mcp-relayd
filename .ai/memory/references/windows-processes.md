---
type: reference
title: "Windows process containment"
description: "Windows console groups and Job Objects contain supervised bridge descendants through x/sys."
tier: code-scope
kind: external-api
role: infrastructure
resource: "URL: https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects"
tags: [windows, process, lifecycle]
updated: "2026-10-07"
---

# Windows process containment

## What it is

Windows Job Objects and console control events provide OS-specific process-tree containment and shutdown.

## How the project uses it

`internal/process/process_tree_windows.go` uses `golang.org/x/sys/windows`, pinned in `go.mod`, for Job Object handles and descendant termination. Console groups support cooperative shutdown before escalation.

## How to consult it

Consult the Job Object documentation above and [console control events](https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent). Build-tagged tests are in `internal/process/process_windows_test.go`.

## Gotchas

Windows binaries/tests cross-compile for amd64 and arm64, but native execution is deferred. Assignment occurs immediately after Start, leaving a brief descendant escape window; this is not suspended-process launch containment. Handles/pipes and the supervised process are closed/reaped on shutdown.
