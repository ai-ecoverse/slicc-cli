//go:build unix && !wasip1

package main

import (
	"os/exec"
	"syscall"
)

func detachLease(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
