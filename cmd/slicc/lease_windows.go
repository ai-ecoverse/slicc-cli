package main

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
)

func detachLease(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}
