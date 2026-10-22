package integration

import (
	"bufio"
	"encoding/json"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func chromeScreen(t *testing.T) ([]string, []string) {
	t.Helper()
	cmd := exec.Command(requireNode(t), "testdata/chrome-screen.mjs")
	cmd.Env = realEnv
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Chrome screen exit: %v %s", err, stderr.String())
			}
			return
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
		select {
		case err := <-done:
			t.Errorf("Chrome screen did not close gracefully: %v", err)
		case <-time.After(reapTimeout):
			t.Error("Chrome screen was not reaped after SIGKILL")
		}
	})
	line := make(chan string, 1)
	go func() { s, _ := bufio.NewReader(stdout).ReadString('\n'); line <- s }()
	var screen struct {
		Flags   []string `json:"flags"`
		Display string   `json:"display"`
	}
	select {
	case s := <-line:
		if err := json.Unmarshal([]byte(s), &screen); err != nil || len(screen.Flags) == 0 {
			t.Fatalf("Chrome screen: %q %s", s, stderr.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("Chrome screen didn't start: %s", stderr.String())
	}
	env := make([]string, 0, len(realEnv)+1)
	for _, e := range realEnv {
		if !strings.HasPrefix(e, "DISPLAY=") {
			env = append(env, e)
		}
	}
	if screen.Display != "" {
		env = append(env, "DISPLAY="+screen.Display)
	}
	return screen.Flags, env
}

func TestChromeScreenMatchesCIModeAndHasItsOwnDisplay(t *testing.T) {
	flags, env := chromeScreen(t)
	ci := realGetenv("CI")
	headless := ci == "" || ci == "false" || ci == "0"
	switch realGetenv("SLICC_HEADLESS") {
	case "1":
		headless = true
	case "0":
		headless = false
	}
	want := "--enable-unsafe-swiftshader"
	if headless {
		want = "--headless=new"
	}
	if len(flags) != 1 || flags[0] != want {
		t.Fatalf("mode %v, want %s", flags, want)
	}
	if runtime.GOOS == "linux" && !headless && ci != "" && ci != "false" && ci != "0" {
		var display string
		for _, e := range env {
			if strings.HasPrefix(e, "DISPLAY=") {
				display = strings.TrimPrefix(e, "DISPLAY=")
			}
		}
		if display == "" || display == realGetenv("DISPLAY") {
			t.Fatalf("headed CI shared inherited display %q", display)
		}
	}
}
