{ config, lib, pkgs, ... }:
{
  packages = [
    pkgs.git
    pkgs.golangci-lint
    pkgs.lefthook
    pkgs.typos
    pkgs.commitizen
    pkgs.python313
    pkgs.uv
  ];

  languages.go = {
    enable = true;
    lsp.enable = true;
  };

  # Use the toolchain supplied by Nix rather than downloading another one.
  env.GOTOOLCHAIN = "local";

  # The bridge is an external runtime, never part of the Go build or release.
  enterShell = ''
    ${pkgs.python313}/bin/python3 "${config.devenv.root}/scripts/provision-mcp-proxy.py" \
      --venv "${config.devenv.root}/.devenv/mcp-proxy" || exit $?
    export PATH="${config.devenv.root}/.devenv/mcp-proxy/bin:$PATH"
  '';

  scripts = {
    run.exec = "go run ./cmd/mcp-relayd";
    build.exec = ''
      mkdir -p bin
      go build -o bin/mcp-relayd ./cmd/mcp-relayd
    '';
    fmt.exec = "golangci-lint fmt ./...";
    lint.exec = ''
      set -eu
      golangci-lint fmt --diff ./...
      golangci-lint run ./...
    '';
    spellcheck.exec = "typos --force-exclude .";
    check.exec = ''
      set -eu
      lint
      spellcheck
      go test ./...
    '';
  };

  tasks."mcp-relayd:quality" = lib.mkIf config.devenv.isTesting {
    before = [ "devenv:enterTest" ];
    after = [ "devenv:enterShell" ];
    exec = ''
      set -eu
      check
      go test ./internal/integration -run '^TestRuntimeSmoke$' -count=1
    '';
  };
}
