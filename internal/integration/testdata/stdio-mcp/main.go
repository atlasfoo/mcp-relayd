// Command stdio-mcp is a controllable MCP server for integration tests.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var initializationCount atomic.Int64

var (
	identity     = flag.String("identity", "fixture", "identity reported by the fixture")
	startupDelay = flag.Duration("startup-delay", 0, "delay before reading MCP input")
	failStartup  = flag.Bool("fail-startup", false, "exit unsuccessfully before initialization")
	ignoreSignal = flag.Bool("ignore-sigterm", false, "ignore SIGTERM for forced-shutdown tests")
	eventFile    = flag.String("event-file", "", "append lifecycle events as JSON lines")
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

type event struct {
	Event    string `json:"event"`
	Identity string `json:"identity"`
	PID      int    `json:"pid"`
}

type writer struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func (out *writer) send(value any) {
	out.mu.Lock()
	defer out.mu.Unlock()
	if err := json.NewEncoder(out.w).Encode(value); err != nil {
		log.Printf("write MCP response: %v", err)
	}
	if err := out.w.Flush(); err != nil {
		log.Printf("flush MCP response: %v", err)
	}
}

func main() {
	flag.Parse()
	if *failStartup {
		os.Exit(42)
	}
	if *ignoreSignal {
		signal.Ignore(syscall.SIGTERM)
	}
	if *startupDelay > 0 {
		time.Sleep(*startupDelay)
	}
	logEvent("started")
	out := &writer{w: bufio.NewWriter(os.Stdout)}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		var request message
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		if len(request.ID) == 0 || string(request.ID) == "null" {
			continue
		}
		go handle(out, request)
	}
	logEvent("stopped")
}

func handle(out *writer, request message) {
	var result any
	switch request.Method {
	case "initialize":
		initializationCount.Add(1)
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		version := params.ProtocolVersion
		if version == "" {
			version = "2025-03-26"
		}
		result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]string{"name": *identity, "version": "1.0.0"},
		}
		logEvent("initialized")
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": []any{
			map[string]any{"name": "fixture_info", "description": "Return fixture identity, PID and initialization count", "inputSchema": objectSchema(map[string]any{})},
			map[string]any{"name": "echo", "description": "Echo text", "inputSchema": objectSchema(map[string]any{"text": map[string]string{"type": "string"}})},
			map[string]any{"name": "delay", "description": "Wait for milliseconds, then return text", "inputSchema": objectSchema(map[string]any{"milliseconds": map[string]string{"type": "number"}, "text": map[string]string{"type": "string"}})},
		}}
	case "tools/call":
		result = callTool(request.Params)
	default:
		out.send(response{JSONRPC: "2.0", ID: json.RawMessage(request.ID), Error: map[string]any{"code": -32601, "message": "method not found"}})
		return
	}
	out.send(response{JSONRPC: "2.0", ID: json.RawMessage(request.ID), Result: result})
}

func callTool(raw json.RawMessage) any {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return toolResult("invalid arguments")
	}
	switch params.Name {
	case "fixture_info":
		return toolResult(fmt.Sprintf("identity=%s pid=%d init_count=%d", *identity, os.Getpid(), initializationCount.Load()))
	case "echo":
		return toolResult(fmt.Sprint(params.Arguments["text"]))
	case "delay":
		milliseconds, _ := strconv.Atoi(fmt.Sprint(params.Arguments["milliseconds"]))
		if milliseconds > 0 {
			logEvent("delay_started")
			time.Sleep(time.Duration(milliseconds) * time.Millisecond)
		}
		return toolResult(fmt.Sprint(params.Arguments["text"]))
	default:
		return map[string]any{"content": []any{map[string]string{"type": "text", "text": "unknown tool"}}, "isError": true}
	}
}

func objectSchema(properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": properties}
}

func toolResult(text string) any {
	return map[string]any{"content": []any{map[string]string{"type": "text", "text": text}}}
}

func logEvent(name string) {
	if *eventFile == "" {
		return
	}
	file, err := os.OpenFile(*eventFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("open event file: %v", err)
		return
	}
	defer file.Close()
	_ = json.NewEncoder(file).Encode(event{Event: name, Identity: *identity, PID: os.Getpid()})
}
