//go:build windows

package sshd

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var terminating = map[string]bool{"HUP": true, "INT": true, "KILL": true, "QUIT": true, "TERM": true, "ABRT": true}

type procTree struct {
	mu  sync.Mutex
	job windows.Handle
}

func startTree(cmd *exec.Cmd) (*procTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(process) }()
	t, err := adoptSuspended(process, cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	return t, nil
}

func adoptSuspended(process windows.Handle, pid int) (*procTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	t := &procTree{job: job}
	if err := resumeThreads(pid); err != nil {
		t.signal("KILL")
		t.release()
		return nil, err
	}
	return t, nil
}

func resumeThreads(pid int) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := 0
	for err = windows.Thread32First(snap, &entry); err == nil; err = windows.Thread32Next(snap, &entry) {
		if entry.OwnerProcessID != uint32(pid) {
			continue
		}
		thread, oerr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if oerr != nil {
			return oerr
		}
		_, rerr := windows.ResumeThread(thread)
		_ = windows.CloseHandle(thread)
		if rerr != nil {
			return rerr
		}
		resumed++
	}
	if resumed == 0 {
		return errors.New("sshd: no thread to resume")
	}
	return nil
}

func (t *procTree) signal(name string) {
	if !terminating[name] {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		_ = windows.TerminateJobObject(t.job, 1)
	}
}

func (t *procTree) interrupt() {}

func (t *procTree) release() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job != 0 {
		_ = windows.CloseHandle(t.job)
		t.job = 0
	}
}

func exitFrom(ps *os.ProcessState, _ error) exitStatus {
	if ps == nil {
		return exitStatus{code: 1}
	}
	return exitStatus{code: ps.ExitCode()}
}
