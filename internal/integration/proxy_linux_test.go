package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"mcp-relayd/internal/process"
)

// Linux process inspection distinguishes terminated orphan zombies from live
// descendants. The manager must reap its direct child; orphan reaping belongs
// to PID 1 and may be delayed in a container.
func runningPID(pid int) (bool, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, rest, ok := strings.Cut(string(data), ") ")
	if !ok || rest == "" {
		return false, fmt.Errorf("malformed process stat for %d", pid)
	}
	return rest[0] != 'Z' && rest[0] != 'X', nil
}

func TestRealProxyBoundedTreeCleanup(t *testing.T) {
	for _, forced := range []bool{false, true} {
		name := "graceful"
		if forced {
			name = "forced"
		}
		t.Run(name, func(t *testing.T) {
			flags := make(map[string][]string)
			if forced {
				flags["alpha"] = []string{"--ignore-sigterm"}
				flags["beta"] = []string{"--ignore-sigterm"}
			}
			h := newProxyHarness(t, 5*time.Second, flags)
			h.ready(t, "alpha", "beta")
			var pids []int
			for _, server := range []string{"alpha", "beta"} {
				client := h.client(t, server)
				info, err := client.tool(testContext(t), "fixture_info", map[string]any{})
				if err != nil {
					t.Fatal(err)
				}
				var identity string
				var upstream, count int
				if _, err := fmt.Sscanf(info, "identity=%s pid=%d init_count=%d", &identity, &upstream, &count); err != nil || upstream <= 0 {
					t.Fatalf("parse upstream PID: %q (%v)", info, err)
				}
				proxy := h.manager.ProxyPID(server)
				if proxy <= 0 || proxy == upstream {
					t.Fatalf("invalid proxy/upstream PIDs: %d/%d", proxy, upstream)
				}
				pids = append(pids, proxy, upstream)
				if forced {
					// Prevent proxy shutdown from closing upstream stdin: ignoring
					// SIGTERM alone does not prevent a fixture's clean EOF exit.
					if err := syscall.Kill(proxy, syscall.SIGSTOP); err != nil {
						t.Fatal(err)
					}
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			start := time.Now()
			if err := h.manager.Stop(ctx); err != nil {
				t.Errorf("bounded %s shutdown: %v", name, err)
			}
			if elapsed := time.Since(start); elapsed >= 4*time.Second {
				t.Errorf("two-server shutdown exceeded global budget: %s", elapsed)
			}
			for _, server := range []string{"alpha", "beta"} {
				if h.manager.State(server) != process.StateStopped || h.manager.ProxyPID(server) != 0 {
					t.Errorf("%s not stopped/reaped", server)
				}
				if !forced {
					// A cooperative fixture reports stopped only after stdin EOF.
					// Killing the whole group immediately is bounded cleanup, but
					// does not satisfy the graceful stdio-close contract.
					data, err := os.ReadFile(h.events[server]) // #nosec G304 -- Test-owned lifecycle file.
					if err != nil {
						t.Fatal(err)
					}
					stopped := 0
					for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
						var event struct {
							Event string `json:"event"`
						}
						if err := json.Unmarshal([]byte(line), &event); err != nil {
							t.Fatal(err)
						}
						if event.Event == "stopped" {
							stopped++
						}
					}
					if stopped != 1 {
						t.Errorf("%s cooperative upstream did not close gracefully: stopped events=%d, want 1", server, stopped)
					}
				}
			}
			deadline := time.Now().Add(time.Second)
			for _, pid := range pids {
				for {
					running, err := runningPID(pid)
					if err != nil {
						t.Fatal(err)
					}
					if !running {
						break
					}
					if time.Now().After(deadline) {
						t.Errorf("%s cleanup left live PID %d", name, pid)
						break
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
		})
	}
}
