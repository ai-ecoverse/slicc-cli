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
	"slices"
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
		"SLICC_DISCOVERY": "installed",
	}
	if runtime.GOOS != "windows" {
		set["HOME"] = root
	}
	return overrideEnv(os.Environ(), set), root
}

const testChromeNotice = "slicc's own Chrome launches never use this machine's Chrome off CI: set SLICC_TEST_CHROME to a Chrome for Testing (macOS) to run them"

func testChrome(t *testing.T, env []string) ([]string, string) {
	t.Helper()
	exe := os.Getenv("SLICC_TEST_CHROME")
	if exe != "" && runtime.GOOS == "darwin" {
		return env, linkTestChrome(t, env, exe)
	}
	if os.Getenv("CI") == "" {
		t.Skip(testChromeNotice)
	}
	r, _ := browsersJSON(t, env)
	for _, e := range r.Entries {
		if e.Kind == browser.KindInstalled && e.ID == "chrome" {
			return env, "chrome"
		}
	}
	t.Fatal("no Google Chrome found, and CI images have it")
	return nil, ""
}

func linkTestChrome(t *testing.T, env []string, exe string) string {
	t.Helper()
	app := exe
	for !strings.HasSuffix(app, ".app") {
		parent := filepath.Dir(app)
		if parent == app {
			t.Fatalf("SLICC_TEST_CHROME=%s is not inside an .app bundle", exe)
		}
		app = parent
	}
	apps := filepath.Join(envMap(env)["HOME"], "Applications")
	if err := os.MkdirAll(apps, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(apps, filepath.Base(app))
	if _, err := os.Lstat(link); err != nil {
		if err := os.Symlink(app, link); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := browsersJSON(t, env)
	for _, e := range r.Entries {
		if e.Kind == browser.KindInstalled && e.Installed != nil && e.Installed.Path == link {
			return e.ID
		}
	}
	t.Fatalf("slicc browsers did not find SLICC_TEST_CHROME at %s", link)
	return ""
}

type launched struct {
	cmd    *exec.Cmd
	events *bufio.Scanner
	stderr *syncBuffer
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
	l := &launched{cmd: cmd, events: bufio.NewScanner(stdout), stderr: &syncBuffer{}, exit: make(chan error, 1)}
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
	env, id := testChrome(t, env)
	profile := "it-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	l := startLaunch(t, env, "--browser", id, "--profile", profile, "--headless", "--url", "about:blank")
	ev := l.next(t)
	if ev.Event != "launched" {
		t.Fatalf("first event %+v; stderr %s", ev, l.stderr.String())
	}
	if ev.ID != id || ev.Profile != profile || !strings.HasPrefix(ev.ProfileDir, root) || filepath.Base(ev.ProfileDir) != profile {
		t.Errorf("launched event %+v outside %s", ev, root)
	}
	if !strings.HasPrefix(ev.CDP, "http://127.0.0.1:") || !strings.HasPrefix(ev.WebSocketURL, "ws://127.0.0.1:") || ev.PID <= 0 {
		t.Errorf("launched event %+v", ev)
	}
	v, err := getVersion(ev.CDP)
	if err != nil || v["webSocketDebuggerUrl"] != ev.WebSocketURL {
		t.Fatalf("/json/version: %v %v", v, err)
	}
	cmdline := chromeCommandLine(t, ev.CDP)
	assertSevenFlags(t, cmdline, true)
	for _, s := range browser.ThrowawaySwitches {
		if !slices.Contains(strings.Fields(cmdline), s) {
			t.Errorf("%s missing for a profile in a temporary home:\n%s", s, cmdline)
		}
	}
	port, ws, err := browser.ReadActivePort(ev.ProfileDir)
	if err != nil || !strings.HasSuffix(ev.CDP, ":"+strconv.Itoa(port)) || !strings.HasSuffix(ev.WebSocketURL, ws) {
		t.Errorf("DevToolsActivePort %d %q %v", port, ws, err)
	}

	second := startLaunch(t, env, "--browser", id, "--profile", profile, "--headless")
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
	assertSevenPrefs(t, readPrefs(t, ev.ProfileDir), true)
}

func assertSevenFlags(t *testing.T, cmdline string, want bool) {
	t.Helper()
	fields := strings.Fields(cmdline)
	var features []string
	for _, f := range fields {
		if v, ok := strings.CutPrefix(f, "--disable-features="); ok {
			features = append(features, v)
		}
	}
	has := func(s string) bool {
		for _, f := range fields {
			if f == s {
				return true
			}
		}
		return false
	}
	for _, s := range browser.SevenSwitches {
		if has(s) != want {
			t.Errorf("%s on the command line: %v, want %v\n%s", s, has(s), want, cmdline)
		}
	}
	if !want {
		if len(features) != 0 {
			t.Errorf("--disable-features without seven flags: %q", features)
		}
		return
	}
	if len(features) != 1 || features[0] != strings.Join(browser.SevenDisabledFeatures, ",") {
		t.Errorf("want one --disable-features=%s, got %q", strings.Join(browser.SevenDisabledFeatures, ","), features)
	}
}

func TestLaunchWithoutSevenFlags(t *testing.T) {
	env, _ := launchEnv(t)
	env, id := testChrome(t, env)
	l := startLaunch(t, env, "--browser", id, "--profile", "plain", "--headless", "--no-seven-flags")
	ev := l.next(t)
	if ev.Event != "launched" {
		t.Fatalf("%+v; stderr %s", ev, l.stderr.String())
	}
	if v, err := getVersion(ev.CDP); err != nil || v["webSocketDebuggerUrl"] != ev.WebSocketURL {
		t.Fatalf("/json/version: %v %v", v, err)
	}
	assertSevenFlags(t, chromeCommandLine(t, ev.CDP), false)
	assertSevenPrefs(t, readPrefs(t, ev.ProfileDir), false)
	if err := interrupt(l.cmd); err != nil {
		t.Fatal(err)
	}
	if e := l.next(t); e.Event != "closed" {
		t.Errorf("after interrupt: %+v", e)
	}
	l.wait(t)
}

func TestLaunchPackageAPIClosesBrowser(t *testing.T) {
	env, root := launchEnv(t)
	_, id := testChrome(t, env)
	t.Setenv(browser.DiscoveryEnv, "installed")
	installed := browser.DiscoverInstalled(context.Background(), browser.Options{Home: root})
	chrome, ok := browser.FindInstalled(installed, id)
	if !ok {
		t.Fatal("slicc browsers found Google Chrome but DiscoverInstalled did not")
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
	env, id := testChrome(t, env)
	l := startLaunch(t, env, "--browser", id, "--profile", "exit", "--headless")
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

func readPrefs(t *testing.T, profileDir string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(profileDir, "Default", "Preferences"))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}
		}
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Preferences: %v", err)
	}
	return m
}

func dig(m map[string]any, path ...string) any {
	var v any = m
	for _, k := range path {
		mm, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = mm[k]
	}
	return v
}

func assertSevenPrefs(t *testing.T, prefs map[string]any, want bool) {
	t.Helper()
	withTime, _ := dig(prefs, "performance_tuning", "tab_discarding", "exceptions_with_time").(map[string]any)
	_, hasSite := withTime[browser.SevenSitePattern]
	if !want {
		if hasSite || dig(prefs, "tab_freezing_enabled") != nil {
			t.Errorf("seven prefs without seven flags: %v", prefs["performance_tuning"])
		}
		return
	}
	if !hasSite {
		t.Errorf("no %s in exceptions_with_time: %v", browser.SevenSitePattern, prefs["performance_tuning"])
	}
	if dig(prefs, "tab_freezing_enabled") != false || dig(prefs, "performance_tuning", "tab_freezing", "enabled") != false {
		t.Errorf("tab freezing not off: %v %v", prefs["tab_freezing_enabled"], prefs["performance_tuning"])
	}
	if dig(prefs, "performance_tuning", "high_efficiency_mode", "state") != float64(0) {
		t.Errorf("Memory Saver not off: %v", prefs["performance_tuning"])
	}
	list, _ := dig(prefs, "performance_tuning", "tab_discarding", "exceptions").([]any)
	n := 0
	for _, e := range list {
		if e == browser.SevenSitePattern {
			n++
		}
	}
	if n != 1 {
		t.Errorf("exceptions list %v", list)
	}
}

func settingsPref(t *testing.T, base, key string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, base+"/json/new?chrome://settings/performance", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var target struct {
		WebSocketURL string `json:"webSocketDebuggerUrl"`
	}
	err = json.NewDecoder(resp.Body).Decode(&target)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	c, err := dialCDP(target.WebSocketURL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		res, err := c.call("Runtime.evaluate", map[string]any{
			"expression":    `new Promise(r => (window.chrome && chrome.settingsPrivate) ? chrome.settingsPrivate.getPref("` + key + `", p => r(JSON.stringify(p.value))) : r(""))`,
			"awaitPromise":  true,
			"returnByValue": true,
		})
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if json.Unmarshal(res, &v) == nil && v.Result.Value != "" {
			return v.Result.Value
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("chrome://settings did not answer")
	return ""
}

func TestLaunchMergesSevenPrefsIntoExistingProfile(t *testing.T) {
	env, root := launchEnv(t)
	env, id := testChrome(t, env)
	profileDir := filepath.Join(launchProfilesDir(root), "merge")
	writeFile(t, filepath.Join(profileDir, "Default", "Preferences"),
		`{"slicc_test":{"keep":"untouched"},"profile":{"name":"Mine","exit_type":"Crashed"},"performance_tuning":{"tab_discarding":{"exceptions":["mine.example"],"exceptions_with_time":{"mine.example":"13400000000000000"}}}}`)
	for round := 0; round < 2; round++ {
		l := startLaunch(t, env, "--browser", id, "--profile", "merge", "--headless")
		ev := l.next(t)
		if ev.Event != "launched" || ev.ProfileDir != profileDir {
			t.Fatalf("%+v want %s; stderr %s", ev, profileDir, l.stderr.String())
		}
		if round == 0 {
			got := settingsPref(t, ev.CDP, "performance_tuning.tab_discarding.exceptions_with_time")
			if !strings.Contains(got, `"`+browser.SevenSitePattern+`"`) || !strings.Contains(got, `"mine.example"`) {
				t.Errorf("Chrome's own view of the exceptions: %s", got)
			}
		}
		if err := interrupt(l.cmd); err != nil {
			t.Fatal(err)
		}
		if e := l.next(t); e.Event != "closed" {
			t.Fatalf("%+v", e)
		}
		l.wait(t)
		prefs := readPrefs(t, profileDir)
		assertSevenPrefs(t, prefs, true)
		raw, _ := os.ReadFile(filepath.Join(profileDir, "Default", "Preferences"))
		if !strings.Contains(string(raw), `"keep":"untouched"`) || dig(prefs, "profile", "name") != "Mine" {
			t.Errorf("round %d clobbered the existing Preferences: %v", round, prefs["slicc_test"])
		}
		if dig(prefs, "performance_tuning", "tab_discarding", "exceptions_with_time", "mine.example") != "13400000000000000" {
			t.Errorf("round %d lost an existing exception: %v", round, prefs["performance_tuning"])
		}
		if list, _ := dig(prefs, "performance_tuning", "tab_discarding", "exceptions").([]any); len(list) != 2 || list[0] != "mine.example" {
			t.Errorf("round %d exceptions list %v", round, list)
		}
	}
}

func launchProfilesDir(root string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(root, "Library", "Application Support", "slicc", "seven-profiles")
	case "windows":
		return filepath.Join(root, "Roaming", "slicc", "seven-profiles")
	default:
		return filepath.Join(root, ".config", "slicc", "seven-profiles")
	}
}
