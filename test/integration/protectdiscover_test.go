package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoverPrintsSevensTabsAsTheirOrigin(t *testing.T) {
	srv := httptest.NewTLSServer(http.FileServer(http.Dir(filepath.Join("testdata", "link"))))
	t.Cleanup(srv.Close)
	const origin = "https://seven.sliccy.ai"
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP seven.sliccy.ai 127.0.0.1:"+srv.URL[strings.LastIndexByte(srv.URL, ':')+1:], "--ignore-certificate-errors")
	env, _ := launchEnv(t)
	tab := b.openPage(t, origin+"/#tailscale=fake-value-0021")
	id := "cdp:127.0.0.1:" + strings.TrimPrefix(b.endpoint, "http://127.0.0.1:")

	out, errOut, code := runWithEnv(t, env, "browsers", "--json", "--cdp", b.endpoint)
	printed := []string{out, errOut}
	var r report
	if err := json.Unmarshal([]byte(out), &r); code != 0 || err != nil {
		t.Fatalf("browsers --json: code %d, %v, stderr %s", code, err, errOut)
	}
	got := entryByCDP(r, b.endpoint)
	if got == nil || !got.Endpoint.SevenOpen || len(got.Endpoint.SevenTabs) != 1 {
		t.Fatalf("browsers --json doesn't list the open seven tab: %+v", r.Entries)
	}
	if listed := got.Endpoint.SevenTabs[0]; listed.URL != origin || listed.Title != origin {
		t.Errorf("browsers --json lists the seven tab as %+v, want url and title %s", listed, origin)
	}
	out, errOut, code = runWithEnv(t, env, "browsers", "--cdp", b.endpoint)
	if code != 0 {
		t.Fatalf("browsers: code %d, stderr %s", code, errOut)
	}
	printed = append(printed, out, errOut)

	f := startFlow(t, env, "--json", "--cdp", b.endpoint, "--browser", id, "--page", origin+"/")
	f.next(t, "discovered")
	f.next(t, "attached")
	if up := f.next(t, "linked"); up.Tab != origin {
		t.Errorf("linked names the seven tab %q, want %s", up.Tab, origin)
	}
	_ = interrupt(f.cmd)
	for e := range f.events {
		f.seen = append(f.seen, e)
	}
	f.wait(t)
	for _, e := range f.seen {
		printed = append(printed, e.raw)
	}
	printed = append(printed, f.stderr.String())
	a := startAttach(t, "--cdp", b.endpoint, "--name", "it-node")
	a.waitStderr(t, `link up with "link test page"`, 60*time.Second)
	if !strings.Contains(a.stderr.String(), "found seven at "+origin+"\n") {
		t.Errorf("attach doesn't name the seven tab by its origin:\n%s", a.stderr.String())
	}
	reap(t, a.cmd, "slicc attach")
	printed = append(printed, a.stderr.String())

	(&navEnv{linkEnv: &linkEnv{t: t, b: b}}).closeTarget(tab.target)
	plain := startFlow(t, append(env, "NO_COLOR=1"), "--plain", "--cdp", b.endpoint, "--browser", id, "--page", origin+"/#proxy=http://x&key=fake-value-0022")
	plain.waitStderr(t, `linked with "link test page" in `+origin+"\n")
	for _, want := range []string{"opened " + origin + " in a new tab\n", "found seven at " + origin + "\n"} {
		if !strings.Contains(plain.stderr.String(), want) {
			t.Errorf("--plain didn't say %q:\n%s", want, plain.stderr.String())
		}
	}
	_ = interrupt(plain.cmd)
	for e := range plain.events {
		printed = append(printed, e.raw)
	}
	plain.wait(t)
	printed = append(printed, plain.stderr.String())
	for _, p := range printed {
		for _, v := range []string{"fake-value-0021", "fake-value-0022"} {
			if strings.Contains(p, v) {
				t.Errorf("slicc printed seven's fragment %s: %s", v, p)
			}
		}
	}
}
