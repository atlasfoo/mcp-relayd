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

    def run(record, run_id, path, repository, completed=True):
        require(type(run_id) is int and run_id > 0, "invalid run ID")
        require(record["id"] == run_id and type(record["id"]) is int, "wrong run")
        require(record["repository"]["full_name"] == repository, "foreign run")
        require(record["path"] == path, "wrong workflow")
        if completed:
            require(record["status"] == "completed" and record["conclusion"] == "success", "unsuccessful run")
        else:
            require((record["status"] == "in_progress" and record["conclusion"] is None) or (record["status"] == "completed" and record["conclusion"] == "success"), "unsuccessful caller")

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
            run(snapshot["build"], metadata["build_run_id"], ".github/workflows/build.yml", repository, completed=False)
            require(trigger == snapshot["build"]["id"] and snapshot["build"]["event"] == "workflow_run", "wrong Build trigger")
            require(snapshot["build"]["head_branch"] == "master" and snapshot["build"]["head_repository"]["full_name"] == repository, "foreign Build head")
            producer = snapshot["producer"]
            attempt = metadata["build_run_attempt"]
            require(type(attempt) is int and 0 < attempt <= snapshot["build"]["run_attempt"], "invalid producer attempt")
            require(producer["name"] == "build" and producer["run_id"] == trigger and producer["run_attempt"] == attempt and producer["head_sha"] == snapshot["build"]["head_sha"] and producer["status"] == "completed" and producer["conclusion"] == "success", "unsuccessful exact producer")
            require(snapshot["caller_workflow"] == repository + "/.github/workflows/build.yml@refs/heads/master" and snapshot["caller_sha"] == snapshot["build"]["head_sha"] and snapshot["caller_event"] == "workflow_run", "foreign caller")
            if snapshot.get("require_chain_binding") is True:
                require(type(metadata["trigger_checks_run_id"]) is int and metadata["trigger_checks_run_id"] == checks["id"], "wrong recorded Checks trigger")
                require(type(snapshot["caller_checks_run_id"]) is int and snapshot["caller_checks_run_id"] == checks["id"], "wrong caller Checks trigger")
                require(type(metadata["checks_run_attempt"]) is int and metadata["checks_run_attempt"] == checks["run_attempt"], "wrong Checks attempt")
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
            for key in ("version", "last_tag", "reason", "bump_sha", "tag", "bundle_sha256"):
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
workflow-handoff asset_dir producer_attempt artifact_id output:
    #!/usr/bin/env python3
    import json
    import os
    import subprocess
    import sys
    from pathlib import Path

    def api(endpoint, *args):
        return json.loads(subprocess.check_output(["gh", "api", endpoint, *args]))

    try:
        directory, attempt, artifact_id, output = sys.argv[1:]
        attempt, artifact_id = int(attempt), int(artifact_id)
        repository, run_id = os.environ["GITHUB_REPOSITORY"], int(os.environ["GITHUB_RUN_ID"])
        metadata = json.loads((Path(directory) / "build-metadata.json").read_text())
        caller_checks = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())["workflow_run"]["id"]
        if attempt <= 0 or artifact_id <= 0 or metadata["build_run_attempt"] != attempt or metadata["build_run_id"] != run_id:
            raise ValueError("wrong producer inputs")
        endpoint = f"repos/{repository}/actions"
        build = api(f"{endpoint}/runs/{run_id}")
        pages = api(f"{endpoint}/runs/{run_id}/attempts/{attempt}/jobs?per_page=100", "--paginate", "--slurp")
        producers = [job for page in pages for job in page["jobs"] if job["name"] == "build"]
        if len(producers) != 1:
            raise ValueError("ambiguous producer")
        artifact = api(f"{endpoint}/artifacts/{artifact_id}")
        expected = f"release-{run_id}-{attempt}"
        if artifact["id"] != artifact_id or artifact["name"] != expected or artifact["expired"] is not False or artifact["workflow_run"]["id"] != run_id or artifact["workflow_run"]["head_sha"] != build["head_sha"]:
            raise ValueError("foreign artifact")
        checks = api(f"{endpoint}/runs/{metadata['checks_run_id']}")
        snapshot = dict(repository=repository, trigger_run_id=run_id, checks=checks, build=build, producer=producers[0], metadata=metadata, require_chain_binding=True, caller_workflow=os.environ["GITHUB_WORKFLOW_REF"], caller_sha=os.environ["GITHUB_SHA"], caller_event=os.environ["GITHUB_EVENT_NAME"], caller_checks_run_id=caller_checks)
        Path(output).write_text(json.dumps(snapshot) + "\n")
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError):
        print("workflow-handoff: invalid producer/artifact/caller", file=sys.stderr)
        sys.exit(1)

ci-build provenance asset_dir output:
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

    def git(*args, cwd=None):
        return subprocess.run(["git", "-c", "core.hooksPath=/dev/null", *args], cwd=cwd, check=True, capture_output=True, text=True).stdout.strip()

    def skip(reason):
        result = dict(metadata, build_run_id=run_id, eligible=False, skipped=True, reason=reason)
        print(json.dumps(result))
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as stream:
                stream.write("eligible=false\nskipped=true\n")
        sys.exit(0)

    try:
        provenance, directory, output = sys.argv[1:]
        Path(output).unlink(missing_ok=True)
        metadata = json.loads(Path(provenance).read_text(encoding="utf-8"))
        source = metadata["source_sha"]
        head = subprocess.run(["git", "rev-parse", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()
        if metadata["verified"] is not True or not re.fullmatch(r"[0-9a-f]{40}", source) or head != source:
            raise ValueError("source checkout differs from verified Checks SHA")
        subprocess.run(["git", "diff", "--quiet", "HEAD", "--"], check=True)
        if type(metadata["checks_run_id"]) is not int or metadata["checks_run_id"] <= 0:
            raise ValueError("invalid Checks run ID")
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
            if not origin:
                skip("development-origin")
            subprocess.run([*command, "release-bot-identity", str(scratch / "identity.json")], check=True)
            os.environ.update(json.loads((scratch / "identity.json").read_text()))
            for key in ("GH_TOKEN", "GITHUB_TOKEN", "RELEASE_READ_TOKEN", "RELEASE_GIT_TOKEN", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"):
                os.environ.pop(key, None)
            os.environ.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null", GIT_TERMINAL_PROMPT="0", GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="core.hooksPath", GIT_CONFIG_VALUE_0="/dev/null", PYTHONPATH="", PYTHONSAFEPATH="1")
            subprocess.run([*command, "release-own-bump", source, str(scratch / "own.json")], check=True)
            if json.loads((scratch / "own.json").read_text())["own_bump"]:
                skip("own-bump")
            remote = subprocess.run(["git", "rev-parse", "--verify", "refs/remotes/origin/master^{commit}"], capture_output=True, text=True)
            current = remote.stdout.strip() if remote.returncode == 0 else source
            bump = ""
            if current != source:
                if git("show", "-s", "--format=%P", current) != source:
                    skip("stale-source")
                subprocess.run([*command, "release-own-bump", current, str(scratch / "own.json")], check=True)
                if not json.loads((scratch / "own.json").read_text())["own_bump"]:
                    skip("stale-source")
                version = tomllib.loads(git("show", current + ":.cz.toml"))["tool"]["commitizen"]["version"]
                tag = "v" + version
                if git("rev-parse", tag + "^{commit}") != current:
                    raise ValueError("conflicting recovered tag")
                bump = current
                # Fresh Checks provenance has no baseline. Recover it from the
                # tested source's tags, excluding the child bump's new tag.
                baseline_tags = []
                for baseline in git("tag", "--merged", source).splitlines():
                    match = re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?", baseline)
                    if not match:
                        continue
                    parts = match[4].split(".") if match[4] else []
                    if any(part.isdigit() and len(part) > 1 and part.startswith("0") for part in parts):
                        continue
                    key = (*map(int, match.group(1, 2, 3)), match[4] is None, tuple((0, int(part)) if part.isdigit() else (1, part) for part in parts))
                    baseline_tags.append((key, baseline))
                last_tag = max(baseline_tags)[1] if baseline_tags else ""
                if "last_tag" in metadata and metadata["last_tag"] != last_tag:
                    skip("stale-tag")
                candidate.update(eligible=True, version=version, reason="recovered-bump", last_tag=last_tag)
            else:
                subprocess.run([*command, "release-candidate", str(scratch / "candidate.json")], check=True)
                candidate = json.loads((scratch / "candidate.json").read_text(encoding="utf-8"))
                if candidate["source_sha"] != source:
                    raise ValueError("candidate source mismatch")
                if "last_tag" in metadata and candidate["last_tag"] != metadata["last_tag"]:
                    skip("stale-tag")
                if not candidate["eligible"]:
                    skip(candidate["reason"])
                if not os.environ.get("RELEASE_BOT_NAME") or not os.environ.get("RELEASE_BOT_EMAIL"):
                    raise ValueError("eligible Build requires trusted bot identity")
                # No candidate hooks/configuration or credentials reach Commitizen.
                environment = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null", GIT_TERMINAL_PROMPT="0", PYTHONPATH="", PYTHONSAFEPATH="1")
                for key in ("GH_TOKEN", "GITHUB_TOKEN", "RELEASE_READ_TOKEN", "RELEASE_GIT_TOKEN", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"):
                    environment.pop(key, None)
                environment.update(GIT_AUTHOR_NAME=environment["RELEASE_BOT_NAME"], GIT_AUTHOR_EMAIL=environment["RELEASE_BOT_EMAIL"], GIT_COMMITTER_NAME=environment["RELEASE_BOT_NAME"], GIT_COMMITTER_EMAIL=environment["RELEASE_BOT_EMAIL"], GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="core.hooksPath", GIT_CONFIG_VALUE_0="/dev/null")
                work = scratch / "bump"
                subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "clone", "--no-local", "--no-checkout", str(Path.cwd()), str(work)], check=True, env=environment, capture_output=True)
                git("checkout", "--detach", source, cwd=work)
                subprocess.run(["cz", "--config", ".cz.toml", "bump", "--yes"], cwd=work, env=environment, check=True)
                bump = git("rev-parse", "HEAD", cwd=work)
                tag = "v" + candidate["version"]
                subprocess.run(["just", "--justfile", str(trusted), "--working-directory", str(work), "release-own-bump", bump, str(scratch / "generated.json")], env=environment, check=True)
                if git("show", "-s", "--format=%P", bump, cwd=work) != source or not json.loads((scratch / "generated.json").read_text())["own_bump"] or git("rev-parse", tag + "^{commit}", cwd=work) != bump or tomllib.loads(git("show", bump + ":.cz.toml", cwd=work))["tool"]["commitizen"]["version"] != candidate["version"]:
                    raise ValueError("unsafe generated bump/tag")
                # Import local objects for the later privileged push, never publish here.
                git("fetch", str(work), "refs/tags/" + tag + ":refs/tags/" + tag)
            eligible = candidate["eligible"]
            version = candidate["version"] if eligible else f"0.0.0-dev.{run_id}+{source[:12]}"
            destination = Path(directory).resolve()
            destination.mkdir(parents=True, exist_ok=True)
            (destination / "build-metadata.json").unlink(missing_ok=True)
            subprocess.run([*command, "release-package", version, source, str(run_id), str(destination), str(scratch / "package.json")], check=True)
            subprocess.run([*command, "release-verify", str(destination), str(scratch / "verified.json")], check=True)
            manifest = json.loads((scratch / "verified.json").read_text(encoding="utf-8"))
            git("bundle", "create", str(destination / "bump.bundle"), "refs/tags/" + tag, "^" + source)
            metadata["bundle_sha256"] = hashlib.sha256((destination / "bump.bundle").read_bytes()).hexdigest()
        metadata.update(build_run_id=run_id, eligible=eligible, skipped=False, version=version, bump_sha=bump, tag=tag, artifact_class="release", last_tag=candidate["last_tag"], reason=candidate["reason"])
        # Keep full chain metadata in the manifest as well as the standalone artifact.
        manifest.update(metadata)
        (destination / "manifest.json").write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n", encoding="utf-8")
        (destination / "build-metadata.json").write_text(json.dumps(metadata) + "\n", encoding="utf-8")
        Path(output).write_text(json.dumps(metadata) + "\n", encoding="utf-8")
        if "GITHUB_OUTPUT" in os.environ:
            with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as step_output:
                step_output.write(f"artifact_class={metadata['artifact_class']}\n")
                step_output.write(f"eligible=true\nskipped=false\nsource_sha={source}\nbump_sha={bump}\ntag={tag}\nversion={version}\n")
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"ci-build: {error}", file=sys.stderr)
        sys.exit(1)

# Import inert Git objects in a fresh bare repository; never checkout candidate code.
ci-push provenance asset_dir output:
    #!/usr/bin/env python3
    import hashlib
    import json
    import os
    import re
    import stat
    import subprocess
    import sys
    import tempfile
    import tomllib
    from pathlib import Path

    def require(condition, message):
        if not condition:
            raise ValueError(message)

    def command(args, env=None):
        process = subprocess.run(args, cwd=work, env=env or environment, capture_output=True)
        operation = next((arg for arg in args[1:] if arg in ("fetch", "push", "clone", "init", "bundle", "release-verify", "release-own-bump", "release-bot-identity", "release-preflight")), "command")
        require(process.returncode == 0, "trusted tool failed: " + args[0] + " " + operation)
        return process.stdout.decode().strip()

    def git(*args, env=None):
        return command(["git", "-c", "core.hooksPath=/dev/null", *args], env)

    def recipe(name, *args, env=None):
        path = scratch / (name + ".json")
        command(["just", "--justfile", str(trusted), "--working-directory", str(work), name, *args, str(path)], env)
        return json.loads(path.read_text())

    def result(ready, pushed, reason):
        Path(output).write_text(json.dumps(dict(ready=ready, pushed=pushed, reason=reason)) + "\n")
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a") as stream:
                stream.write(f"ready={str(ready).lower()}\npushed={str(pushed).lower()}\n")

    try:
        provenance, directory, output = sys.argv[1:]
        Path(output).unlink(missing_ok=True)
        metadata = json.loads(Path(provenance).read_text())
        repository = os.environ["GITHUB_REPOSITORY"]
        require(re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository), "invalid repository")
        require(metadata["verified"] is True and metadata["repository"] == repository and metadata["head_repository"] == repository and metadata["event"] == "push" and metadata["head_branch"] == "master" and metadata["eligible"] is True, "ineligible provenance")
        source, bump, tag, version = (metadata[key] for key in ("source_sha", "bump_sha", "tag", "version"))
        require(all(re.fullmatch(r"[0-9a-f]{40}", value) for value in (source, bump)) and re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?", tag) and tag == "v" + version, "invalid bump/tag")
        trusted = Path(os.environ["CI_JUSTFILE"]).resolve(strict=True)
        assets = Path(directory).resolve(strict=True)
        environment = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null", GIT_TERMINAL_PROMPT="0", PYTHONPATH="", PYTHONSAFEPATH="1")
        for key in ("GH_TOKEN", "GITHUB_TOKEN", "RELEASE_READ_TOKEN", "RELEASE_GIT_TOKEN", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"):
            environment.pop(key, None)
        environment.update(GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="core.hooksPath", GIT_CONFIG_VALUE_0="/dev/null")
        with tempfile.TemporaryDirectory(prefix="mcp-relayd-push-") as temporary:
            scratch = Path(temporary)
            work = scratch
            manifest = recipe("release-verify", str(assets))
            for key in ("repository", "head_repository", "event", "head_branch", "checks_run_id", "build_run_id", "source_sha", "version", "eligible", "artifact_class", "last_tag", "bump_sha", "tag", "bundle_sha256", "build_run_attempt", "build_head_sha", "trigger_checks_run_id", "checks_run_attempt"):
                if key in metadata:
                    require(type(manifest[key]) is type(metadata[key]) and manifest[key] == metadata[key], "manifest mismatch: " + key)
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
            git("init", "--bare", str(scratch / "objects"))
            work = scratch / "objects"
            git("remote", "add", "origin", remote)
            git("fetch", "origin", "refs/heads/master:refs/remotes/origin/master", "refs/tags/*:refs/tags/*")
            remote_tags = git("ls-remote", "--tags", "origin")
            require(git("bundle", "list-heads", str(bundle)) == bump + " refs/tags/" + tag, "unexpected bundle refs")
            git("bundle", "verify", str(bundle))
            git("fetch", "--no-tags", str(bundle), "refs/tags/" + tag + ":refs/handoff/tag")
            require(git("cat-file", "-t", "refs/handoff/tag") == "commit" and git("rev-parse", "refs/handoff/tag") == bump and git("show", "-s", "--format=%P", bump) == source, "wrong handoff parent/tag")
            # Identity and exact config/diff recognition use trusted tooling only.
            identity_env = dict(environment)
            if read_token:
                identity_env["GH_TOKEN"] = read_token
            identity = recipe("release-bot-identity", env=identity_env)
            environment.update(identity)
            require(recipe("release-own-bump", bump)["own_bump"] is True and tomllib.loads(git("show", bump + ":.cz.toml"))["tool"]["commitizen"]["version"] == version, "forged handoff bump")
            def refs():
                current = git("ls-remote", "origin", "refs/heads/master").split()[0]
                tags = git("ls-remote", "--tags", "origin")
                recovered = current == bump and (bump + "\trefs/tags/" + tag) in tags.splitlines()
                return current, tags, recovered
            current, tags, recovered = refs()
            if not recovered and current != source:
                result(False, False, "stale-source")
                sys.exit(0)
            if not recovered:
                require(not git("tag", "--list", tag), "conflicting remote tag")
                preflight = recipe("release-preflight", source, metadata["last_tag"])
                if not preflight["publish"] or tags != remote_tags:
                    result(False, False, "stale-tag")
                    sys.exit(0)
            if os.environ.get("RELEASE_PREFLIGHT_ONLY") == "1":
                result(True, recovered, "recovered" if recovered else "current")
                sys.exit(0)
            require(os.environ.get("GH_TOKEN"), "push requires App token")
            # No nested tooling/code runs with the App token: only Git transport.
            push_env = dict(environment, RELEASE_GIT_TOKEN=os.environ["GH_TOKEN"], GIT_CONFIG_COUNT="3", GIT_CONFIG_KEY_1="credential.helper", GIT_CONFIG_VALUE_1="", GIT_CONFIG_KEY_2="credential.helper", GIT_CONFIG_VALUE_2=helper)
            again, final_tags, final_recovered = refs()
            require(again == current and final_tags == tags and final_recovered == recovered, "remote refs changed")
            if not recovered:
                git("push", "--atomic", "origin", bump + ":refs/heads/master", "refs/handoff/tag:refs/tags/" + tag, env=push_env)
            require(refs()[2], "push refs not confirmed")
            result(True, True, "recovered" if recovered else "pushed")
    except (OSError, ValueError, KeyError, TypeError, IndexError, subprocess.CalledProcessError) as error:
        print("ci-push: " + (str(error) if isinstance(error, ValueError) else "handoff/ref verification or atomic push failed"), file=sys.stderr)
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
            if event == "push" and os.environ.get("GITHUB_REPOSITORY") and os.environ.get("CHECKS_HEAD_REPOSITORY") == os.environ["GITHUB_REPOSITORY"] and os.environ.get("CHECKS_HEAD_BRANCH") == "master":
                identity = subprocess.run(["just", "release-bot-identity", str(scratch / "identity.json")])
                if identity.returncode == 0:
                    os.environ.update(json.loads((scratch / "identity.json").read_text()))
                    recognition = subprocess.run(["just", "release-own-bump", source, str(scratch / "own.json")])
                    if recognition.returncode == 0:
                        own = json.loads((scratch / "own.json").read_text())["own_bump"] is True
            if not own:
                subprocess.run(["just", "ci-commits", base, source, title if event == "pull_request" else "", str(scratch / "commits.json")], check=True)
        run_id = int(os.environ["GITHUB_RUN_ID"])
        if run_id <= 0:
            raise ValueError("invalid Checks run ID")
        metadata = dict(source_sha=source, repository=os.environ["GITHUB_REPOSITORY"], head_repository=os.environ["CHECKS_HEAD_REPOSITORY"], event=event, head_branch=os.environ["CHECKS_HEAD_BRANCH"], checks_run_id=run_id)
        if os.environ.get("GITHUB_ACTIONS") == "true":
            metadata["checks_run_attempt"] = int(os.environ["GITHUB_RUN_ATTEMPT"])
            if metadata["checks_run_attempt"] <= 0:
                raise ValueError("invalid Checks attempt")
        # Native Skip CI suppresses normal bump events; recognition is a safe
        # fallback if Checks is invoked anyway, never a message-only bypass.
        if not own:
            for command in (["go", "test", "./..."], ["golangci-lint", "fmt", "--diff", "./..."], ["golangci-lint", "run", "./..."], ["typos", "--force-exclude", "."], ["go", "vet", "./..."], ["check"], ["go", "test", "./internal/process", "./internal/gateway", "./internal/integration", "-count=1"], ["actionlint", "-shellcheck=", "-pyflakes="]):
                subprocess.run(command, check=True)
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
        standard = dict(name="cz_conventional_commits", version_provider="commitizen", version_scheme="semver2", tag_format="v$version", major_version_zero=True, update_changelog_on_bump=True, changelog_file="CHANGELOG.md", bump_message="chore(release): bump version $current_version → $new_version [skip ci]")
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
        standard = dict(name="cz_conventional_commits", version_provider="commitizen", version_scheme="semver2", tag_format="v$version", major_version_zero=True, update_changelog_on_bump=True, changelog_file="CHANGELOG.md", bump_message="chore(release): bump version $current_version → $new_version [skip ci]")
        if any(tomllib.loads(value) != {"tool": {"commitizen": dict(standard, version=version)}} for value, version in ((old, before), (new, after))):
            return False
        semver = r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?"
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
        return git("show", "-s", "--format=%B", source) == f"chore(release): bump version {before} → {after} [skip ci]"

    try:
        Path(sys.argv[2]).write_text(json.dumps(dict(own_bump=own(sys.argv[1]))) + "\n")
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        print(f"release-own-bump: {error}", file=sys.stderr)
        sys.exit(1)

# Consume published refs and prebuilt bytes; never version, checkout or push.
ci-release provenance asset_dir output:
    #!/usr/bin/env python3
    import hashlib
    import json
    import os
    import re
    import stat
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
            operation = next((arg for arg in args[1:] if arg in ("fetch", "init", "bundle", "show", "rev-parse", "tag")), "command")
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
        process = subprocess.run(["gh", "api", endpoint, *args], env=publication_environment, capture_output=True)
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
        # Publication credentials never reach Git or nested tooling.
        environment = dict(os.environ, GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL="/dev/null", GIT_TERMINAL_PROMPT="0", PYTHONPATH="", PYTHONSAFEPATH="1")
        for key in ("GH_TOKEN", "GITHUB_TOKEN", "RELEASE_READ_TOKEN", "RELEASE_GIT_TOKEN", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"):
            environment.pop(key, None)
        environment.update(GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="core.hooksPath", GIT_CONFIG_VALUE_0="/dev/null")
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
            bump, tag, digest = (metadata[key] for key in ("bump_sha", "tag", "bundle_sha256"))
            require(re.fullmatch(r"[0-9a-f]{40}", bump) and tag == "v" + version and re.fullmatch(r"[0-9a-f]{64}", digest), "invalid handoff")
            for key in ("bump_sha", "tag", "bundle_sha256"):
                require(type(manifest[key]) is type(metadata[key]) and manifest[key] == metadata[key], "manifest mismatch: " + key)
            require(int(os.environ["GITHUB_RUN_ID"]) == metadata["build_run_id"], "foreign caller run")
            if os.environ.get("GITHUB_ACTIONS") == "true":
                require(os.environ["GITHUB_WORKFLOW_REF"] == repository + "/.github/workflows/build.yml@refs/heads/master" and os.environ["GITHUB_EVENT_NAME"] == "workflow_run" and os.environ["GITHUB_SHA"] == metadata["build_head_sha"], "foreign caller")
                require(json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())["workflow_run"]["id"] == metadata["checks_run_id"] == metadata["trigger_checks_run_id"], "foreign Checks trigger")
                for key in ("checks_run_attempt", "build_run_attempt"):
                    require(type(metadata[key]) is int and metadata[key] > 0, "invalid producer attempt")
            bundle = assets / "bump.bundle"
            require(stat.S_ISREG(bundle.lstat().st_mode) and hashlib.sha256(bundle.read_bytes()).hexdigest() == digest, "invalid bundle checksum")
            remote = git("remote", "get-url", "origin")
            if os.environ.get("GITHUB_ACTIONS") == "true":
                require(remote == f"https://github.com/{repository}.git", "foreign Git remote")
            # Read-only token for private-repository fetches, held in environment only.
            read_token = os.environ.get("RELEASE_READ_TOKEN")
            helper = '!f() { test "$1" = get && printf "username=x-access-token\\npassword=%s\\n" "$RELEASE_GIT_TOKEN"; }; f'
            if read_token:
                environment.update(RELEASE_GIT_TOKEN=read_token, GIT_CONFIG_COUNT="3", GIT_CONFIG_KEY_1="credential.helper", GIT_CONFIG_VALUE_1="", GIT_CONFIG_KEY_2="credential.helper", GIT_CONFIG_VALUE_2=helper)
            work = scratch / "objects"
            command(["git", "-c", "core.hooksPath=/dev/null", "init", "--bare", str(work)], env=environment)
            git("remote", "add", "origin", remote)
            refresh()
            remote_tags = git("ls-remote", "--tags", "origin")
            require(git("cat-file", "-t", "refs/tags/" + tag) == "commit" and git("rev-parse", "refs/tags/" + tag) == bump, "unpublished or conflicting tag")
            require(git("rev-parse", "refs/remotes/origin/master") == bump and git("show", "-s", "--format=%P", bump) == source, "unpublished or stale bump")
            require(git("bundle", "list-heads", str(bundle)) == bump + " refs/tags/" + tag, "unexpected bundle refs")
            git("bundle", "verify", str(bundle))
            require(recipe("release-own-bump", bump)["own_bump"] is True and tomllib.loads(git("show", bump + ":.cz.toml"))["tool"]["commitizen"]["version"] == version, "forged published bump")
            require(recipe("release-preflight", bump, tag)["publish"], "stale-tag")
            baseline_tags = []
            for baseline in git("tag", "--merged", source).splitlines():
                match = re.fullmatch(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?", baseline)
                if not match:
                    continue
                parts = match[4].split(".") if match[4] else []
                if any(part.isdigit() and len(part) > 1 and part.startswith("0") for part in parts):
                    continue
                key = (*map(int, match.group(1, 2, 3)), match[4] is None, tuple((0, int(part)) if part.isdigit() else (1, part) for part in parts))
                baseline_tags.append((key, baseline))
            require((max(baseline_tags)[1] if baseline_tags else "") == last_tag, "stale baseline tag")

            def stable_refs():
                require(git("ls-remote", "origin", "refs/heads/master").split()[0] == bump and git("ls-remote", "--tags", "origin") == remote_tags, "published refs changed")

            stable_refs()
            if os.environ.get("RELEASE_PREFLIGHT_ONLY") == "1":
                result(False, "current", True)
                sys.exit(0)
            require(os.environ.get("GH_TOKEN") and environment.get("RELEASE_BOT_NAME") and environment.get("RELEASE_BOT_EMAIL"), "eligible publication requires App token and identity")
            publication_environment = dict(environment, GH_TOKEN=os.environ["GH_TOKEN"])
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
                stable_refs()
                api(f"https://uploads.github.com/{release_endpoint}/assets?name=" + quote(name, safe=""), "-X", "POST", "-H", "Content-Type: application/octet-stream", "--input", str(files[name]))
            require(verify_remote() == set(files), "incomplete uploaded assets")
            if release["draft"]:
                stable_refs()
                api(release_endpoint, "-X", "PATCH", "-F", "draft=false")
            result(True, "published")
    except (OSError, ValueError, KeyError, TypeError, IndexError, subprocess.CalledProcessError) as error:
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
