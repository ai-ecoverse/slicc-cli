package integration

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const reapTimeout = 20 * time.Second

func reap(t *testing.T, cmd *exec.Cmd, what string) {
	t.Helper()
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	timer := time.NewTimer(reapTimeout)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
	}
	t.Errorf("%s (pid %d) was not reaped %s after SIGKILL\n%s", what, cmd.Process.Pid, reapTimeout, processReport(cmd.Process.Pid))
}

func processReport(pid int) string {
	p := strconv.Itoa(pid)
	var b strings.Builder
	probe := func(name string, args ...string) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		if len(lines) > 80 {
			lines = append(lines[:80], fmt.Sprintf("… %d more lines", len(lines)-80))
		}
		fmt.Fprintf(&b, "$ %s %s (%v)\n%s\n", name, strings.Join(args, " "), err, strings.Join(lines, "\n"))
	}
	if runtime.GOOS == "windows" {
		probe("tasklist", "/fi", "PID eq "+p, "/v")
		return b.String()
	}
	probe("ps", "-o", "pid,ppid,pgid,stat,wchan,etime,rss,command", "-p", p)
	probe("pgrep", "-l", "-P", p)
	probe("lsof", "-n", "-P", "-p", p)
	if runtime.GOOS == "darwin" {
		probe("sample", p, "1")
	} else {
		probe("cat", "/proc/"+p+"/stack")
		probe("cat", "/proc/"+p+"/status")
	}
	return b.String()
}
