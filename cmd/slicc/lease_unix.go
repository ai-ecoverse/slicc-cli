//go:build unix && !wasip1

package main

import (
	"os"
	"os/exec"
	ossignal "os/signal"
	"syscall"
)

func detachLease(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func ignoreStopSignals() func() {
	ossignal.Ignore(syscall.SIGTSTP)
	return func() { ossignal.Reset(syscall.SIGTSTP) }
}

func signalLeaseStop(p *os.Process) error {
	if p == nil {
		return nil
	}
	return p.Signal(syscall.SIGTERM)
}
