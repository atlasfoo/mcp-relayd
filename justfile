set positional-arguments
set quiet

# Explicit infrastructure gate; the normal devenv check remains app-only.
check-pipeline:
    #!/usr/bin/env bash
    set -euo pipefail
    (cd tests/release && go test -count=1 ./... && golangci-lint fmt --config ../../.golangci.yml --diff ./... && golangci-lint run --config ../../.golangci.yml ./... && go vet ./...)
    actionlint -shellcheck= -pyflakes=
    python3 -m unittest discover -s tests/pipeline -p 'test_*.py'

check-changes *range:
    python3 '{{justfile_directory()}}/tooling/change_selection.py' "$@"

# Resolve code beside this trusted justfile, not in the candidate working directory.
workflow-provenance mode input output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' workflow-provenance "$@"

workflow-handoff asset_dir producer_attempt artifact_id output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' workflow-handoff "$@"

ci-build provenance asset_dir output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' ci-build "$@"

ci-push provenance asset_dir output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' ci-push "$@"

ci-commits base head squash_title output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' ci-commits "$@"

ci-checks:
    python3 -I '{{justfile_directory()}}/tooling/release.py' ci-checks

release-candidate output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' release-candidate "$@"

release-preflight source_sha last_tag output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' release-preflight "$@"

release-bot-identity output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' release-bot-identity "$@"

release-own-bump source_sha output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' release-own-bump "$@"

ci-release provenance asset_dir output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' ci-release "$@"

release-package version source_sha run_id asset_dir output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' release-package "$@"

release-verify asset_dir output:
    python3 -I '{{justfile_directory()}}/tooling/release.py' release-verify "$@"
