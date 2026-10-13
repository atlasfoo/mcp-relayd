package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"mcp-relayd/internal/config"
	"mcp-relayd/internal/gateway"
	"mcp-relayd/internal/process"
)

// These are mandatory contract tests, not optional smoke tests. T-022 must
// provision the pinned executable in PATH; a local test-only override is useful
// when investigating the adapter without changing daemon configuration.
func realProxy(t *testing.T) string {
	t.Helper()
	command := os.Getenv("MCP_PROXY_TEST_COMMAND")
	if command == "" {
		command = "mcp-proxy"
	}
	path, err := exec.LookPath(command)
	if err != nil {
		t.Fatalf("integration dependency missing: provision mcp-proxy==0.12.0 in devenv/CI (T-022), or set MCP_PROXY_TEST_COMMAND to its executable: %v", err)
	}
	output, err := exec.CommandContext(testContext(t), path, "--version").CombinedOutput() // #nosec G204 G702 -- Explicit test-only executable selection, without a shell.
	if err != nil || strings.TrimSpace(string(output)) != "mcp-proxy 0.12.0" {
		t.Fatalf("integration requires working mcp-proxy 0.12.0; --version: %q, error: %v", output, err)
	}
	return path
}

type proxyHarness struct {
	manager *process.Manager
	url     string
	events  map[string]string
}

func newProxyHarness(t *testing.T, timeout time.Duration, flags map[string][]string) *proxyHarness {
	t.Helper()
	proxy := realProxy(t)
	directory := t.TempDir()
	fixture := filepath.Join(directory, "stdio-mcp")
	if runtime.GOOS == "windows" {
		fixture += ".exe"
	}
	if output, err := exec.CommandContext(testContext(t), "go", "build", "-o", fixture, "./testdata/stdio-mcp").CombinedOutput(); err != nil { // #nosec G204 -- Compile the local fixture into a test-owned directory.
		t.Fatalf("build fixture: %v: %s", err, output)
	}
	cfg := config.Config{
		Proxy:   config.Proxy{Command: proxy, StartupTimeout: timeout},
		Gateway: config.Gateway{ShutdownTimeout: 4 * time.Second},
		Servers: make(map[string]config.Server),
	}
	h := &proxyHarness{events: make(map[string]string)}
	for _, name := range []string{"alpha", "beta", "disabled"} {
		h.events[name] = filepath.Join(directory, name+".jsonl")
		args := []string{"--identity", name, "--event-file", h.events[name]}
		cfg.Servers[name] = config.Server{Command: fixture, Args: append(args, flags[name]...), Autostart: name != "disabled"}
	}
	h.manager = process.NewManager(cfg, process.NewExecAdapter(nil))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.manager.Stop(ctx); err != nil {
			t.Errorf("cleanup real proxy trees: %v", err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	host, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Gateway.Host = host
	cfg.Gateway.Port, err = strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	public, err := gateway.NewServer(cfg.Gateway, gateway.NewManagerRegistry(h.manager, cfg))
	if err != nil {
		t.Fatal(err)
	}
	server.Config = public
	server.Start()
	h.url = server.URL
	if err := h.manager.Start(testContext(t)); err != nil {
		t.Fatalf("start real proxy manager: %v", err)
	}
	return h
}

func (h *proxyHarness) ready(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if state := h.manager.State(name); state != process.StateReady {
			t.Fatalf("real proxy %s state = %s, want READY after upstream initialization", name, state)
		}
	}
}

type rpcClient struct {
	url     string
	session string
	http    *http.Client
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

func (c *rpcClient) send(ctx context.Context, id int, method string, params any) (rpcResponse, int, string, error) {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return rpcResponse{}, 0, "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return rpcResponse{}, 0, "", err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-03-26")
	origin, _, _ := strings.Cut(c.url, "/mcp/")
	request.Header.Set("Origin", origin)
	if c.session != "" {
		request.Header.Set("Mcp-Session-Id", c.session)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return rpcResponse{}, 0, "", err
	}
	defer func() { _ = response.Body.Close() }()
	var rpc rpcResponse
	if response.StatusCode != http.StatusOK {
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 4096))
		if readErr != nil {
			return rpc, response.StatusCode, "", fmt.Errorf("read HTTP error response: %w", readErr)
		}
		return rpc, response.StatusCode, response.Header.Get("Mcp-Session-Id"), fmt.Errorf("HTTP %d: %s", response.StatusCode, data)
	}
	if strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
				err = json.Unmarshal([]byte(data), &rpc)
				break
			}
		}
		if err == nil && rpc.JSONRPC == "" {
			err = fmt.Errorf("SSE ended without a JSON-RPC response")
			if scanner.Err() != nil {
				err = fmt.Errorf("read SSE response: %w", scanner.Err())
			}
		}
	} else {
		err = json.NewDecoder(response.Body).Decode(&rpc)
	}
	if err == nil && (rpc.JSONRPC != "2.0" || rpc.ID != id || len(rpc.Error) != 0) {
		err = fmt.Errorf("unexpected JSON-RPC response: %+v, want id %d", rpc, id)
	}
	return rpc, response.StatusCode, response.Header.Get("Mcp-Session-Id"), err
}

func (h *proxyHarness) client(t *testing.T, name string) *rpcClient {
	t.Helper()
	c := &rpcClient{url: h.url + "/mcp/" + name, http: &http.Client{Timeout: 5 * time.Second}}
	rpc, _, session, err := c.send(testContext(t), 1, "initialize", map[string]any{
		"protocolVersion": "2025-03-26", "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "integration", "version": "1"},
	})
	if err != nil {
		t.Fatalf("initialize %s: %v", name, err)
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(rpc.Result, &initialized); err != nil || initialized.ProtocolVersion != "2025-03-26" {
		t.Fatalf("initialize result: %s (%v)", rpc.Result, err)
	}
	c.session = session
	// The initialized notification completes each public client's handshake.
	request, err := http.NewRequestWithContext(testContext(t), http.MethodPost, c.url, strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-03-26")
	request.Header.Set("Mcp-Session-Id", session)
	request.Header.Set("Origin", h.url)
	response, err := c.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("initialized notification HTTP %d, want 202", response.StatusCode)
	}
	return c
}

func (c *rpcClient) tool(ctx context.Context, name string, args map[string]any) (string, error) {
	rpc, _, _, err := c.send(ctx, 7, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return "", err
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(rpc.Result, &result); err != nil {
		return "", err
	}
	if result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" {
		return "", fmt.Errorf("unexpected tool result: %s", rpc.Result)
	}
	return result.Content[0].Text, nil
}

func TestRealProxySharedUpstreamAndCollidingIDs(t *testing.T) {
	h := newProxyHarness(t, 5*time.Second, nil)
	h.ready(t, "alpha", "beta")
	if err := h.manager.Start(testContext(t)); err != nil {
		t.Fatal(err)
	}
	clients := []*rpcClient{h.client(t, "alpha"), h.client(t, "alpha"), h.client(t, "beta"), h.client(t, "beta")}
	pids := make(map[string]int)
	for i, client := range clients {
		name := []string{"alpha", "alpha", "beta", "beta"}[i]
		info, err := client.tool(testContext(t), "fixture_info", map[string]any{})
		var identity string
		var pid, count int
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Sscanf(info, "identity=%s pid=%d init_count=%d", &identity, &pid, &count); err != nil || identity != name || pid <= 0 || count != 1 {
			t.Fatalf("shared upstream %s: %q (%v)", name, info, err)
		}
		if previous := pids[name]; previous != 0 && previous != pid {
			t.Fatalf("%s spawned an upstream per client: %d != %d", name, previous, pid)
		}
		pids[name] = pid
	}
	if pids["alpha"] == pids["beta"] || h.manager.ProxyPID("alpha") == h.manager.ProxyPID("beta") {
		t.Fatal("separate routes share a process")
	}
	start := make(chan struct{})
	results := make(chan error, len(clients))
	ctx := testContext(t)
	for i, client := range clients {
		go func() {
			<-start
			want := fmt.Sprintf("client-%d", i)
			text, err := client.tool(ctx, "delay", map[string]any{"milliseconds": 100 + i*50, "text": want})
			if err == nil && text != want {
				err = fmt.Errorf("colliding id 7: got %q, want %q", text, want)
			}
			results <- err
		}()
	}
	close(start)
	for range clients {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	for _, name := range []string{"alpha", "beta"} {
		assertLifecycle(t, h.events[name], 1, 1)
	}
	assertLifecycle(t, h.events["disabled"], 0, 0)
	// Identical session headers and request IDs on separate routes must not
	// acquire another client's session. Valid sessions above remain usable.
	for _, name := range []string{"alpha", "beta"} {
		invalid := &rpcClient{url: h.url + "/mcp/" + name, session: "same-unknown-session", http: clients[0].http}
		_, status, _, _ := invalid.send(testContext(t), 7, "ping", map[string]any{})
		if status != http.StatusNotFound {
			t.Errorf("%s unknown colliding session HTTP %d, want 404", name, status)
		}
	}
	if clients[0].session == "" || clients[2].session == "" {
		t.Fatal("stateful proxy did not negotiate public sessions")
	}
	foreign := &rpcClient{url: clients[2].url, session: clients[0].session, http: clients[2].http}
	if _, status, _, _ := foreign.send(testContext(t), 7, "ping", map[string]any{}); status != http.StatusNotFound {
		t.Errorf("alpha session reused on beta: HTTP %d, want 404", status)
	}
	for _, client := range clients {
		if _, _, _, err := client.send(testContext(t), 7, "ping", map[string]any{}); err != nil {
			t.Errorf("valid session affected by session collisions: %v", err)
		}
	}
}

func TestRealProxyClientCancellationKeepsSharedUpstream(t *testing.T) {
	h := newProxyHarness(t, 5*time.Second, nil)
	h.ready(t, "alpha", "beta")
	first, second := h.client(t, "alpha"), h.client(t, "alpha")
	before, err := second.tool(testContext(t), "fixture_info", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	pid := h.manager.ProxyPID("alpha")
	ctx, cancel := context.WithTimeout(testContext(t), 100*time.Millisecond)
	defer cancel()
	if _, err := first.tool(ctx, "delay", map[string]any{"milliseconds": 500, "text": "canceled"}); err == nil || ctx.Err() == nil {
		t.Fatalf("long request was not canceled: %v, context: %v", err, ctx.Err())
	}
	// Wait past the upstream response to catch delayed exit or response leakage.
	time.Sleep(600 * time.Millisecond)
	after, err := second.tool(testContext(t), "fixture_info", map[string]any{})
	if err != nil || before != after || h.manager.ProxyPID("alpha") != pid {
		t.Fatalf("cancellation changed shared upstream: before=%q after=%q error=%v", before, after, err)
	}
	text, err := first.tool(testContext(t), "echo", map[string]any{"text": "still alive"})
	if err != nil || text != "still alive" {
		t.Fatalf("canceled client's session is unusable: %q, %v", text, err)
	}
	h.ready(t, "alpha", "beta")
	assertLifecycle(t, h.events["alpha"], 1, 1)
}

func TestRealProxyStartupFailuresAreIsolated(t *testing.T) {
	for _, scenario := range []struct {
		name string
		args []string
	}{
		{"failure", []string{"--fail-startup"}},
		{"timeout", []string{"--startup-delay", "30s"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			start := time.Now()
			h := newProxyHarness(t, 3*time.Second, map[string][]string{"alpha": scenario.args})
			if elapsed := time.Since(start); elapsed > 10*time.Second {
				t.Errorf("startup and cleanup exceeded bounded budget: %s", elapsed)
			}
			if state := h.manager.State("alpha"); state != process.StateFailed {
				t.Errorf("bad upstream state = %s, want FAILED", state)
			}
			if pid := h.manager.ProxyPID("alpha"); pid != 0 {
				t.Errorf("startup failure left proxy PID %d running", pid)
			}
			h.ready(t, "beta")
			client := h.client(t, "beta")
			text, err := client.tool(testContext(t), "echo", map[string]any{"text": "healthy"})
			if err != nil || text != "healthy" {
				t.Fatalf("healthy route affected: %q, %v", text, err)
			}
			for name, want := range map[string]int{"alpha": 503, "disabled": 503, "unknown": 404} {
				c := &rpcClient{url: h.url + "/mcp/" + name, http: client.http}
				_, status, _, _ := c.send(testContext(t), 7, "ping", map[string]any{})
				if status != want {
					t.Errorf("%s HTTP %d, want %d", name, status, want)
				}
			}
			assertLifecycle(t, h.events["beta"], 1, 1)
		})
	}
}

func assertLifecycle(t *testing.T, path string, starts, initializations int) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- Lifecycle file created in the test-owned temporary directory.
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var event struct {
			Event string `json:"event"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		counts[event.Event]++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if counts["started"] != starts || counts["initialized"] != initializations {
		t.Errorf("lifecycle %s = %v, want started=%d initialized=%d", filepath.Base(path), counts, starts, initializations)
	}
}
