# Pipeline tests

This suite tests release tooling and workflow contracts, not the MCP daemon.
It exercises the real `justfile` recipes with disposable Git repositories and
offline tool doubles. No production release or authenticated GitHub write is
performed.

## Run the suite

From the repository root, enter the pinned development environment and run:

```sh
devenv shell
just check-pipeline
```

For only the Go contract tests, run `(cd tests/release && go test -count=1 ./...)`.
The pipeline gate runs this nested module's formatting, lint, vet, and Go tests,
`actionlint`, and `unittest discover` for the 25 current Python tests under
`tests/pipeline`. `just check-changes` selects the app gate and adds the pipeline
gate for relevant staged, unstaged or untracked inputs;
`just check-changes BASE HEAD` selects from the entire explicit commit range.

The environment supplies Go, Python, just, Commitizen, gh and actionlint.
Fixtures remove inherited credentials and Git configuration, disable hooks,
and replace network-facing commands with strict offline doubles. Missing tools
or recipes are failures, not successful skips.

**Gate boundary:** this is a nested Go module. Root `go test ./...` and `check`
validate only the app, not this module or CI infrastructure. Request that
validation with `just check-pipeline`, or use `just check-changes` to select the
gate based on changes. The independent Pipeline Checks workflow runs on PRs and
master pushes changing pipeline inputs, not app-only or documentation-only
changes. It restores but never saves the shared NAR cache, needs no App
credentials and does not trigger Build or produce releases.

## Current production flow

- Checks validates app changes on PRs and normal master pushes. Pipeline Checks
  independently validates pipeline-input changes on those events. Build listens
  exclusively to successful Checks for an own-repository master push; Pipeline
  Checks does not trigger Build.
- Build evaluates feat/fix history and an increasing Commitizen candidate before
  preparing a local bump/tag and six packages. No increment means no packages
  and no Release call.
- After verification and artifact upload, a separate privileged Build job
  revalidates provenance and refs, then pushes the bump/tag atomically.
- Release is reusable (`workflow_call`) inside the Build run. It publishes or
  recovers verified assets without Commitizen, new commits/tags or Git pushes.
- The canonical bump carries `[skip ci]`. Only master Checks writes the shared
  NAR cache; PRs, Build and Release only restore it.

The scripts are `tooling/release.py` (Python standard library only) and the
root `justfile`. In Build/Push/Release, run `justfile` and tooling from the
trusted workflow checkout using isolated Python (`python -I`); the selected
source checkout supplies application source, not the privileged control plane.
PR and development-build paths do not produce packages; Release rejects a
nonpublishable caller. `act` is a future evaluation, not an adopted or
smoke-tested gate. If evaluated, remove redundant tooling/tests only when an
equivalent test preserves real coverage. Emulation cannot establish GitHub
permissions or workflow-chain behavior and is not a substitute for hosted
validation. Prior maintainer confirmation of hosted behavior predates this
refactor and does not validate it.

See [the workflow guide](../../docs/actions-flow.md) for orchestration and App
prerequisites.

## Coverage

| Tests | Behavior protected |
| --- | --- |
| `release_test.go` | Eligibility, Commitizen failures and stale-candidate decisions |
| `checks_test.go` | Commit validation, own-bump recognition and Checks metadata |
| `build_test.go` | Early omission, trusted tooling, source binding and build failures |
| `packaging_test.go` | Six targets, archive reproducibility, checksums and unsafe assets |
| `provenance_test.go` | Exact run/attempt/artifact binding and inert atomic-push handoff |
| `publication_test.go` | Draft recovery, partial uploads, exact-byte retries and credential isolation |
| `workflows_test.go` | Parsed YAML contracts, triggers, permissions and job dependencies |

Packaging uses a Go double to inspect cross-build flags rather than compiling
six real binaries in every scenario. Publication uses local bare Git origins
and a strict GitHub API double: unexpected operations fail instead of falling
back to the real service. Assets are verified as data, not executed with write
credentials.

## Evidence and limits

Offline tests cover error and recovery paths that a successful hosted run does
not establish. They do not reproduce GitHub permissions, branch-protection
bypass, native Skip CI, cache delivery or the hosted workflow scheduler.
Prior hosted confirmation predates the pipeline refactor and is not evidence
that the refactored workflow has passed on GitHub. No hosted validation is
claimed here.

`act` is a possible future smoke-test tool, not a current dependency, adopted
decision, or tested gate. Any future adoption must preserve equivalent real
coverage; it cannot replace provenance, atomic-push, exact-byte recovery, GitHub
permissions, or workflow-chain validation.
