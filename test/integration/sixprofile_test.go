package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

const sixBusy = "six's Chrome is using this profile; close it, or use --browser/--profile to pick another"

func sixRootIn(h fakeHome) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(h.root, "Library", "Application Support", "Slicc", "profiles")
	case "windows":
		return filepath.Join(h.root, "Local", "Slicc", "profiles")
	default:
		return filepath.Join(h.root, ".local", "state", "slicc", "profiles")
	}
}

func makeSixTree(t *testing.T, dir, activePort string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "Default", "Preferences"), `{"profile":{"name":"six"},"performance_tuning":{"tab_freezing":{"enabled":true}}}`)
	writeFile(t, filepath.Join(dir, "Local State"), `{"user_experience_metrics":{"stability":{"stats_version":"153.0.7900.1-64"}}}`)
	writeFile(t, filepath.Join(dir, "Last Version"), "153.0.7900.1")
	writeFile(t, filepath.Join(dir, "Default", "Sessions", "Session_13400000000000000"), "six's session")
	writeFile(t, filepath.Join(dir, "Default", "Last Session"), "six's last session")
	writeFile(t, filepath.Join(dir, "Default", "Extensions", "sixext", "1.0", "manifest.json"), `{"name":"six"}`)
	writeFile(t, filepath.Join(dir, "First Run"), "")
	if activePort != "" {
		writeFile(t, filepath.Join(dir, "DevToolsActivePort"), activePort)
	}
}

func staleActivePort(t *testing.T) string {
	return fmt.Sprintf("%d\n/devtools/browser/stale\n", closedPort(t))
}

func snapshotTree(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if rel == "." || slices.Contains(skip, rel) {
			return nil
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			out[rel] = "link " + target
		case info.IsDir():
			out[rel] = "dir"
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out[rel] = fmt.Sprintf("file %d %s", info.ModTime().UnixNano(), b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func requireSameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if reflect.DeepEqual(before, after) {
		return
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s: %s changed: %q -> %q", what, k, v, after[k])
		}
	}
	for k, v := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("%s: %s appeared: %q", what, k, v)
		}
	}
	t.FailNow()
}

func holdSixLock(t *testing.T, dir string, pid int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		f, err := os.OpenFile(filepath.Join(dir, "lockfile"), os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return
	}
	if err := os.Symlink(fmt.Sprintf("%s-%d", lockHost(), pid), filepath.Join(dir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
}

func staleSixLock(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		writeFile(t, filepath.Join(dir, "lockfile"), "")
		return
	}
	cmd := exec.Command(binary, "--version")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fmt.Sprintf("%s-%d", lockHost(), cmd.Process.Pid), filepath.Join(dir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
}

func lockHost() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "testhost"
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("no symlinks here: %v", err)
		}
		t.Fatal(err)
	}
}

func buildFakeChrome(t *testing.T) browser.Installed {
	t.Helper()
	exe := filepath.Join(t.TempDir(), exeName("fakechrome"))
	build := exec.Command("go", "build", "-o", exe, "./testdata/fakechrome")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		t.Fatal(err)
	}
	return browser.Installed{
		ID: "chrome", Name: "Fake Chrome", Engine: browser.EngineChromium, Version: "154.0.8037.98",
		Path: exe, Executable: exe, DebugSupport: browser.DebugSupported,
	}
}

func sixEnv(h fakeHome, mode string) []string {
	return overrideEnv(h.env, map[string]string{
		browser.DiscoveryEnv:    mode,
		"SLICC_NO_UPDATE_CHECK": "1",
		"SLICC_NO_TELEMETRY":    "1",
	})
}

func TestSixProfilePathsFollowSixOnEveryOS(t *testing.T) {
	cases := []struct {
		goos, home string
		env        map[string]string
		port       int
		want       string
	}{
		{"darwin", "/Users/me", nil, 0, "/Users/me/Library/Application Support/Slicc/profiles/browser-coding-agent-chrome"},
		{"darwin", "/Users/me", nil, 5710, "/Users/me/Library/Application Support/Slicc/profiles/browser-coding-agent-chrome"},
		{"darwin", "/Users/me", map[string]string{"XDG_STATE_HOME": "/state"}, 5720, "/Users/me/Library/Application Support/Slicc/profiles/browser-coding-agent-chrome-5720"},
		{"linux", "/home/me", nil, 0, "/home/me/.local/state/slicc/profiles/browser-coding-agent-chrome"},
		{"linux", "/home/me", map[string]string{"XDG_STATE_HOME": "/state"}, 5711, "/state/slicc/profiles/browser-coding-agent-chrome-5711"},
		{"windows", `C:\Users\me`, nil, 0, `C:\Users\me\AppData\Local\Slicc\profiles\browser-coding-agent-chrome`},
		{"windows", `C:\Users\me`, map[string]string{"LOCALAPPDATA": `D:\Local`}, 5720, `D:\Local\Slicc\profiles\browser-coding-agent-chrome-5720`},
		{"linux", "/home/me", map[string]string{"SLICC_USER_DATA_DIR": "  /tmp/fresh  "}, 5720, "/tmp/fresh"},
		{"darwin", "/Users/me", map[string]string{"SLICC_USER_DATA_DIR": "   "}, 0, "/Users/me/Library/Application Support/Slicc/profiles/browser-coding-agent-chrome"},
	}
	for _, c := range cases {
		o := browser.Options{Home: c.home, Getenv: func(k string) string { return c.env[k] }}
		if got := browser.SixProfileDirFor(c.goos, o, c.port); got != c.want {
			t.Errorf("%s %v port %d: %q, want %q", c.goos, c.env, c.port, got, c.want)
		}
	}
	parse := []struct {
		in   string
		port int
		six  bool
		bad  bool
	}{
		{"six", 0, true, false}, {"six:5720", 5720, true, false}, {"six:5710", 5710, true, false},
		{"six:0", 0, true, true}, {"six:65536", 0, true, true}, {"six:x", 0, true, true}, {"six:05720", 0, true, true},
		{"six:", 0, true, true}, {"Six", 0, false, false}, {"sixth", 0, false, false}, {"chrome", 0, false, false},
	}
	for _, p := range parse {
		port, six, err := browser.ParseSixProfile(p.in)
		if six != p.six || (err != nil) != p.bad || (!p.bad && port != p.port) {
			t.Errorf("ParseSixProfile(%q) = %d, %v, %v", p.in, port, six, err)
		}
	}
	if browser.SixLabel(0) != "six" || browser.SixLabel(5710) != "six" || browser.SixLabel(5720) != "six:5720" {
		t.Error("six labels")
	}
}

func TestSixProfilesAreFoundOnlyWhereSixLooksOnThisOS(t *testing.T) {
	h := newFakeHome(t)
	layouts := map[string]string{
		"darwin":  filepath.Join(h.root, "Library", "Application Support", "Slicc", "profiles"),
		"linux":   filepath.Join(h.root, ".local", "state", "slicc", "profiles"),
		"windows": filepath.Join(h.root, "Local", "Slicc", "profiles"),
	}
	for _, root := range layouts {
		for _, name := range []string{"browser-coding-agent-chrome", "browser-coding-agent-chrome-5720", "Browser-Coding-Agent-Chrome-5721", "leader", "browser-coding-agent-chrome-x"} {
			makeSixTree(t, filepath.Join(root, name), "")
		}
		writeFile(t, filepath.Join(root, "browser-coding-agent-chrome-5799"), "a file, not a profile")
	}
	got := browser.FindSixProfiles(h.options())
	var labels []string
	for _, s := range got {
		labels = append(labels, s.Label)
		if filepath.Dir(s.Dir) != layouts[runtime.GOOS] {
			t.Errorf("%s found outside this OS's layout: %s", s.Label, s.Dir)
		}
	}
	if want := []string{"six", "six:5720", "six:5721"}; !reflect.DeepEqual(labels, want) {
		t.Fatalf("found %v, want %v", labels, want)
	}
	if browser.SixProfileFor(h.options(), 0).Dir != filepath.Join(layouts[runtime.GOOS], "browser-coding-agent-chrome") {
		t.Fatalf("six's default profile: %+v", browser.SixProfileFor(h.options(), 0))
	}

	o := h.options()
	custom := filepath.Join(h.root, "elsewhere", "fresh")
	makeSixTree(t, custom, "")
	env := envMap(h.env)
	o.Getenv = func(k string) string {
		if k == "SLICC_USER_DATA_DIR" {
			return " " + custom + " "
		}
		return env[k]
	}
	got = browser.FindSixProfiles(o)
	if len(got) != 1 || got[0].Dir != custom || got[0].Label != "six" || browser.SixProfileFor(o, 5720).Dir != custom {
		t.Fatalf("with SLICC_USER_DATA_DIR: %+v", got)
	}
	if browser.FindSixProfiles(browser.Options{Home: t.TempDir(), Getenv: func(string) string { return "" }}) != nil {
		t.Fatal("six profiles in an empty home")
	}
}

func TestSixDirsMatchCaseInsensitivelyAndThroughSymlinks(t *testing.T) {
	h := newFakeHome(t)
	root := sixRootIn(h)
	dir := filepath.Join(root, "browser-coding-agent-chrome")
	makeSixTree(t, dir, "")
	o := h.options()
	upperRoot := filepath.Join(filepath.Dir(filepath.Dir(root)), strings.ToUpper(filepath.Base(filepath.Dir(root))), strings.ToUpper(filepath.Base(root)))
	for _, d := range []string{dir, filepath.Join(root, "BROWSER-CODING-AGENT-CHROME-5720"), filepath.Join(upperRoot, "Browser-Coding-Agent-Chrome"), dir + string(filepath.Separator)} {
		if !browser.IsSixDir(o, d) {
			t.Errorf("%s is six's", d)
		}
	}
	for _, d := range []string{filepath.Join(root, "leader"), filepath.Join(root, "browser-coding-agent-chrome-x"), filepath.Join(filepath.Dir(root), "browser-coding-agent-chrome"), filepath.Join(h.root, "browser-coding-agent-chrome"), ""} {
		if browser.IsSixDir(o, d) {
			t.Errorf("%s is not six's", d)
		}
	}

	link := filepath.Join(t.TempDir(), "six-link")
	symlinkOrSkip(t, dir, link)
	if !browser.IsSixDir(o, link) {
		t.Errorf("a symlink to six's profile is six's: %s", link)
	}

	h2 := newFakeHome(t)
	real := filepath.Join(t.TempDir(), "real-profiles")
	makeSixTree(t, filepath.Join(real, "browser-coding-agent-chrome-5711"), "")
	symlinkOrSkip(t, real, sixRootIn(h2))
	o2 := h2.options()
	for _, d := range []string{filepath.Join(real, "browser-coding-agent-chrome-5711"), filepath.Join(sixRootIn(h2), "browser-coding-agent-chrome-5711")} {
		if !browser.IsSixDir(o2, d) {
			t.Errorf("under a symlinked root, %s is six's", d)
		}
	}
	if got := browser.FindSixProfiles(o2); len(got) != 1 || got[0].Label != "six:5711" {
		t.Errorf("a symlinked root's profiles: %+v", got)
	}

	custom := filepath.Join(t.TempDir(), "custom")
	makeSixTree(t, custom, "")
	customLink := filepath.Join(t.TempDir(), "custom-link")
	symlinkOrSkip(t, custom, customLink)
	o3 := h.options()
	o3.Getenv = func(k string) string {
		if k == "SLICC_USER_DATA_DIR" {
			return customLink
		}
		return ""
	}
	if !browser.IsSixDir(o3, custom) || !browser.IsSixDir(o3, customLink) {
		t.Error("SLICC_USER_DATA_DIR, through a symlink, is six's")
	}
}

func TestSixDiscoveryMarksSixRanksItLastAndNeverChoosesIt(t *testing.T) {
	h := newFakeHome(t)
	root := sixRootIn(h)
	a := newFakeCDP(t, "Chrome/154.0.8037.98", sevenPages())
	b := newFakeCDP(t, "Chrome/154.0.8037.98", nil)
	c := newFakeCDP(t, "Chrome/154.0.8037.98", sevenPages())
	d := newFakeCDP(t, "Chrome/154.0.8037.98", nil)
	makeSixTree(t, filepath.Join(root, "browser-coding-agent-chrome"), a.activePort())
	makeSixTree(t, filepath.Join(root, "browser-coding-agent-chrome-5711"), "")
	makeSixTree(t, filepath.Join(root, "Browser-Coding-Agent-Chrome-5720"), "")
	bDir := filepath.Join(root, "browser-coding-agent-chrome-5711")
	if runtime.GOOS != "windows" {
		bDir = filepath.Join(t.TempDir(), "linked-six")
		symlinkOrSkip(t, filepath.Join(root, "browser-coding-agent-chrome-5711"), bDir)
	}
	table := processTable([][2]any{
		{101, fmt.Sprintf("/opt/google/chrome/chrome --remote-debugging-port=%d --user-data-dir=%s --no-first-run", b.port, bDir)},
		{102, fmt.Sprintf("/opt/google/chrome/chrome --remote-debugging-port=%d --user-data-dir=%s", c.port, strings.ToUpper(filepath.Join(root, "Browser-Coding-Agent-Chrome-5720")))},
		{103, fmt.Sprintf("/opt/google/chrome/chrome --remote-debugging-port=%d --user-data-dir=%s", d.port, filepath.Join(h.root, "pw"))},
	})
	o := h.options()
	o.NoProbe = false
	o.ProbePorts = []int{c.port}
	o.Processes = func(context.Context) ([]byte, error) { return table, nil }
	eps, errs := browser.DiscoverEndpoints(context.Background(), o)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	chrome := browser.Installed{ID: "chrome", Name: "Google Chrome", Engine: browser.EngineChromium, Version: "154.0.8037.98", Path: "/g", Executable: "/g", DebugSupport: browser.DebugSupported}
	entries := browser.Rank(eps, []browser.Installed{chrome})
	r := report{Entries: entries}
	for _, f := range []*fakeCDP{a, b, c} {
		e := entryByCDP(r, f.URL)
		if e == nil || !e.Endpoint.Six || e.Tier != browser.TierSix {
			t.Fatalf("%s should be six's, in tier %d: %+v", f.URL, browser.TierSix, e)
		}
	}
	if e := entryByCDP(r, d.URL); e == nil || e.Endpoint.Six || e.Tier != browser.TierEndpoint {
		t.Fatalf("%s is not six's: %+v", d.URL, e)
	}
	if got := entryByCDP(r, c.URL); got.Endpoint.Source != browser.SourceProcess || got.Endpoint.UserDataDir == "" {
		t.Fatalf("the probe hit the process list maps to six: %+v", got.Endpoint)
	}
	assertTiersAscending(t, entries)
	for _, e := range entries[len(entries)-3:] {
		if e.Tier != browser.TierSix {
			t.Fatalf("six's endpoints aren't last: %v", ids(entries))
		}
	}
	if e, err := flow.Default(entries); err != nil || flow.IsSix(e) || e.ID != "cdp:"+strings.TrimPrefix(d.URL, "http://") {
		t.Fatalf("the default chose %s (%v)", e.ID, err)
	}

	var onlySix []browser.Entry
	for _, e := range entries {
		if flow.IsSix(e) {
			onlySix = append(onlySix, e)
		}
	}
	if e, err := flow.Default(onlySix); !errors.Is(err, flow.ErrNoBrowser) {
		t.Fatalf("with only six's endpoints, the default chose %s", e.ID)
	}
	if _, err := flow.Resolve(onlySix, nil); err == nil {
		t.Fatal("the non-interactive rule chose six's endpoint")
	}
	if _, err := flow.Resolve(entries, []string{onlySix[0].ID}); err == nil || !strings.Contains(err.Error(), "only --profile six uses it") {
		t.Fatalf("--browser %s: %v", onlySix[0].ID, err)
	}

	rows := flow.WithSixRows(o, entries)
	var sixRows, runningRows []string
	for _, e := range rows {
		if e.Kind == browser.KindSix {
			sixRows = append(sixRows, e.ID)
			if e.Tier != browser.TierSix || e.Name == "" || e.Installed == nil || e.Installed.ID != "chrome" {
				t.Fatalf("six row %+v", e)
			}
		}
		if e.Kind != browser.KindSix && e.Endpoint != nil && e.Endpoint.Six {
			t.Fatalf("six's endpoint %s is listed next to its profile's row", e.ID)
		}
		if e.Kind == browser.KindSix && e.Endpoint != nil {
			runningRows = append(runningRows, e.ID)
		}
	}
	if len(runningRows) == 0 {
		t.Fatalf("no six row carries its running endpoint: %v", ids(rows))
	}
	if want := []string{"six", "six:5711", "six:5720"}; !reflect.DeepEqual(sixRows, want) {
		t.Fatalf("six rows %v, want %v", sixRows, want)
	}
	if rows[len(rows)-1].Kind != browser.KindSix || rows[0].Name != entries[0].Name {
		t.Fatalf("rows %v", ids(rows))
	}
	empty := newFakeHome(t)
	if got := flow.WithSixRows(empty.options(), entries); len(got) != len(entries) {
		t.Fatalf("six rows without six's profile: %v", ids(got))
	}
}

func TestSixBrowserIsTheOneSixUsed(t *testing.T) {
	dir := t.TempDir()
	stable := browser.Installed{ID: "chrome", Name: "Google Chrome", Engine: browser.EngineChromium, Version: "154.0.8037.98"}
	canary := browser.Installed{ID: "chrome-canary", Name: "Google Chrome Canary", Engine: browser.EngineChromium, Version: "156.0.8100.0"}
	cft := browser.Installed{ID: "chrome-for-testing", Name: "Google Chrome for Testing", Engine: browser.EngineChromium, Version: "153.0.7900.1"}
	brave := browser.Installed{ID: "brave", Name: "Brave", Engine: browser.EngineChromium, Version: "153.0.7900.1"}
	all := []browser.Installed{brave, canary, cft, stable}
	pick := func(list []browser.Installed) string {
		in, ok := browser.SixBrowser(dir, list)
		if !ok {
			return ""
		}
		return in.ID
	}
	if got := pick(all); got != "chrome" {
		t.Fatalf("without a version: %s", got)
	}
	writeFile(t, filepath.Join(dir, "Local State"), `{"user_experience_metrics":{"stability":{"stats_version":"156.0.8100.0-64"}}}`)
	if got := pick(all); got != "chrome-canary" {
		t.Fatalf("from Local State: %s", got)
	}
	writeFile(t, filepath.Join(dir, "Last Version"), "153.0.7900.1\n")
	if got := pick(all); got != "chrome-for-testing" {
		t.Fatalf("from Last Version: %s", got)
	}
	writeFile(t, filepath.Join(dir, "Last Version"), "150.0.1.1")
	if got := pick(all); got != "chrome" {
		t.Fatalf("an unknown version falls back to Chrome stable: %s", got)
	}
	if got := pick([]browser.Installed{brave, canary}); got != "" {
		t.Fatalf("no Chrome stable and no match: %s", got)
	}
}

func TestSixLaunchLeavesTheProfileAsItWas(t *testing.T) {
	h := newFakeHome(t)
	o := h.options()
	six := browser.SixProfileFor(o, 0)
	makeSixTree(t, six.Dir, staleActivePort(t))
	staleSixLock(t, six.Dir)
	stale := readText(t, filepath.Join(six.Dir, "DevToolsActivePort"))
	chromeOwn := []string{"DevToolsActivePort", "SingletonLock", "lockfile"}
	before := snapshotTree(t, six.Dir, chromeOwn...)
	in := buildFakeChrome(t)
	log := filepath.Join(t.TempDir(), "fakechrome.json")
	t.Setenv("FAKE_CHROME_LOG", log)

	ctx := context.Background()
	if ep, err := browser.ProbeSix(ctx, o, six.Dir); ep != nil || err != nil {
		t.Fatalf("a stale lock means free: %+v %v", ep, err)
	}
	b, err := browser.LaunchSix(ctx, in, six, browser.LaunchOptions{Options: o, URL: "about:blank", Headless: true, LaunchTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if b.Profile != "six" || b.ProfileDir != six.Dir || !strings.HasPrefix(b.CDP, "http://127.0.0.1:") || b.PID <= 0 {
		t.Fatalf("%+v", b)
	}
	var logged struct {
		Args          []string `json:"args"`
		SawActivePort string   `json:"sawActivePort"`
	}
	if err := json.Unmarshal([]byte(readText(t, log)), &logged); err != nil {
		t.Fatal(err)
	}
	if want := browser.ChromiumArgs(six.Dir, "about:blank", true, true, nil); !reflect.DeepEqual(logged.Args, want) {
		t.Fatalf("args %q, want only the runtime flags %q", logged.Args, want)
	}
	if logged.SawActivePort != stale {
		t.Fatalf("the browser found DevToolsActivePort %q, not six's stale %q", logged.SawActivePort, stale)
	}
	requireSameTree(t, "after the launch", before, snapshotTree(t, six.Dir, chromeOwn...))

	ep, err := browser.ProbeSix(ctx, o, six.Dir)
	if err != nil || ep == nil || ep.CDP != b.CDP || !ep.Six {
		t.Fatalf("the running browser in six's profile is live and debuggable: %+v %v", ep, err)
	}
	if _, err := browser.LaunchSix(ctx, in, six, browser.LaunchOptions{Options: o, Headless: true}); !errors.Is(err, browser.ErrSixBusy) {
		t.Fatalf("a second launch: %v", err)
	}
	closeAndCheck(t, b)
	requireSameTree(t, "after the exit", before, snapshotTree(t, six.Dir, chromeOwn...))
	if ep, err := browser.ProbeSix(ctx, o, six.Dir); ep != nil || err != nil {
		t.Fatalf("after the exit, six's profile is free: %+v %v", ep, err)
	}
}

func TestSixAttachOpensSevenAndLeavesTheProfileAsItWas(t *testing.T) {
	b := launchBrowser(t)
	v, err := getVersion(b.endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := url.Parse(fmt.Sprint(v["webSocketDebuggerUrl"]))
	h := newFakeHome(t)
	dir := filepath.Join(sixRootIn(h), "browser-coding-agent-chrome")
	makeSixTree(t, dir, fmt.Sprintf("%d\n%s", b.port, ws.Path))
	holdSixLock(t, dir, b.cmd.Process.Pid)
	before := snapshotTree(t, dir)
	pageURL := pageServer(t).URL + "/"
	start := time.Now()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("page server since %s:\n%s%s", start.Format("15:04:05.000"), pageRequests.since(start), b.report())
		}
	})

	f := startFlow(t, sixEnv(h, "isolated"), "--json", "--profile", "six", "--page", pageURL, "--no-kernel")
	f.next(t, "discovered")
	a := f.next(t, "attached")
	if a.CDP != b.endpoint || a.Launched || a.Role != "seven" {
		t.Fatalf("attached %s", a.raw)
	}
	fb := f.next(t, "foreign-browser")
	if !strings.Contains(fb.raw, `"profile":"six"`) || !strings.Contains(fb.raw, `"openedTab":true`) || fb.CDP != b.endpoint {
		t.Fatalf("foreign-browser %s", fb.raw)
	}
	f.waitStderr(t, "(profile six) at "+b.endpoint+", which slicc didn't launch; opened a new seven tab.")
	up := f.next(t, "linked")
	if up.Tab != pageURL {
		t.Fatalf("linked %s", up.raw)
	}
	if n := countTabs(t, b.client, pageURL); n != 1 {
		t.Fatalf("%d seven tabs", n)
	}
	requireSameTree(t, "after the attach", before, snapshotTree(t, dir))
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	f.next(t, "link-lost")
	if c := f.next(t, "closed"); len(c.Browsers) != 0 {
		t.Fatalf("closed %s", c.raw)
	}
	if code := f.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if _, err := getVersion(b.endpoint); err != nil {
		t.Fatalf("slicc closed six's Chrome: %v", err)
	}
	requireSameTree(t, "after the exit", before, snapshotTree(t, dir))
	if strings.Contains(f.stderr.String(), dir) {
		t.Fatalf("stderr names six's profile dir: %s", f.stderr.String())
	}
}

func TestSixLaunchCommandAttachesWithoutClosing(t *testing.T) {
	cdpSrv := newFakeCDP(t, "Chrome/154.0.8037.98", nil)
	h := newFakeHome(t)
	dir := filepath.Join(sixRootIn(h), "browser-coding-agent-chrome-5720")
	makeSixTree(t, dir, cdpSrv.activePort())
	holdSixLock(t, dir, os.Getpid())
	before := snapshotTree(t, dir)
	l := startLaunch(t, sixEnv(h, "isolated"), "--profile", "six:5720")
	e := l.next(t)
	if e.Event != "attached" || e.CDP != cdpSrv.URL || e.Profile != "six:5720" || e.ProfileDir != dir {
		t.Fatalf("event %+v; stderr %s", e, l.stderr.String())
	}
	if err := interrupt(l.cmd); err != nil {
		t.Fatal(err)
	}
	if e := l.next(t); e.Event != "detached" {
		t.Fatalf("event %+v", e)
	}
	if code := l.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	requireSameTree(t, "after slicc launch detached", before, snapshotTree(t, dir))
}

func TestSixBusyOrMissingProfileIsRefused(t *testing.T) {
	h := newFakeHome(t)
	root := sixRootIn(h)
	env := sixEnv(h, "isolated")
	for _, args := range [][]string{{"--json", "--profile", "six"}, {"launch", "--json", "--profile", "six"}} {
		out, errOut, code := runWithEnv(t, env, args...)
		if code != 1 || !strings.Contains(errOut, browser.ErrSixMissing.Error()) || strings.Contains(errOut, root) {
			t.Fatalf("%v without six's profile: %d %q %q", args, code, out, errOut)
		}
		if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%v created six's root: %v", args, err)
		}
	}

	makeSixTree(t, filepath.Join(root, "browser-coding-agent-chrome"), staleActivePort(t))
	makeSixTree(t, filepath.Join(root, "browser-coding-agent-chrome-5720"), "")
	holdSixLock(t, filepath.Join(root, "browser-coding-agent-chrome"), os.Getpid())
	holdSixLock(t, filepath.Join(root, "browser-coding-agent-chrome-5720"), os.Getpid())
	before := snapshotTree(t, root)
	for _, args := range [][]string{
		{"--json", "--profile", "six"}, {"--plain", "--profile", "six:5720"},
		{"launch", "--json", "--profile", "six"}, {"launch", "--profile", "six:5720"},
	} {
		out, errOut, code := runWithEnv(t, env, args...)
		if code != 1 || !strings.Contains(errOut, sixBusy) {
			t.Fatalf("%v: %d %q %q", args, code, out, errOut)
		}
		if args[0] == "--json" && !strings.Contains(out, `"event":"error","message":"`+sixBusy+`"`) {
			t.Fatalf("%v: stdout %q", args, out)
		}
	}
	requireSameTree(t, "after the refusals", before, snapshotTree(t, root))

	for _, p := range []string{"six:0", "six:65536", "six:x", "six:05720"} {
		for _, args := range [][]string{{"--profile", p}, {"launch", "--profile", p}} {
			if _, errOut, code := runWithEnv(t, env, args...); code != 2 || !strings.Contains(errOut, "1-65535") {
				t.Fatalf("%v: %d %q", args, code, errOut)
			}
		}
	}
}

func TestBrowsersListsSixsChromeLastAndOnlyProfileSixUsesIt(t *testing.T) {
	h := newFakeHome(t)
	six := newFakeCDP(t, "Chrome/154.0.8037.98", sevenPages())
	ours := newFakeCDP(t, "Chrome/154.0.8037.97", nil)
	makeSixTree(t, filepath.Join(sixRootIn(h), "browser-coding-agent-chrome"), six.activePort())
	writeFile(t, filepath.Join(h.profilesDir(), "chrome", "DevToolsActivePort"), ours.activePort())
	env := sixEnv(h, "home")
	r, _ := browsersJSON(t, env)
	e := entryByCDP(r, six.URL)
	if e == nil || !e.Endpoint.Six || e.Tier != browser.TierSix || !e.Endpoint.SevenOpen || r.Entries[len(r.Entries)-1].ID != e.ID {
		t.Fatalf("six's Chrome: %+v in %v", e, ids(r.Entries))
	}
	if o := entryByCDP(r, ours.URL); o == nil || o.Endpoint.Six {
		t.Fatalf("our profile's endpoint: %+v", o)
	}
	out, _, code := runWithEnv(t, env, "browsers")
	if code != 0 || !strings.Contains(out, "running, seven open, SLICC six's profile") {
		t.Fatalf("browsers: %d %s", code, out)
	}
	id := "cdp:127.0.0.1:" + strconv.Itoa(six.port)
	_, errOut, code := runWithEnv(t, env, "--json", "--browser", id)
	if code != 1 || !strings.Contains(errOut, id+" is SLICC six's Chrome; only --profile six uses it") {
		t.Fatalf("--browser %s: %d %q", id, code, errOut)
	}
}

func TestSixErrorsSendNoPaths(t *testing.T) {
	col := newCollector(t)
	bin := buildVersion(t, "1.0.0", t.TempDir())
	state := filepath.Join(t.TempDir(), "state")
	env := isolatedEnv(t, append(col.env(), "SLICC_NO_UPDATE_CHECK=1", "XDG_STATE_HOME="+state)...)
	m := envMap(env)
	var root string
	switch runtime.GOOS {
	case "darwin":
		root = filepath.Join(m["HOME"], "Library", "Application Support", "Slicc", "profiles")
	case "windows":
		root = filepath.Join(m["LOCALAPPDATA"], "Slicc", "profiles")
	default:
		root = filepath.Join(state, "slicc", "profiles")
	}
	dir := filepath.Join(root, "browser-coding-agent-chrome")
	makeSixTree(t, dir, staleActivePort(t))
	holdSixLock(t, dir, os.Getpid())
	r := runBin(t, env, bin, "--json", "--profile", "six")
	if r.code != 1 || !strings.Contains(r.stderr, sixBusy) {
		t.Fatalf("%+v", r)
	}
	failure := col.find("error")
	if failure == nil || failure.Source != "browser" {
		t.Fatalf("beacons: %+v", col.got())
	}
	for _, s := range []string{dir, root, "browser-coding-agent-chrome", filepath.ToSlash(dir)} {
		if strings.Contains(failure.raw, s) {
			t.Fatalf("the beacon carries %q: %s", s, failure.raw)
		}
	}
}

func TestSixLaunchesRealChromeWithoutSeedingTheProfile(t *testing.T) {
	h := newFakeHome(t)
	env := sixEnv(h, "installed")
	dir := filepath.Join(sixRootIn(h), "browser-coding-agent-chrome")
	if runtime.GOOS == "windows" {
		real, _ := launchEnv(t)
		dir = filepath.Join(t.TempDir(), "six-user-data-dir")
		env = overrideEnv(real, map[string]string{"SLICC_USER_DATA_DIR": dir, "SLICC_NO_UPDATE_CHECK": "1", "SLICC_NO_TELEMETRY": "1"})
	}
	env, id := testChrome(t, env)
	makeSixTree(t, dir, staleActivePort(t))
	staleSixLock(t, dir)
	args := []string{"--profile", "six", "--headless"}
	if id != "chrome" {
		args = append(args, "--browser", id)
	}
	l := startLaunch(t, env, args...)
	e := l.next(t)
	if e.Event != "launched" || e.ID != id || e.Profile != "six" || e.ProfileDir != dir || !strings.HasPrefix(e.CDP, "http://127.0.0.1:") {
		t.Fatalf("event %+v; stderr %s", e, l.stderr.String())
	}
	if _, err := getVersion(e.CDP); err != nil {
		t.Fatal(err)
	}
	if err := interrupt(l.cmd); err != nil {
		t.Fatal(err)
	}
	if e := l.next(t); e.Event != "closed" {
		t.Fatalf("event %+v", e)
	}
	if code := l.wait(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	prefs := readText(t, filepath.Join(dir, "Default", "Preferences"))
	if strings.Contains(prefs, "sliccy.ai") {
		t.Fatalf("slicc seeded six's Preferences: %s", prefs)
	}
}

func TestASevenProfileThatIsSixsIsNeverPrepared(t *testing.T) {
	h := newFakeHome(t)
	dir := filepath.Join(h.profilesDir(), "chrome")
	makeSixTree(t, dir, staleActivePort(t))
	before := snapshotTree(t, dir)
	env := envMap(h.env)
	o := h.options()
	o.Getenv = func(k string) string {
		if k == "SLICC_USER_DATA_DIR" {
			return dir
		}
		return env[k]
	}
	in := browser.Installed{ID: "chrome", Name: "Google Chrome", Engine: browser.EngineChromium, Path: "/nonexistent", Executable: "/nonexistent", DebugSupport: browser.DebugSupported}
	if _, err := browser.Launch(context.Background(), in, browser.LaunchOptions{Options: o, Headless: true}); !errors.Is(err, browser.ErrSixDir) {
		t.Fatalf("launching into six's profile as a SLICC profile: %v", err)
	}
	requireSameTree(t, "after the refused launch", before, snapshotTree(t, dir))
}

func TestNothingMovesOutOfSixsProfileWhenItIsTheOldProfilesFolder(t *testing.T) {
	h := newFakeHome(t)
	legacy := h.legacyProfilesDir(t)
	makeSixTree(t, legacy, "")
	writeFile(t, filepath.Join(legacy, "chrome", "Default", "Preferences"), `{"looks":"like a seven profile"}`)
	before := snapshotTree(t, legacy)
	env := overrideEnv(sixEnv(h, "home"), map[string]string{"SLICC_USER_DATA_DIR": legacy})
	browsersJSON(t, env)
	requireSameTree(t, "after the migration check", before, snapshotTree(t, legacy))
	if _, err := os.Stat(filepath.Join(h.profilesDir(), "Default")); err == nil {
		t.Fatal("a folder of six's profile was moved into seven-profiles")
	}
}

func TestSixWithTheSameBrowserTwiceHasNoExtra(t *testing.T) {
	h := newFakeHome(t)
	o := h.options()
	makeSixTree(t, browser.SixProfileFor(o, 0).Dir, "")
	chrome := browser.Installed{ID: "chrome", Name: "Google Chrome", Engine: browser.EngineChromium, Version: "154.0.8037.98", Path: "/g", Executable: "/g", DebugSupport: browser.DebugSupported}
	entries := browser.Rank(nil, []browser.Installed{chrome})
	sel, err := flow.ResolveSix(o, 0, entries, []string{"chrome", "Google Chrome"})
	if err != nil || len(sel) != 1 || sel[0].ID != "six" || sel[0].Installed.ID != "chrome" {
		t.Fatalf("selection %v, %v", ids(sel), err)
	}
	if _, err := flow.ResolveSix(o, 0, entries, []string{"electron:none"}); err == nil {
		t.Fatal("an unknown --browser with --profile six")
	}
}

func TestSixLaunchLeavesTheStalePortFileClosedWhileTheBrowserStarts(t *testing.T) {
	h := newFakeHome(t)
	o := h.options()
	six := browser.SixProfileFor(o, 0)
	makeSixTree(t, six.Dir, staleActivePort(t))
	staleSixLock(t, six.Dir)
	in := buildFakeChrome(t)
	log := filepath.Join(t.TempDir(), "fakechrome.json")
	t.Setenv("FAKE_CHROME_LOG", log)
	t.Setenv("FAKE_CHROME_PROBE_MS", "1500")
	b, err := browser.LaunchSix(context.Background(), in, six, browser.LaunchOptions{Options: o, Headless: true, LaunchTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAndCheck(t, b)
	var logged struct {
		SharedOpens int `json:"sharedOpens"`
	}
	if err := json.Unmarshal([]byte(readText(t, log)), &logged); err != nil {
		t.Fatal(err)
	}
	if logged.SharedOpens != 0 {
		t.Fatalf("slicc held six's stale DevToolsActivePort open %d times while the browser was starting", logged.SharedOpens)
	}
}
