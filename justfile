set positional-arguments

# Offline verification of API runs and exact-run artifact claims; never execute data.
workflow-provenance mode input output:
    #!/usr/bin/env python3
    import json
    import re
    import sys
    from pathlib import Path

    def require(condition, message):
        if not condition:
            raise ValueError(message)

    def run(record, run_id, path, repository):
        require(type(run_id) is int and run_id > 0, "invalid run ID")
        require(record["id"] == run_id and type(record["id"]) is int, "wrong run")
        require(record["repository"]["full_name"] == repository, "foreign run")
        require(record["path"] == path, "wrong workflow")
        require(record["status"] == "completed" and record["conclusion"] == "success", "unsuccessful run")

    try:
        mode, input_path, output_path = sys.argv[1:]
        Path(output_path).unlink(missing_ok=True)
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
            # GitHub's run API can expose only id/name/url in pull_requests.head.repo.
            pr_repo = pulls[0]["head"]["repo"]
            if "full_name" in pr_repo:
                require(pr_repo["full_name"] == head_repo, "foreign PR head")
            else:
                require(type(pr_repo["id"]) is int and pr_repo["id"] > 0 and pr_repo["id"] == checks["head_repository"]["id"], "foreign PR head ID")
        require(isinstance(source, str) and re.fullmatch(r"[0-9a-f]{40}", source), "invalid source SHA")
        require(isinstance(head_repo, str) and re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", head_repo), "invalid head repository")
        require(isinstance(branch, str) and branch and not any(ord(c) < 32 or ord(c) == 127 for c in branch), "invalid branch")
        for key, expected in (("repository", repository), ("source_sha", source), ("event", event), ("head_repository", head_repo), ("head_branch", branch)):
            require(metadata[key] == expected, "metadata mismatch: " + key)
        release_origin = event == "push" and head_repo == repository and branch == "master"
        if mode == "build":
            require(trigger == checks["id"], "wrong Checks trigger")
        else:
            run(snapshot["build"], metadata["build_run_id"], ".github/workflows/build.yml", repository)
            require(trigger == snapshot["build"]["id"] and snapshot["build"]["event"] == "workflow_run", "wrong Build trigger")
            if snapshot.get("require_chain_binding") is True:
                require(type(metadata["trigger_checks_run_id"]) is int and metadata["trigger_checks_run_id"] == checks["id"], "wrong recorded Checks trigger")
                require(type(metadata["checks_run_attempt"]) is int and metadata["checks_run_attempt"] == checks["run_attempt"], "wrong Checks attempt")
                require(type(metadata["build_run_attempt"]) is int and metadata["build_run_attempt"] == snapshot["build"]["run_attempt"], "wrong Build attempt")
                require(metadata["build_head_sha"] == snapshot["build"]["head_sha"], "wrong Build execution SHA")
            require(type(metadata["eligible"]) is bool, "invalid eligibility")
            require(release_origin or (snapshot.get("allow_development_skip") is True and not metadata["eligible"]), "ineligible release origin")
            require(metadata["artifact_class"] == ("release" if metadata["eligible"] else "dev"), "invalid artifact class")
        # Checks metadata normally has no eligibility. Build computes it from source later.
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
            for key in ("version", "last_tag", "reason"):
                if key in metadata:
                    require(isinstance(metadata[key], str) and not any(ord(c) < 32 for c in metadata[key]), "invalid " + key)
                    result[key] = metadata[key]
            for key in ("trigger_checks_run_id", "build_run_attempt", "build_head_sha"):
                if key in metadata:
                    result[key] = metadata[key]
        Path(output_path).write_text(json.dumps(result) + "\n", encoding="utf-8")
    except (OSError, ValueError, KeyError, TypeError, IndexError) as error:
        print(f"workflow-provenance: {error}", file=sys.stderr)
        sys.exit(1)

# Execute trusted recipes in the candidate worktree, never its justfile or devenv.
ci-build provenance asset_dir output:
    #!/usr/bin/env python3
    import json
    import os
    import re
    import subprocess
    import sys
    import tempfile
    from pathlib import Path

    try:
        provenance, directory, output = sys.argv[1:]
        Path(output).unlink(missing_ok=True)
        metadata = json.loads(Path(provenance).read_text(encoding="utf-8"))
        source = metadata["source_sha"]
        head = subprocess.run(["git", "rev-parse", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()
        if metadata["verified"] is not True or not re.fullmatch(r"[0-9a-f]{40}", source) or head != source:
            raise ValueError("source checkout differs from verified Checks SHA")
        run_id = int(os.environ["GITHUB_RUN_ID"])
        if run_id <= 0:
            raise ValueError("invalid Build run ID")
        if os.environ.get("GITHUB_ACTIONS") == "true":
            event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
            if event["workflow_run"]["id"] != metadata["checks_run_id"]:
                raise ValueError("Build trigger differs from verified Checks")
            metadata.update(trigger_checks_run_id=event["workflow_run"]["id"], build_run_attempt=int(os.environ["GITHUB_RUN_ATTEMPT"]), build_head_sha=os.environ["GITHUB_SHA"])
        trusted = Path(os.environ["CI_JUSTFILE"]).resolve(strict=True)
        command = ["just", "--justfile", str(trusted), "--working-directory", str(Path.cwd())]
        origin = metadata["event"] == "push" and metadata["head_repository"] == metadata["repository"] and metadata["head_branch"] == "master"
        candidate = dict(eligible=False, last_tag="", version="", reason="development-origin")
        with tempfile.TemporaryDirectory(prefix="mcp-relayd-build-") as temporary:
            scratch = Path(temporary)
            if origin:
                subprocess.run([*command, "release-bot-identity", str(scratch / "identity.json")], check=True)
                os.environ.update(json.loads((scratch / "identity.json").read_text()))
                subprocess.run([*command, "release-own-bump", source, str(scratch / "own.json")], check=True)
                if json.loads((scratch / "own.json").read_text())["own_bump"]:
                    candidate["reason"] = "own-bump"
                else:
                    subprocess.run([*command, "release-candidate", str(scratch / "candidate.json")], check=True)
                    candidate = json.loads((scratch / "candidate.json").read_text(encoding="utf-8"))
                    if candidate["source_sha"] != source:
                        raise ValueError("candidate source mismatch")
            eligible = candidate["eligible"]
            version = candidate["version"] if eligible else f"0.0.0-dev.{run_id}+{source[:12]}"
            destination = Path(directory).resolve()
            destination.mkdir(parents=True, exist_ok=True)
            (destination / "build-metadata.json").unlink(missing_ok=True)
            subprocess.run([*command, "release-package", version, source, str(run_id), str(destination), str(scratch / "package.json")], check=True)
            subprocess.run([*command, "release-verify", str(destination), str(scratch / "verified.json")], check=True)
            manifest = json.loads((scratch / "verified.json").read_text(encoding="utf-8"))
        metadata.update(build_run_id=run_id, eligible=eligible, version=version, artifact_class="release" if eligible else "dev", last_tag=candidate["last_tag"], reason=candidate["reason"])
        # Keep full chain metadata in the manifest as well as the standalone artifact.
        manifest.update(metadata)
        (destination / "manifest.json").write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n", encoding="utf-8")
        (destination / "build-metadata.json").write_text(json.dumps(metadata) + "\n", encoding="utf-8")
        Path(output).write_text(json.dumps(metadata) + "\n", encoding="utf-8")
        if "GITHUB_OUTPUT" in os.environ:
            with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as step_output:
                step_output.write(f"artifact_class={metadata['artifact_class']}\n")
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"ci-build: {error}", file=sys.stderr)
        sys.exit(1)

# Validate only the new range. All untrusted messages are subprocess arguments.
ci-commits base head squash_title output:
    #!/usr/bin/env python3
    import json
    import re
    import subprocess
    import sys
    from pathlib import Path

    def git(*args):
        return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout.strip()

    try:
        base, head, title, output = sys.argv[1:]
        for value in (base, head):
            if not re.fullmatch(r"[0-9a-f]{40}", value):
                raise ValueError("expected a full commit SHA")
            git("cat-file", "-e", value + "^{commit}")
        if git("rev-parse", "HEAD") != head:
            raise ValueError("HEAD differs from the requested source")
        # Skip actual merge objects, not arbitrary subjects starting with "Merge".
        commits = git("rev-list", "--no-merges", f"{base}..{head}").splitlines()
        for commit in commits:
            message = git("show", "-s", "--format=%B", commit)
            subprocess.run(["cz", "check", "--allowed-prefixes", "--message", message], check=True, stdout=sys.stderr)
        if title:
            if "\n" in title or "\r" in title:
                raise ValueError("squash title must be one line")
            # Unlike merge commits, squash titles may not use cz's ignored prefixes.
            subprocess.run(["cz", "check", "--allowed-prefixes", "--message", title], check=True, stdout=sys.stderr)
        Path(output).write_text(json.dumps(dict(verified=True, source_sha=head)) + "\n", encoding="utf-8")
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"ci-commits: {error}", file=sys.stderr)
        sys.exit(1)

# One serial environment, same local gate, no release-eligibility dependency.
ci-checks:
    #!/usr/bin/env python3
    import json
    import os
    import re
    import subprocess
    import sys
    import tempfile
    from pathlib import Path

    def git(*args):
        return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout.strip()

    try:
        Path("checks-metadata.json").unlink(missing_ok=True)
        event = os.environ["CHECKS_EVENT"]
        source = os.environ["CHECKS_SOURCE_SHA"]
        base = os.environ["CHECKS_BASE_SHA"]
        title = os.environ.get("CHECKS_PR_TITLE", "")
        if event not in ("pull_request", "push") or git("rev-parse", "HEAD") != source:
            raise ValueError("unsupported event or mismatched checkout")
        if not re.fullmatch(r"[0-9a-f]{40}", base):
            raise ValueError("expected full base SHA")
        # A first push or a range starting before commit tooling excludes legacy history.
        introductions = git("log", "--format=%H", "--diff-filter=A", source, "--", ".cz.toml").splitlines()
        if introductions:
            baseline = introductions[-1]
            if base == "0" * 40 or subprocess.run(["git", "merge-base", "--is-ancestor", baseline, base], capture_output=True).returncode == 1:
                base = baseline
        elif base == "0" * 40:
            raise ValueError("first push has no commit-tooling baseline")
        if event == "pull_request" and not title:
            raise ValueError("missing squash title")
        with tempfile.TemporaryDirectory(prefix="mcp-relayd-checks-") as temporary:
            scratch = Path(temporary)
            own = False
            if event == "push" and os.environ.get("CHECKS_HEAD_REPOSITORY") == os.environ.get("GITHUB_REPOSITORY") and os.environ.get("CHECKS_HEAD_BRANCH") == "master":
                subprocess.run(["just", "release-bot-identity", str(scratch / "identity.json")], check=True)
                os.environ.update(json.loads((scratch / "identity.json").read_text()))
                subprocess.run(["just", "release-own-bump", source, str(scratch / "own.json")], check=True)
                own = json.loads((scratch / "own.json").read_text())["own_bump"]
            if not own:
                subprocess.run(["just", "ci-commits", base, source, title if event == "pull_request" else "", str(scratch / "commits.json")], check=True)
        for command in (["go", "test", "./..."], ["golangci-lint", "fmt", "--diff", "./..."], ["golangci-lint", "run", "./..."], ["typos", "--force-exclude", "."], ["go", "vet", "./..."], ["check"], ["go", "test", "./internal/process", "./internal/gateway", "./internal/integration", "-count=1"], ["actionlint", "-shellcheck=", "-pyflakes="]):
            subprocess.run(command, check=True)
        run_id = int(os.environ["GITHUB_RUN_ID"])
        if run_id <= 0:
            raise ValueError("invalid Checks run ID")
        metadata = dict(source_sha=source, repository=os.environ["GITHUB_REPOSITORY"], head_repository=os.environ["CHECKS_HEAD_REPOSITORY"], event=event, head_branch=os.environ["CHECKS_HEAD_BRANCH"], checks_run_id=run_id)
        if os.environ.get("GITHUB_ACTIONS") == "true":
            metadata["checks_run_attempt"] = int(os.environ["GITHUB_RUN_ATTEMPT"])
            if metadata["checks_run_attempt"] <= 0:
                raise ValueError("invalid Checks attempt")
        Path("checks-metadata.json").write_text(json.dumps(metadata) + "\n", encoding="utf-8")
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError) as error:
        print(f"ci-checks: {error}", file=sys.stderr)
        sys.exit(1)

# Read-only release eligibility; only the requested JSON output is written.
release-candidate output:
    #!/usr/bin/env python3
    import json
    import re
    import subprocess
    import sys
    import tomllib
    from pathlib import Path

    def git(*args):
        return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout.strip()

    def version(value):
        match = re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?", value)
        if not match:
            raise ValueError(f"invalid SemVer: {value!r}")
        prerelease = match[4]
        identifiers = []
        for part in prerelease.split(".") if prerelease else []:
            if part.isdigit():
                if len(part) > 1 and part.startswith("0"):
                    raise ValueError(f"invalid SemVer prerelease: {value!r}")
                identifiers.append((0, int(part)))
            else:
                identifiers.append((1, part))
        return (*map(int, match.group(1, 2, 3)), prerelease is None, tuple(identifiers))

    def decide():
        source = git("rev-parse", "HEAD")
        tags = []
        for tag in git("tag", "--merged", source).splitlines():
            if tag.startswith("v"):
                try:
                    tags.append((version(tag[1:]), tag))
                except ValueError:
                    pass
        last_tag = max(tags)[1] if tags else ""
        result = dict(eligible=False, source_sha=source, last_tag=last_tag, version="", reason="no-feat-fix")
        history = f"{last_tag}..{source}" if last_tag else source
        subjects = git("log", "--no-merges", "--format=%s", history).splitlines()
        if not any(re.match(r"^(feat|fix)(\([^()\r\n]+\))?!?: .+", subject) for subject in subjects):
            return result
        with open(".cz.toml", "rb") as config_file:
            configuration = tomllib.load(config_file)
        standard = dict(name="cz_conventional_commits", version_provider="commitizen", version_scheme="semver2", tag_format="v$version", major_version_zero=True, update_changelog_on_bump=True, changelog_file="CHANGELOG.md", bump_message="chore(release): bump version $current_version → $new_version")
        settings = configuration["tool"]["commitizen"]
        configured = settings["version"]
        if configuration != {"tool": {"commitizen": dict(standard, version=configured)}} or Path(".cz.toml").is_symlink():
            raise ValueError("only standard Commitizen configuration is allowed")
        baseline = version(last_tag[1:] if last_tag else configured)
        for option in ("--dry-run", "--get-next"):
            process = subprocess.run(["cz", "--config", ".cz.toml", "bump", option, "--yes"], capture_output=True, text=True)
            if process.returncode == 21:
                result["reason"] = "no-increment"
                return result
            if process.returncode != 0:
                raise RuntimeError(f"cz bump {option} failed ({process.returncode}): {process.stdout}{process.stderr}")
        next_version = process.stdout.strip()
        proposed = version(next_version)
        if proposed < baseline:
            raise ValueError("Commitizen proposed a decreased version")
        if proposed == baseline:
            result["reason"] = "no-increment"
            return result
        result.update(eligible=True, version=next_version, reason="eligible")
        return result

    try:
        decision = decide()
        Path(sys.argv[1]).write_text(json.dumps(decision) + "\n", encoding="utf-8")
    except (OSError, ValueError, KeyError, subprocess.CalledProcessError, RuntimeError) as error:
        print(f"release-candidate: {error}", file=sys.stderr)
        sys.exit(1)

# Uses already-fetched refs. Fetching and publication belong to the caller.
release-preflight source_sha last_tag output:
    #!/usr/bin/env python3
    import json
    import re
    import subprocess
    import sys
    from pathlib import Path

    def git(*args):
        return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout.strip()

    try:
        current = git("rev-parse", "refs/remotes/origin/master^{commit}")
        tags = []
        for tag in git("tag", "--merged", current).splitlines():
            match = re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?", tag)
            if not match:
                continue
            prerelease = match[4]
            parts = prerelease.split(".") if prerelease else []
            if any(part.isdigit() and len(part) > 1 and part.startswith("0") for part in parts):
                continue
            identifiers = tuple((0, int(part)) if part.isdigit() else (1, part) for part in parts)
            key = (*map(int, match.group(1, 2, 3)), prerelease is None, identifiers)
            tags.append((key, tag))
        latest = max(tags)[1] if tags else ""
        reason = "stale-source" if current != sys.argv[1] else "stale-tag" if latest != sys.argv[2] else "current"
        Path(sys.argv[3]).write_text(json.dumps(dict(publish=reason == "current", reason=reason)) + "\n", encoding="utf-8")
    except (OSError, subprocess.CalledProcessError) as error:
        print(f"release-preflight: {error}", file=sys.stderr)
        sys.exit(1)

# Public bot identity needs no installation token or private key.
release-bot-identity output:
    #!/usr/bin/env python3
    import json
    import os
    import re
    import subprocess
    import sys
    from pathlib import Path

    try:
        slug = os.environ.get("RELEASE_BOT_SLUG", "")
        identity = {}
        if slug:
            if not re.fullmatch(r"[A-Za-z0-9-]+", slug):
                raise ValueError("invalid App slug")
            user = json.loads(subprocess.check_output(["gh", "api", "users/" + slug + "%5Bbot%5D"]))
            if type(user["id"]) is not int or user["id"] <= 0 or user["login"] != slug + "[bot]":
                raise ValueError("invalid App bot identity")
            identity = dict(RELEASE_BOT_NAME=slug + "[bot]", RELEASE_BOT_EMAIL=str(user["id"]) + "+" + slug + "[bot]@users.noreply.github.com")
        Path(sys.argv[1]).write_text(json.dumps(identity) + "\n")
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError):
        print("release-bot-identity: could not resolve public App identity", file=sys.stderr)
        sys.exit(1)

release-own-bump source_sha output:
    #!/usr/bin/env python3
    import json
    import os
    import re
    import subprocess
    import sys
    import tomllib
    from pathlib import Path

    def git(*args):
        return subprocess.run(["git", "-c", "core.hooksPath=/dev/null", *args], check=True, capture_output=True, text=True).stdout.rstrip("\n")

    def own(source):
        if not re.fullmatch(r"[0-9a-f]{40}", source):
            raise ValueError("invalid source SHA")
        name, email = os.environ.get("RELEASE_BOT_NAME", ""), os.environ.get("RELEASE_BOT_EMAIL", "")
        if not name or not email:
            return False
        parents = git("show", "-s", "--format=%P", source).split()
        if len(parents) != 1:
            return False
        parent = parents[0]
        if git("show", "-s", "--format=%an%n%ae%n%cn%n%ce", source).splitlines() != [name, email, name, email]:
            return False
        if git("diff", "--name-only", parent, source) != ".cz.toml\nCHANGELOG.md":
            return False
        for ref in (parent, source):
            for file in (".cz.toml", "CHANGELOG.md"):
                entry = git("ls-tree", ref, "--", file)
                if entry and not entry.startswith("100644 blob "):
                    return False
        old, new = (git("show", ref + ":.cz.toml") for ref in (parent, source))
        before, after = (tomllib.loads(value)["tool"]["commitizen"]["version"] for value in (old, new))
        standard = dict(name="cz_conventional_commits", version_provider="commitizen", version_scheme="semver2", tag_format="v$version", major_version_zero=True, update_changelog_on_bump=True, changelog_file="CHANGELOG.md", bump_message="chore(release): bump version $current_version → $new_version")
        if any(tomllib.loads(value) != {"tool": {"commitizen": dict(standard, version=version)}} for value, version in ((old, before), (new, after))):
            return False
        semver = r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?"
        if before == after or not re.fullmatch(semver, before) or not re.fullmatch(semver, after):
            return False
        def version_key(value):
            core, _, prerelease = value.split("+", 1)[0].partition("-")
            parts = prerelease.split(".") if prerelease else []
            if any(part.isdigit() and len(part) > 1 and part.startswith("0") for part in parts):
                raise ValueError("invalid prerelease")
            return (*map(int, core.split(".")), not prerelease, tuple((0, int(part)) if part.isdigit() else (1, part) for part in parts))
        if version_key(after) <= version_key(before):
            return False
        expected, count = re.subn(r'(?m)^version = "' + re.escape(before) + r'"$', 'version = "' + after + '"', old)
        if count != 1 or new != expected:
            return False
        return git("show", "-s", "--format=%B", source) == f"chore(release): bump version {before} → {after}"

    try:
        Path(sys.argv[2]).write_text(json.dumps(dict(own_bump=own(sys.argv[1]))) + "\n")
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"release-own-bump: {error}", file=sys.stderr)
        sys.exit(1)

# Prevalidate without App credentials; publish in a fresh, hook-disabled clone.
ci-release provenance asset_dir output:
    #!/usr/bin/env python3
    import hashlib
    import json
    import os
    import re
    import subprocess
    import sys
    import tempfile
    import tomllib
    from pathlib import Path
    from urllib.parse import quote

    def require(condition, message):
        if not condition:
            raise ValueError(message)

    def command(args, cwd=None, env=None):
        process = subprocess.run(args, cwd=cwd, env=env, capture_output=True)
        if process.returncode:
            operation = next((arg for arg in args[1:] if arg in ("fetch", "push", "clone", "reset", "show", "rev-parse", "tag")), "command")
            raise ValueError("tool failed: " + args[0] + " " + operation + " (exit " + str(process.returncode) + ")")
        return process.stdout

    def git(*args):
        return command(["git", "-c", "core.hooksPath=/dev/null", *args], cwd=work, env=environment).decode().strip()

    def recipe(name, *args):
        path = scratch / (name + ".json")
        command(["just", "--justfile", str(trusted), "--working-directory", str(work), name, *args, str(path)], env=environment)
        return json.loads(path.read_text())

    def result(published, reason, ready=False):
        Path(output).write_text(json.dumps(dict(published=published, reason=reason, ready=ready)) + "\n")
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a") as stream:
                stream.write(f"ready={'true' if ready else 'false'}\n")

    def api(endpoint, *args, missing=False, raw=False):
        process = subprocess.run(["gh", "api", endpoint, *args], env=environment, capture_output=True)
        if missing and process.returncode and b"404" in process.stderr:
            return None
        require(process.returncode == 0, "GitHub release API request failed")
        return process.stdout if raw else json.loads(process.stdout)

    def refresh():
        git("fetch", "origin", "refs/heads/master:refs/remotes/origin/master", "refs/tags/*:refs/tags/*")

    def api_list(endpoint):
        pages = api(endpoint + "?per_page=100", "--paginate", raw=True).decode()
        decoder, records = json.JSONDecoder(), []
        while pages.strip():
            page, end = decoder.raw_decode(pages.lstrip())
            require(isinstance(page, list), "invalid paginated API response")
            records.extend(page)
            pages = pages.lstrip()[end:]
        return records

    try:
        provenance, directory, output = sys.argv[1:]
        Path(output).unlink(missing_ok=True)
        metadata = json.loads(Path(provenance).read_text())
        repository = os.environ["GITHUB_REPOSITORY"]
        require(re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository), "invalid repository")
        require(metadata["verified"] is True and metadata["repository"] == repository and metadata["head_repository"] == repository and metadata["event"] == "push" and metadata["head_branch"] == "master", "ineligible provenance")
        source, version, last_tag = (metadata[key] for key in ("source_sha", "version", "last_tag"))
        require(re.fullmatch(r"[0-9a-f]{40}", source), "invalid source")
        for key in ("checks_run_id", "build_run_id"):
            require(type(metadata[key]) is int and metadata[key] > 0, "invalid run")
        require(type(metadata["eligible"]) is bool and metadata["artifact_class"] == ("release" if metadata["eligible"] else "dev"), "invalid eligibility")
        trusted = Path(os.environ["CI_JUSTFILE"]).resolve(strict=True)
        assets = Path(directory).resolve(strict=True)
        # Never pass App credentials to Commitizen or to nested tooling.
        environment = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null", GIT_TERMINAL_PROMPT="0", PYTHONPATH="", PYTHONSAFEPATH="1")
        for key in ("GH_TOKEN", "GITHUB_TOKEN", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"):
            environment.pop(key, None)
        with tempfile.TemporaryDirectory(prefix="mcp-relayd-release-") as temporary:
            scratch = Path(temporary)
            work = Path.cwd()
            manifest = recipe("release-verify", str(assets))
            for key in ("repository", "head_repository", "event", "head_branch", "checks_run_id", "build_run_id", "source_sha", "version", "eligible", "artifact_class", "last_tag"):
                require(type(manifest[key]) is type(metadata[key]) and manifest[key] == metadata[key], "manifest mismatch: " + key)
            for key in ("checks_run_attempt", "trigger_checks_run_id", "build_run_attempt", "build_head_sha"):
                if key in metadata:
                    require(type(manifest[key]) is type(metadata[key]) and manifest[key] == metadata[key], "manifest mismatch: " + key)
            require(manifest["run_id"] == str(metadata["build_run_id"]), "wrong artifact run")
            if not metadata["eligible"]:
                require(metadata.get("reason") in ("no-feat-fix", "no-increment", "own-bump"), "invalid skip reason")
                result(False, metadata["reason"])
                sys.exit(0)
            require(re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?", version), "invalid version")
            require(last_tag == "" or re.fullmatch(r"v[0-9A-Za-z.+-]+", last_tag), "invalid last tag")
            remote = command(["git", "remote", "get-url", "origin"]).decode().strip()
            if os.environ.get("GITHUB_ACTIONS") == "true":
                require(remote == f"https://github.com/{repository}.git", "foreign Git remote")
            # Read-only token for private-repository fetches, held in environment only.
            read_token = os.environ.get("RELEASE_READ_TOKEN")
            helper = '!f() { test "$1" = get && printf "username=x-access-token\\npassword=%s\\n" "$RELEASE_GIT_TOKEN"; }; f'
            if read_token:
                environment.update(RELEASE_GIT_TOKEN=read_token, GIT_CONFIG_COUNT="2", GIT_CONFIG_KEY_0="credential.helper", GIT_CONFIG_VALUE_0="", GIT_CONFIG_KEY_1="credential.helper", GIT_CONFIG_VALUE_1=helper)
            work = scratch / "source"
            command(["git", "-c", "core.hooksPath=/dev/null", "clone", "--no-checkout", "--no-local", remote, str(work)], env=environment)
            refresh()
            remote_tags = git("ls-remote", "--tags", "origin")
            tag = "v" + version
            existing = git("tag", "--list", tag)
            bump = git("rev-parse", tag + "^{commit}") if existing else ""
            current = git("rev-parse", "refs/remotes/origin/master")
            if existing:
                require(git("show", "-s", "--format=%P", bump) == source and recipe("release-own-bump", bump)["own_bump"], "conflicting release tag")
                require(tomllib.loads(git("show", bump + ":.cz.toml"))["tool"]["commitizen"]["version"] == version, "conflicting bump version")
                require(current == bump, "master differs from recovered bump")
                git("tag", "-d", tag)
            elif current != source:
                result(False, "stale-source")
                sys.exit(0)
            git("reset", "--hard", source)
            for file in (".cz.toml", "CHANGELOG.md"):
                entry = git("ls-tree", source, "--", file)
                if entry:
                    require(entry.startswith("100644 blob "), "unsafe version file")
                    (work / file).write_bytes(command(["git", "show", source + ":" + file], cwd=work, env=environment))
            candidate = recipe("release-candidate")
            if candidate["last_tag"] != last_tag:
                result(False, "stale-tag")
                sys.exit(0)
            require(candidate["eligible"] and candidate["version"] == version and candidate["source_sha"] == source, "candidate differs from Build")
            if bump:
                refresh()
                require(git("rev-parse", "refs/remotes/origin/master") == bump and git("rev-parse", tag + "^{commit}") == bump and git("ls-remote", "--tags", "origin") == remote_tags, "recovered refs changed")
            if os.environ.get("RELEASE_PREFLIGHT_ONLY") == "1":
                result(False, "current", True)
                sys.exit(0)
            require(os.environ.get("GH_TOKEN") and environment.get("RELEASE_BOT_NAME") and environment.get("RELEASE_BOT_EMAIL"), "eligible publication requires App token and identity")
            if not bump:
                refresh()
                preflight = recipe("release-preflight", source, last_tag)
                if not preflight["publish"]:
                    result(False, preflight["reason"])
                    sys.exit(0)
                environment.update(GIT_AUTHOR_NAME=environment["RELEASE_BOT_NAME"], GIT_AUTHOR_EMAIL=environment["RELEASE_BOT_EMAIL"], GIT_COMMITTER_NAME=environment["RELEASE_BOT_NAME"], GIT_COMMITTER_EMAIL=environment["RELEASE_BOT_EMAIL"])
                environment.pop("RELEASE_GIT_TOKEN", None)
                environment.pop("RELEASE_READ_TOKEN", None)
                # Prevent source hooks; standard configuration was validated above.
                environment.update(GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="core.hooksPath", GIT_CONFIG_VALUE_0="/dev/null")
                command(["cz", "--config", ".cz.toml", "bump", "--yes"], cwd=work, env=environment)
                bump = git("rev-parse", "HEAD")
                require(git("show", "-s", "--format=%P", bump) == source and recipe("release-own-bump", bump)["own_bump"], "unsafe generated bump")
                require(tomllib.loads(git("show", bump + ":.cz.toml"))["tool"]["commitizen"]["version"] == version, "generated version differs from Build")
                require(git("rev-parse", tag + "^{commit}") == bump, "wrong generated tag")
                # Re-fetch immediately before the non-force atomic push.
                environment.update(RELEASE_GIT_TOKEN=os.environ["GH_TOKEN"], GIT_CONFIG_COUNT="3", GIT_CONFIG_KEY_0="core.hooksPath", GIT_CONFIG_VALUE_0="/dev/null", GIT_CONFIG_KEY_1="credential.helper", GIT_CONFIG_VALUE_1="", GIT_CONFIG_KEY_2="credential.helper", GIT_CONFIG_VALUE_2=helper)
                refresh()
                require(git("rev-parse", "refs/remotes/origin/master") == source, "master changed before push")
                require(git("ls-remote", "--tags", "origin") == remote_tags, "tags changed before push")
                git("push", "--atomic", "origin", bump + ":refs/heads/master", "refs/tags/" + tag + ":refs/tags/" + tag)
            refresh()
            require(git("rev-parse", "refs/remotes/origin/master") == bump and git("rev-parse", tag + "^{commit}") == bump, "published refs changed")
            environment["GH_TOKEN"] = os.environ["GH_TOKEN"]
            endpoint = f"repos/{repository}/releases"
            # The tag endpoint does not reliably return drafts; production also
            # resolves drafts through the authenticated releases listing below.
            release = api(endpoint + "/tags/" + quote(tag, safe=""), missing=True)
            if release is None:
                matches = [item for item in api_list(endpoint) if item["tag_name"] == tag]
                require(len(matches) <= 1, "duplicate releases")
                release = matches[0] if matches else None
            notes = git("show", bump + ":CHANGELOG.md")
            section = re.search(r"(?ms)^## v?" + re.escape(version) + r"(?:[ \t][^\n]*)?\n.*?(?=^## |\Z)", notes)
            require(section is not None, "missing incremental changelog")
            notes = section[0].strip()
            if release is None:
                release = api(endpoint, "-X", "POST", "-f", "tag_name=" + tag, "-f", "target_commitish=" + bump, "-F", "draft=true", "-f", "body=" + notes)
            require(release["tag_name"] == tag and type(release["id"]) is int and release["id"] > 0 and type(release["draft"]) is bool, "conflicting release")
            require(release.get("target_commitish", bump) == bump, "conflicting release target")
            release_endpoint = endpoint + "/" + str(release["id"])
            files = {asset["name"]: assets / asset["name"] for asset in manifest["assets"]}
            sums = scratch / "SHA256SUMS"
            sums.write_text("".join(asset["sha256"] + "  " + asset["name"] + "\n" for asset in sorted(manifest["assets"], key=lambda item: item["name"])))
            files["SHA256SUMS"] = sums

            def verify_remote():
                records = api_list(release_endpoint + "/assets")
                seen = set()
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
                api(f"https://uploads.github.com/{release_endpoint}/assets?name=" + quote(name, safe=""), "-X", "POST", "-H", "Content-Type: application/octet-stream", "--input", str(files[name]))
            require(verify_remote() == set(files), "incomplete uploaded assets")
            if release["draft"]:
                api(release_endpoint, "-X", "PATCH", "-F", "draft=false")
            result(True, "published")
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        # Never print captured command output/arguments (may contain credentials).
        print("ci-release: " + (str(error) if not isinstance(error, subprocess.CalledProcessError) else "tool command failed"), file=sys.stderr)
        sys.exit(1)

release-package version source_sha run_id asset_dir output:
    #!/usr/bin/env python3
    import gzip
    import hashlib
    import io
    import json
    import os
    import re
    import subprocess
    import sys
    import tarfile
    import tempfile
    import zipfile
    from pathlib import Path

    try:
        version, source, run, directory, output = sys.argv[1:]
        for label, value in (("version", version), ("source_sha", source), ("run_id", run)):
            if not re.fullmatch(r"[0-9A-Za-z][0-9A-Za-z.+_-]*", value):
                raise ValueError(f"invalid {label}")
        destination = Path(directory).resolve()
        destination.mkdir(parents=True, exist_ok=True)
        # Stage all six builds before writing the manifest (the completion marker).
        manifest_path = destination / "manifest.json"
        manifest_path.unlink(missing_ok=True)
        assets = []
        with tempfile.TemporaryDirectory(prefix="mcp-relayd-package-") as temporary:
            stage = Path(temporary)
            for goos in ("linux", "darwin", "windows"):
                for goarch in ("amd64", "arm64"):
                    executable = "mcp-relayd.exe" if goos == "windows" else "mcp-relayd"
                    binary = stage / executable
                    environment = dict(os.environ, GOOS=goos, GOARCH=goarch, CGO_ENABLED="0")
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
        Path(output).write_text(json.dumps(manifest) + "\n", encoding="utf-8")
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(f"release-package: {error}", file=sys.stderr)
        sys.exit(1)

# Treat downloaded artifacts as untrusted: exact names, regular files, no extraction.
release-verify asset_dir output:
    #!/usr/bin/env python3
    import hashlib
    import json
    import re
    import stat
    import sys
    import tarfile
    import zipfile
    from pathlib import Path

    def regular(path):
        if not stat.S_ISREG(path.lstat().st_mode):
            raise ValueError(f"not a regular file: {path.name}")

    try:
        directory = Path(sys.argv[1]).resolve()
        manifest_path = directory / "manifest.json"
        regular(manifest_path)
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        for field in ("version", "source_sha", "run_id"):
            if not isinstance(manifest[field], str) or not re.fullmatch(r"[0-9A-Za-z][0-9A-Za-z.+_-]*", manifest[field]):
                raise ValueError(f"invalid {field}")
        version = manifest["version"]
        expected = {f"mcp-relayd_{version}_{goos}_{goarch}.{'zip' if goos == 'windows' else 'tar.gz'}" for goos in ("linux", "darwin", "windows") for goarch in ("amd64", "arm64")}
        assets = manifest["assets"]
        if not isinstance(assets, list) or len(assets) != 6:
            raise ValueError("manifest must contain six assets")
        seen = set()
        for asset in assets:
            name, digest = asset["name"], asset["sha256"]
            if not isinstance(name, str) or name not in expected or name in seen:
                raise ValueError("unexpected or duplicate asset name")
            seen.add(name)
            if not isinstance(digest, str) or not re.fullmatch(r"[0-9a-f]{64}", digest):
                raise ValueError("invalid SHA-256")
            path = directory / name
            regular(path)
            if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
                raise ValueError(f"checksum mismatch: {name}")
            if name.endswith(".zip"):
                with zipfile.ZipFile(path) as package:
                    members = package.infolist()
                    if len(members) != 1 or members[0].filename != "mcp-relayd.exe" or not stat.S_ISREG(members[0].external_attr >> 16) or members[0].file_size == 0:
                        raise ValueError(f"unsafe zip member: {name}")
                    if package.testzip() is not None:
                        raise ValueError(f"invalid zip content: {name}")
            else:
                with tarfile.open(path, "r:gz") as package:
                    members = package.getmembers()
                    if len(members) != 1 or members[0].name != "mcp-relayd" or not members[0].isreg() or members[0].size == 0 or members[0].mode != 0o755:
                        raise ValueError(f"unsafe tar member: {name}")
        Path(sys.argv[2]).write_text(json.dumps(dict(manifest, verified=True)) + "\n", encoding="utf-8")
    except (OSError, ValueError, KeyError, TypeError, tarfile.TarError, zipfile.BadZipFile, EOFError) as error:
        print(f"release-verify: {error}", file=sys.stderr)
        sys.exit(1)
