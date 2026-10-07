// Package gateway forwards MCP HTTP requests to shared private proxies.
package gateway

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"mcp-relayd/internal/process"
)

// Registry provides read-only access to configured servers. Addr is either a
// host:port pair (as returned by process.Manager) or an absolute HTTP URL.
type Registry interface {
	Contains(name string) bool
	State(name string) process.State
	Addr(name string) string
}

type handler struct {
	servers   Registry
	transport *http.Transport
}

// NewHandler routes /mcp/{name} without owning any backend process lifetime.
// Public Host/Origin validation must wrap this handler before serving clients.
// The HTTP server must not impose a WriteTimeout on long-lived MCP streams.
func NewHandler(servers Registry) http.Handler {
	// Private loopback requests must not use environment proxies or transparent
	// compression. There is no response-header or stream lifetime timeout.
	return &handler{servers: servers, transport: &http.Transport{DisableCompression: true}}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, matched := strings.CutPrefix(r.URL.Path, "/mcp/")
	if !matched || name == "" || strings.Contains(name, "/") || !h.servers.Contains(name) {
		http.NotFound(w, r)
		return
	}
	if h.servers.State(name) != process.StateReady {
		http.Error(w, "server unavailable", http.StatusServiceUnavailable)
		return
	}
	addr := h.servers.Addr(name)
	if addr != "" && !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	target, err := url.Parse(addr)
	if err != nil || target.Scheme != "http" || target.Host == "" || target.User != nil {
		http.Error(w, "server unavailable", http.StatusServiceUnavailable)
		return
	}
	proxy := httputil.ReverseProxy{
		Transport:     h.transport,
		FlushInterval: -1, // Flush each write, including non-SSE streaming responses.
		Rewrite: func(request *httputil.ProxyRequest) {
			request.Out.URL.Scheme = target.Scheme
			request.Out.URL.Host = target.Host
			request.Out.URL.Path = "/mcp"
			request.Out.URL.RawPath = ""
			// ReverseProxy sanitizes query strings before Rewrite; MCP queries
			// are opaque transport data and must retain their original encoding.
			request.Out.URL.RawQuery = request.In.URL.RawQuery
			request.Out.Host = target.Host
			if request.In.Header.Get("Origin") != "" {
				request.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			// A READY process can exit between lookup and forwarding. Do not
			// expose its private address or log request bodies on failure.
			http.Error(w, "server unavailable", http.StatusServiceUnavailable)
		},
	}
	proxy.ServeHTTP(w, r)
}
