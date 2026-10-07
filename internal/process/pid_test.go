package process

import "testing"

func TestProxyPIDOptionalReporting(t *testing.T) {
	child := &execProcess{tree: newAdapterTestProcessTree(), exited: make(chan struct{})}
	manager := &Manager{servers: map[string]*managedServer{
		"ready":    {state: StateReady, child: child},
		"disabled": {state: StateStopped},
		"starting": {state: StateStarting},
		"no-pid":   {state: StateReady, child: pidlessProcess{child}},
		"failed":   {state: StateFailed, child: child},
	}}
	for _, name := range []string{"missing", "disabled", "starting", "no-pid"} {
		if got := manager.ProxyPID(name); got != 0 {
			t.Errorf("ProxyPID(%q) = %d, want zero", name, got)
		}
	}
	for _, name := range []string{"ready", "failed"} {
		if got := manager.ProxyPID(name); got != 1234 {
			t.Errorf("ProxyPID(%q) = %d, want process tree PID 1234", name, got)
		}
	}
	close(child.exited)
	if got := manager.ProxyPID("ready"); got != 0 {
		t.Errorf("PID after exit = %d, want zero", got)
	}
	manager.servers["ready"].child = nil
	if got := manager.ProxyPID("ready"); got != 0 {
		t.Errorf("PID after child cleanup = %d, want zero", got)
	}
}

// Embed only the required interface, deliberately omitting optional PID.
type pidlessProcess struct{ Process }
