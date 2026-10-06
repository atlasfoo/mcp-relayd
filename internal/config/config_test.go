package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "mcp-relayd.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	configPath := writeConfig(t, `
[servers.alpha]
command = "node"
`)

	got, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.Gateway.Host != "127.0.0.1" {
		t.Errorf("Gateway.Host = %q, want 127.0.0.1", got.Gateway.Host)
	}
	if got.Gateway.Port != 9876 {
		t.Errorf("Gateway.Port = %d, want 9876", got.Gateway.Port)
	}
	if got.Proxy.Command != "mcp-proxy" {
		t.Errorf("Proxy.Command = %q, want mcp-proxy", got.Proxy.Command)
	}
	if !got.Servers["alpha"].Autostart {
		t.Error("Servers[alpha].Autostart = false, want true")
	}
}

func TestLoadReadsTOMLValues(t *testing.T) {
	workingDir := t.TempDir()
	configPath := writeConfig(t, `
[gateway]
host = "127.0.0.1"
port = 8765

[proxy]
command = "/usr/local/bin/mcp-proxy"

[servers.alpha]
command = "node"
args = ["server.js", "--mode", "stdio"]
cwd = "`+workingDir+`"
autostart = false

[servers.alpha.env]
NODE_ENV = "test"
`)

	got, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.Gateway.Port != 8765 || got.Gateway.Host != "127.0.0.1" {
		t.Errorf("Gateway = %+v, want host 127.0.0.1 and port 8765", got.Gateway)
	}
	if got.Proxy.Command != "/usr/local/bin/mcp-proxy" {
		t.Errorf("Proxy.Command = %q", got.Proxy.Command)
	}
	server := got.Servers["alpha"]
	if server.Command != "node" || server.Autostart {
		t.Errorf("Servers[alpha] = %+v, want command node and autostart false", server)
	}
	if strings.Join(server.Args, " ") != "server.js --mode stdio" {
		t.Errorf("Servers[alpha].Args = %q", server.Args)
	}
	if server.CWD != workingDir {
		t.Errorf("Servers[alpha].CWD = %q, want %q", server.CWD, workingDir)
	}
	if server.Env["NODE_ENV"] != "test" {
		t.Errorf("Servers[alpha].Env[NODE_ENV] = %q, want test", server.Env["NODE_ENV"])
	}
}

func TestLoadExpandsVariablesOnlyInEnvironmentValues(t *testing.T) {
	t.Setenv("MCP_RELAYD_TEST_TOKEN", "test-token-value")
	configPath := writeConfig(t, `
[servers.alpha]
command = "echo"
args = ["${MCP_RELAYD_TEST_TOKEN}"]

[servers.alpha.env]
TOKEN = "${MCP_RELAYD_TEST_TOKEN}"
`)

	got, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	server := got.Servers["alpha"]
	if server.Env["TOKEN"] != "test-token-value" {
		t.Errorf("Servers[alpha].Env[TOKEN] = %q, want expanded value", server.Env["TOKEN"])
	}
	if len(server.Args) != 1 || server.Args[0] != "${MCP_RELAYD_TEST_TOKEN}" {
		t.Errorf("Servers[alpha].Args = %q, want the unexpanded literal", server.Args)
	}
}

func TestLoadRejectsMissingEnvironmentVariableWithoutLeakingValue(t *testing.T) {
	t.Setenv("MCP_RELAYD_TEST_MISSING", "")
	configPath := writeConfig(t, `
[servers.alpha]
command = "echo"

[servers.alpha.env]
TOKEN = "${MCP_RELAYD_TEST_MISSING}"
`)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("Load() error = nil, want missing environment variable error")
	}
	if strings.Contains(err.Error(), "MCP_RELAYD_TEST_MISSING") {
		t.Errorf("Load() error exposes environment variable name: %v", err)
	}
	if strings.Contains(err.Error(), "test-token-value") {
		t.Errorf("Load() error exposes environment variable value: %v", err)
	}
}

func TestLoadRejectsUnknownTOMLKeys(t *testing.T) {
	configPath := writeConfig(t, `
[gateway]
porrt = 9876

[servers.alpha]
command = "echo"
`)

	if _, err := Load(configPath); err == nil {
		t.Fatal("Load() error = nil, want unknown TOML key error")
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	workingDir := t.TempDir()
	tests := []struct {
		name    string
		content string
	}{
		{
			name: "unsafe server name",
			content: `
[servers."bad/name"]
command = "echo"
`,
		},
		{
			name: "empty command",
			content: `
[servers.alpha]
command = ""
`,
		},
		{
			name: "missing command",
			content: `
[servers.alpha]
args = ["server.js"]
`,
		},
		{
			name: "invalid working directory",
			content: `
[servers.alpha]
command = "echo"
cwd = "` + filepath.Join(workingDir, "missing") + `"
`,
		},
		{
			name: "port out of range",
			content: `
[gateway]
port = 65536

[servers.alpha]
command = "echo"
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, test.content)); err == nil {
				t.Fatal("Load() error = nil, want validation error")
			}
		})
	}
}
