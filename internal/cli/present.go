package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
)

// NewPresenter constructs a slog.Handler that formats events according to the CLI output specification.
// In normal mode (verbose == false), it renders an allowlist of events as human-friendly single lines.
// In verbose mode (verbose == true), it outputs every Info record as a single JSON object line.
func NewPresenter(w io.Writer, verbose bool) slog.Handler {
	if verbose {
		return slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})
	}
	return &presenter{
		shared: &presenterShared{
			w:           w,
			lastFailure: make(map[string]string),
			ready:       make(map[string]struct{}),
		},
	}
}

// withGreeting makes a normal-mode presenter write greeting once, on the first
// handled record. Verbose handlers are JSON and are returned unchanged.
// Operational failures that never log therefore stay a single line.
func withGreeting(h slog.Handler, greeting []byte) slog.Handler {
	p, ok := h.(*presenter)
	if !ok || len(greeting) == 0 {
		return h
	}
	p.shared.greeting = greeting
	return p
}

type presenterShared struct {
	mu          sync.Mutex
	w           io.Writer
	lastFailure map[string]string
	ready       map[string]struct{}
	gatewayAddr string
	greeting    []byte
	greeted     bool
}

type presenter struct {
	shared *presenterShared
	attrs  []slog.Attr
	groups []string
}

func (p *presenter) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (p *presenter) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return p
	}
	newAttrs := make([]slog.Attr, len(p.attrs)+len(attrs))
	copy(newAttrs, p.attrs)
	copy(newAttrs[len(p.attrs):], attrs)
	return &presenter{
		shared: p.shared,
		attrs:  newAttrs,
		groups: p.groups,
	}
}

func (p *presenter) WithGroup(name string) slog.Handler {
	if name == "" {
		return p
	}
	newGroups := make([]string, len(p.groups)+1)
	copy(newGroups, p.groups)
	newGroups[len(p.groups)] = name
	return &presenter{
		shared: p.shared,
		attrs:  p.attrs,
		groups: newGroups,
	}
}

func (p *presenter) Handle(_ context.Context, r slog.Record) error {
	if r.Level < slog.LevelInfo {
		return nil
	}

	attrs := extractAttrs(p, r)
	event := getString(attrs["event"])
	if event == "" {
		return nil
	}

	p.shared.mu.Lock()
	defer p.shared.mu.Unlock()

	if err := p.writeGreetingLocked(); err != nil {
		return err
	}

	var line string

	switch event {
	case "gateway_bound":
		addr := getString(attrs["addr"])
		if addr == "" {
			return nil
		}
		// The address is kept for the startup listing. It is not a line of its own.
		p.shared.gatewayAddr = addr
		return nil

	case "state":
		server := getString(attrs["server"])
		if server != "" {
			delete(p.shared.lastFailure, server)
		}
		state := getString(attrs["state"])
		if state == "READY" && server != "" {
			p.shared.ready[server] = struct{}{}
			line = fmt.Sprintf("server %s ready\n", server)
		} else {
			if server != "" {
				delete(p.shared.ready, server)
			}
			return nil
		}

	case "start_failed":
		server := getString(attrs["server"])
		if server == "" {
			return nil
		}
		delete(p.shared.ready, server)
		if p.shared.lastFailure[server] == "start_failed" {
			return nil
		}
		p.shared.lastFailure[server] = "start_failed"
		line = fmt.Sprintf("server %s failed to start\n", server)

	case "readiness_failed":
		server := getString(attrs["server"])
		if server == "" {
			return nil
		}
		delete(p.shared.ready, server)
		if p.shared.lastFailure[server] == "readiness_failed" {
			return nil
		}
		p.shared.lastFailure[server] = "readiness_failed"
		line = fmt.Sprintf("server %s failed to initialize\n", server)

	case "exit":
		server := getString(attrs["server"])
		if server == "" {
			return nil
		}
		delete(p.shared.ready, server)
		state := getString(attrs["state"])
		if state == "FAILED" {
			const key = "exit:FAILED"
			if p.shared.lastFailure[server] == key {
				return nil
			}
			p.shared.lastFailure[server] = key
			line = fmt.Sprintf("server %s exited unexpectedly\n", server)
		} else {
			return nil
		}

	case "startup_complete":
		// The human summary is the Local: listing, not a ready/failed count.
		// Disabled and failed servers are absent from the ready set.
		line = formatLocalLines(p.shared.gatewayAddr, p.shared.ready)
		if line == "" {
			return nil
		}

	case "shutdown_complete":
		line = "shutdown complete\n"

	default:
		return nil
	}

	_, err := p.shared.w.Write([]byte(line))
	return err
}

func (p *presenter) writeGreetingLocked() error {
	if p.shared.greeted || len(p.shared.greeting) == 0 {
		return nil
	}
	p.shared.greeted = true
	_, err := p.shared.w.Write(p.shared.greeting)
	return err
}

func formatLocalLines(addr string, ready map[string]struct{}) string {
	if addr == "" || len(ready) == 0 {
		return ""
	}
	names := make([]string, 0, len(ready))
	for name := range ready {
		names = append(names, name)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "Local: http://%s/mcp/%s\n", addr, name)
	}
	return b.String()
}

func extractAttrs(p *presenter, r slog.Record) map[string]slog.Value {
	attrs := make(map[string]slog.Value)

	var addAttr func(a slog.Attr)
	addAttr = func(a slog.Attr) {
		if a.Key == "" && a.Value.Kind() != slog.KindGroup {
			return
		}
		if a.Value.Kind() == slog.KindGroup {
			for _, ga := range a.Value.Group() {
				addAttr(ga)
			}
		} else {
			attrs[a.Key] = a.Value
		}
	}

	for _, a := range p.attrs {
		addAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		addAttr(a)
		return true
	})

	return attrs
}

func getString(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindAny:
		if v.Any() == nil {
			return ""
		}
		if s, ok := v.Any().(string); ok {
			return s
		}
		if s, ok := v.Any().(fmt.Stringer); ok {
			return s.String()
		}
	}
	return ""
}
