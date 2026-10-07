package gateway_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"mcp-relayd/internal/gateway"
	"mcp-relayd/internal/process"
)

func TestHandlerRoutesAndForwardsMCPRequests(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/mcp" || r.URL.RawQuery != "mode=fast&x=1" {
			t.Errorf("upstream request = %s %s?%s, want POST /mcp?mode=fast&x=1", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := r.Header.Get("MCP-Session-Id"); got != "session-123" {
			t.Errorf("MCP-Session-Id = %q, want session-123", got)
		}
		if got := r.Header.Get("MCP-Protocol-Version"); got != "2025-03-26" {
			t.Errorf("MCP-Protocol-Version = %q, want preserved value", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"jsonrpc":"2.0"}` {
			t.Errorf("request body = %q", body)
		}
		w.Header().Set("MCP-Session-Id", "session-123")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"result":"ok"}`)
	}))
	defer upstream.Close()

	handler := gateway.NewHandler(fakeServers{"demo": {state: process.StateReady, addr: upstream.URL}})
	req := newRequest(t, http.MethodPost, "/mcp/demo?mode=fast&x=1", strings.NewReader(`{"jsonrpc":"2.0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Session-Id", "session-123")
	req.Header.Set("MCP-Protocol-Version", "2025-03-26")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusAccepted || response.Body.String() != `{"result":"ok"}` {
		t.Fatalf("response = %d %q, want 202 JSON result", response.Code, response.Body.String())
	}
	if response.Header().Get("MCP-Session-Id") != "session-123" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("response headers = %v, want content type and MCP session preserved", response.Header())
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want one", calls.Load())
	}
}

func TestHandlerForwardsGETAndDELETESessionMethods(t *testing.T) {
	var methods []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.URL.RawQuery != "cursor=2" || r.Header.Get("MCP-Session-Id") != "s-1" {
			t.Errorf("forwarded request = %s?%s headers %v", r.URL.Path, r.URL.RawQuery, r.Header)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	handler := gateway.NewHandler(fakeServers{"demo": {state: process.StateReady, addr: upstream.URL}})
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		req := newRequest(t, method, "/mcp/demo?cursor=2", nil)
		req.Header.Set("MCP-Session-Id", "s-1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusNoContent {
			t.Errorf("%s status = %d, want 204", method, response.Code)
		}
	}
	if fmt.Sprint(methods) != "[GET DELETE]" {
		t.Fatalf("upstream methods = %v, want GET and DELETE", methods)
	}
}

func TestHandlerReturnsNotFoundForUnknownAndUnavailableForKnownNotReadyServers(t *testing.T) {
	handler := gateway.NewHandler(fakeServers{"starting": {state: process.StateStarting, addr: "http://127.0.0.1:1"}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest(t, http.MethodPost, "/mcp/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Errorf("unknown server status = %d, want 404", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest(t, http.MethodPost, "/mcp/starting", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("known but unavailable server status = %d, want 503", response.Code)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest(t, http.MethodPost, "/not-mcp", nil))
	if response.Code != http.StatusNotFound {
		t.Errorf("unmatched path status = %d, want 404", response.Code)
	}
}

func TestHandlerStreamsLongSSEWithoutBuffering(t *testing.T) {
	started := make(chan struct{})
	continueStream := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-continueStream:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: second\n\n")
	}))
	defer upstream.Close()
	handler := gateway.NewHandler(fakeServers{"events": {state: process.StateReady, addr: upstream.URL}})
	server := httptest.NewServer(handler)
	defer server.Close()
	defer func() {
		select {
		case <-continueStream:
		default:
			close(continueStream)
		}
	}()
	requestDone := make(chan error, 1)
	firstEvent := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/mcp/events", nil)
		if err != nil {
			requestDone <- err
			firstEvent <- err
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			requestDone <- err
			firstEvent <- err
			return
		}
		defer func() { _ = response.Body.Close() }()
		reader := bufio.NewReader(response.Body)
		line, err := reader.ReadString('\n')
		if err == nil && line != "data: first\n" {
			err = fmt.Errorf("first streamed line = %q, want first event before second is released", line)
		}
		firstEvent <- err
		body, readErr := io.ReadAll(reader)
		if err == nil {
			err = readErr
		}
		if err == nil && (!strings.Contains(string(body), "data: second") || !strings.Contains(line, "data: first")) {
			err = fmt.Errorf("stream remainder = %q, missing second SSE event", body)
		}
		requestDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream did not receive stream request")
	}
	select {
	case err := <-firstEvent:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first SSE event was not delivered while upstream was still holding the second")
	}
	close(continueStream)
	select {
	case err := <-requestDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("long-lived SSE response did not complete; gateway may have buffered or timed out")
	}
}

func TestClientCancellationDoesNotStopSharedBackend(t *testing.T) {
	cancelObserved := make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			close(cancelObserved)
			return
		}
		_, _ = io.WriteString(w, "backend still serves")
	}))
	defer upstream.Close()
	handler := gateway.NewHandler(fakeServers{"shared": {state: process.StateReady, addr: upstream.URL}})
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/mcp/shared", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	cancel()
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("upstream request context was not canceled with client request")
	}
	secondCtx, secondCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer secondCancel()
	secondRequest, err := http.NewRequestWithContext(secondCtx, http.MethodGet, server.URL+"/mcp/shared", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := http.DefaultClient.Do(secondRequest)
	if err != nil {
		t.Fatalf("subsequent request to shared backend failed after client cancellation: %v", err)
	}
	body, readErr := io.ReadAll(second.Body)
	_ = second.Body.Close()
	if readErr != nil || string(body) != "backend still serves" {
		t.Fatalf("subsequent response = %q, %v", body, readErr)
	}
}

type server struct {
	state process.State
	addr  string
}

type fakeServers map[string]server

func (servers fakeServers) Contains(name string) bool {
	_, ok := servers[name]
	return ok
}
func (servers fakeServers) State(name string) process.State { return servers[name].state }
func (servers fakeServers) Addr(name string) string         { return servers[name].addr }

func newRequest(t *testing.T, method, path string, body io.Reader) *http.Request {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), method, path, body)
	request.Host = "127.0.0.1:9876"
	return request
}
