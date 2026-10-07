package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRuntimeSmoke(t *testing.T) {
	proxy := realProxy(t)
	directory := t.TempDir()
	fixture := filepath.Join(directory, "stdio-mcp")
	daemonPath := filepath.Join(directory, "mcp-relayd")
	if runtime.GOOS == "windows" {
		fixture += ".exe"
		daemonPath += ".exe"
	}
	for _, build := range []struct {
		name string
		path string
		pkg  string
	}{
		{name: "fixture", path: fixture, pkg: "./testdata/stdio-mcp"},
		{name: "daemon", path: daemonPath, pkg: "../../cmd/mcp-relayd"},
	} {
		if output, err := exec.CommandContext(testContext(t), "go", "build", "-o", build.path, build.pkg).CombinedOutput(); err != nil { // #nosec G204 -- Destinations and package paths are fixed or confined to the test's TempDir.
			t.Fatalf("build %s: %v: %s", build.name, err, output)
		}
	}
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "runtime.toml")
	eventFile := filepath.Join(directory, "fixture-events.jsonl")
	configText := fmt.Sprintf(`[gateway]
host = "127.0.0.1"
port = %d
shutdown_timeout = "5s"

[proxy]
command = %q
startup_timeout = "10s"

[servers.fixture]
command = %q
args = ["--identity", "runtime-smoke", "--event-file", %q]
`, port, proxy, fixture, eventFile)
	if err := os.WriteFile(configPath, []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	processContext, cancelProcess := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelProcess()
	command := exec.CommandContext(processContext, daemonPath, "run", "--config", configPath) // #nosec G204 -- The executable and configuration are created in this test's TempDir.
	var logs bytes.Buffer
	command.Stdout, command.Stderr = &logs, &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	waitContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	baseURL := "http://127.0.0.1:" + portText
	if err := waitForHTTP(waitContext, baseURL+"/health"); err != nil {
		t.Fatalf("daemon did not become healthy: %v; logs: %s", err, logs.String())
	}
	request, err := http.NewRequestWithContext(testContext(t), http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	healthResponse, err := (&http.Client{Timeout: time.Second}).Do(request) // #nosec G107 -- Local daemon on an ephemeral loopback port.
	if err != nil {
		t.Fatal(err)
	}
	var health struct {
		Status  string `json:"status"`
		Servers []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(healthResponse.Body).Decode(&health); err != nil {
		_ = healthResponse.Body.Close()
		t.Fatal(err)
	}
	_ = healthResponse.Body.Close()
	if health.Status != "ok" || len(health.Servers) != 1 || health.Servers[0].Name != "fixture" || health.Servers[0].State != "READY" {
		t.Fatalf("health = %+v, want fixture READY", health)
	}
	client := &rpcClient{url: baseURL + "/mcp/fixture", http: &http.Client{Timeout: 30 * time.Second}}
	if _, _, session, err := client.send(testContext(t), 1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26", "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "runtime-smoke", "version": "1"},
	}); err != nil {
		t.Fatalf("initialize: %v", err)
	} else {
		client.session = session
	}
	text, err := client.tool(testContext(t), "echo", map[string]any{"text": "runtime-ok"})
	if err != nil || text != "runtime-ok" {
		t.Fatalf("echo tool = %q, %v; want runtime-ok", text, err)
	}
	activeRequest := make(chan error, 1)
	callContext, cancelCall := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCall()
	go func() {
		_, err := client.tool(callContext, "delay", map[string]any{"milliseconds": 30000, "text": "must be interrupted"})
		activeRequest <- err
	}()
	streamContext, cancelStreamWait := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStreamWait()
	if err := waitForFixtureEvent(streamContext, eventFile, "delay_started"); err != nil {
		t.Fatalf("long MCP request did not reach fixture: %v", err)
	}
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal daemon: %v", err)
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	select {
	case err := <-finished:
		waited = true
		if err != nil {
			t.Fatalf("daemon exit: %v; logs: %s", err, logs.String())
		}
	case <-time.After(8 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		waited = true
		t.Fatalf("daemon failed to stop within bounded deadline; logs: %s", logs.String())
	}
	select {
	case err := <-activeRequest:
		if err == nil {
			t.Error("active MCP request completed successfully after shutdown began")
		}
	case <-time.After(time.Second):
		t.Error("active MCP request remained open after forced HTTP close")
	}
	cleanupContext, cancelCleanupWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelCleanupWait()
	if err := waitForFixtureEvent(cleanupContext, eventFile, "stopped"); err != nil {
		t.Errorf("fixture process was not cleaned up: %v", err)
	}
	request, err = http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	if response, err := (&http.Client{Timeout: time.Second}).Do(request); err == nil {
		_ = response.Body.Close()
		t.Errorf("health remained reachable after shutdown: HTTP %d", response.StatusCode)
	}
}

func waitForFixtureEvent(ctx context.Context, path, name string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(path) // #nosec G304 -- Path is created under the smoke test's TempDir.
		if err == nil {
			for line := range strings.SplitSeq(string(data), "\n") {
				var event struct {
					Event string `json:"event"`
				}
				if json.Unmarshal([]byte(line), &event) == nil && event.Event == name {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for fixture event %q: %w", name, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForHTTP(ctx context.Context, url string) error {
	client := &http.Client{Timeout: 250 * time.Millisecond}
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			var health struct {
				Status  string `json:"status"`
				Servers []struct {
					Name  string `json:"name"`
					State string `json:"state"`
				} `json:"servers"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&health)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK && decodeErr == nil && health.Status == "ok" && len(health.Servers) == 1 && health.Servers[0].Name == "fixture" && health.Servers[0].State == "READY" {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", url, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
