package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

type launchEvent struct {
	Event        string `json:"event"`
	ID           string `json:"id"`
	PID          int    `json:"pid"`
	Profile      string `json:"profile"`
	ProfileDir   string `json:"profileDir"`
	CDP          string `json:"cdp"`
	WebSocketURL string `json:"webSocketDebuggerUrl"`
	Message      string `json:"message"`
}

func launchEnv(t *testing.T) ([]string, string) {
	t.Helper()
	root := t.TempDir()
	set := map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(root, ".config"),
		"APPDATA":         filepath.Join(root, "Roaming"),
	}
	if runtime.GOOS != "windows" {
		set["HOME"] = root
	}
	return overrideEnv(os.Environ(), set), root
}

func requireChrome(t *testing.T, env []string) {
	t.Helper()
	r, _ := browsersJSON(t, env)
	for _, e := range r.Entries {
		if e.Kind == browser.KindInstalled && e.ID == "chrome" {
			return
		}
	}
	t.Skip("no Google Chrome found on this machine; skipping the real launch")
}

type launched struct {
	cmd    *exec.Cmd
	events *bufio.Scanner
	stderr *strings.Builder
	exit   chan error
}

func startLaunch(t *testing.T, env []string, args ...string) *launched {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"launch", "--json"}, args...)...)
	cmd.Env = env
	prepareInterruptible(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	l := &launched{cmd: cmd, events: bufio.NewScanner(stdout), stderr: &strings.Builder{}, exit: make(chan error, 1)}
	cmd.Stderr = l.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = interrupt(cmd)
			select {
			case <-l.exit:
			case <-time.After(20 * time.Second):
				_ = cmd.Process.Kill()
			}
		}
	})
	return l
}

func (l *launched) next(t *testing.T) launchEvent {
	t.Helper()
	ch := make(chan launchEvent, 1)
	go func() {
		var e launchEvent
		if l.events.Scan() {
			_ = json.Unmarshal(l.events.Bytes(), &e)
		}
		ch <- e
	}()
	select {
	case e := <-ch:
		return e
	case <-time.After(60 * time.Second):
		t.Fatalf("no event from slicc launch; stderr %s", l.stderr.String())
	}
	return launchEvent{}
}

func (l *launched) wait(t *testing.T) int {
	t.Helper()
	go func() { l.exit <- l.cmd.Wait() }()
	select {
	case <-l.exit:
	case <-time.After(30 * time.Second):
		t.Fatalf("slicc launch did not exit; stderr %s", l.stderr.String())
	}
	return l.cmd.ProcessState.ExitCode()
}

func getVersion(base string) (map[string]any, error) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(base + "/json/version")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var v map[string]any
	return v, json.Unmarshal(b, &v)
}

func TestLaunchRealChromeAndCloseOnInterrupt(t *testing.T) {
	env, root := launchEnv(t)
	requireChrome(t, env)
	profile := "it-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	l := startLaunch(t, env, "--browser", "chrome", "--profile", profile, "--headless", "--url", "about:blank")
	ev := l.next(t)
	if ev.Event != "launched" {
		t.Fatalf("first event %+v; stderr %s", ev, l.stderr.String())
	}
	if ev.ID != "chrome" || ev.Profile != profile || !strings.HasPrefix(ev.ProfileDir, root) || filepath.Base(ev.ProfileDir) != profile {
		t.Errorf("launched event %+v outside %s", ev, root)
	}
	if !strings.HasPrefix(ev.CDP, "http://127.0.0.1:") || !strings.HasPrefix(ev.WebSocketURL, "ws://127.0.0.1:") || ev.PID <= 0 {
		t.Errorf("launched event %+v", ev)
	}
	v, err := getVersion(ev.CDP)
	if err != nil || v["webSocketDebuggerUrl"] != ev.WebSocketURL {
		t.Fatalf("/json/version: %v %v", v, err)
	}
	port, ws, err := browser.ReadActivePort(ev.ProfileDir)
	if err != nil || !strings.HasSuffix(ev.CDP, ":"+strconv.Itoa(port)) || !strings.HasSuffix(ev.WebSocketURL, ws) {
		t.Errorf("DevToolsActivePort %d %q %v", port, ws, err)
	}

	second := startLaunch(t, env, "--browser", "chrome", "--profile", profile, "--headless")
	if e := second.next(t); e.Event != "error" || !strings.Contains(e.Message, "in use") {
		t.Errorf("second launch on the same profile: %+v", e)
	}
	if code := second.wait(t); code != 1 {
		t.Errorf("second launch exit %d", code)
	}

	if err := interrupt(l.cmd); err != nil {
		t.Fatal(err)
	}
	if e := l.next(t); e.Event != "closed" {
		t.Errorf("after interrupt: %+v; stderr %s", e, l.stderr.String())
	}
	if code := l.wait(t); code != 0 {
		t.Errorf("exit %d; stderr %s", code, l.stderr.String())
	}
	if _, err := getVersion(ev.CDP); err == nil {
		t.Errorf("CDP still answers at %s after close", ev.CDP)
	}
}

func TestLaunchPackageAPIClosesBrowser(t *testing.T) {
	env, root := launchEnv(t)
	requireChrome(t, env)
	installed := browser.DiscoverInstalled(context.Background(), browser.Options{})
	chrome, ok := browser.FindInstalled(installed, "chrome")
	if !ok {
		t.Skip("no Google Chrome found on this machine")
	}
	profiles := filepath.Join(root, "profiles")
	b, err := browser.Launch(context.Background(), chrome, browser.LaunchOptions{
		Options: browser.Options{ProfilesDir: profiles}, Profile: "api", Headless: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.ProfileDir != filepath.Join(profiles, "api") || b.CDP == "" {
		t.Errorf("%+v", b)
	}
	if _, err := getVersion(b.CDP); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := b.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.Done():
	case <-time.After(5 * time.Second):
		t.Error("Done not closed after Close")
	}
	if _, err := getVersion(b.CDP); err == nil {
		t.Error("CDP still answers after Close")
	}
}

func TestLaunchRejectsBadInput(t *testing.T) {
	env, _ := launchEnv(t)
	_, errOut, code := runWithEnv(t, env, "launch", "--profile", "../x")
	if code != 2 || !strings.Contains(errOut, "invalid profile name") {
		t.Errorf("code %d stderr %q", code, errOut)
	}
	out, _, code := runWithEnv(t, env, "launch", "--json", "--browser", "no-such-browser")
	if code != 1 || !strings.Contains(out, `"event":"error"`) || !strings.Contains(out, "no-such-browser") {
		t.Errorf("code %d stdout %q", code, out)
	}
}

func TestLaunchReportsBrowserExit(t *testing.T) {
	env, _ := launchEnv(t)
	requireChrome(t, env)
	l := startLaunch(t, env, "--browser", "chrome", "--profile", "exit", "--headless")
	ev := l.next(t)
	if ev.Event != "launched" {
		t.Fatalf("%+v; stderr %s", ev, l.stderr.String())
	}
	p, err := os.FindProcess(ev.PID)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Kill(); err != nil {
		t.Fatal(err)
	}
	if e := l.next(t); e.Event != "exited" {
		t.Errorf("after the browser died: %+v", e)
	}
	if code := l.wait(t); code != 0 {
		t.Errorf("exit %d; stderr %s", code, l.stderr.String())
	}
}
