//go:build windows

package integration

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

func tieToTest(cmd *exec.Cmd) func() (*lifeline, error) {
	return func() (*lifeline, error) {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return nil, err
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE}}
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			_ = windows.CloseHandle(job)
			return nil, err
		}
		h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
		if err != nil {
			_ = windows.CloseHandle(job)
			return nil, err
		}
		err = windows.AssignProcessToJobObject(job, h)
		_ = windows.CloseHandle(h)
		if err != nil {
			_ = windows.CloseHandle(job)
			return nil, err
		}
		return holdLifeline(func() { _ = windows.CloseHandle(job) }), nil
	}
}

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, _ := windows.WaitForSingleObject(h, 0)
	return ev == uint32(windows.WAIT_TIMEOUT)
}

func killOrphan(pid int) {
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return
	}
	_ = windows.TerminateProcess(h, 1)
	_ = windows.CloseHandle(h)
}
