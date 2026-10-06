//go:build unix

package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const processTreeTestMode = "MCP_RELAYD_PROCESS_TREE_TEST_MODE"

func TestUnixProcessTreeHelper(t *testing.T) {
	switch os.Getenv(processTreeTestMode) {
	case "parent", "parent-exit":
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		ready, readyWriter, err := os.Pipe()
		if err != nil {
			os.Exit(2)
		}
		child := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestUnixProcessTreeHelper$") // #nosec G204,G702 -- Re-exec the current test binary to create a controlled descendant.
		child.Env = processTreeHelperEnv("ignore-term")
		child.Stdout = readyWriter
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_ = readyWriter.Close()
		if _, err := bufio.NewReader(ready).ReadString('\n'); err != nil {
			os.Exit(2)
		}
		if _, err := fmt.Fprintln(os.Stdout, child.Process.Pid); err != nil {
			os.Exit(2)
		}
		if os.Getenv(processTreeTestMode) == "parent-exit" {
			if _, err := bufio.NewReader(os.Stdin).ReadByte(); err != nil {
				os.Exit(2)
			}
			os.Exit(0)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "ignore-term":
		signal.Ignore(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		_, _ = fmt.Fprintln(os.Stdout, "ready")
		for {
			time.Sleep(time.Hour)
		}
	}
}

func TestStopUnixProcessTerminatesAndReapsDescendantTree(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestUnixProcessTreeHelper$") // #nosec G204,G702 -- Re-exec the current test binary as the supervised helper.
	cmd.Env = processTreeHelperEnv("parent")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	cmd.Stderr = os.Stderr
	grace := 100 * time.Millisecond
	tree, err := startProcessTree(cmd, grace)
	if err != nil {
		t.Fatalf("startProcessTree() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tree.Stop(ctx)
	}()
	if err := waitForProcess(tree.PID(), true, time.Second); err != nil {
		t.Fatalf("root process was not running after start: %v", err)
	}

	childPID, err := readProcessPID(stdout)
	if err != nil {
		t.Fatalf("read descendant PID: %v", err)
	}
	if err := waitForProcess(childPID, true, time.Second); err != nil {
		t.Fatalf("descendant was not running before shutdown: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := tree.Stop(ctx); err != nil {
		t.Fatalf("processTree.Stop() error = %v", err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("processTree.Stop() returned without waiting for and reaping the root process")
	}
	select {
	case _, ok := <-tree.Done():
		if !ok {
			t.Fatal("processTree.Done() closed without reporting root process termination")
		}
	case <-time.After(time.Second):
		t.Fatal("processTree.Done() did not report root process termination")
	}
	if err := waitForProcess(childPID, false, time.Second); err != nil {
		t.Fatalf("descendant %d remained after process-tree shutdown: %v", childPID, err)
	}
}

func TestUnixRootExitTerminatesSurvivingDescendant(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestUnixProcessTreeHelper$") // #nosec G204,G702 -- Re-exec the controlled helper to verify orphan cleanup.
	cmd.Env = processTreeHelperEnv("parent-exit")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	tree, err := startProcessTree(cmd, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = tree.Stop(ctx)
	})
	childPID, err := readProcessPID(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitForProcess(childPID, true, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := stdin.Write([]byte{'\n'}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-tree.Done():
		if err != nil {
			t.Fatalf("natural root exit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("root exit cleanup did not finish")
	}
	if cmd.ProcessState == nil {
		t.Fatal("root exit was not reaped")
	}
	if err := waitForProcess(childPID, false, time.Second); err != nil {
		t.Fatalf("live descendant remained after root exit: %v", err)
	}
}

func processTreeHelperEnv(mode string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, processTreeTestMode+"=") {
			env = append(env, item)
		}
	}
	return append(env, processTreeTestMode+"="+mode)
}

func readProcessPID(stdout io.Reader) (int, error) {
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(line))
}

func waitForProcess(pid int, wantRunning bool, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		running := processExists(pid)
		if running == wantRunning {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("process %d running = %t, want %t", pid, processExists(pid), wantRunning)
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	if err != nil && err != syscall.EPERM {
		return false
	}
	if runtime.GOOS == "linux" {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid)) // #nosec G703 -- Integer PID forms a fixed procfs path; no caller-controlled path components.
		if errors.Is(err, os.ErrNotExist) {
			return false
		}
		if err == nil {
			fields := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
			if len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X") {
				return false
			}
		}
	}
	return true
}
