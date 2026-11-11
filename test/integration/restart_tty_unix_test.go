//go:build !windows

package integration

import (
	"context"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/cdp"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
)

var dashLaunched = regexp.MustCompile(`launched · CDP :(\d+)`)

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

func dashboardLaunch(t *testing.T, bin string, env []string, chromeID string) (*ttyProc, string) {
	t.Helper()
	home := envMap(env)["HOME"]
	srv := pageServer(t)
	profile := "it-dash-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	p := startTTYBin(t, bin, env, "--no-fallback", "--profile", profile, "--headless", "--page", srv.URL+"/index.html", "--name", "it-dash", "--no-kernel")
	pickInTTY(t, p, env, chromeID)
	dash := p.wait(t, 0, `linked with "link test page"`)
	m := dashLaunched.FindStringSubmatch(dash)
	if m == nil {
		t.Fatalf("no launched browser on the dashboard:\n%s", dash)
	}
	endpoint := "http://127.0.0.1:" + m[1]
	t.Cleanup(func() { closeEndpoint(t, endpoint, home, profile) })
	return p, endpoint
}

func closeEndpoint(t *testing.T, endpoint, home, profile string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c, err := cdp.Dial(ctx, endpoint); err == nil {
		_ = c.Call(ctx, "", "Browser.close", nil, nil)
		_ = c.Close()
	}
	var dir string
	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && d.Name() == profile && filepath.Base(filepath.Dir(path)) == "seven-profiles" {
			dir = path
			return filepath.SkipAll
		}
		return nil
	})
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
	p, endpoint := dashboardLaunch(t, binary, env, chromeID)
	if !strings.Contains(p.text(), "d detach") {
		t.Fatal("the dashboard offers no d")
	}
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
	p, endpoint := dashboardLaunch(t, bin, env, chromeID)
	if !strings.Contains(p.text(), "u update") {
		t.Fatal("the dashboard offers no u")
	}
	mark := len(p.text())
	p.keys(t, "u")
	p.wait(t, mark, "updating slicc 1.0.0 to 1.1.0")
	p.wait(t, mark, "updated to slicc 1.1.0 (SHA256SUMS and attestation checked); restarting")
	fresh := p.wait(t, mark, "slicc · looking for browsers…")
	mark += strings.LastIndex(fresh, "slicc · looking for browsers…")
	again := p.wait(t, mark, `linked with "link test page"`)
	if m := dashLaunched.FindStringSubmatch(again); m == nil || "http://127.0.0.1:"+m[1] != endpoint {
		t.Fatalf("after the restart the dashboard doesn't own the browser at %s:\n%s", endpoint, again)
	}
	if strings.Contains(again, "choose where seven runs") {
		t.Fatal("the restart showed the picker")
	}
	if got := versionOf(t, bin); got != "slicc 1.1.0" {
		t.Fatalf("binary after u: %q", got)
	}
	st, err := ctl.Query(context.Background(), ctlSocket(t, p.cmd.Process.Pid))
	if err != nil || st.Version != "1.1.0" {
		t.Fatalf("the process after the restart: %+v, %v", st, err)
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
