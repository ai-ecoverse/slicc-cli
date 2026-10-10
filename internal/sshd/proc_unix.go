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

type procTree struct {
	pid int
}

func startTree(cmd *exec.Cmd) (*procTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &procTree{pid: cmd.Process.Pid}, nil
}

func (t *procTree) signal(name string) {
	signalPgid(t.pid, name)
}

func (t *procTree) interrupt() {
	signalPgid(t.pid, "INT")
}

func (t *procTree) release() {}

func signalPgid(pgid int, name string) {
	sig, ok := signalNames[name]
	if !ok || pgid <= 0 {
		return
	}
	if syscall.Kill(-pgid, sig) != nil {
		_ = syscall.Kill(pgid, sig)
	}
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
