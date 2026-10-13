//go:build windows

package process

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	windowsTreeHelperModeEnv = "MCP_RELAYD_WINDOWS_TREE_HELPER_MODE"
	windowsTreeGateEnv       = "MCP_RELAYD_WINDOWS_TREE_GATE"
	windowsTreeAddressEnv    = "MCP_RELAYD_WINDOWS_TREE_ADDRESS"
)

func TestWindowsProcessTreeStopCleansDescendants(t *testing.T) {
	dir := t.TempDir()
	gatePath := filepath.Join(dir, "start-descendant")
	addressPath := filepath.Join(dir, "descendant-address")

	cmd := windowsTreeHelperCommand("root")
	cmd.Env = append(cmd.Env,
		windowsTreeGateEnv+"="+gatePath,
		windowsTreeAddressEnv+"="+addressPath,
	)
	tree, err := startProcessTree(cmd, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("startProcessTree() error = %v", err)
	}
	defer stopWindowsTestTree(t, tree)
	if tree.PID() <= 0 {
		t.Fatalf("process tree PID() = %d, want a positive process ID", tree.PID())
	}

	if err := os.WriteFile(gatePath, nil, 0o600); err != nil {
		t.Fatalf("release root helper: %v", err)
	}
	address := waitForWindowsTreeAddress(t, addressPath)
	assertWindowsTreeListenerReachable(t, address)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tree.Stop(ctx); err != nil {
		t.Fatalf("process tree Stop() error = %v", err)
	}
	assertWindowsTreeDone(t, tree)
	assertWindowsTreeListenerClosed(t, address)
}

func TestWindowsProcessTreeStopClosesPipes(t *testing.T) {
	cmd := windowsTreeHelperCommand("root")
	cmd.Env = append(cmd.Env,
		windowsTreeGateEnv+"="+filepath.Join(t.TempDir(), "start-descendant"),
		windowsTreeAddressEnv+"="+filepath.Join(t.TempDir(), "descendant-address"),
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe() error = %v", err)
	}
	tree, err := startProcessTree(cmd, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("startProcessTree() error = %v", err)
	}
	defer stopWindowsTestTree(t, tree)
	if tree.PID() <= 0 {
		t.Fatalf("process tree PID() = %d, want a positive process ID", tree.PID())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tree.Stop(ctx); err != nil {
		t.Fatalf("process tree Stop() error = %v", err)
	}
	assertWindowsTreeDone(t, tree)
	assertWindowsTreePipeClosed(t, stdout)
	assertWindowsTreePipeClosed(t, stderr)
}

// TestWindowsProcessTreeHelper is the subprocess entry point used by the tests.
func TestWindowsProcessTreeHelper(t *testing.T) {
	switch os.Getenv(windowsTreeHelperModeEnv) {
	case "root":
		gatePath := os.Getenv(windowsTreeGateEnv)
		for {
			//nolint:gosec // The parent test passes its own temporary gate path to this subprocess.
			if _, err := os.Stat(gatePath); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		child := windowsTreeHelperCommand("leaf")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, "start leaf:", err)
			os.Exit(2)
		}
		_ = child.Wait()
	case "leaf":
		var listenConfig net.ListenConfig
		listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintln(os.Stderr, "listen:", err)
			os.Exit(2)
		}
		addressPath := os.Getenv(windowsTreeAddressEnv)
		//nolint:gosec // The parent test owns the temporary address path passed through this fixture environment.
		if err := os.WriteFile(addressPath, []byte(listener.Addr().String()), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "write address:", err)
			os.Exit(2)
		}
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}
}

func windowsTreeHelperCommand(mode string) *exec.Cmd {
	//nolint:gosec // Re-executes this test binary with a fixed helper selector; processTree owns cancellation.
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestWindowsProcessTreeHelper$")
	cmd.Env = append(os.Environ(), windowsTreeHelperModeEnv+"="+mode)
	return cmd
}

func waitForWindowsTreeAddress(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		//nolint:gosec // Only a temporary address file created by this test fixture is read.
		address, err := os.ReadFile(path)
		if err == nil && len(address) > 0 {
			return string(address)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant did not publish its listener address at %q", path)
	return ""
}

func assertWindowsTreeListenerReachable(t *testing.T, address string) {
	t.Helper()
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(t.Context(), "tcp", address)
	if err != nil {
		t.Fatalf("descendant listener is not reachable at %s: %v", address, err)
	}
	_ = conn.Close()
}

func assertWindowsTreeListenerClosed(t *testing.T, address string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	for time.Now().Before(deadline) {
		conn, err := dialer.DialContext(t.Context(), "tcp", address)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant listener at %s remained reachable after Stop()", address)
}

func assertWindowsTreePipeClosed(t *testing.T, pipe interface{ Read([]byte) (int, error) }) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		buffer := make([]byte, 1)
		_, err := pipe.Read(buffer)
		result <- err
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatalf("pipe Read() error = %v, want closed pipe", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pipe remained open after Stop()")
	}
}

func assertWindowsTreeDone(t *testing.T, tree processTree) {
	t.Helper()
	select {
	case <-tree.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() did not report root process exit after Stop()")
	}
}

func stopWindowsTestTree(t *testing.T, tree processTree) {
	t.Helper()
	select {
	case <-tree.Done():
		return
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tree.Stop(ctx); err != nil {
		t.Errorf("cleanup process tree: %v", err)
	}
}
