//go:build windows

package sshd

import (
	"os"
	"os/exec"
)

var terminating = map[string]bool{"HUP": true, "INT": true, "KILL": true, "QUIT": true, "TERM": true, "ABRT": true}

func setProcAttr(*exec.Cmd) {}

func signalGroup(p *os.Process, name string) {
	if p != nil && terminating[name] {
		_ = p.Kill()
	}
}

func interrupt(*os.Process) {}

func exitFrom(ps *os.ProcessState, _ error) exitStatus {
	if ps == nil {
		return exitStatus{code: 1}
	}
	return exitStatus{code: ps.ExitCode()}
}
