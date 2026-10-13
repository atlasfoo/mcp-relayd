package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mcp-relayd/internal/config"
	"mcp-relayd/internal/process"
)

// ServerStatus exposes only public lifecycle information, not configuration.
type ServerStatus struct {
	Name     string        `json:"name"`
	State    process.State `json:"state"`
	ProxyPID int           `json:"proxy_pid"`
}

// HealthRegistry extends routing lookups with configured server health.
type HealthRegistry interface {
	Registry
	Snapshot() []ServerStatus
}

// NewServer constructs the public loopback boundary without starting a listener.
// WriteTimeout stays zero so long-lived MCP streams are not interrupted.
func NewServer(cfg config.Gateway, servers HealthRegistry) (*http.Server, error) {
	if err := validateLoopback(cfg.Host); err != nil {
		return nil, err
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("gateway.port must be between 1 and 65535")
	}
	if servers == nil {
		return nil, fmt.Errorf("gateway requires a health registry")
	}
	authority := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	proxy := NewHandler(servers)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Match the configured authority, not arbitrary loopback aliases. Exact
		// comparison also rejects malformed authorities and noncanonical ports.
		if !strings.EqualFold(r.Host, authority) {
			http.Error(w, "invalid host", http.StatusMisdirectedRequest)
			return
		}
		origins := r.Header.Values("Origin")
		if len(origins) > 1 || (len(origins) == 1 && !validOrigin(origins[0], authority)) {
			http.Error(w, "invalid origin", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/health" && r.Method == http.MethodGet {
			statuses := servers.Snapshot()
			if statuses == nil {
				statuses = []ServerStatus{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(struct {
				Status  string         `json:"status"`
				Servers []ServerStatus `json:"servers"`
			}{Status: "ok", Servers: statuses})
			return
		}
		proxy.ServeHTTP(w, r)
	})
	return &http.Server{Addr: authority, Handler: handler, ReadHeaderTimeout: 5 * time.Second}, nil
}

func validOrigin(origin, authority string) bool {
	host, ok := strings.CutPrefix(origin, "http://")
	return ok && strings.EqualFold(host, authority)
}

func validateLoopback(host string) error {
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	if strings.EqualFold(host, "localhost") {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err == nil && len(addresses) > 0 {
			for _, address := range addresses {
				if !address.IP.IsLoopback() {
					return fmt.Errorf("gateway.host must resolve exclusively to loopback addresses")
				}
			}
			return nil
		}
	}
	return fmt.Errorf("gateway.host must be a loopback IP or localhost resolving exclusively to loopback")
}
