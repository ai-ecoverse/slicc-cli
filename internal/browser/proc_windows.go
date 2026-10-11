//go:build windows

package browser

import (
	"context"
	"encoding/csv"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

func pidAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil {
		return false
	}
	return code == 259
}

func signalTree(pid, extra int, force bool) {
	for _, p := range []int{pid, extra} {
		if p <= 0 {
			continue
		}
		args := []string{"/T", "/PID", strconv.Itoa(p)}
		if force {
			args = append([]string{"/F"}, args...)
		}
		cmd := exec.Command("taskkill", args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		_ = cmd.Run()
	}
}

func findProcess(ctx context.Context, exe string) int {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	image := filepath.Base(exe)
	out, err := exec.CommandContext(ctx, "tasklist", "/FO", "CSV", "/NH", "/FI", "IMAGENAME eq "+image).Output()
	if err != nil {
		return 0
	}
	return matchTasklist(string(out), image)
}

func matchTasklist(out, image string) int {
	r := csv.NewReader(strings.NewReader(out))
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		return 0
	}
	for _, rec := range records {
		if len(rec) > 1 && strings.EqualFold(rec[0], image) {
			if pid, err := strconv.Atoi(rec[1]); err == nil && pid > 0 {
				return pid
			}
		}
	}
	return 0
}

func processTable(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	argv := catalog.Discovery.Process.Windows
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Output()
}
