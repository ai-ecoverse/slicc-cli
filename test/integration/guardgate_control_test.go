package integration

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGuardGateAcknowledgementAndLedgerRemainAfterRelease(t *testing.T) {
	g := newGuardGate(t, "http://unused.invalid", "http://client.test")
	c := &gateConnection{
		sessions:  map[string]string{"client": "T"},
		urls:      map[string][]string{"T": {"http://seven.test/bfcache?n=case", "http://ordinary.test/bfcache?n=case-b"}},
		forwarded: map[string]bool{gateKey("client", 12): true},
	}
	g.connections = append(g.connections, c)
	call := func(body string) map[string]any {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, g.srv.URL+"/gate", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", g.origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%d: %s", resp.StatusCode, body)
		}
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	ack := call(`{"action":"arm","targetId":"T","sessionId":"client","ms":1500,"side":"control","protectedOrigins":["http://seven.test"],"ordinaryOrigin":"http://ordinary.test"}`)
	c.mu.Lock()
	controlOnly := c.controlOnly
	c.first, c.events = time.Now().Add(-2*time.Second).UnixMilli(), 1
	c.mu.Unlock()
	if ack["protectedObserved"] != true || ack["ordinaryObserved"] != true || ack["gate"] != true || !controlOnly {
		t.Fatalf("unacknowledged seed: %v", ack)
	}
	released := call(`{"action":"release","sessionId":"client"}`)
	c.mu.Lock()
	active := c.hold != nil
	c.mu.Unlock()
	if released["delayedEvents"] != float64(1) || active {
		t.Fatal(released)
	}
	ledger := call(`{"action":"audit","sessionId":"client","probes":[{"id":1,"wireId":11},{"id":2,"wireId":12}]}`)
	entries := ledger["probes"].([]any)
	if entries[0].(map[string]any)["forwarded"] != false || entries[1].(map[string]any)["forwarded"] != true {
		t.Fatal(ledger)
	}
}
