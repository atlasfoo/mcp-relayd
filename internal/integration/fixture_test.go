package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestStdioFixtureSupportsMCPAndConcurrentToolCalls(t *testing.T) {
	command := exec.CommandContext(testContext(t), "go", "run", "./testdata/stdio-mcp", "--identity", "alpha")
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = input.Close()
		_ = command.Wait()
	}()
	reader := bufio.NewScanner(output)
	reader.Buffer(make([]byte, 4096), 1024*1024)
	responses := make(chan map[string]any, 4)
	go func() {
		for reader.Scan() {
			var response map[string]any
			if json.Unmarshal(reader.Bytes(), &response) == nil {
				responses <- response
			}
		}
	}()

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fixture_info","arguments":{}}}`,
	}
	for _, request := range requests {
		if _, err := fmt.Fprintln(input, request); err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[string]map[string]any)
	for range 3 {
		response := receiveResponse(t, responses)
		if response["error"] != nil {
			t.Fatalf("JSON-RPC error: %v", response["error"])
		}
		seen[fmt.Sprint(response["id"])] = response
	}
	initialize := seen["1"]["result"].(map[string]any)
	if initialize["protocolVersion"] != "2025-03-26" {
		t.Fatalf("initialize result = %v", initialize)
	}
	tools := seen["2"]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("tools/list returned %d tools, want 3", len(tools))
	}
	info := seen["3"]["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	for _, expected := range []string{"identity=alpha", "pid=", "init_count=1"} {
		if !strings.Contains(info, expected) {
			t.Fatalf("fixture_info = %q, missing %q", info, expected)
		}
	}

	for id := 10; id < 12; id++ {
		request := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"delay","arguments":{"milliseconds":20,"text":"%d"}}}`, id, id)
		if _, err := fmt.Fprintln(input, request); err != nil {
			t.Fatal(err)
		}
	}
	first := receiveResponse(t, responses)
	second := receiveResponse(t, responses)
	if first["id"] == second["id"] {
		t.Fatalf("concurrent responses reused an id: %v", first["id"])
	}
	_ = input.Close()
}

func TestStdioFixtureStartupFailureIsConfigurable(t *testing.T) {
	command := exec.CommandContext(testContext(t), "go", "run", "./testdata/stdio-mcp", "--fail-startup")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("fixture succeeded with --fail-startup")
	}
	if !strings.Contains(string(output), "exit status 42") {
		t.Fatalf("startup failure output = %q, want exit status 42", output)
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func receiveResponse(t *testing.T, responses <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case response := <-responses:
		return response
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for fixture response")
		return nil
	}
}
