//go:build !windows

package integration

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func tieToTest(cmd *exec.Cmd) func() (*lifeline, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	return func() (*lifeline, error) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		pgid := strconv.Itoa(cmd.Process.Pid)
		watch := exec.Command("/bin/sh", "-c", `read -r _; kill -9 -"$0" 2>/dev/null; exit 0`, pgid)
		watch.Stdin = r
		watch.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		err = watch.Start()
		_ = r.Close()
		if err != nil {
			_ = w.Close()
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return nil, err
		}
		return holdLifeline(func() {
			_ = w.Close()
			done := make(chan struct{})
			go func() {
				_ = watch.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				_ = watch.Process.Kill()
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		}), nil
	}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func killOrphan(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
}
