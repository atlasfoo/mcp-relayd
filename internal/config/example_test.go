package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExampleConfigurationLoadsAndValidates(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "mcp-relayd.toml")
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load(example) error = %v", err)
	}

	if got.Gateway.Host != "127.0.0.1" || got.Gateway.Port != 9876 || got.Gateway.ShutdownTimeout.String() != "10s" {
		t.Errorf("Gateway = %+v, want loopback:9876 and 10s shutdown timeout", got.Gateway)
	}
	if got.Proxy.Command != "mcp-proxy" || got.Proxy.StartupTimeout.String() != "30s" {
		t.Errorf("Proxy = %+v, want mcp-proxy and 30s startup timeout", got.Proxy)
	}

	server, ok := got.Servers["example"]
	if !ok {
		t.Fatal("Servers[example] missing")
	}
	if server.Command != "node" || !reflect.DeepEqual(server.Args, []string{"server.js", "--transport", "stdio"}) {
		t.Errorf("Servers[example] command/args = %q %q", server.Command, server.Args)
	}
	if server.CWD != "" || !server.Autostart {
		t.Errorf("Servers[example] cwd/autostart = %q/%t, want empty cwd and autostart", server.CWD, server.Autostart)
	}
	if server.Env["NODE_ENV"] != "production" {
		t.Errorf("Servers[example].Env[NODE_ENV] = %q, want production", server.Env["NODE_ENV"])
	}
}

func TestSchemaFileIsValidJSON(t *testing.T) {
	path := filepath.Join("..", "..", "schema", "mcp-relayd.schema.json")
	data, err := os.ReadFile(path) // #nosec G304 -- test reads static schema file
	if err != nil {
		t.Fatalf("ReadFile(schema) error = %v", err)
	}

	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("json.Unmarshal(schema) error = %v", err)
	}

	if schema["$schema"] != "http://json-schema.org/draft-07/schema#" {
		t.Errorf("schema.$schema = %v, want http://json-schema.org/draft-07/schema#", schema["$schema"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema.properties missing or not an object")
	}
	for _, expectedProp := range []string{"$schema", "schema", "gateway", "proxy", "servers"} {
		if _, exists := props[expectedProp]; !exists {
			t.Errorf("schema.properties missing %q", expectedProp)
		}
	}
}
