package gateway_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mcp-relayd/internal/config"
	"mcp-relayd/internal/gateway"
	"mcp-relayd/internal/process"
)

func TestHandlerRewritesPrivateAuthorityAndKeepsOpaqueQuery(t *testing.T) {
	for _, origin := range []string{"", "http://127.0.0.1:9876"} {
		t.Run(origin, func(t *testing.T) {
			var privateHost string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/mcp" || r.URL.RawQuery != "value=%2F&value=two;three&empty=" {
					t.Errorf("private URL = %s, want /mcp with unchanged query", r.URL)
				}
				if r.Host != privateHost {
					t.Errorf("private Host = %q, want %q", r.Host, privateHost)
				}
				wantOrigin := ""
				if origin != "" {
					wantOrigin = "http://" + r.Host
				}
				if got := r.Header.Get("Origin"); got != wantOrigin {
					t.Errorf("private Origin = %q, want %q", got, wantOrigin)
				}
				if r.Header.Get("Accept") != "application/json, text/event-stream" || r.Header.Get("Last-Event-ID") != "event-2" {
					t.Errorf("MCP headers not preserved: %v", r.Header)
				}
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"error":"session conflict"}`)
			}))
			defer upstream.Close()
			privateHost = strings.TrimPrefix(upstream.URL, "http://")
			// The production manager returns a bare host:port, not a URL.
			handler := gateway.NewHandler(fakeServers{"demo": {state: process.StateReady, addr: strings.TrimPrefix(upstream.URL, "http://")}})
			request := newRequest(t, http.MethodGet, "/mcp/demo?value=%2F&value=two;three&empty=", nil)
			request.Header.Set("Accept", "application/json, text/event-stream")
			request.Header.Set("Last-Event-ID", "event-2")
			if origin != "" {
				request.Header.Set("Origin", origin)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusConflict || response.Header().Get("Content-Type") != "application/problem+json" || response.Body.String() != `{"error":"session conflict"}` {
				t.Fatalf("response = %d %v %s", response.Code, response.Header(), response.Body)
			}
			if request.Host != "127.0.0.1:9876" || request.Header.Get("Origin") != origin || request.URL.Path != "/mcp/demo" {
				t.Fatal("forwarding mutated the original public request")
			}
		})
	}
}

func TestHandlerUnavailableStatesAndAddresses(t *testing.T) {
	for _, state := range []process.State{process.StateStopped, process.StateStarting, process.StateInitializing, process.StateFailed, process.StateStopping} {
		t.Run(string(state), func(t *testing.T) {
			handler := gateway.NewHandler(fakeServers{"demo": {state: state, addr: "http://127.0.0.1:1"}})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, newRequest(t, http.MethodPost, "/mcp/demo", nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", response.Code)
			}
		})
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	for _, addr := range []string{"", "://invalid", "https://127.0.0.1:1", closed.URL} {
		t.Run(addr, func(t *testing.T) {
			handler := gateway.NewHandler(fakeServers{"demo": {state: process.StateReady, addr: addr}})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, newRequest(t, http.MethodPost, "/mcp/demo", nil))
			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", response.Code)
			}
		})
	}
}

func TestHandlerMatchesOnlyServerRoute(t *testing.T) {
	handler := gateway.NewHandler(fakeServers{"demo": {state: process.StateStopped}})
	for _, path := range []string{"/", "/mcp", "/mcp/", "/mcp/demo/", "/mcp/demo/extra", "/mcp/missing"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, newRequest(t, http.MethodPost, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, response.Code)
		}
	}
}

func TestManagerRegistrySharesOneBackendPerDefinition(t *testing.T) {
	var launches atomic.Int32
	backends := make(map[string]string)
	for _, name := range []string{"first", "second"} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, name)
		}))
		defer upstream.Close()
		backends[name] = strings.TrimPrefix(upstream.URL, "http://")
	}
	cfg := config.Config{
		Gateway: config.Gateway{ShutdownTimeout: time.Second},
		Proxy:   config.Proxy{StartupTimeout: time.Second},
		Servers: map[string]config.Server{
			"first":    {Autostart: true},
			"second":   {Autostart: true},
			"disabled": {Autostart: false},
		},
	}
	manager := process.NewManager(cfg, registryAdapter{backends: backends, launches: &launches})
	registry := gateway.NewManagerRegistry(manager, cfg)
	if !registry.Contains("disabled") || registry.Contains("missing") {
		t.Fatal("registry must distinguish configured disabled servers from unknown names")
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	// The wrapper owns a name snapshot, not the caller's mutable config map.
	delete(cfg.Servers, "disabled")
	statuses := registry.Snapshot()
	if len(statuses) != 3 {
		t.Fatalf("snapshot = %+v, want three configured servers", statuses)
	}
	for i, name := range []string{"disabled", "first", "second"} {
		wantState, wantPID := process.StateReady, 4321
		if name == "disabled" {
			wantState, wantPID = process.StateStopped, 0
		}
		if statuses[i] != (gateway.ServerStatus{Name: name, State: wantState, ProxyPID: wantPID}) {
			t.Errorf("snapshot[%d] = %+v, want %s %s PID %d", i, statuses[i], name, wantState, wantPID)
		}
	}
	handler := gateway.NewHandler(registry)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newRequest(t, http.MethodGet, "/mcp/disabled", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled status = %d, want 503", response.Code)
	}
	var clients sync.WaitGroup
	for range 8 {
		for _, name := range []string{"first", "second"} {
			clients.Go(func() {
				request := newRequest(t, http.MethodPost, "/mcp/"+name, strings.NewReader(`{"jsonrpc":"2.0","id":1}`))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusOK || response.Body.String() != name {
					t.Errorf("%s response = %d %q", name, response.Code, response.Body)
				}
			})
		}
	}
	clients.Wait()
	if launches.Load() != 2 {
		t.Fatalf("proxy launches = %d, want one per enabled definition", launches.Load())
	}
}

type registryAdapter struct {
	backends map[string]string
	launches *atomic.Int32
}

func (a registryAdapter) Start(_ context.Context, name string, _ config.Proxy, _ config.Server) (process.Process, error) {
	a.launches.Add(1)
	return &registryProcess{addr: a.backends[name], done: make(chan error)}, nil
}

type registryProcess struct {
	addr string
	done chan error
}

func (p *registryProcess) WaitReady(context.Context) error { return nil }
func (p *registryProcess) Addr() string                    { return p.addr }
func (p *registryProcess) PID() int                        { return 4321 }
func (p *registryProcess) Done() <-chan error              { return p.done }
func (p *registryProcess) Stop(context.Context) error      { return nil }
