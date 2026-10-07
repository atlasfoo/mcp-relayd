package gateway_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"mcp-relayd/internal/config"
	"mcp-relayd/internal/gateway"
	"mcp-relayd/internal/process"
)

func TestPublicServerHealthReportsLiveAndConfiguredServerStates(t *testing.T) {
	registry := publicRegistry{
		servers: map[string]server{
			"disabled": {state: process.StateStopped},
			"failed":   {state: process.StateFailed},
			"ready":    {state: process.StateReady},
		},
	}
	server, authority := newPublicServer(t, "127.0.0.1", registry)
	request := newRequest(t, http.MethodGet, "/health", nil)
	request.Host = authority
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200: %s", response.Code, response.Body)
	}
	var health struct {
		Status  string                 `json:"status"`
		Servers []gateway.ServerStatus `json:"servers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if health.Status != "ok" {
		t.Errorf("health status field = %q, want ok", health.Status)
	}
	want := map[string]process.State{
		"disabled": process.StateStopped,
		"failed":   process.StateFailed,
		"ready":    process.StateReady,
	}
	if len(health.Servers) != len(want) {
		t.Fatalf("health servers = %+v, want %d entries", health.Servers, len(want))
	}
	for _, entry := range health.Servers {
		if state, ok := want[entry.Name]; !ok || state != entry.State {
			t.Errorf("health server entry = %+v, unexpected name or state", entry)
		}
		delete(want, entry.Name)
	}
	if len(want) > 0 {
		t.Errorf("health response omitted servers: %v", want)
	}
	for _, forbidden := range []string{"env", "command", "addr", "config"} {
		if strings.Contains(response.Body.String(), `"`+forbidden+`"`) {
			t.Errorf("health response exposes forbidden field %q: %s", forbidden, response.Body)
		}
	}
}

func TestPublicServerLivenessIsIndependentOfUpstreamFailure(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	addr := strings.TrimPrefix(closed.URL, "http://")
	closed.Close()
	registry := publicRegistry{servers: map[string]server{
		"broken": {state: process.StateReady, addr: addr},
	}}
	server, authority := newPublicServer(t, "127.0.0.1", registry)
	request := newRequest(t, http.MethodGet, "/mcp/broken", nil)
	request.Host = authority
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed upstream status = %d, want 503", response.Code)
	}
	request = newRequest(t, http.MethodGet, "/health", nil)
	request.Host = authority
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("liveness after upstream failure = %d, want 200", response.Code)
	}
}

func TestPublicServerRejectsUnknownRoutesAndReturns503ForKnownUnavailable(t *testing.T) {
	registry := publicRegistry{servers: map[string]server{
		"disabled": {state: process.StateStopped},
		"failed":   {state: process.StateFailed},
	}}
	server, authority := newPublicServer(t, "127.0.0.1", registry)
	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/missing", want: http.StatusNotFound},
		{path: "/mcp/missing", want: http.StatusNotFound},
		{path: "/mcp/disabled", want: http.StatusServiceUnavailable},
		{path: "/mcp/failed", want: http.StatusServiceUnavailable},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := newRequest(t, http.MethodGet, test.path, nil)
			request.Host = authority
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Errorf("%s status = %d, want %d", test.path, response.Code, test.want)
			}
		})
	}
}

func TestPublicServerValidatesHostAndOriginBeforeForwarding(t *testing.T) {
	var forwarded atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		forwarded.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	registry := publicRegistry{servers: map[string]server{
		"demo": {state: process.StateReady, addr: strings.TrimPrefix(upstream.URL, "http://")},
	}}
	server, authority := newPublicServer(t, "127.0.0.1", registry)
	for _, test := range []struct {
		name   string
		host   string
		origin []string
		want   int
	}{
		{name: "allowed absent origin", host: authority, want: http.StatusNoContent},
		{name: "allowed local origin", host: authority, origin: []string{"http://" + authority}, want: http.StatusNoContent},
		{name: "spoofed host", host: "attacker.example:" + portOf(authority), want: http.StatusMisdirectedRequest},
		{name: "foreign origin", host: authority, origin: []string{"https://attacker.example"}, want: http.StatusForbidden},
		{name: "null origin", host: authority, origin: []string{"null"}, want: http.StatusForbidden},
		{name: "malformed origin", host: authority, origin: []string{"http://[::1"}, want: http.StatusForbidden},
		{name: "origin with path", host: authority, origin: []string{"http://" + authority + "/path"}, want: http.StatusForbidden},
		{name: "wrong origin port", host: authority, origin: []string{"http://127.0.0.1:1"}, want: http.StatusForbidden},
		{name: "duplicate origins", host: authority, origin: []string{"http://" + authority, "http://" + authority}, want: http.StatusForbidden},
		{name: "wrong host port", host: "127.0.0.1:1", want: http.StatusMisdirectedRequest},
		{name: "empty host", host: "", want: http.StatusMisdirectedRequest},
		{name: "missing host port", host: "127.0.0.1", want: http.StatusMisdirectedRequest},
		{name: "host with userinfo", host: "user@" + authority, want: http.StatusMisdirectedRequest},
		{name: "host with path", host: authority + "/", want: http.StatusMisdirectedRequest},
		{name: "loopback alias", host: "localhost:" + portOf(authority), want: http.StatusMisdirectedRequest},
		{name: "empty origin", host: authority, origin: []string{""}, want: http.StatusForbidden},
		{name: "origin with trailing slash", host: authority, origin: []string{"http://" + authority + "/"}, want: http.StatusForbidden},
		{name: "origin with query", host: authority, origin: []string{"http://" + authority + "?x=1"}, want: http.StatusForbidden},
		{name: "origin with fragment", host: authority, origin: []string{"http://" + authority + "#fragment"}, want: http.StatusForbidden},
		{name: "origin with userinfo", host: authority, origin: []string{"http://user@" + authority}, want: http.StatusForbidden},
		{name: "origin list", host: authority, origin: []string{"http://" + authority + ", http://" + authority}, want: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := forwarded.Load()
			request := newRequest(t, http.MethodGet, "/mcp/demo", nil)
			request.Host = test.host
			for _, origin := range test.origin {
				request.Header.Add("Origin", origin)
			}
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Errorf("response status = %d, want %d", response.Code, test.want)
			}
			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Error("public server must not grant CORS access")
			}
			if test.want != http.StatusNoContent && forwarded.Load() != before {
				t.Errorf("invalid authority/origin forwarded upstream (%d -> %d)", before, forwarded.Load())
			}
		})
	}
}

func TestPublicServerSupportsConfiguredIPv6LoopbackAuthority(t *testing.T) {
	registry := publicRegistry{servers: map[string]server{}}
	server, authority := newPublicServer(t, "::1", registry)
	request := newRequest(t, http.MethodGet, "/health", nil)
	request.Host = authority
	request.Header.Set("Origin", "http://"+authority)
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("IPv6 loopback health status = %d, want 200: %s", response.Code, response.Body)
	}
}

func TestPublicServerNormalizesConfiguredDNSAuthorityCase(t *testing.T) {
	server, authority := newPublicServer(t, "localhost", publicRegistry{})
	request := newRequest(t, http.MethodGet, "/health", nil)
	request.Host = strings.ToUpper(authority)
	request.Header.Set("Origin", "http://"+strings.ToUpper(authority))
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("case-normalized DNS health status = %d, want 200: %s", response.Code, response.Body)
	}
}

func TestPublicServerRejectsNonLoopbackConfiguredHost(t *testing.T) {
	for _, host := range []string{"192.0.2.1", "0.0.0.0", "::", "", "attacker.example", "localhost.", "[::1]", "127.0.0.1:9876"} {
		if _, err := gateway.NewServer(config.Gateway{Host: host, Port: 9876}, publicRegistry{}); err == nil {
			t.Errorf("NewServer accepted invalid gateway host %q", host)
		}
	}
}

func TestPublicServerRejectsInvalidPorts(t *testing.T) {
	for _, port := range []int{-1, 0, 65536} {
		if _, err := gateway.NewServer(config.Gateway{Host: "127.0.0.1", Port: port}, publicRegistry{}); err == nil {
			t.Errorf("NewServer accepted invalid gateway port %d", port)
		}
	}
}

func TestPublicServerValidatesBeforeAllRoutes(t *testing.T) {
	server, authority := newPublicServer(t, "127.0.0.1", publicRegistry{})
	for _, path := range []string{"/health", "/missing", "/mcp/missing"} {
		for _, invalidHost := range []bool{false, true} {
			request := newRequest(t, http.MethodGet, path, nil)
			request.Host = authority
			request.Header.Set("Origin", "null")
			want := http.StatusForbidden
			if invalidHost {
				request.Host = "attacker.example:9876"
				want = http.StatusMisdirectedRequest
			}
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, request)
			if response.Code != want {
				t.Errorf("%s invalidHost=%t: status = %d, want %d", path, invalidHost, response.Code, want)
			}
		}
	}
}

func TestPublicServerHealthUsesJSONAndEmptyArray(t *testing.T) {
	server, authority := newPublicServer(t, "127.0.0.1", publicRegistry{})
	request := newRequest(t, http.MethodGet, "/health", nil)
	request.Host = authority
	response := httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Header().Get("Content-Type") != "application/json" || strings.TrimSpace(response.Body.String()) != `{"status":"ok","servers":[]}` {
		t.Fatalf("empty health response = %v %s", response.Header(), response.Body)
	}
	request.Method = http.MethodPost
	response = httptest.NewRecorder()
	server.Handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Errorf("POST health = %d, want 404", response.Code)
	}
}

func newPublicServer(t *testing.T, host string, registry publicRegistry) (*http.Server, string) {
	t.Helper()
	const port = 9876
	server, err := gateway.NewServer(config.Gateway{Host: host, Port: port}, registry)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if server.Addr != netJoinHostPort(host, port) {
		t.Errorf("server address = %q, want configured authority %q", server.Addr, netJoinHostPort(host, port))
	}
	if server.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %s, want zero for long-lived MCP streams", server.WriteTimeout)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server, server.Addr
}

func netJoinHostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func portOf(authority string) string {
	_, port, err := net.SplitHostPort(authority)
	if err != nil {
		return "9876"
	}
	return port
}

type publicRegistry struct {
	servers map[string]server
}

func (r publicRegistry) Contains(name string) bool {
	_, ok := r.servers[name]
	return ok
}

func (r publicRegistry) State(name string) process.State { return r.servers[name].state }
func (r publicRegistry) Addr(name string) string         { return r.servers[name].addr }

func (r publicRegistry) Snapshot() []gateway.ServerStatus {
	statuses := make([]gateway.ServerStatus, 0, len(r.servers))
	for name, entry := range r.servers {
		statuses = append(statuses, gateway.ServerStatus{Name: name, State: entry.state})
	}
	return statuses
}
