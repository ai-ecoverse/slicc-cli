//go:build !windows

package sshd

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

var signalNames = map[string]syscall.Signal{
	"ABRT": syscall.SIGABRT,
	"ALRM": syscall.SIGALRM,
	"FPE":  syscall.SIGFPE,
	"HUP":  syscall.SIGHUP,
	"ILL":  syscall.SIGILL,
	"INT":  syscall.SIGINT,
	"KILL": syscall.SIGKILL,
	"PIPE": syscall.SIGPIPE,
	"QUIT": syscall.SIGQUIT,
	"SEGV": syscall.SIGSEGV,
	"TERM": syscall.SIGTERM,
	"USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
}

func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalGroup(p *os.Process, name string) {
	if p != nil {
		signalPgid(p.Pid, name)
	}
}

func signalPgid(pgid int, name string) {
	sig, ok := signalNames[name]
	if !ok || pgid <= 0 {
		return
	}
	if syscall.Kill(-pgid, sig) != nil {
		_ = syscall.Kill(pgid, sig)
	}
}

func interrupt(p *os.Process) {
	signalGroup(p, "INT")
}

func exitFrom(ps *os.ProcessState, err error) exitStatus {
	if ps == nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			ps = ee.ProcessState
		} else {
			return exitStatus{code: 1}
		}
	}
	ws, ok := ps.Sys().(syscall.WaitStatus)
	if ok && ws.Signaled() {
		for name, sig := range signalNames {
			if sig == ws.Signal() {
				return exitStatus{signal: name, core: ws.CoreDump()}
			}
		}
		return exitStatus{code: 128 + int(ws.Signal())}
	}
	return exitStatus{code: ps.ExitCode()}
}
