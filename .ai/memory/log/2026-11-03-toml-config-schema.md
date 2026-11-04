---
type: log-entry
date: 2026-11-03
slug: toml-config-schema
kind: work-closure
depth: design
adr: none
---

# TOML configuration JSON Schema and IntelliSense support

> The work-closure entry for `.ai/memory/log/` — drafted in
> `.ai/work/` by `conduct:summarize`, moved by
> `scripts/archive.sh`. English, always (long-term memory).

## What happened

Added a full JSON Schema Draft-07 at `schema/mcp-relayd.schema.json` for the daemon's TOML configuration. Updated `internal/config/config.go` (`fileConfig`) to decode root schema fields without unknown field errors, updated `examples/mcp-relayd.toml` and embedded `internal/cli/example.toml` with the schema directive and property, and added `.taplo.toml` for local workspace IntelliSense.

## Criteria delivered

None — design depth; done = plan scope + gate.

## Phases executed

| # | Theme | Notable result |
|---|---|---|
| 1 | Schema JSON y soporte en parser | Authored `schema/mcp-relayd.schema.json`, mapped `$schema` and `schema` in `fileConfig`, and added parser unit tests. |
| 2 | Plantillas, comando config y validación final | Updated template files, `.taplo.toml`, `README.md`, verified tests and clean Quality Gate. |

## Decisions and deviations

- Allowed both `"$schema"` and `"schema"` on the root level of `fileConfig` to accommodate diverse TOML tooling and user formatting styles.
- Added both the `# :schema` top comment directive and the `"$schema"` root property in generated configurations for maximum editor compatibility.
- Added `.taplo.toml` at the repository root to link `schema/mcp-relayd.schema.json` to any `mcp-relayd.toml` or example TOML file.

## ADR proposals & brain updates

No ADR proposed; all decisions were tactical schema and tooling additions.

## Pointers

- Consulted: `[references/go-toml.md](../references/go-toml.md)`, `[identity/quality-gate.md](../identity/quality-gate.md)`
- Schema file: `schema/mcp-relayd.schema.json`

## Suggested final commit

```
feat(config): add JSON Schema for TOML configuration and editor intellisense

Add JSON Schema Draft-07 for mcp-relayd configuration, include "$schema"
in the generated example TOML template, and allow schema keys in the
strict Go TOML parser.
```

## Gate at close

Pass (full devenv check and devenv test suites clean).
