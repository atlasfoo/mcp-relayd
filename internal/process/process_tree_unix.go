//go:build unix

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type unixProcessTree struct {
	pid         int
	grace       time.Duration
	done        chan error
	exited      chan struct{}
	stopOnce    sync.Once
	stopped     chan struct{}
	stopErr     error
	groupMu     sync.Mutex
	groupClosed bool
}

func startProcessTree(cmd *exec.Cmd, grace time.Duration) (processTree, error) {
	// Bound inherited output pipes after root exit, including descendants
	// that detach from the process group and retain a pipe descriptor.
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = time.Second
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start process group: %w", err)
	}
	tree := &unixProcessTree{
		pid: cmd.Process.Pid, grace: max(grace, 0),
		done: make(chan error, 1), exited: make(chan struct{}), stopped: make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		// The root may exit while an upstream remains alive. Clean up the
		// still-addressable group before publishing termination, then never
		// signal that group ID again after cleanup has been confirmed.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = errors.Join(err, tree.signal(syscall.SIGKILL), tree.waitGroup(cleanupCtx))
		cancel()
		tree.done <- err
		close(tree.done)
		close(tree.exited)
	}()
	return tree, nil
}

func (t *unixProcessTree) PID() int { return t.pid }

func (t *unixProcessTree) Done() <-chan error { return t.done }

func (t *unixProcessTree) Stop(ctx context.Context) error {
	t.stopOnce.Do(func() { go t.stop(ctx) })
	select {
	case <-t.stopped:
		return t.stopErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *unixProcessTree) signal(signal syscall.Signal) error {
	t.groupMu.Lock()
	defer t.groupMu.Unlock()
	if t.groupClosed {
		return nil
	}
	err := syscall.Kill(-t.pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		t.groupClosed = true
		return nil
	}
	return err
}

// groupGone distinguishes dead orphan zombies from running descendants on
// Linux. Zombies belong to their new parent to reap and cannot execute work.
func (t *unixProcessTree) groupGone() bool {
	if errors.Is(syscall.Kill(-t.pid, 0), syscall.ESRCH) {
		return true
	}
	if runtime.GOOS != "linux" {
		return false
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false
		}
		fields := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
		if len(fields) < 3 {
			return false
		}
		if fields[2] == strconv.Itoa(t.pid) && fields[0] != "Z" && fields[0] != "X" {
			return false
		}
	}
	return true
}

func (t *unixProcessTree) waitGroup(ctx context.Context) error {
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		t.groupMu.Lock()
		if t.groupClosed || t.groupGone() {
			t.groupClosed = true
			t.groupMu.Unlock()
			return nil
		}
		t.groupMu.Unlock()
		select {
		case <-poll.C:
		case <-ctx.Done():
			return fmt.Errorf("wait for process group termination: %w", ctx.Err())
		}
	}
}

func (t *unixProcessTree) stop(ctx context.Context) {
	defer close(t.stopped)
	cleanupCtx, cancel := context.WithTimeout(ctx, t.grace+time.Second)
	defer cancel()
	if err := t.signal(syscall.SIGTERM); err != nil {
		t.stopErr = fmt.Errorf("terminate process group: %w", err)
	}
	grace := time.NewTimer(t.grace)
	defer grace.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	waiting := true
	for waiting {
		select {
		case <-grace.C:
			waiting = false
		case <-cleanupCtx.Done():
			waiting = false
		case <-poll.C:
			if errors.Is(syscall.Kill(-t.pid, 0), syscall.ESRCH) {
				waiting = false
			}
		}
	}
	if err := t.signal(syscall.SIGKILL); err != nil {
		t.stopErr = errors.Join(t.stopErr, fmt.Errorf("kill process group: %w", err))
	}
	// Wait always runs independently of caller cancellation, so an expired
	// shutdown deadline cannot leave the direct child unreaped.
	select {
	case <-t.exited:
		t.stopErr = errors.Join(t.stopErr, t.waitGroup(cleanupCtx))
	case <-cleanupCtx.Done():
		t.stopErr = errors.Join(t.stopErr, cleanupCtx.Err())
	}
}
