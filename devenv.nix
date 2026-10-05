{ config, lib, pkgs, ... }:
{
  packages = [
    pkgs.git
    pkgs.golangci-lint
    pkgs.lefthook
    pkgs.typos
    pkgs.commitizen
  ];

  languages.go = {
    enable = true;
    lsp.enable = true;
  };

  # Use the toolchain supplied by Nix rather than downloading another one.
  env.GOTOOLCHAIN = "local";

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
      test "$(go run ./cmd/mcp-relayd)" = "Hello from mcp-relayd!"
    '';
  };
}
