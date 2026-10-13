package integration

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

func buildFakeOpen(t *testing.T) string {
	t.Helper()
	tool := filepath.Join(t.TempDir(), exeName("fakeopen"))
	if out, err := exec.Command("go", "build", "-o", tool, "./testdata/fakeopen").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return tool
}

func (f *flowProc) nextAny(t *testing.T) flowEvent {
	t.Helper()
	select {
	case e, ok := <-f.events:
		if !ok {
			t.Fatalf("stdout closed; events %v", f.kinds())
		}
		f.seen = append(f.seen, e)
		return e
	case <-time.After(90 * time.Second):
		t.Fatalf("no event after %v", f.kinds())
	}
	return flowEvent{}
}

func TestFallbackLinksSevenInTheDefaultBrowserWhenNothingElseIsFound(t *testing.T) {
	tool := buildFakeOpen(t)
	setup := func(t *testing.T) (*loopEnv, string) {
		e := newLoopEnv(t)
		out := filepath.Join(t.TempDir(), "opened")
		e.env = overrideEnv(e.env, map[string]string{"SLICC_OPEN_COMMAND": tool, "SLICC_FAKEOPEN_OUT": out})
		return e, out
	}
	page := "https://seven.sliccy.ai/"

	t.Run("without a terminal it prints the pairing URL and opens nothing", func(t *testing.T) {
		e, out := setup(t)
		o := startLoop(t, e, "", "--plain", "--page", page)
		if u := o.url(t); !strings.HasPrefix(u, page+"#slicc-pair=") {
			t.Fatalf("pairing URL %q", u)
		}
		o.waitStderr(t, "slicc: "+flow.FallbackNotice)
		o.waitStderr(t, "listening on 127.0.0.1:")
		time.Sleep(time.Second)
		if _, err := os.Stat(out); err == nil {
			t.Fatal("the fallback opened a browser without a terminal")
		}
	})

	t.Run("--open opens the pairing URL", func(t *testing.T) {
		e, out := setup(t)
		o := startLoop(t, e, "", "--yes", "--open", "--page", page)
		u := o.url(t)
		o.waitStderr(t, "opened the pairing URL in the default browser")
		if got, _ := os.ReadFile(out); string(got) != u+"\n" {
			t.Fatalf("the opener got %q, stdout had %q", got, u)
		}
	})

	t.Run("--json reports the mode and the pairing URL", func(t *testing.T) {
		e, _ := setup(t)
		f := startFlow(t, e.env, "--json", "--page", page)
		f.next(t, "discovered")
		m := f.next(t, "mode")
		var mode map[string]any
		if json.Unmarshal([]byte(m.raw), &mode) != nil || mode["mode"] != "loopback" || mode["reason"] != "no-browser" {
			t.Fatalf("mode event %s", m.raw)
		}
		pe := f.next(t, "pairing")
		var pairingEvent struct {
			URL string `json:"url"`
		}
		if json.Unmarshal([]byte(pe.raw), &pairingEvent) != nil || !pairingURL.MatchString(pairingEvent.URL) {
			t.Fatalf("pairing event %s", pe.raw)
		}
		f.waitStderr(t, flow.FallbackNotice)
	})

	t.Run("--no-fallback exits as before", func(t *testing.T) {
		e, _ := setup(t)
		out, errOut, code := runWithEnv(t, e.env, "--plain", "--no-fallback")
		if code != 1 || out != "" || !strings.Contains(errOut, "no browser to control: run `slicc open` to link seven in your everyday browser") || strings.Contains(errOut, flow.FallbackNotice) {
			t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("a given endpoint wins", func(t *testing.T) {
		e, _ := setup(t)
		out, errOut, code := runWithEnv(t, e.env, "--json", "--cdp", closedAddr(t))
		if code != 1 || strings.Contains(out, `"event":"mode"`) || strings.Contains(errOut, flow.FallbackNotice) {
			t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("a found endpoint wins", func(t *testing.T) {
		h := newFakeHome(t)
		fake := newFakeCDP(t, "Chrome/154.0.0.0", sevenPages())
		writeFile(t, filepath.Join(h.profilesDir(), "it", "DevToolsActivePort"), fake.activePort())
		env := overrideEnv(h.env, map[string]string{"SLICC_OPEN_COMMAND": tool, "SLICC_FAKEOPEN_OUT": filepath.Join(t.TempDir(), "opened")})
		f := startFlow(t, env, "--json", "--page", page)
		d := f.next(t, "discovered")
		if len(d.Entries) == 0 || d.Entries[0].Kind != "endpoint" {
			t.Fatalf("discovered %s", d.raw)
		}
		if e := f.nextAny(t); e.Event == "mode" || strings.Contains(f.stderr.String(), flow.FallbackNotice) {
			t.Fatalf("fell back with an endpoint found: %s", e.raw)
		}
	})

	t.Run("an existing SLICC profile wins", func(t *testing.T) {
		h := newFakeHome(t)
		env := overrideEnv(h.env, map[string]string{"SLICC_OPEN_COMMAND": tool, "SLICC_FAKEOPEN_OUT": filepath.Join(t.TempDir(), "opened")})
		out, _, code := runWithEnv(t, env, "browsers", "--json")
		var r report
		if code != 0 || json.Unmarshal([]byte(out), &r) != nil {
			t.Fatalf("browsers: %d %s", code, out)
		}
		var chromium *browser.Installed
		for _, e := range r.Entries {
			if e.Installed != nil && e.Installed.Engine == browser.EngineChromium {
				chromium = e.Installed
				break
			}
		}
		if chromium == nil {
			t.Skip("no installed Chromium browser to find a SLICC profile for")
		}
		if err := os.MkdirAll(filepath.Join(h.profilesDir(), "it-fallback"), 0o700); err != nil {
			t.Fatal(err)
		}
		f := startFlow(t, env, "--json", "--headless", "--page", page, "--no-kernel")
		f.next(t, "discovered")
		e := f.nextAny(t)
		if e.Event == "mode" || strings.Contains(f.stderr.String(), flow.FallbackNotice) {
			t.Fatalf("fell back with a SLICC profile: %s", e.raw)
		}
		if e.Event == "launched" {
			t.Cleanup(func() { closeLeftBrowser(t, e.CDP, e.PID, e.ProfileDir) })
		}
	})
}

func TestFallbackTriggersOnlyWithoutProfileAndEndpoint(t *testing.T) {
	dir := t.TempDir()
	o := browser.Options{ProfilesDir: dir}
	chrome := browser.Entry{ID: "chrome", Kind: browser.KindInstalled, Installed: &browser.Installed{ID: "chrome", Engine: browser.EngineChromium}}
	app := browser.Entry{ID: "electron:app", Kind: browser.KindInstalled, Installed: &browser.Installed{ID: "electron:app", Engine: browser.EngineElectron}}
	endpoint := browser.Entry{ID: "cdp:127.0.0.1:9300", Kind: browser.KindEndpoint, Endpoint: &browser.Endpoint{CDP: "http://127.0.0.1:9300"}}
	six := browser.Entry{ID: "cdp:127.0.0.1:9301", Kind: browser.KindEndpoint, Endpoint: &browser.Endpoint{CDP: "http://127.0.0.1:9301", Six: true}}
	cases := []struct {
		name    string
		entries []browser.Entry
		want    bool
	}{
		{"nothing at all", nil, true},
		{"an installed browser without a SLICC profile", []browser.Entry{chrome, app}, true},
		{"only SLICC six's Chrome", []browser.Entry{six}, true},
		{"a running endpoint", []browser.Entry{chrome, endpoint}, false},
	}
	for _, c := range cases {
		if got := flow.Fallback(o, c.entries); got != c.want {
			t.Errorf("%s: fallback %v, want %v", c.name, got, c.want)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if flow.Fallback(o, []browser.Entry{app}) != true {
		t.Error("an Electron app with a SLICC profile dir made the fallback stop")
	}
	if flow.Fallback(o, []browser.Entry{chrome}) != false {
		t.Error("an installed browser with a SLICC profile fell back")
	}
}
