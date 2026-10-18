//go:build windows

package browser

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

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

const powershellWait = 5 * time.Second

func processTable(ctx context.Context) ([]byte, error) {
	out, err := nativeProcessTable()
	if err == nil {
		return out, nil
	}
	out, perr := powershellTable(ctx)
	if perr != nil {
		return nil, fmt.Errorf("%v; %v", err, perr)
	}
	return out, nil
}

func powershellTable(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, powershellWait)
	defer cancel()
	argv := catalog.Discovery.Process.Windows
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil && ctx.Err() != nil {
		return nil, fmt.Errorf("%s: no answer in %s", argv[0], powershellWait)
	}
	if err != nil {
		return nil, commandError(argv[0], err)
	}
	return out, nil
}

type cimProcess struct {
	ProcessID   uint32  `json:"ProcessId"`
	CommandLine *string `json:"CommandLine"`
}

func nativeProcessTable() ([]byte, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("the process snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var rows []cimProcess
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		row := cimProcess{ProcessID: e.ProcessID}
		if line, ok := processCommandLine(e.ProcessID); ok {
			row.CommandLine = &line
		}
		rows = append(rows, row)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, fmt.Errorf("the process snapshot: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("the process snapshot is empty")
	}
	return json.Marshal(rows)
}

func processCommandLine(pid uint32) (string, bool) {
	if pid == 0 {
		return "", false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	buf := make([]uint64, 512)
	for {
		var need uint32
		size := uint32(len(buf) * 8)
		st := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation, unsafe.Pointer(&buf[0]), size, &need)
		if st == nil {
			return (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])).String(), true
		}
		if need <= size || need > 1<<20 {
			return "", false
		}
		buf = make([]uint64, (need+7)/8)
	}
}
