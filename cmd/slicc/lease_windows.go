package main

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func detachLease(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}

func ignoreStopSignals() func() { return func() {} }

func signalLeaseStop(p *os.Process) error {
	if p == nil {
		return nil
	}
	return p.Kill()
}
