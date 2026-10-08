# Release decision contract

These Linux tooling tests run with `go test ./tests/release` inside devenv,
and are included in `go test ./...`. They intentionally fail until the
versioned root `justfile` implements these bounded entrypoints:

- `just release-candidate OUTPUT`: inspect HEAD and write one JSON object to
  OUTPUT: `eligible` (boolean), `source_sha`, `last_tag` (empty without tags),
  `version` (empty when skipped), and `reason` (`eligible`, `no-feat-fix`, or
  `no-increment`). Successful skips exit zero; real errors exit nonzero.
- `just release-preflight SOURCE_SHA LAST_TAG OUTPUT`: recheck the candidate
  against `refs/remotes/origin/master` and the latest version tag. Write
  `publish` (boolean) and `reason` (`current`, `stale-source`, or `stale-tag`).
  Stale candidates exit zero, without bumping, tagging or pushing.

Both entrypoints are read-only: no version/changelog changes, commits, tags,
pushes or publication. OUTPUT lives outside the fixture worktree. Stdout is
not a version API; use `cz bump --get-next --yes` for the structured version,
and independently require a successful `cz bump --dry-run --yes` increase.
Only feat/fix subjects (optional scope and breaking marker) since the last
published tag qualify; synthetic merges and historical features do not.
In these fixtures every version tag represents a published release. Remote
publication/API resolution belongs to later workflow tests, not this suite.

The suite copies the real justfile into disposable repositories and executes
it there. Normal cases use real Commitizen with the project's `.cz.toml`.
A strict local `cz` double models exit 21 (no increment), real failures,
unchanged/decreased versions, malformed output and dry-run/get-next disagreement.
It also records arguments and rejects any mutating invocation. Git has no
remote; a local tracking ref models master. No credentials are inherited.
Missing justfile/tools are failures, not skips or mocked implementations.

## Build and package contract (T-028)

`just release-package VERSION SOURCE_SHA RUN_ID OUTPUT_DIR` is the shared
build/package entrypoint. From Linux it invokes Go six times with
`CGO_ENABLED=0` for linux, darwin and windows on amd64 and arm64. Each binary
is built with `-X main.version=VERSION` and
`-X main.sourceSHA=SOURCE_SHA`; `--version` must identify both values.
Packages are named `mcp-relayd_VERSION_GOOS_GOARCH.tar.gz` for Linux/macOS
and `mcp-relayd_VERSION_GOOS_GOARCH.zip` for Windows (containing
`mcp-relayd.exe`). The output directory also contains `manifest.json` with
`source_sha`, `run_id`, `version`, and six `{name, sha256}` asset records.
`just release-verify OUTPUT_DIR` validates the manifest and every asset
checksum, returning nonzero for a mismatch. Identical inputs produce byte-
identical archives and manifest. Development builds and release candidates
use the same build contract and differ only in their supplied version/run
metadata; publishing remains outside these recipes.

The packaging tests use a local mock Go executable to assert cross-build flags
without requiring six real cross-compilations in every test run.

## Workflow chain contract (T-030)

`workflows_test.go` parses real YAML (including flow collections and quoted
`on`) using the test-only go.yaml.in/yaml/v3 dependency. No line/comment search
stands in for trigger, job, step, permission or concurrency structure. Checks,
Build and Release are independent files; missing files/recipes fail, not skip.
The structural contracts use explicit positive conjunctions in security guards.

`just ci-commits BASE HEAD SQUASH_TITLE OUTPUT` validates only BASE..HEAD and
the squash title, writing `{verified: true, source_sha: HEAD}` on success;
invalid commits/titles exit nonzero. Fixtures include non-conventional historical
commits outside the range and valid docs-only changes with no bump required.
Checks calls this shared helper as part of ci-checks, independently of release
eligibility. Missing helpers are checked before negative cases so a missing
recipe cannot masquerade as successful rejection.

Future workflow entrypoints are `just ci-checks`, `just ci-build` and
`just ci-release`, executed by `devenv shell`. Checks owns new-range commit and
squash-title validation (not bump eligibility), and uploads checks-metadata.json
as `checks-metadata`. Build uploads six packages plus manifest using names
containing `steps.provenance.outputs.artifact_class` (dev/release) and run ID.
The existing packaging suite supplies executable six-target/checksum evidence.

Downstream jobs have a `provenance` step: use `gh api` on exact actions/runs IDs,
then `just workflow-provenance MODE INPUT OUTPUT` inside devenv. That read-only
recipe consumes a JSON API snapshot, and returns JSON with `source_sha` and
`artifact_class`; orchestration exports verified fields as step outputs. INPUT
contains `repository`, `trigger_run_id`, `checks`, `build`, and `metadata`.
Run objects use GitHub fields id/path/status/conclusion/event/head_sha and
repository/head_repository.full_name. Metadata contains source_sha, event,
repository, head_repository, head_branch, checks_run_id, build_run_id,
artifact_class and eligible. Mode build binds the trigger to Checks; mode release
binds it to Build and follows metadata.checks_run_id back to the Checks API run.
Fetched API runs, not artifact claims alone, must confirm these fields.

The offline fixtures reject failed Checks, wrong workflow/repository/run/link,
missing metadata and untested SHAs. Valid PR heads build as dev; a PR SHA must
match the API pull_requests head SHA, not an implicit merge/workflow SHA.
Build's workflow_run head_sha can legitimately differ from the tested source.
Release additionally rejects failed Build, PRs, non-master and foreign heads.
Build ignores Build-only API fields; it must accept successful Checks for PR,
non-master and no-bump development builds. No-bump push metadata passes provenance
in both modes; release orchestration skips publication without requiring App
credentials. Checks explicitly checks out PR head SHA or push github.sha.
Live API fetching/authentication and App publication are not verified locally.

Artifact downloads specify the triggering run ID and current repository.
Checkout disables persisted credentials: trusted workflow code comes from
github.workflow_sha at `trusted`, Build source from verified provenance at
`source`. Release invokes only `--justfile trusted/justfile`; it never checks out
or executes candidate source/artifacts with the App token. Build has no App token;
the Release token specifies current owner and one repository. Release concurrency
is repository-wide, stable across runs/SHAs, with cancel-in-progress false.

All jobs use Ubuntu, restore/save Nix caches explicitly with actions/cache,
and key invalidation includes runner.os, runner.arch and hashFiles of devenv.nix,
devenv.yaml and devenv.lock. Cache writes require push (and same-repository origin
downstream), so PR runs cannot poison publication caches. Cold caches must work.
Bootstrap Nix/devenv is allowed; manual dependency installs, setup-go/setup-python
and shared .sh scripts are forbidden. Static checks are guardrails, not a proof
against arbitrary obfuscated shell code. The recipes and live provenance
acquisition remain implementation work for T-031/32/34; the offline contract
tests are intentionally red until those entrypoints and workflows exist.

## Build orchestration (T-032)

`just ci-build PROVENANCE OUTPUT_DIR OUTPUT` consumes the JSON returned by
`workflow-provenance build`, requires HEAD to equal its verified source SHA,
and uses `CI_JUSTFILE` (an absolute trusted justfile path) for every nested
recipe with an explicit candidate working directory. `GITHUB_RUN_ID` identifies
the Build. Only same-repository master pushes call release-candidate; PRs,
forks, other branches and no-bump pushes produce `dev` artifacts with version
`0.0.0-dev.RUN_ID+SHORT_SHA`. Real candidate/build errors fail closed.
All six assets are verified before writing build-metadata.json and OUTPUT.
The manifest also carries the full chain: checks_run_id, build_run_id, event,
repository, head_repository, head_branch, source_sha, version, eligible and
artifact_class. Publication remains deferred to Release.

Build bootstraps only trusted/ at github.workflow_sha, then downloads exact-run
Checks metadata and fetches that run's API response. PR source comes from
pull_requests.head.sha (not run.head_sha); API head.repo may identify the fork
by numeric id rather than full_name. Missing/ambiguous PR heads fail closed.
Only validated, single-line fields become Actions outputs. Source is checked
out separately, without persisted credentials; devenv always starts from
trusted/ even when just's working directory is source/. The final upload name
uses package.outputs.artifact_class, falling back to the preliminary provenance
class, and the Build run ID: version eligibility is computed only after source
checkout, not trusted from the Checks artifact. Build's NAR cache namespace
is separate from Checks; only verified same-repository pushes save it.

build_test.go exercises real recipes with a hostile candidate justfile,
mock Go/Commitizen, no credentials and no network. It covers six-asset metadata,
dev/release classification, candidate/build failures, mismatched checkout,
fork PR API identity, ambiguous/missing heads and output injection rejection.
The structural recipe matcher accepts --working-directory as well as
--justfile: selecting trusted tooling with a separate source cwd is intentional.
Live GitHub API/artifact behavior and cold-cache hosted runs still need a run
after merge; local fixtures are not evidence of those integrations.

## Publication contract (T-033 → T-034)

`publication_test.go` requires these bounded entrypoints before any negative
case can count as a rejection (missing recipes are failures, not skips):

- `just ci-release PROVENANCE ASSET_DIR OUTPUT` runs from a disposable Git
  worktree with a local bare origin. PROVENANCE is the verified Release output
  extended with Build's `version`, `last_tag`, eligibility and skip reason.
  It binds the manifest's source/version/run and full chain to that provenance,
  verifies all six archives, refreshes remote refs, then either skips or publishes.
  OUTPUT is JSON with `published` and `reason`; skip reasons are `no-feat-fix`,
  `no-increment`, `stale-source`, `stale-tag`. Errors exit nonzero.
- `just release-own-bump SOURCE_SHA OUTPUT` is read-only and writes
  `{ "own_bump": true|false }`. It requires both author and committer App
  identity, the exact configured bump message, a single parent, the expected
  version transition, and changes restricted to version/changelog. Merely
  changing other Commitizen configuration inside `.cz.toml` does not qualify.

`CI_JUSTFILE` is an absolute trusted tooling path. `GITHUB_REPOSITORY` identifies
the destination; `RELEASE_BOT_NAME` and `RELEASE_BOT_EMAIL` supply the App identity
(workflow derives them from the App, not candidate data). Eligible publication
requires `GH_TOKEN`; these tests provide only `offline-placeholder`. A no-bump
or obsolete run must succeed without these three App variables and without API
calls. `GITHUB_RUN_ID=303` identifies Release, **not** Build's manifest run 202.

The fake `gh` is a strict, offline GitHub REST adapter, never a real executable
fallback. The implementation can use `gh api` with `--method`/`-X`, fields
(`--field`/`-F`, `--raw-field`/`-f`), `--input`, headers and `--paginate`.
Responses are JSON; do not rely on `--jq` in this adapter. Supported operations:

- GET `repos/owner/mcp-relayd/releases/tags/v0.2.0` (404 when absent).
- POST `repos/owner/mcp-relayd/releases`, with `tag_name`, `draft=true` and
  incremental changelog `body`; returns id 17 and GitHub-shaped upload_url.
- GET `.../releases/17` and `.../releases/17/assets`.
- POST the upload_url (strip its template, add URL-encoded `?name=...`), using
  `--input FILE`; response includes id, name, size and `digest=sha256:...`.
- GET `.../releases/assets/ID`, optionally with Accept application/octet-stream
  for raw content. Existing assets must match content/checksum, not only names.
- PATCH `.../releases/17` with `draft=false`, only after all assets are verified.

Only six exact package names and `SHA256SUMS` may be published. SHA256SUMS uses
`DIGEST  FILENAME` lines for all six packages. Duplicate uploads, asset deletion,
overwriting and duplicate creation are rejected by the adapter. The third upload
can fail after two successful uploads: retry must reuse the same tag/bump/draft
and upload only the five missing assets. A fully completed retry is a no-op.

The remote tag can be recovered only when it resolves to the exact expected
version-only bump whose sole parent is compiled source. Wrong parent, identity,
message, version, code changes and configuration changes fail closed. Recovery
also works after atomic push but before draft creation. A Git wrapper races
remote master or the candidate tag immediately before the actual push, exercising
Git's atomic rejection rather than merely modeling a stale tracking ref.
No force push is allowed; both master and tag must move together.

Candidate justfile/devenv are never used. Candidate `.cz.toml` hooks are hostile
data: publication must reject customization or isolate Commitizen's input using
trusted configuration before calling it. Invoking a trusted recipe is **not**
sufficient if `cz` still loads candidate hooks/plugins. Tests allow either safe
rejection or successful isolated publication, but never hook execution. Assets
are verified/uploaded as data, not extracted or executed with the App token.

All fixtures discard inherited credentials/global Git configuration, disable Git
hooks, and use temporary repositories. Static workflow concurrency and token
scope remain covered by workflows_test.go. These tests prove offline behavior,
not real App authentication, branch-protection bypass or GitHub service delivery.

## Publication implementation (T-034)

Release receives Build's `workflow_run`, **not** `push`: Build is triggered by
Checks completion. The corrected structural guard requires that event; the exact
Checks API run proves the original same-repository master push. Production also
binds the recorded Checks trigger, both run attempts and Build execution SHA to
API records. Verified development origins can skip orchestration; they never
reach App acquisition. Invalid provenance still fails closed.

`RELEASE_PREFLIGHT_ONLY=1 just ci-release PROVENANCE ASSET_DIR OUTPUT` validates
the six archives and chain, recomputes eligibility with standard Commitizen,
refreshes refs and returns `ready` without needing or obtaining an App token.
The workflow acquires the repository-scoped, Contents-write installation token
only after `ready=true`, then repeats these validations before publication.
Git operations use a disposable clone; tests fetch its objects only for assertions
without changing local fixture refs. A candidate's justfile/devenv never executes.
Commitizen always receives explicit `--config .cz.toml`, with a standard-only
allowlist excluding hooks, custom plugins and other settings. Alternative default
configuration locations cannot override that selection. Git hooks are disabled.

The public `vars.RELEASE_BOT_SLUG` permits own-bump suppression and recovery before
App acquisition. `release-bot-identity OUTPUT` resolves its numeric bot user ID
via GitHub's users API without a private key. Actual publishing identity is derived
again from the token action's real App slug, never from candidate metadata.
Checks still runs the quality gate for own bumps; Build classifies them as dev
with reason `own-bump`, so they cannot publish again. Missing App private settings
do not fail docs/no-increment/stale runs. A missing public slug prevents recovering
a pushed bump before token acquisition; eligible recovery needs that public setting.

Git credentials use an environment-only helper, with no token in remote URLs,
command arguments, repository configuration or secret files. Commitizen never
receives the installation token. Draft recovery uses authenticated, paginated
release listing because the tag endpoint may not return drafts. Asset listings
are also paginated; every existing asset is downloaded and byte-compared before
missing assets are uploaded. No deletion/overwrite is permitted. Publication
occurs only after seven verified assets, including SHA256SUMS, are complete.

Sources: [workflow_run](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#workflow_run),
[run API](https://docs.github.com/en/rest/actions/workflow-runs#get-a-workflow-run),
[App token and bot identity](https://github.com/actions/create-github-app-token/tree/fee1f7d63c2ff003460e3d139729b119787bc349),
and [Commitizen bump](https://commitizen-tools.github.io/commitizen/commands/bump/).
Hosted cold/warm caches, exact-run artifact delivery, private-repository Git auth,
actual App identity/token scope, branch-protection bypass and interrupted draft
recovery still need a real GitHub run after merge. No live publication occurs in
these offline tests.
