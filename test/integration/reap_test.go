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
		if len(lines) > 400 {
			lines = append(lines[:400], fmt.Sprintf("… %d more lines", len(lines)-400))
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
		probe("sudo", "-n", "spindump", p, "2", "-stdout", "-noProcessingWhileSampling")
		probe("sudo", "-n", "spindump", p, "2", "-stdout")
	} else {
		probe("cat", "/proc/"+p+"/stack")
		probe("cat", "/proc/"+p+"/status")
	}
	return b.String()
}

func chromeTree(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if runtime.GOOS == "windows" {
		out, err := exec.CommandContext(ctx, "tasklist", "/fi", "IMAGENAME eq chrome.exe").CombinedOutput()
		return fmt.Sprintf("$ tasklist (%v)\n%s\n", err, out)
	}
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=,stat=,%cpu=,rss=,etime=,command=").CombinedOutput()
	tree := map[string]bool{strconv.Itoa(pid): true}
	rows := strings.Split(string(out), "\n")
	for grew := true; grew; {
		grew = false
		for _, row := range rows {
			f := strings.Fields(row)
			if len(f) > 1 && tree[f[1]] && !tree[f[0]] {
				tree[f[0]], grew = true, true
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Chrome process tree (%v):\n", err)
	for _, row := range rows {
		if f := strings.Fields(row); len(f) > 0 && tree[f[0]] {
			if len(row) > 240 {
				row = row[:240]
			}
			b.WriteString("  " + row + "\n")
		}
	}
	return b.String()
}

func chromeSample(pid int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sample", strconv.Itoa(pid), "2").CombinedOutput()
	lines := strings.Split(string(out), "\n")
	if len(lines) > 300 {
		lines = lines[:300]
	}
	return fmt.Sprintf("$ sample %d 2 (%v)\n%s\n", pid, err, strings.Join(lines, "\n"))
}
