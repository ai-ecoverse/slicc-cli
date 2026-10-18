//go:build !windows

package browser

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
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

func processTable(ctx context.Context) ([]byte, error) {
	return listProcesses(ctx, catalog.Discovery.Process.Unix, runtime.GOOS, procRoot())
}

func listProcesses(ctx context.Context, argv []string, goos, root string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	if err == nil {
		return out, nil
	}
	err = commandError(argv[0], err)
	if goos != "linux" {
		return nil, err
	}
	out, perr := procTable(root)
	if perr != nil {
		return nil, fmt.Errorf("%v; %s: %v", err, root, perr)
	}
	return out, nil
}

func commandError(name string, err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if line, _, _ := strings.Cut(strings.TrimSpace(string(exit.Stderr)), "\n"); line != "" {
			return fmt.Errorf("%s: %v: %s", name, err, line)
		}
	}
	if strings.HasPrefix(err.Error(), name) || strings.Contains(err.Error(), `"`+name+`"`) {
		return err
	}
	return fmt.Errorf("%s: %v", name, err)
}
