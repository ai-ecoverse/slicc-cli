//go:build !windows

package integration

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
)

func pickInTTY(t *testing.T, p *ttyProc, env []string, id string) {
	t.Helper()
	r, _ := browsersJSON(t, env)
	name := ""
	for _, e := range r.Entries {
		if e.ID == id {
			name = e.Name
		}
	}
	if name == "" {
		t.Fatalf("slicc browsers lists no %s", id)
	}
	row := regexp.MustCompile(regexp.QuoteMeta(name) + `\s+\d`)
	p.wait(t, 0, "choose where seven runs")
	p.wait(t, 0, "↵ ")
	time.Sleep(500 * time.Millisecond)
	picker := p.text()
	var items []string
	for _, l := range strings.Split(picker[strings.LastIndex(picker, "choose where seven runs"):], "\n") {
		if strings.Contains(l, "○") || strings.Contains(l, "▸ ") {
			items = append(items, l)
		}
	}
	at := -1
	for i, l := range items {
		if row.MatchString(l) {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatalf("no %s row in %q", name, items)
	}
	p.keys(t, strings.Repeat("j", at))
	time.Sleep(200 * time.Millisecond)
	p.keys(t, "\r")
}

func dashboardLaunch(t *testing.T, bin, version string, env []string, chromeID string) (*ttyProc, string) {
	t.Helper()
	home := envMap(env)["HOME"]
	srv := pageServer(t)
	profile := "it-dash-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	p := startTTYBin(t, bin, env, "--no-fallback", "--profile", profile, "--headless", "--page", srv.URL+"/index.html", "--name", "it-dash", "--no-kernel")
	pickInTTY(t, p, env, chromeID)
	linkedAs(t, p, version)
	endpoint := profileEndpoint(t, home, profile)
	t.Cleanup(func() { closeEndpoint(t, endpoint, home, profile) })
	return p, endpoint
}

func linkedAs(t *testing.T, p *ttyProc, version string) *ctl.Status {
	t.Helper()
	path := filepath.Join(ctlDir(t), strconv.Itoa(p.cmd.Process.Pid)+".sock")
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if st, err := ctl.Query(context.Background(), path); err == nil && st.Version == version && st.State == ctl.StateConnected && st.Links > 0 {
			return st
		}
		select {
		case err := <-p.done:
			t.Fatalf("slicc exited (%v) before %s linked seven", err, version)
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("slicc %s never linked seven", version)
		}
	}
}

func profileEndpoint(t *testing.T, home, profile string) string {
	t.Helper()
	dir := profileDir(home, profile)
	if dir == "" {
		t.Fatalf("no SLICC profile %s under %s", profile, home)
	}
	b, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	if err != nil {
		t.Fatalf("the launched browser wrote no DevToolsActivePort: %v", err)
	}
	port, _, _ := strings.Cut(string(b), "\n")
	return "http://127.0.0.1:" + strings.TrimSpace(port)
}

func profileDir(home, profile string) string {
	var dir string
	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && d.Name() == profile && filepath.Base(filepath.Dir(path)) == "seven-profiles" {
			dir = path
			return filepath.SkipAll
		}
		return nil
	})
	return dir
}

func closeEndpoint(t *testing.T, endpoint, home, profile string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c, err := cdp.Dial(ctx, endpoint); err == nil {
		_ = c.Call(ctx, "", "Browser.close", nil, nil)
		_ = c.Close()
	}
	dir := profileDir(home, profile)
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if _, err := getVersion(endpoint); err == nil {
			continue
		}
		if dir == "" || profileReleased(dir) {
			return
		}
	}
	t.Errorf("the browser at %s did not close", endpoint)
}

func ctlDir(t *testing.T) string {
	t.Helper()
	dir, err := ctl.Dir()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func ttyExit(t *testing.T, p *ttyProc) {
	t.Helper()
	select {
	case err := <-p.done:
		if err != nil {
			t.Fatalf("exit: %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("slicc did not exit")
	}
}

func TestDashboardDetachKeyLeavesTheLaunchedBrowserRunning(t *testing.T) {
	env, _ := launchEnv(t)
	env, chromeID := testChrome(t, env)
	p, endpoint := dashboardLaunch(t, binary, "1.2.3-test", env, chromeID)
	p.wait(t, 0, "d detach")
	p.keys(t, "d")
	ttyExit(t, p)
	p.wait(t, 0, "slicc: detached; left "+chromeID+" running")
	time.Sleep(2 * time.Second)
	if _, err := getVersion(endpoint); err != nil {
		t.Fatalf("d closed the browser: %v", err)
	}
}

func TestDashboardUpdateKeyRestartsWithoutClosingTheBrowser(t *testing.T) {
	gh := newFakeGitHub(t, "v1.1.0", newRelease(t, "1.1.0"))
	bin := buildVersion(t, "1.0.0", t.TempDir())
	env, _ := launchEnv(t)
	env, chromeID := testChrome(t, env)
	ghEnv := envMap(gh.env(t))
	env = overrideEnv(env, map[string]string{
		"SLICC_UPDATE_API_BASE":       ghEnv["SLICC_UPDATE_API_BASE"],
		"SLICC_UPDATE_WEB_BASE":       ghEnv["SLICC_UPDATE_WEB_BASE"],
		"SLICC_SIGSTORE_TRUSTED_ROOT": ghEnv["SLICC_SIGSTORE_TRUSTED_ROOT"],
		"SLICC_LAUNCHER":              "",
		"SLICC_NO_UPDATE_CHECK":       "1",
	})
	p, endpoint := dashboardLaunch(t, bin, "1.0.0", env, chromeID)
	p.wait(t, 0, "u update")
	mark := len(p.text())
	p.keys(t, "u")
	st := linkedAs(t, p, "1.1.0")
	if st.Seven != "link test page" || !slices.Contains(st.Offers, "cdp") {
		t.Fatalf("the process after the restart: %+v", st)
	}
	if _, err := getVersion(endpoint); err != nil {
		t.Fatalf("the restart closed the browser slicc launched: %v", err)
	}
	if strings.Contains(p.since(mark), "choose where seven runs") {
		t.Fatal("the restart showed the picker")
	}
	if got := versionOf(t, bin); got != "slicc 1.1.0" {
		t.Fatalf("binary after u: %q", got)
	}
	p.keys(t, "q")
	ttyExit(t, p)
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		if _, err := getVersion(endpoint); err != nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("q after the restart left the browser slicc launched running")
		}
	}
}
