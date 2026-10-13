//go:build windows

package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessTree struct {
	pid       int
	groupID   uint32
	job       windows.Handle
	grace     time.Duration
	done      chan error
	rootExit  chan error
	stop      chan context.Context
	finished  chan struct{}
	stopOnce  sync.Once
	stopError error
}

func startProcessTree(cmd *exec.Cmd, grace time.Duration) (processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create process job: %w", err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	//nolint:gosec // The Windows API requires a pointer to this exact native job-limit structure.
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("configure process job: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	// Bound os/exec's pipe-copy goroutines when descendants inherit a pipe.
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = max(grace, 100*time.Millisecond)
	}
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	groupID := uint32(cmd.Process.Pid) //nolint:gosec // Windows process IDs originate from a DWORD in CreateProcess.
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, groupID)
	if err == nil {
		// os/exec does not expose a suspended launch. Assignment precedes the
		// return, but descendants launched between Start and assignment can escape.
		err = windows.AssignProcessToJobObject(job, process)
		_ = windows.CloseHandle(process)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("contain process in job: %w", err)
	}
	tree := &windowsProcessTree{
		pid: cmd.Process.Pid, groupID: groupID, job: job, grace: max(grace, 0),
		done: make(chan error, 1), rootExit: make(chan error, 1),
		stop: make(chan context.Context, 1), finished: make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		tree.rootExit <- err
		tree.done <- err
		close(tree.done)
	}()
	go tree.cleanup()
	return tree, nil
}

func (t *windowsProcessTree) PID() int { return t.pid }

func (t *windowsProcessTree) Done() <-chan error { return t.done }

func (t *windowsProcessTree) Stop(ctx context.Context) error {
	t.stopOnce.Do(func() { t.stop <- ctx })
	select {
	case <-t.finished:
		return t.stopError
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *windowsProcessTree) cleanup() {
	defer close(t.finished)
	rootExited := false
	select {
	case <-t.rootExit:
		rootExited = true
	case ctx := <-t.stop:
		// A detached daemon may have no console. Failure here still escalates
		// to Job Object termination after the bounded cooperative interval.
		_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, t.groupID)
		timer := time.NewTimer(t.grace)
		select {
		case <-t.rootExit:
			rootExited = true
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
	}
	// Also kill surviving descendants when the root exits unexpectedly.
	terminateErr := windows.TerminateJobObject(t.job, 1)
	waitErr := t.waitForJobExit(rootExited)
	closeErr := windows.CloseHandle(t.job)
	t.stopError = errors.Join(terminateErr, waitErr, closeErr)
}

// Windows exposes this accounting layout through QueryInformationJobObject.
type jobAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func (t *windowsProcessTree) waitForJobExit(rootExited bool) error {
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var accounting jobAccounting
		//nolint:gosec // The native API writes only the declared size of this Windows accounting structure.
		if err := windows.QueryInformationJobObject(t.job, windows.JobObjectBasicAccountingInformation,
			uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil); err != nil {
			return fmt.Errorf("query process job: %w", err)
		}
		if !rootExited {
			select {
			case <-t.rootExit:
				rootExited = true
			default:
			}
		}
		if rootExited && accounting.ActiveProcesses == 0 {
			return nil
		}
		select {
		case <-deadline.C:
			return errors.New("timed out waiting for process job termination")
		case <-ticker.C:
		}
	}
}
