# mcp-relayd

Run your local stdio-based [Model Context Protocol (MCP)](https://modelcontextprotocol.io/) servers behind one HTTP address, then connect your MCP clients by URL instead of having each client launch its own server.

`mcp-relayd` starts one external `mcp-proxy` bridge per configured server and exposes it at `/mcp/<server-name>`. It listens only on loopback: clients must run on the same machine. It is not a LAN or remote-access gateway.

## Why mcp-relayd?

Using several agents or client sessions often means duplicating MCP server commands, environment variables, and runtime settings across client configurations. Each client may also launch its own copy of the same stdio server, repeating startup work and consuming extra resources.

`mcp-relayd` centralizes those settings in one per-user TOML file. Configure your servers once, then point each agent at the same HTTP URLs. While the daemon is running, multiple agents and new sessions reuse the running servers instead of managing separate processes. Server configuration stays in one place; each client only needs the endpoint URL.

## Requirements

- **`mcp-relayd`**: download the executable for your platform below. Go, Nix, and devenv are not required to use a release binary.
- **[uv](https://docs.astral.sh/uv/getting-started/installation/)** (recommended): installs `mcp-proxy` in an isolated tool environment.
- **`mcp-proxy`**: the required external stdio-to-HTTP bridge; it is not bundled with the daemon.
- **Your MCP servers**: install each server and its runtime (for example, Node.js or Python). Server commands must support stdio transport and be available on `PATH`, or use absolute executable paths.
- **An MCP client with Streamable HTTP support** to connect to the exposed servers.

## Quick start

### 1. Install mcp-relayd

Download the archive for your OS and CPU architecture, plus `SHA256SUMS`, from [GitHub Releases](https://github.com/atlasfoo/mcp-relayd/releases). Put both files in the same directory and verify the archive before extracting it:

```sh
sha256sum -c --ignore-missing SHA256SUMS
```

On macOS, use `shasum` instead (replace the placeholder with your archive's filename):

```sh
grep -F '  mcp-relayd_<version>_<os>_<arch>.<ext>' SHA256SUMS | shasum -a 256 -c -
```

On Windows, use PowerShell's `Get-FileHash <archive> -Algorithm SHA256` and compare the result with the matching entry in `SHA256SUMS`.

Extract `mcp-relayd` (`mcp-relayd.exe` on Windows) and place it on your `PATH`, or invoke it by its full path.

### 2. Install the mcp-proxy bridge

With uv installed, run:

```sh
uv tool install mcp-proxy
```

Ensure `mcp-proxy` is on your `PATH`; if needed, run `uv tool update-shell` and restart your shell. Alternatively, set `proxy.command` in your TOML file to its absolute executable path. No repository checkout or Go build is needed.

**Compatibility:** the tested versions are **mcp-proxy 0.12.0** and **MCP SDK 1.30.0**. The command above does not pin versions, so compatibility with the installed versions is not guaranteed. The project's [provisioning script](scripts/provision-mcp-proxy.py) uses the [hash-locked dependencies](tooling/mcp-proxy/requirements.lock) for reproducible development and CI.

### 3. Create your configuration

Create the parent directory if needed, then save your configuration at:

| Platform | Default configuration file |
| --- | --- |
| Linux/macOS | `~/.config/mcp-relayd.toml` |
| Windows | `%USERPROFILE%\.config\mcp-relayd.toml` |

Start with this example or copy [`examples/mcp-relayd.toml`](examples/mcp-relayd.toml). Replace the command, arguments, and working directory with those of your installed stdio MCP server; `server.js` below is a placeholder, not a bundled server.

```toml
[gateway]
host = "127.0.0.1"
port = 9876

[proxy]
command = "mcp-proxy" # Or the absolute path from step 2.

[servers.filesystem]
command = "node"
args = ["server.js", "--transport", "stdio"]
cwd = "/path/to/filesystem-server"

[servers.filesystem.env]
NODE_ENV = "production"
```

Add another `[servers.<name>]` table for each additional server. The table name becomes its URL: `filesystem` is available at `/mcp/filesystem`. See the [configuration reference](#configuration-reference) for all fields and defaults.

### 4. Start and connect

```sh
mcp-relayd run
# Or:
mcp-relayd run --config ./mcp-relayd.toml
```

The daemon does not generate a configuration file or fall back to another location if it is missing or invalid. A relative `--config` path is resolved from the directory where you run the command.

In your MCP client's settings, add a remote/HTTP server using:

| Client setting | Value for the example above |
| --- | --- |
| Transport | Streamable HTTP |
| URL | `http://127.0.0.1:9876/mcp/filesystem` |

Use the host and port from `[gateway]` if you changed them. The client's `Host` header must match that authority; an `Origin` header, if supplied, must match the same `http://` origin. Browser-based clients with a different origin are rejected.

Keep the daemon running while using your clients. Stop it with **Ctrl-C** (or SIGTERM) for graceful shutdown: it drains HTTP requests and stops server process trees within `gateway.shutdown_timeout`, then forces termination if necessary.

## Configuration reference

The TOML file has three sections: `[gateway]` for the HTTP listener, `[proxy]` for the bridge, and `[servers.<name>]` for each stdio server. Unknown fields are rejected. Omitted values use the defaults below; durations use Go syntax such as `10s` or `1m` and must be positive.

| TOML field | Type | Default | Validation / behavior |
| --- | --- | --- | --- |
| `[gateway].host` | string | `"127.0.0.1"` | Loopback IP address, or `localhost` resolving exclusively to loopback addresses. |
| `[gateway].port` | integer | `9876` | Integer from 1 through 65535. |
| `[gateway].shutdown_timeout` | duration string | `"10s"` | Positive Go duration; global graceful-shutdown budget. |
| `[proxy].command` | string | `"mcp-proxy"` | Must not be blank; executable used to start the external bridge. |
| `[proxy].startup_timeout` | duration string | `"30s"` | Positive Go duration for bridge startup. |
| `[servers.<name>].command` | string | required | Must not be blank; server executable. |
| `[servers.<name>].args` | array of strings | `[]` | Arguments passed directly to the executable, without shell parsing. |
| `[servers.<name>].cwd` | string | `""` | Empty means daemon's current directory; relative paths are config-file-relative; non-empty path must be an existing directory. |
| `[servers.<name>].env` | table of string values | `{}` | Extra environment variables for the server; supports `${NAME}` expansion as described below. |
| `[servers.<name>].autostart` | boolean | `true` | `false` leaves the server stopped until configuration is changed and daemon restarted. |

Server names (the `<name>` in `[servers.<name>]`) must begin with an ASCII letter or digit and contain only ASCII letters, digits, `_`, or `-`; the name is also used in `/mcp/<name>`.

Commands and arguments are executed **without a shell**: shell operators, `~`, and variable references in `command`, `args`, or `cwd` are not expanded. Use full executable paths when a command is not on `PATH`. On Windows, TOML literal strings such as `cwd = 'C:\servers\filesystem'` avoid escaping backslashes.

Servers do **not** inherit your full environment. Their baseline contains only the daemon's `PATH` (plus `SystemRoot` and `WINDIR` on Windows); add other required variables explicitly under `[servers.<name>.env]`. For example:

```toml
[servers.filesystem.env]
NODE_ENV = "production"
API_TOKEN = "${FILESYSTEM_API_TOKEN}"
```

Set `FILESYSTEM_API_TOKEN` in the environment **before starting the daemon**. Each referenced variable must exist and be non-empty, or configuration loading fails. Expansion applies only to `env` values; this lets you keep secrets out of the TOML file.

## Status and troubleshooting

Check the daemon and its servers with:

```sh
curl http://127.0.0.1:9876/health
mcp-relayd --help
mcp-relayd --version
```

`/health` returns JSON with overall status and each server's name, state (`STARTING`, `READY`, `STOPPED`, `STOPPING`, or `FAILED`), and proxy PID when available. `--help` and `--version` work without a configuration file; the latter prints the build version and source SHA. Startup and bridge errors are written as structured JSON logs to stderr.

| Problem | What to check |
| --- | --- |
| Missing or invalid configuration | Check the selected file, TOML field names, `cwd` directories, and referenced environment variables. |
| Bridge or server does not start | Check `proxy.command`, server executables, runtimes, arguments, and stderr logs. |
| Connection refused | Ensure the daemon is running and your client uses the configured host and port. |
| HTTP 404 | The server name in `/mcp/<name>` must match a configured table name. |
| HTTP 503 | Check `/health` and logs: the server may be starting, stopped, or failed. `READY` is a snapshot, not a guarantee that the upstream is still alive. |

## Behavior and limitations

- **Restart after editing the configuration.** It is read only at startup; there is no hot reload.
- **Servers start by default.** With `autostart = false`, a server stays stopped; client requests do not start it. Set it back to `true` and restart the daemon to enable it.
- **Clients share state.** Each server has one shared upstream bridge and MCP session, not a separate instance or independent session for each client.
- **MCP feature support depends on the bridge.** Server-to-client requests such as sampling, elicitation, and roots are not guaranteed to work.
- **Local use only.** Automatic server restart, OS service installation, LAN exposure, and a native bridge are not provided.

## Development

For contributors, install [Nix](https://nixos.org/download/) and [devenv](https://devenv.sh/getting-started/), clone the repository, and run `devenv shell` from its root. The shell supplies Go, Python 3.13, uv, and the pinned project tools, and provisions the locked bridge (initially requiring PyPI access).

Inside the shell:

```sh
run run       # Run the daemon; add --config PATH if needed.
build         # Build bin/mcp-relayd.
check         # Formatting, lint, spelling, and Go tests; no file changes.
go vet ./...
devenv test   # Full checks plus a smoke test using the real bridge.
```

For a Go-only build without Nix, install **Go 1.25 or later**:

```sh
go build -o bin/mcp-relayd ./cmd/mcp-relayd
```

On Windows, use `-o bin/mcp-relayd.exe`. A Go-only build does not need Python or install the bridge; the bridge is still required to run the daemon. Optional Git hooks: `lefthook install` (Conventional Commits via Commitizen).

See [the CI workflow guide](docs/actions-flow.md) for Checks → Build → Release, provenance checks, and release prerequisites. Release archives contain only the daemon; the bridge remains external.
