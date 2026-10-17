"""Trusted release operations. Candidate repositories and artifacts are data only."""

import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tarfile
import tempfile
import tomllib
from urllib.parse import quote
import zipfile


STANDARD = dict(name="cz_conventional_commits", version_provider="commitizen", version_scheme="semver2", tag_format="v$version", major_version_zero=True, update_changelog_on_bump=True, changelog_file="CHANGELOG.md", bump_message="chore(release): bump version $current_version → $new_version [skip ci]")
TOKENS = ("GH_TOKEN", "GITHUB_TOKEN", "RELEASE_READ_TOKEN", "RELEASE_GIT_TOKEN")


def require(condition, message):
    if not condition:
        raise ValueError(message)


def semver_key(value):
    require(isinstance(value, str), "invalid SemVer")
    match = re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?", value)
    require(match is not None, "invalid SemVer")
    parts = match[4].split(".") if match[4] else []
    require(not any(p.isdigit() and len(p) > 1 and p.startswith("0") for p in parts), "invalid SemVer prerelease")
    return (*map(int, match.group(1, 2, 3)), match[4] is None, tuple((0, int(p)) if p.isdigit() else (1, p) for p in parts))


def latest_tag(tags):
    valid = []
    for tag in tags:
        if isinstance(tag, str) and tag.startswith("v"):
            try:
                valid.append((semver_key(tag[1:]), tag))
            except ValueError:
                pass
    return max(valid)[1] if valid else ""


def require_metadata_match(expected, actual, keys):
    for key in keys:
        require(key in expected and key in actual and type(expected[key]) is type(actual[key]) and expected[key] == actual[key], "metadata mismatch: " + key)


def validate_commitizen_config(config):
    try:
        settings = config["tool"]["commitizen"]
        version = settings["version"]
        semver_key(version)
        require(config.keys() == {"tool"} and config["tool"].keys() == {"commitizen"} and settings.keys() == STANDARD.keys() | {"version"}, "only standard Commitizen configuration is allowed")
        require_metadata_match(STANDARD, settings, STANDARD)
        return version
    except (KeyError, TypeError, AttributeError) as error:
        raise ValueError("only standard Commitizen configuration is allowed") from error


def clean_environment(environment):
    clean = {k: v for k, v in environment.items() if k not in (*TOKENS, "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "PYTHONHOME") and not k.startswith(("GIT_CONFIG_KEY_", "GIT_CONFIG_VALUE_"))}
    clean.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null", GIT_TERMINAL_PROMPT="0", GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="core.hooksPath", GIT_CONFIG_VALUE_0="/dev/null", PYTHONPATH="", PYTHONSAFEPATH="1")
    return clean


def git(*args, cwd=None, env=None):
    return subprocess.run(["git", "-c", "core.hooksPath=/dev/null", *args], cwd=cwd, env=clean_environment(os.environ) if env is None else env, check=True, capture_output=True, text=True).stdout.rstrip("\n")


def write_json(path, value):
    Path(path).write_text(json.dumps(value) + "\n", encoding="utf-8")


def workflow_provenance(mode, input_path):
    def run(record, run_id, path, repository, completed=True):
        require(type(run_id) is int and run_id > 0, "invalid run ID")
        require(record["id"] == run_id and type(record["id"]) is int, "wrong run")
        require(record["repository"]["full_name"] == repository, "foreign run")
        require(record["path"] == path, "wrong workflow")
        if completed:
            require(record["status"] == "completed" and record["conclusion"] == "success", "unsuccessful run")
        else:
            require((record["status"] == "in_progress" and record["conclusion"] is None) or (record["status"] == "completed" and record["conclusion"] == "success"), "unsuccessful caller")

    require(mode in ("build", "release"), "invalid mode")
    snapshot = json.loads(Path(input_path).read_text(encoding="utf-8"))
    repository, trigger = snapshot["repository"], snapshot["trigger_run_id"]
    require(isinstance(repository, str) and re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository), "invalid repository")
    require(type(trigger) is int and trigger > 0, "invalid trigger")
    checks, metadata = snapshot["checks"], snapshot["metadata"]
    run(checks, metadata["checks_run_id"], ".github/workflows/checks.yml", repository)
    if snapshot.get("require_checks_attempt") is True:
        require(type(metadata["checks_run_attempt"]) is int and metadata["checks_run_attempt"] == checks["run_attempt"], "wrong Checks artifact attempt")
    event = checks["event"]
    require(event in ("push", "pull_request"), "unsupported Checks event")
    source, head_repo, branch = checks["head_sha"], checks["head_repository"]["full_name"], checks["head_branch"]
    if event == "pull_request":
        pulls = checks["pull_requests"]
        require(isinstance(pulls, list) and len(pulls) == 1, "ambiguous PR head")
        source = pulls[0]["head"]["sha"]
        pr_repo = pulls[0]["head"]["repo"]
        if "full_name" in pr_repo:
            require(pr_repo["full_name"] == head_repo, "foreign PR head")
        else:
            require(type(pr_repo["id"]) is int and pr_repo["id"] > 0 and pr_repo["id"] == checks["head_repository"]["id"], "foreign PR head ID")
    require(isinstance(source, str) and re.fullmatch(r"[0-9a-f]{40}", source), "invalid source SHA")
    require(isinstance(head_repo, str) and re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", head_repo), "invalid head repository")
    require(isinstance(branch, str) and branch and not any(ord(c) < 32 or ord(c) == 127 for c in branch), "invalid branch")
    require_metadata_match(dict(repository=repository, source_sha=source, event=event, head_repository=head_repo, head_branch=branch), metadata, ("repository", "source_sha", "event", "head_repository", "head_branch"))
    release_origin = event == "push" and head_repo == repository and branch == "master"
    if mode == "build":
        require(trigger == checks["id"], "wrong Checks trigger")
    else:
        run(snapshot["build"], metadata["build_run_id"], ".github/workflows/build.yml", repository, completed=False)
        require(trigger == snapshot["build"]["id"] and snapshot["build"]["event"] == "workflow_run", "wrong Build trigger")
        require(snapshot["build"]["head_branch"] == "master" and snapshot["build"]["head_repository"]["full_name"] == repository, "foreign Build head")
        producer, attempt = snapshot["producer"], metadata["build_run_attempt"]
        require(type(attempt) is int and 0 < attempt <= snapshot["build"]["run_attempt"], "invalid producer attempt")
        require(producer["name"] == "build" and producer["run_id"] == trigger and producer["run_attempt"] == attempt and producer["head_sha"] == snapshot["build"]["head_sha"] and producer["status"] == "completed" and producer["conclusion"] == "success", "unsuccessful exact producer")
        require(snapshot["caller_workflow"] == repository + "/.github/workflows/build.yml@refs/heads/master" and snapshot["caller_sha"] == snapshot["build"]["head_sha"] and snapshot["caller_event"] == "workflow_run", "foreign caller")
        if snapshot.get("require_chain_binding") is True:
            require(type(metadata["trigger_checks_run_id"]) is int and metadata["trigger_checks_run_id"] == checks["id"], "wrong recorded Checks trigger")
            require(type(snapshot["caller_checks_run_id"]) is int and snapshot["caller_checks_run_id"] == checks["id"], "wrong caller Checks trigger")
            require(type(metadata["checks_run_attempt"]) is int and metadata["checks_run_attempt"] == checks["run_attempt"], "wrong Checks attempt")
            require(metadata["build_head_sha"] == snapshot["build"]["head_sha"], "wrong Build execution SHA")
        require(type(metadata["eligible"]) is bool, "invalid eligibility")
        require(release_origin, "ineligible release origin")
        require(metadata["eligible"] is True and metadata["artifact_class"] == "release", "invalid artifact class")
    eligible = metadata.get("eligible", False)
    require(type(eligible) is bool, "invalid eligibility")
    require(not eligible or release_origin, "ineligible artifact origin")
    if "artifact_class" in metadata:
        require(metadata["artifact_class"] == ("release" if eligible else "dev"), "invalid artifact class")
    result = dict(verified=True, repository=repository, source_sha=source, event=event, head_repository=head_repo, head_branch=branch, checks_run_id=checks["id"], eligible=eligible, artifact_class="release" if eligible else "dev")
    if "run_attempt" in checks:
        require(type(checks["run_attempt"]) is int and checks["run_attempt"] > 0, "invalid Checks attempt")
        result["checks_run_attempt"] = checks["run_attempt"]
    if mode == "release":
        result["build_run_id"] = metadata["build_run_id"]
        for key in ("version", "last_tag", "reason", "bump_sha", "tag", "bundle_sha256"):
            if key in metadata:
                require(isinstance(metadata[key], str) and not any(ord(c) < 32 for c in metadata[key]), "invalid " + key)
                result[key] = metadata[key]
        for key in ("trigger_checks_run_id", "build_run_attempt", "build_head_sha"):
            if key in metadata:
                result[key] = metadata[key]
    return result


def workflow_handoff(directory, attempt, artifact_id):
    def api(endpoint, *args):
        return json.loads(subprocess.check_output(["gh", "api", endpoint, *args]))

    attempt, artifact_id = int(attempt), int(artifact_id)
    repository, run_id = os.environ["GITHUB_REPOSITORY"], int(os.environ["GITHUB_RUN_ID"])
    metadata = json.loads((Path(directory) / "build-metadata.json").read_text())
    caller_checks = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())["workflow_run"]["id"]
    require(attempt > 0 and artifact_id > 0 and metadata["build_run_attempt"] == attempt and metadata["build_run_id"] == run_id, "wrong producer inputs")
    endpoint = f"repos/{repository}/actions"
    build = api(f"{endpoint}/runs/{run_id}")
    pages = api(f"{endpoint}/runs/{run_id}/attempts/{attempt}/jobs?per_page=100", "--paginate", "--slurp")
    producers = [job for page in pages for job in page["jobs"] if job["name"] == "build"]
    require(len(producers) == 1, "ambiguous producer")
    artifact = api(f"{endpoint}/artifacts/{artifact_id}")
    require(artifact["id"] == artifact_id and artifact["name"] == f"release-{run_id}-{attempt}" and artifact["expired"] is False and artifact["workflow_run"]["id"] == run_id and artifact["workflow_run"]["head_sha"] == build["head_sha"], "foreign artifact")
    checks = api(f"{endpoint}/runs/{metadata['checks_run_id']}")
    return dict(repository=repository, trigger_run_id=run_id, checks=checks, build=build, producer=producers[0], metadata=metadata, require_chain_binding=True, caller_workflow=os.environ["GITHUB_WORKFLOW_REF"], caller_sha=os.environ["GITHUB_SHA"], caller_event=os.environ["GITHUB_EVENT_NAME"], caller_checks_run_id=caller_checks)


def release_candidate():
    source = git("rev-parse", "HEAD")
    last = latest_tag(git("tag", "--merged", source).splitlines())
    result = dict(eligible=False, source_sha=source, last_tag=last, version="", reason="no-feat-fix")
    subjects = git("log", "--no-merges", "--format=%s", f"{last}..{source}" if last else source).splitlines()
    if not any(re.match(r"^(feat|fix)(\([^()\r\n]+\))?!?: .+", subject) for subject in subjects):
        return result
    require(not Path(".cz.toml").is_symlink(), "only standard Commitizen configuration is allowed")
    configured = validate_commitizen_config(tomllib.loads(Path(".cz.toml").read_text()))
    baseline = semver_key(last[1:] if last else configured)
    for option in ("--dry-run", "--get-next"):
        process = subprocess.run(["cz", "--config", ".cz.toml", "bump", option, "--yes"], env=clean_environment(os.environ), capture_output=True, text=True)
        if process.returncode == 21:
            result["reason"] = "no-increment"
            return result
        require(process.returncode == 0, "Commitizen policy probe failed")
    next_version = process.stdout.strip()
    proposed = semver_key(next_version)
    require(proposed >= baseline, "Commitizen proposed a decreased version")
    if proposed == baseline:
        result["reason"] = "no-increment"
    else:
        result.update(eligible=True, version=next_version, reason="eligible")
    return result


def release_preflight(source, last, *, cwd=None, env=None):
    current = git("rev-parse", "refs/remotes/origin/master^{commit}", cwd=cwd, env=env)
    latest = latest_tag(git("tag", "--merged", current, cwd=cwd, env=env).splitlines())
    reason = "stale-source" if current != source else "stale-tag" if latest != last else "current"
    return dict(publish=reason == "current", reason=reason)


def release_bot_identity(*, env=None):
    environment = os.environ if env is None else env
    slug = environment.get("RELEASE_BOT_SLUG", "")
    if not slug:
        return {}
    require(re.fullmatch(r"[A-Za-z0-9-]+", slug), "invalid App slug")
    user = json.loads(subprocess.check_output(["gh", "api", "users/" + slug + "%5Bbot%5D"], env=environment))
    require(type(user["id"]) is int and user["id"] > 0 and user["login"] == slug + "[bot]", "invalid App bot identity")
    return dict(RELEASE_BOT_NAME=slug + "[bot]", RELEASE_BOT_EMAIL=str(user["id"]) + "+" + slug + "[bot]@users.noreply.github.com")


def release_own_bump(source, *, cwd=None, env=None):
    environment = clean_environment(os.environ if env is None else env)
    def read(*args):
        return git(*args, cwd=cwd, env=environment)

    def own():
        require(re.fullmatch(r"[0-9a-f]{40}", source), "invalid source SHA")
        name, email = environment.get("RELEASE_BOT_NAME", ""), environment.get("RELEASE_BOT_EMAIL", "")
        if not name or not email:
            return False
        parents = read("show", "-s", "--format=%P", source).split()
        if len(parents) != 1:
            return False
        parent = parents[0]
        if read("show", "-s", "--format=%an%n%ae%n%cn%n%ce", source).splitlines() != [name, email, name, email]:
            return False
        if read("diff", "--name-only", parent, source) != ".cz.toml\nCHANGELOG.md":
            return False
        for ref in (parent, source):
            for file in (".cz.toml", "CHANGELOG.md"):
                entry = read("ls-tree", ref, "--", file)
                if entry and not entry.startswith("100644 blob "):
                    return False
        old, new = (read("show", ref + ":.cz.toml") for ref in (parent, source))
        try:
            before, after = (validate_commitizen_config(tomllib.loads(value)) for value in (old, new))
        except ValueError:
            return False
        if semver_key(after) <= semver_key(before):
            return False
        expected, count = re.subn(r'(?m)^version = "' + re.escape(before) + r'"$', 'version = "' + after + '"', old)
        return count == 1 and new == expected and read("show", "-s", "--format=%B", source) == f"chore(release): bump version {before} → {after} [skip ci]"

    return dict(own_bump=own())


def ci_commits(base, head, title):
    for value in (base, head):
        require(re.fullmatch(r"[0-9a-f]{40}", value), "expected a full commit SHA")
        git("cat-file", "-e", value + "^{commit}")
    require(git("rev-parse", "HEAD") == head, "HEAD differs from the requested source")
    for commit in git("rev-list", "--no-merges", f"{base}..{head}").splitlines():
        subprocess.run(["cz", "check", "--allowed-prefixes", "--message", git("show", "-s", "--format=%B", commit)], env=clean_environment(os.environ), check=True, stdout=sys.stderr)
    if title:
        require("\n" not in title and "\r" not in title, "squash title must be one line")
        subprocess.run(["cz", "check", "--allowed-prefixes", "--message", title], env=clean_environment(os.environ), check=True, stdout=sys.stderr)
    return dict(verified=True, source_sha=head)


def ci_checks():
    Path("checks-metadata.json").unlink(missing_ok=True)
    event, source, base = (os.environ[key] for key in ("CHECKS_EVENT", "CHECKS_SOURCE_SHA", "CHECKS_BASE_SHA"))
    title = os.environ.get("CHECKS_PR_TITLE", "")
    require(event in ("pull_request", "push") and git("rev-parse", "HEAD") == source, "unsupported event or mismatched checkout")
    require(re.fullmatch(r"[0-9a-f]{40}", base), "expected full base SHA")
    introductions = git("log", "--format=%H", "--diff-filter=A", source, "--", ".cz.toml").splitlines()
    if introductions:
        baseline = introductions[-1]
        if base == "0" * 40 or subprocess.run(["git", "merge-base", "--is-ancestor", baseline, base], capture_output=True).returncode == 1:
            base = baseline
    else:
        require(base != "0" * 40, "first push has no commit-tooling baseline")
    require(event != "pull_request" or title, "missing squash title")
    own = False
    if event == "push" and os.environ.get("GITHUB_REPOSITORY") and os.environ.get("CHECKS_HEAD_REPOSITORY") == os.environ["GITHUB_REPOSITORY"] and os.environ.get("CHECKS_HEAD_BRANCH") == "master":
        try:
            os.environ.update(release_bot_identity())
            own = release_own_bump(source)["own_bump"] is True
        except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError):
            pass
    if not own:
        ci_commits(base, source, title if event == "pull_request" else "")
    run_id = int(os.environ["GITHUB_RUN_ID"])
    require(run_id > 0, "invalid Checks run ID")
    metadata = dict(source_sha=source, repository=os.environ["GITHUB_REPOSITORY"], head_repository=os.environ["CHECKS_HEAD_REPOSITORY"], event=event, head_branch=os.environ["CHECKS_HEAD_BRANCH"], checks_run_id=run_id)
    if os.environ.get("GITHUB_ACTIONS") == "true":
        metadata["checks_run_attempt"] = int(os.environ["GITHUB_RUN_ATTEMPT"])
        require(metadata["checks_run_attempt"] > 0, "invalid Checks attempt")
    if not own:
        for command in (["check"], ["go", "vet", "./..."], ["go", "test", "./internal/process", "./internal/gateway", "./internal/integration", "-count=1"]):
            subprocess.run(command, check=True)
    write_json("checks-metadata.json", metadata)


def step_output(text):
    if os.environ.get("GITHUB_OUTPUT"):
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as stream:
            stream.write(text)


def ci_build(provenance, directory):
    metadata = json.loads(Path(provenance).read_text(encoding="utf-8"))
    source = metadata["source_sha"]
    require(metadata["verified"] is True and re.fullmatch(r"[0-9a-f]{40}", source) and git("rev-parse", "HEAD") == source, "source checkout differs from verified Checks SHA")
    git("diff", "--quiet", "HEAD", "--")
    require(type(metadata["checks_run_id"]) is int and metadata["checks_run_id"] > 0, "invalid Checks run ID")
    run_id = int(os.environ["GITHUB_RUN_ID"])
    require(run_id > 0, "invalid Build run ID")
    if os.environ.get("GITHUB_ACTIONS") == "true":
        event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
        require(event["workflow_run"]["id"] == metadata["checks_run_id"], "Build trigger differs from verified Checks")
        metadata.update(trigger_checks_run_id=event["workflow_run"]["id"], build_run_attempt=int(os.environ["GITHUB_RUN_ATTEMPT"]), build_head_sha=os.environ["GITHUB_SHA"])

    def skip(reason):
        print(json.dumps(dict(metadata, build_run_id=run_id, eligible=False, skipped=True, reason=reason)))
        step_output("eligible=false\nskipped=true\n")

    origin = metadata["event"] == "push" and metadata["head_repository"] == metadata["repository"] and metadata["head_branch"] == "master"
    if not origin:
        return skip("development-origin")
    os.environ.update(release_bot_identity())
    environment = clean_environment(os.environ)
    # Build never has a publication capability, including its Go subprocesses.
    os.environ.clear()
    os.environ.update(environment)
    if release_own_bump(source)["own_bump"]:
        return skip("own-bump")
    remote = subprocess.run(["git", "rev-parse", "--verify", "refs/remotes/origin/master^{commit}"], env=environment, capture_output=True, text=True)
    current = remote.stdout.strip() if remote.returncode == 0 else source
    with tempfile.TemporaryDirectory(prefix="mcp-relayd-build-") as temporary:
        scratch = Path(temporary)
        if current != source:
            if git("show", "-s", "--format=%P", current) != source or not release_own_bump(current)["own_bump"]:
                return skip("stale-source")
            version = validate_commitizen_config(tomllib.loads(git("show", current + ":.cz.toml")))
            tag, bump = "v" + version, current
            require(git("rev-parse", tag + "^{commit}") == current, "conflicting recovered tag")
            last = latest_tag(git("tag", "--merged", source).splitlines())
            if "last_tag" in metadata and metadata["last_tag"] != last:
                return skip("stale-tag")
            candidate = dict(eligible=True, version=version, reason="recovered-bump", last_tag=last)
        else:
            candidate = release_candidate()
            require(candidate["source_sha"] == source, "candidate source mismatch")
            if "last_tag" in metadata and candidate["last_tag"] != metadata["last_tag"]:
                return skip("stale-tag")
            if not candidate["eligible"]:
                return skip(candidate["reason"])
            require(environment.get("RELEASE_BOT_NAME") and environment.get("RELEASE_BOT_EMAIL"), "eligible Build requires trusted bot identity")
            environment.update(GIT_AUTHOR_NAME=environment["RELEASE_BOT_NAME"], GIT_AUTHOR_EMAIL=environment["RELEASE_BOT_EMAIL"], GIT_COMMITTER_NAME=environment["RELEASE_BOT_NAME"], GIT_COMMITTER_EMAIL=environment["RELEASE_BOT_EMAIL"])
            work = scratch / "bump"
            subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "clone", "--no-local", "--no-checkout", str(Path.cwd()), str(work)], check=True, env=environment, capture_output=True)
            git("checkout", "--detach", source, cwd=work, env=environment)
            subprocess.run(["cz", "--config", ".cz.toml", "bump", "--yes"], cwd=work, env=environment, check=True)
            bump = git("rev-parse", "HEAD", cwd=work)
            tag = "v" + candidate["version"]
            require(git("show", "-s", "--format=%P", bump, cwd=work) == source and release_own_bump(bump, cwd=work, env=environment)["own_bump"] and git("rev-parse", tag + "^{commit}", cwd=work) == bump and validate_commitizen_config(tomllib.loads(git("show", bump + ":.cz.toml", cwd=work))) == candidate["version"], "unsafe generated bump/tag")
            git("fetch", str(work), "refs/tags/" + tag + ":refs/tags/" + tag)
        version = candidate["version"]
        destination = Path(directory).resolve()
        destination.mkdir(parents=True, exist_ok=True)
        (destination / "build-metadata.json").unlink(missing_ok=True)
        release_package(version, source, str(run_id), str(destination))
        manifest = release_verify(str(destination))
        git("bundle", "create", str(destination / "bump.bundle"), "refs/tags/" + tag, "^" + source)
        metadata["bundle_sha256"] = hashlib.sha256((destination / "bump.bundle").read_bytes()).hexdigest()
    metadata.update(build_run_id=run_id, eligible=True, skipped=False, version=version, bump_sha=bump, tag=tag, artifact_class="release", last_tag=candidate["last_tag"], reason=candidate["reason"])
    manifest.update(metadata)
    (destination / "manifest.json").write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n", encoding="utf-8")
    write_json(destination / "build-metadata.json", metadata)
    step_output(f"artifact_class={metadata['artifact_class']}\neligible=true\nskipped=false\nsource_sha={source}\nbump_sha={bump}\ntag={tag}\nversion={version}\n")
    return metadata


def manifest_matches(metadata, manifest, keys):
    require_metadata_match(metadata, manifest, [key for key in keys if key in metadata])


def ci_push(provenance, directory):
    metadata = json.loads(Path(provenance).read_text())
    repository = os.environ["GITHUB_REPOSITORY"]
    require(re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository), "invalid repository")
    require(metadata["verified"] is True and metadata["repository"] == repository and metadata["head_repository"] == repository and metadata["event"] == "push" and metadata["head_branch"] == "master" and metadata["eligible"] is True, "ineligible provenance")
    source, bump, tag, version = (metadata[key] for key in ("source_sha", "bump_sha", "tag", "version"))
    semver_key(version)
    require(all(re.fullmatch(r"[0-9a-f]{40}", value) for value in (source, bump)) and tag == "v" + version, "invalid bump/tag")
    assets = Path(directory).resolve(strict=True)
    environment = clean_environment(os.environ)

    def result(ready, pushed, reason):
        step_output(f"ready={str(ready).lower()}\npushed={str(pushed).lower()}\n")
        return dict(ready=ready, pushed=pushed, reason=reason)

    with tempfile.TemporaryDirectory(prefix="mcp-relayd-push-") as temporary:
        scratch = Path(temporary)
        manifest = release_verify(str(assets))
        manifest_matches(metadata, manifest, ("repository", "head_repository", "event", "head_branch", "checks_run_id", "build_run_id", "source_sha", "version", "eligible", "artifact_class", "last_tag", "bump_sha", "tag", "bundle_sha256", "build_run_attempt", "build_head_sha", "trigger_checks_run_id", "checks_run_attempt"))
        require(manifest["run_id"] == str(metadata["build_run_id"]), "wrong artifact run")
        bundle = assets / "bump.bundle"
        require(stat.S_ISREG(bundle.lstat().st_mode) and hashlib.sha256(bundle.read_bytes()).hexdigest() == metadata["bundle_sha256"], "invalid bundle checksum")
        remote = os.environ.get("RELEASE_REMOTE", f"https://github.com/{repository}.git")
        if os.environ.get("GITHUB_ACTIONS") == "true":
            require(remote == f"https://github.com/{repository}.git", "foreign remote")
        helper = '!f() { test "$1" = get && printf "username=x-access-token\\npassword=%s\\n" "$RELEASE_GIT_TOKEN"; }; f'
        read_token = os.environ.get("RELEASE_READ_TOKEN")
        if read_token:
            environment.update(RELEASE_GIT_TOKEN=read_token, GIT_CONFIG_COUNT="3", GIT_CONFIG_KEY_1="credential.helper", GIT_CONFIG_VALUE_1="", GIT_CONFIG_KEY_2="credential.helper", GIT_CONFIG_VALUE_2=helper)
        work = scratch / "objects"
        def transport(*args, env=None):
            try:
                return git(*args, cwd=work, env=environment if env is None else env)
            except subprocess.CalledProcessError as error:
                raise ValueError("handoff/ref verification or atomic push failed") from error

        git("init", "--bare", str(work), env=environment)
        transport("remote", "add", "origin", remote)
        transport("fetch", "origin", "refs/heads/master:refs/remotes/origin/master", "refs/tags/*:refs/tags/*")
        remote_tags = transport("ls-remote", "--tags", "origin")
        require(transport("bundle", "list-heads", str(bundle)) == bump + " refs/tags/" + tag, "unexpected bundle refs")
        transport("bundle", "verify", str(bundle))
        transport("fetch", "--no-tags", str(bundle), "refs/tags/" + tag + ":refs/handoff/tag")
        require(transport("cat-file", "-t", "refs/handoff/tag") == "commit" and transport("rev-parse", "refs/handoff/tag") == bump and transport("show", "-s", "--format=%P", bump) == source, "wrong handoff parent/tag")
        identity_env = dict(environment)
        if read_token:
            identity_env["GH_TOKEN"] = read_token
        environment.update(release_bot_identity(env=identity_env))
        require(release_own_bump(bump, cwd=work, env=environment)["own_bump"] is True and validate_commitizen_config(tomllib.loads(transport("show", bump + ":.cz.toml"))) == version, "forged handoff bump")

        def refs():
            current = transport("ls-remote", "origin", "refs/heads/master").split()[0]
            tags = transport("ls-remote", "--tags", "origin")
            return current, tags, current == bump and (bump + "\trefs/tags/" + tag) in tags.splitlines()

        current, tags, recovered = refs()
        if not recovered and current != source:
            return result(False, False, "stale-source")
        if not recovered:
            require(not transport("tag", "--list", tag), "conflicting remote tag")
            if not release_preflight(source, metadata["last_tag"], cwd=work, env=environment)["publish"] or tags != remote_tags:
                return result(False, False, "stale-tag")
        if os.environ.get("RELEASE_PREFLIGHT_ONLY") == "1":
            return result(True, recovered, "recovered" if recovered else "current")
        require(os.environ.get("GH_TOKEN"), "push requires App token")
        push_env = dict(environment, RELEASE_GIT_TOKEN=os.environ["GH_TOKEN"], GIT_CONFIG_COUNT="3", GIT_CONFIG_KEY_1="credential.helper", GIT_CONFIG_VALUE_1="", GIT_CONFIG_KEY_2="credential.helper", GIT_CONFIG_VALUE_2=helper)
        require(refs() == (current, tags, recovered), "remote refs changed")
        if not recovered:
            transport("push", "--atomic", "origin", bump + ":refs/heads/master", "refs/handoff/tag:refs/tags/" + tag, env=push_env)
        require(refs()[2], "push refs not confirmed")
        return result(True, True, "recovered" if recovered else "pushed")


def ci_release(provenance, directory):
    metadata = json.loads(Path(provenance).read_text())
    repository = os.environ["GITHUB_REPOSITORY"]
    require(re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository), "invalid repository")
    require(metadata["verified"] is True and metadata["repository"] == repository and metadata["head_repository"] == repository and metadata["event"] == "push" and metadata["head_branch"] == "master", "ineligible provenance")
    source, version, last_tag = (metadata[key] for key in ("source_sha", "version", "last_tag"))
    require(re.fullmatch(r"[0-9a-f]{40}", source), "invalid source")
    for key in ("checks_run_id", "build_run_id"):
        require(type(metadata[key]) is int and metadata[key] > 0, "invalid run")
    require(metadata["eligible"] is True and metadata["artifact_class"] == "release", "invalid eligibility")
    assets = Path(directory).resolve(strict=True)
    environment = clean_environment(os.environ)

    def result(published, reason, ready=False):
        step_output(f"ready={'true' if ready else 'false'}\n")
        return dict(published=published, reason=reason, ready=ready)

    with tempfile.TemporaryDirectory(prefix="mcp-relayd-release-") as temporary:
        scratch = Path(temporary)
        manifest = release_verify(str(assets))
        require_metadata_match(metadata, manifest, ("repository", "head_repository", "event", "head_branch", "checks_run_id", "build_run_id", "source_sha", "version", "eligible", "artifact_class", "last_tag"))
        manifest_matches(metadata, manifest, ("checks_run_attempt", "trigger_checks_run_id", "build_run_attempt", "build_head_sha"))
        require(manifest["run_id"] == str(metadata["build_run_id"]), "wrong artifact run")
        semver_key(version)
        require(last_tag == "" or re.fullmatch(r"v[0-9A-Za-z.+-]+", last_tag), "invalid last tag")
        bump, tag, digest = (metadata[key] for key in ("bump_sha", "tag", "bundle_sha256"))
        require(re.fullmatch(r"[0-9a-f]{40}", bump) and tag == "v" + version and re.fullmatch(r"[0-9a-f]{64}", digest), "invalid handoff")
        require_metadata_match(metadata, manifest, ("bump_sha", "tag", "bundle_sha256"))
        require(int(os.environ["GITHUB_RUN_ID"]) == metadata["build_run_id"], "foreign caller run")
        if os.environ.get("GITHUB_ACTIONS") == "true":
            require(os.environ["GITHUB_WORKFLOW_REF"] == repository + "/.github/workflows/build.yml@refs/heads/master" and os.environ["GITHUB_EVENT_NAME"] == "workflow_run" and os.environ["GITHUB_SHA"] == metadata["build_head_sha"], "foreign caller")
            require(json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())["workflow_run"]["id"] == metadata["checks_run_id"] == metadata["trigger_checks_run_id"], "foreign Checks trigger")
            for key in ("checks_run_attempt", "build_run_attempt"):
                require(type(metadata[key]) is int and metadata[key] > 0, "invalid producer attempt")
        bundle = assets / "bump.bundle"
        require(stat.S_ISREG(bundle.lstat().st_mode) and hashlib.sha256(bundle.read_bytes()).hexdigest() == digest, "invalid bundle checksum")
        remote = git("remote", "get-url", "origin", env=environment)
        if os.environ.get("GITHUB_ACTIONS") == "true":
            require(remote == f"https://github.com/{repository}.git", "foreign Git remote")
        read_token = os.environ.get("RELEASE_READ_TOKEN")
        helper = '!f() { test "$1" = get && printf "username=x-access-token\\npassword=%s\\n" "$RELEASE_GIT_TOKEN"; }; f'
        if read_token:
            environment.update(RELEASE_GIT_TOKEN=read_token, GIT_CONFIG_COUNT="3", GIT_CONFIG_KEY_1="credential.helper", GIT_CONFIG_VALUE_1="", GIT_CONFIG_KEY_2="credential.helper", GIT_CONFIG_VALUE_2=helper)
        work = scratch / "objects"
        git("init", "--bare", str(work), env=environment)
        def transport(*args):
            return git(*args, cwd=work, env=environment)

        transport("remote", "add", "origin", remote)
        transport("fetch", "origin", "refs/heads/master:refs/remotes/origin/master", "refs/tags/*:refs/tags/*")
        remote_tags = transport("ls-remote", "--tags", "origin")
        require(transport("cat-file", "-t", "refs/tags/" + tag) == "commit" and transport("rev-parse", "refs/tags/" + tag) == bump, "unpublished or conflicting tag")
        require(transport("rev-parse", "refs/remotes/origin/master") == bump and transport("show", "-s", "--format=%P", bump) == source, "unpublished or stale bump")
        require(transport("bundle", "list-heads", str(bundle)) == bump + " refs/tags/" + tag, "unexpected bundle refs")
        transport("bundle", "verify", str(bundle))
        require(release_own_bump(bump, cwd=work, env=environment)["own_bump"] is True and validate_commitizen_config(tomllib.loads(transport("show", bump + ":.cz.toml"))) == version, "forged published bump")
        require(release_preflight(bump, tag, cwd=work, env=environment)["publish"], "stale-tag")
        require(latest_tag(transport("tag", "--merged", source).splitlines()) == last_tag, "stale baseline tag")

        def stable_refs():
            require(transport("ls-remote", "origin", "refs/heads/master").split()[0] == bump and transport("ls-remote", "--tags", "origin") == remote_tags, "published refs changed")

        stable_refs()
        if os.environ.get("RELEASE_PREFLIGHT_ONLY") == "1":
            return result(False, "current", True)
        require(os.environ.get("GH_TOKEN") and environment.get("RELEASE_BOT_NAME") and environment.get("RELEASE_BOT_EMAIL"), "eligible publication requires App token and identity")
        publication_environment = dict(environment, GH_TOKEN=os.environ["GH_TOKEN"])
        def api(endpoint, *args, missing=False, raw=False):
            process = subprocess.run(["gh", "api", endpoint, *args], env=publication_environment, capture_output=True)
            if missing and process.returncode and b"404" in process.stderr:
                return None
            require(process.returncode == 0, "GitHub release API request failed")
            return process.stdout if raw else json.loads(process.stdout)

        def api_list(endpoint):
            pages = api(endpoint + "?per_page=100", "--paginate", raw=True).decode()
            decoder, records = json.JSONDecoder(), []
            while pages.strip():
                page, end = decoder.raw_decode(pages.lstrip())
                require(isinstance(page, list), "invalid paginated API response")
                records.extend(page)
                pages = pages.lstrip()[end:]
            return records

        endpoint = f"repos/{repository}/releases"
        release = api(endpoint + "/tags/" + quote(tag, safe=""), missing=True)
        if release is None:
            matches = [item for item in api_list(endpoint) if item["tag_name"] == tag]
            require(len(matches) <= 1, "duplicate releases")
            release = matches[0] if matches else None
        notes = transport("show", bump + ":CHANGELOG.md")
        section = re.search(r"(?ms)^## v?" + re.escape(version) + r"(?:[ \t][^\n]*)?\n.*?(?=^## |\Z)", notes)
        require(section is not None, "missing incremental changelog")
        notes = section[0].strip()
        if release is None:
            stable_refs()
            release = api(endpoint, "-X", "POST", "-f", "tag_name=" + tag, "-f", "target_commitish=" + bump, "-F", "draft=true", "-f", "body=" + notes)
        require(release["tag_name"] == tag and type(release["id"]) is int and release["id"] > 0 and type(release["draft"]) is bool, "conflicting release")
        require(release["target_commitish"] == bump and release["body"] == notes, "conflicting release target/notes")
        release_endpoint = endpoint + "/" + str(release["id"])
        files = {asset["name"]: assets / asset["name"] for asset in manifest["assets"]}
        sums = scratch / "SHA256SUMS"
        sums.write_text("".join(asset["sha256"] + "  " + asset["name"] + "\n" for asset in sorted(manifest["assets"], key=lambda item: item["name"])))
        files["SHA256SUMS"] = sums

        def verify_remote():
            records, seen = api_list(release_endpoint + "/assets"), set()
            for asset in records:
                name = asset["name"]
                require(name in files and name not in seen and type(asset["id"]) is int and asset["id"] > 0, "conflicting remote assets")
                seen.add(name)
                data = api(endpoint + "/assets/" + str(asset["id"]), "-H", "Accept: application/octet-stream", raw=True)
                require(data == files[name].read_bytes(), "conflicting asset bytes")
            return seen

        seen = verify_remote()
        require(release["draft"] or seen == set(files), "published release incomplete")
        for name in sorted(set(files) - seen):
            stable_refs()
            api(f"https://uploads.github.com/{release_endpoint}/assets?name=" + quote(name, safe=""), "-X", "POST", "-H", "Content-Type: application/octet-stream", "--input", str(files[name]))
        require(verify_remote() == set(files), "incomplete uploaded assets")
        if release["draft"]:
            stable_refs()
            api(release_endpoint, "-X", "PATCH", "-F", "draft=false")
        return result(True, "published")


def release_package(version, source, run, directory):
    for label, value in (("version", version), ("source_sha", source), ("run_id", run)):
        require(re.fullmatch(r"[0-9A-Za-z][0-9A-Za-z.+_-]*", value), "invalid " + label)
    destination = Path(directory).resolve()
    destination.mkdir(parents=True, exist_ok=True)
    manifest_path = destination / "manifest.json"
    manifest_path.unlink(missing_ok=True)
    assets = []
    with tempfile.TemporaryDirectory(prefix="mcp-relayd-package-") as temporary:
        stage = Path(temporary)
        for goos in ("linux", "darwin", "windows"):
            for goarch in ("amd64", "arm64"):
                executable = "mcp-relayd.exe" if goos == "windows" else "mcp-relayd"
                binary = stage / executable
                environment = dict(clean_environment(os.environ), GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
                subprocess.run([os.environ.get("GO", "go"), "build", "-trimpath", "-buildvcs=false", "-ldflags", f"-X main.version={version} -X main.sourceSHA={source}", "-o", str(binary), "./cmd/mcp-relayd"], env=environment, check=True)
                data = binary.read_bytes()
                suffix = "zip" if goos == "windows" else "tar.gz"
                name = f"mcp-relayd_{version}_{goos}_{goarch}.{suffix}"
                archive = stage / name
                if goos == "windows":
                    with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as package:
                        member = zipfile.ZipInfo(executable, date_time=(1980, 1, 1, 0, 0, 0))
                        member.create_system = 3
                        member.external_attr = 0o100755 << 16
                        member.compress_type = zipfile.ZIP_DEFLATED
                        package.writestr(member, data)
                else:
                    with archive.open("wb") as raw:
                        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
                            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.USTAR_FORMAT) as package:
                                member = tarfile.TarInfo(executable)
                                member.size, member.mode, member.mtime = len(data), 0o755, 0
                                package.addfile(member, io.BytesIO(data))
                assets.append(dict(name=name, sha256=hashlib.sha256(archive.read_bytes()).hexdigest()))
        for asset in assets:
            (destination / asset["name"]).write_bytes((stage / asset["name"]).read_bytes())
    manifest = dict(source_sha=source, run_id=run, version=version, assets=assets)
    manifest_path.write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n", encoding="utf-8")
    return manifest


def release_verify(directory):
    def regular(path):
        require(stat.S_ISREG(path.lstat().st_mode), "not a regular file: " + path.name)

    directory = Path(directory).resolve()
    manifest_path = directory / "manifest.json"
    regular(manifest_path)
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    for field in ("version", "source_sha", "run_id"):
        require(isinstance(manifest[field], str) and re.fullmatch(r"[0-9A-Za-z][0-9A-Za-z.+_-]*", manifest[field]), "invalid " + field)
    version = manifest["version"]
    expected = {f"mcp-relayd_{version}_{goos}_{goarch}.{'zip' if goos == 'windows' else 'tar.gz'}" for goos in ("linux", "darwin", "windows") for goarch in ("amd64", "arm64")}
    assets, seen = manifest["assets"], set()
    require(isinstance(assets, list) and len(assets) == 6, "manifest must contain six assets")
    for asset in assets:
        name, digest = asset["name"], asset["sha256"]
        require(isinstance(name, str) and name in expected and name not in seen, "unexpected or duplicate asset name")
        seen.add(name)
        require(isinstance(digest, str) and re.fullmatch(r"[0-9a-f]{64}", digest), "invalid SHA-256")
        path = directory / name
        regular(path)
        require(hashlib.sha256(path.read_bytes()).hexdigest() == digest, "checksum mismatch: " + name)
        if name.endswith(".zip"):
            with zipfile.ZipFile(path) as package:
                members = package.infolist()
                require(len(members) == 1 and members[0].filename == "mcp-relayd.exe" and stat.S_ISREG(members[0].external_attr >> 16) and members[0].file_size > 0, "unsafe zip member: " + name)
                require(package.testzip() is None, "invalid zip content: " + name)
        else:
            with tarfile.open(path, "r:gz") as package:
                members = package.getmembers()
                require(len(members) == 1 and members[0].name == "mcp-relayd" and members[0].isreg() and members[0].size > 0 and members[0].mode == 0o755, "unsafe tar member: " + name)
    return dict(manifest, verified=True)


COMMANDS = {function.__name__.replace("_", "-"): function for function in (workflow_provenance, workflow_handoff, ci_build, ci_push, ci_commits, ci_checks, release_candidate, release_preflight, release_bot_identity, release_own_bump, ci_release, release_package, release_verify)}


def main():
    name = sys.argv[1]
    try:
        operation = COMMANDS[name]
        args = sys.argv[2:]
        output = args.pop() if name != "ci-checks" else None
        if output and name in ("workflow-provenance", "ci-build", "ci-push", "ci-release"):
            Path(output).unlink(missing_ok=True)
        value = operation(*args)
        if output and value is not None:
            write_json(output, value)
        return 0
    except (OSError, ValueError, KeyError, TypeError, IndexError, subprocess.CalledProcessError, tarfile.TarError, zipfile.BadZipFile, EOFError):
        # Do not expose subprocess arguments, outputs, or credential-bearing data.
        print(name + ": trusted operation failed", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
