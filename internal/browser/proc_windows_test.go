//go:build windows

package browser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeProcessTableListsThisProcess(t *testing.T) {
	out, err := nativeProcessTable()
	if err != nil {
		t.Fatal(err)
	}
	var rows []cimProcess
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatal(err)
	}
	self := uint32(os.Getpid())
	for _, r := range rows {
		if r.ProcessID == self {
			if r.CommandLine == nil || !strings.Contains(*r.CommandLine, filepath.Base(os.Args[0])) {
				t.Fatalf("this process's command line: %v", r.CommandLine)
			}
			if hits := parseCIM(out, 0); len(hits) != 0 {
				t.Fatalf("hits without a debugging port: %+v", hits)
			}
			return
		}
	}
	t.Fatalf("pid %d is not in %d rows", self, len(rows))
}

func TestProcessCommandLineOfNoProcess(t *testing.T) {
	for _, pid := range []uint32{0, 0xfffffff0} {
		if line, ok := processCommandLine(pid); ok {
			t.Fatalf("pid %d: %q", pid, line)
		}
	}
}

func TestPowershellTableNamesItsFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "powershell.bat"), []byte("@echo boom 1>&2\r\n@exit /b 1\r\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	_, err := powershellTable(context.Background())
	if err == nil || !strings.Contains(err.Error(), "powershell: exit status 1: boom") {
		t.Fatalf("err %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := powershellTable(ctx); err == nil || !strings.Contains(err.Error(), "no answer in 5s") {
		t.Fatalf("a cancelled scan: %v", err)
	}
}
