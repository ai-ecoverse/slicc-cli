package integration

import (
	"strings"
	"testing"
)

func TestCDPRefusesAllLaterDocumentMethodsExactlyWithoutForwarding(t *testing.T) {
	fixture := persistentFixture(t)
	srv := pageServer(t)
	b := launchBrowser(t)
	gate := newGuardGate(t, b.endpoint, srv.URL)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--name", "refusal-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	ordinary := strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1)
	opts := map[string]any{"browser": "chrome", "ordinary": ordinary, "gate": gate.srv.URL + "/gate"}
	var rows []struct {
		Method string     `json:"method"`
		Setup  cdpOutcome `json:"setup"`
		Audit  struct {
			Complete bool `json:"complete"`
			Probes   []struct {
				Forwarded bool `json:"forwarded"`
			} `json:"probes"`
		} `json:"audit"`
	}
	env.p.eval("linkTest.laterAdmission("+jsArgs(opts)+")", &rows)
	if len(rows) != 4 {
		t.Fatalf("missing refusal rows: %+v", rows)
	}
	for _, row := range rows {
		if !row.Setup.refused() || row.Setup.Error.Message != refusedLaterText || !row.Audit.Complete || len(row.Audit.Probes) != 1 || row.Audit.Probes[0].Forwarded {
			t.Errorf("later method %s forwarded, faked or returned wrong CDP error: %+v", row.Method, row)
		}
	}
}
