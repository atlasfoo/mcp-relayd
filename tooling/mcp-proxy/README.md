# Locked external bridge

`requirements.in` declares `mcp-proxy==0.12.0` and `mcp==1.30.0`.
`requirements.lock` pins the complete dependency closure with SHA-256 hashes
and platform markers, including `pywin32` on Windows. MCP 2.x breaks the
bridge's `request_ctx` import; pinning only the proxy is not sufficient.

The supported provisioning interpreter is CPython 3.13. devenv supplies Python
and uv from the existing `devenv.lock` revision. Shell entry runs the shared
provisioner and adds `.devenv/mcp-proxy/bin` to PATH. The virtual environment is
recreated on every entry, using uv's cache, so stale packages cannot survive a
lock or interpreter change. Installation requires binary wheels and verifies
hashes; it never resolves new dependency versions or downloads Python.
Initial provisioning requires public PyPI access, but no credentials.

## CI entrypoint (no workflow required)

After checkout, supply Python 3.13 and uv, then run:

```sh
python scripts/provision-mcp-proxy.py --github-actions
```

On GitHub Actions this appends the environment's native `bin` or `Scripts`
directory to `GITHUB_PATH` and the absolute executable to
`MCP_PROXY_TEST_COMMAND` in `GITHUB_ENV`. Subsequent steps can run `go test ./...`
without activation or shell-specific syntax, on Linux, macOS and Windows.
The script must be invoked with the Python 3.13 selected by the runner setup;
devenv currently supplies Python 3.13.15 and uv 0.12.11; uv 0.12.21 was used
to generate the lock and is also suitable outside devenv.
Use `--venv PATH` to select another **dedicated** environment; the provisioner
clears that directory. Do not point it at a shared environment.

For local use without Actions, omit `--github-actions`, then add the reported
executable directory to PATH or set `MCP_PROXY_TEST_COMMAND` to that executable.
The Go daemon and its release packages neither install nor bundle this runtime.
Tests require the bridge and fail rather than skip if it is absent.

## Intentional updates

Change the direct pins in `requirements.in`, then regenerate the universal lock
with uv 0.12.21:

```sh
uv pip compile tooling/mcp-proxy/requirements.in --python-version 3.13 --universal --generate-hashes --no-emit-index-url --output-file tooling/mcp-proxy/requirements.lock
```

Review every changed version/hash and run the full integration and phase gates
in devenv. Normal shell entry and CI only consume the committed lock; they never
run the compiler. Nix toolchain updates remain separate (`devenv update`).
