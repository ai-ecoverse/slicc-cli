package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "slicc-cli-it-")
	if err != nil {
		panic(err)
	}
	name := "slicc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary = filepath.Join(dir, name)
	home := filepath.Join(dir, "home")
	isolateProcess(home)
	build := exec.Command("go", "build", "-ldflags", "-X main.version=1.2.3-test", "-o", binary, "../../cmd/slicc")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}
	if err := guard.start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	stalls.start()
	code := m.Run()
	stalls.report()
	if hits := guard.stop(); len(hits) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: tests reached 127.0.0.1:%d-%d outside the fakes:\n  %s\n", firstRealPort, lastRealPort, strings.Join(hits, "\n  "))
		code = 1
	}
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func slicc(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

func TestVersionPrintsTheLinkedVersion(t *testing.T) {
	for _, flag := range []string{"--version", "-V"} {
		out, _, code := slicc(t, flag)
		if code != 0 || out != "slicc 1.2.3-test\n" {
			t.Fatalf("%s: code %d, output %q", flag, code, out)
		}
	}
}

func TestHelpGoesToStdout(t *testing.T) {
	out, _, code := slicc(t, "--help")
	if code != 0 || !strings.Contains(out, "usage:") {
		t.Fatalf("code %d, output %q", code, out)
	}
}

func TestUnknownCommandFailsWithUsage(t *testing.T) {
	_, errOut, code := slicc(t, "frobnicate")
	if code != 2 || !strings.Contains(errOut, `unknown command "frobnicate"`) || !strings.Contains(errOut, "usage:") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}
