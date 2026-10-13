package gateway

import (
	"slices"

	"mcp-relayd/internal/config"
	"mcp-relayd/internal/process"
)

type managerRegistry struct {
	manager *process.Manager
	names   map[string]struct{}
}

// NewManagerRegistry adapts a manager created from cfg to the gateway registry.
// Names are copied, including disabled servers, so unknown and STOPPED remain
// distinguishable without adding gateway concerns to the process manager.
func NewManagerRegistry(manager *process.Manager, cfg config.Config) HealthRegistry {
	names := make(map[string]struct{}, len(cfg.Servers))
	for name := range cfg.Servers {
		names[name] = struct{}{}
	}
	return &managerRegistry{manager: manager, names: names}
}

func (r *managerRegistry) Contains(name string) bool {
	_, ok := r.names[name]
	return ok
}

func (r *managerRegistry) State(name string) process.State { return r.manager.State(name) }
func (r *managerRegistry) Addr(name string) string         { return r.manager.Addr(name) }

func (r *managerRegistry) Snapshot() []ServerStatus {
	names := make([]string, 0, len(r.names))
	for name := range r.names {
		names = append(names, name)
	}
	slices.Sort(names)
	statuses := make([]ServerStatus, 0, len(names))
	for _, name := range names {
		statuses = append(statuses, ServerStatus{Name: name, State: r.manager.State(name), ProxyPID: r.manager.ProxyPID(name)})
	}
	return statuses
}
