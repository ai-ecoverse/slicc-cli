//go:build !windows

package browser

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func signalTree(pid, extra int, force bool) {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if pid > 0 {
		_ = syscall.Kill(-pid, sig)
		_ = syscall.Kill(pid, sig)
	}
	if extra > 0 && extra != pid {
		_ = syscall.Kill(extra, sig)
	}
}

func findProcess(ctx context.Context, exe string) int {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-ax", "-o", "pid=,args=").Output()
	if err != nil {
		return 0
	}
	return matchProcess(string(out), exe)
}
