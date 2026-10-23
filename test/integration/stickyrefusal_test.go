package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCDPRefusesStickyEmulationAndInheritedNavigationWithoutForwarding(t *testing.T) {
	fixture := stickyFixture(t)
	srv := pageServer(t)
	b := launchBrowser(t)
	gate := newGuardGate(t, b.endpoint, srv.URL)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--name", "sticky-refusal-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	opts := map[string]any{"browser": "chrome", "ordinary": strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1), "gate": gate.srv.URL + "/gate"}
	var rows []struct {
		Method   string          `json:"method"`
		Params   json.RawMessage `json:"params"`
		Expected *string         `json:"expected"`
		Setup    cdpOutcome      `json:"setup"`
		Audit    struct {
			Complete bool `json:"complete"`
			Probes   []struct {
				Forwarded bool `json:"forwarded"`
			} `json:"probes"`
		} `json:"audit"`
	}
	env.p.eval("linkTest.stickyAdmission("+jsArgs(opts)+")", &rows)
	if len(rows) != 26 {
		t.Fatalf("missing sticky refusal or regex boundary cases: %d", len(rows))
	}
	for _, row := range rows {
		if !row.Audit.Complete || len(row.Audit.Probes) != 1 {
			t.Fatalf("wire ledger missing: %+v", row)
		}
		if row.Expected == nil {
			if !row.Audit.Probes[0].Forwarded || row.Setup.refused() {
				t.Errorf("nonmatching Emulation method over-blocked: %s %+v", row.Method, row)
			}
			continue
		}
		want := refusedHeadersText
		if row.Method == "Page.navigate" {
			want = refusedInheritedText
		}
		if *row.Expected != want || row.Setup.OK || !row.Setup.refused() || row.Setup.Error.Message != want || row.Audit.Probes[0].Forwarded {
			t.Errorf("%s %s: forwarded, faked or wrong exact error: %+v", row.Method, row.Params, row)
		}
	}
}
