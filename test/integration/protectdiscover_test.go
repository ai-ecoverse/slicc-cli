package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverPrintsSevensTabsAsTheirOrigin(t *testing.T) {
	srv := httptest.NewTLSServer(http.FileServer(http.Dir(filepath.Join("testdata", "link"))))
	t.Cleanup(srv.Close)
	const origin = "https://seven.sliccy.ai"
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP seven.sliccy.ai 127.0.0.1:"+srv.URL[strings.LastIndexByte(srv.URL, ':')+1:], "--ignore-certificate-errors")
	env, _ := launchEnv(t)
	b.openPage(t, origin+"/#tailscale=fake-value-0021")
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
	if tab := got.Endpoint.SevenTabs[0]; tab.URL != origin || tab.Title != origin {
		t.Errorf("browsers --json lists the seven tab as %+v, want url and title %s", tab, origin)
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
	for _, p := range printed {
		if strings.Contains(p, "fake-value-0021") {
			t.Errorf("slicc printed seven's fragment: %s", p)
		}
	}
}
