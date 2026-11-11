package integration

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestCDPStripsIsolatedWorldGrantsOnAnOrdinaryBrowser(t *testing.T) {
	fixture := persistentFixture(t)
	ordinary := strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1)
	srv := pageServer(t)
	b := launchBrowser(t)
	gate := newGuardGate(t, b.endpoint, srv.URL)
	var mu sync.Mutex
	var worlds []string
	gate.observe = func(m gateCommand) {
		if m.Method != "Page.createIsolatedWorld" {
			return
		}
		mu.Lock()
		worlds = append(worlds, string(m.Params))
		mu.Unlock()
	}
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--name", "isolated-strip-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	opts := map[string]any{"browser": "chrome", "ordinary": ordinary}
	var r struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		World  int64  `json:"executionContextId"`
		Params string `json:"params"`
	}
	env.p.eval("linkTest.stockIsolatedWorld("+jsArgs(opts)+")", &r)
	if !r.OK || r.World == 0 {
		t.Fatalf("Page.createIsolatedWorld on an ordinary browser: %+v", r)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(worlds) != 1 {
		t.Fatalf("Chrome saw %d createIsolatedWorld calls, want 1: %v", len(worlds), worlds)
	}
	if strings.Contains(worlds[0], "grantUniveralAccess") || strings.Contains(worlds[0], "contentSecurityPolicy") {
		t.Fatalf("Chrome received grants that should have been stripped: %s", worlds[0])
	}
}

func TestCDPRefusesRemovingAnAdmittedScriptIdentifierTwice(t *testing.T) {
	fixture := persistentFixture(t)
	ordinary := strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1)
	srv := pageServer(t)
	b := launchBrowser(t)
	gate := newGuardGate(t, b.endpoint, srv.URL)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--name", "script-remove-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	opts := map[string]any{"browser": "chrome", "ordinary": ordinary}
	var r struct {
		Identifier string     `json:"identifier"`
		First      cdpOutcome `json:"first"`
		Second     cdpOutcome `json:"second"`
	}
	env.p.eval("linkTest.stockScriptRemoveTwice("+jsArgs(opts)+")", &r)
	if r.Identifier == "" || !r.First.OK || !r.Second.refused() || r.Second.Error.Message != refusedLaterText {
		t.Fatalf("removing an admitted script identifier twice: %+v", r)
	}
	var forwarded int
	for _, c := range gate.connections {
		c.mu.Lock()
		for _, entry := range c.ledger {
			if entry["method"] == "Page.removeScriptToEvaluateOnNewDocument" {
				forwarded++
			}
		}
		c.mu.Unlock()
	}
	if forwarded != 1 {
		t.Fatalf("Chrome saw %d removeScript calls, want 1", forwarded)
	}
}

func TestGuardGateLedgerEntryFallsBackToMethod(t *testing.T) {
	g := newGuardGate(t, "http://unused.invalid", "http://client.test")
	c := &gateConnection{
		ledger: []map[string]any{
			{"method": "Page.enable", "id": int64(1), "sessionId": "S"},
			{"method": "Page.createIsolatedWorld", "id": int64(99), "sessionId": "S", "params": map[string]any{"frameId": "T", "worldName": "probe"}},
		},
	}
	g.connections = append(g.connections, c)
	entry := g.ledgerEntry(7, "Page.createIsolatedWorld", "S")
	if entry == nil || entry["method"] != "Page.createIsolatedWorld" {
		t.Fatalf("method-scoped lookup: %+v", entry)
	}
	raw, _ := json.Marshal(entry["params"])
	if !strings.Contains(string(raw), `"worldName":"probe"`) {
		t.Fatalf("method-scoped entry lost params: %s", raw)
	}
}
