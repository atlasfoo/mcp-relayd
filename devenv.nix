{ pkgs, ... }:
{
  packages = [ pkgs.git ];

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
    check.exec = ''
      set -eu
      unformatted=$(gofmt -l cmd)
      if [ -n "$unformatted" ]; then
        printf 'Run gofmt on:\n%s\n' "$unformatted"
        exit 1
      fi
      go vet ./...
      go test ./...
    '';
  };

  enterTest = ''
    set -eu
    check
    test "$(go run ./cmd/mcp-relayd)" = "Hello from mcp-relayd!"
  '';
}
