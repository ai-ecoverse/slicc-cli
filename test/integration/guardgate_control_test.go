package integration

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
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
	commands, ok := ledger["commands"].([]any)
	if !ok {
		t.Fatalf("audit must carry commands: %v", ledger)
	}
	if len(commands) != 0 {
		t.Fatalf("empty hold window must yield empty commands: %v", commands)
	}
	if ledger["invalidFrames"] != float64(0) {
		t.Fatalf("empty hold window must report invalidFrames=0: %v", ledger)
	}
}

func TestGuardGateAuditCommandsOrderedSessionFilteredCumulative(t *testing.T) {
	g := newGuardGate(t, "http://unused.invalid", "http://client.test")
	c := &gateConnection{
		sessions:  map[string]string{"client": "T", "other": "T2"},
		urls:      map[string][]string{"T": {"http://seven.test/bfcache?n=case", "http://ordinary.test/bfcache?n=case-b"}},
		forwarded: map[string]bool{},
		ledger: []map[string]any{
			{"method": "Page.enable", "id": int64(1), "sessionId": "client"},
			{"method": "Runtime.evaluate", "id": int64(2), "sessionId": "other", "params": map[string]any{"expression": "1"}},
		},
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
	call(`{"action":"arm","targetId":"T","sessionId":"client","ms":1500,"side":"guarded","protectedOrigins":["http://seven.test"],"ordinaryOrigin":"http://ordinary.test"}`)
	c.mu.Lock()
	if c.auditFrom != 2 {
		c.mu.Unlock()
		t.Fatalf("arm must start commands after pre-hold ledger entries: auditFrom=%d", c.auditFrom)
	}
	c.ledger = append(c.ledger,
		map[string]any{"method": "DOM.getDocument", "id": int64(10), "sessionId": "client", "params": map[string]any{"depth": float64(-1), "pierce": true}},
		map[string]any{"method": "Runtime.evaluate", "id": int64(11), "sessionId": "other", "params": map[string]any{"expression": "secret"}},
		map[string]any{"method": "DOM.disable", "id": int64(12), "sessionId": "client"},
	)
	c.mu.Unlock()
	first := call(`{"action":"audit","sessionId":"client","probes":[]}`)
	wantFirst := []any{
		map[string]any{"method": "DOM.getDocument", "params": map[string]any{"depth": float64(-1), "pierce": true}},
		map[string]any{"method": "DOM.disable"},
	}
	if !reflect.DeepEqual(first["commands"], wantFirst) {
		t.Fatalf("first audit commands:\n got %#v\nwant %#v", first["commands"], wantFirst)
	}
	c.mu.Lock()
	c.ledger = append(c.ledger, map[string]any{"method": "DOM.getDocument", "id": int64(13), "sessionId": "client", "params": map[string]any{"depth": float64(-1), "pierce": true}})
	c.mu.Unlock()
	second := call(`{"action":"audit","sessionId":"client","probes":[]}`)
	wantSecond := append(append([]any{}, wantFirst...), map[string]any{"method": "DOM.getDocument", "params": map[string]any{"depth": float64(-1), "pierce": true}})
	if !reflect.DeepEqual(second["commands"], wantSecond) {
		t.Fatalf("second audit must extend the first (cumulative):\n got %#v\nwant %#v", second["commands"], wantSecond)
	}
	c.mu.Lock()
	c.first = time.Now().Add(-2 * time.Second).UnixMilli()
	c.events = 1
	c.mu.Unlock()
	call(`{"action":"release","sessionId":"client"}`)
	call(`{"action":"arm","targetId":"T","sessionId":"client","ms":1500,"side":"guarded","protectedOrigins":["http://seven.test"],"ordinaryOrigin":"http://ordinary.test"}`)
	c.mu.Lock()
	from := c.auditFrom
	c.ledger = append(c.ledger, map[string]any{"method": "Page.bringToFront", "id": int64(20), "sessionId": "client"})
	c.mu.Unlock()
	if from != 6 {
		t.Fatalf("re-arm must reset the commands start to the current ledger length: auditFrom=%d", from)
	}
	reset := call(`{"action":"audit","sessionId":"client","probes":[]}`)
	wantReset := []any{map[string]any{"method": "Page.bringToFront"}}
	if !reflect.DeepEqual(reset["commands"], wantReset) {
		t.Fatalf("re-arm must drop pre-arm commands from the next audit:\n got %#v\nwant %#v", reset["commands"], wantReset)
	}
}

func TestGuardGateRecorderSessionlessAndConcurrent(t *testing.T) {
	g := newGuardGate(t, "http://unused.invalid", "http://client.test")
	c := &gateConnection{
		sessions:  map[string]string{"client": "T"},
		urls:      map[string][]string{"T": {"http://seven.test/bfcache?n=case", "http://ordinary.test/bfcache?n=case-b"}},
		forwarded: map[string]bool{},
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
	call(`{"action":"arm","targetId":"T","sessionId":"client","ms":1500,"side":"guarded","protectedOrigins":["http://seven.test"],"ordinaryOrigin":"http://ordinary.test"}`)
	c.noteInbound([]byte(`{"id":1,"method":"Target.getTargetInfo","params":{"targetId":"T"}}`), nil)
	c.noteInbound([]byte(`{"id":2,"method":"DOM.getDocument","sessionId":"client","params":{"depth":-1,"pierce":true}}`), nil)
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		n := i
		go func() {
			defer ready.Done()
			<-start
			c.noteInbound([]byte(`{"id":`+strconv.Itoa(10+n)+`,"method":"DOM.disable","sessionId":"client"}`), nil)
		}()
	}
	close(start)
	ready.Wait()
	audit := call(`{"action":"audit","sessionId":"client","probes":[]}`)
	commands := audit["commands"].([]any)
	if len(commands) != 3 {
		t.Fatalf("sessionless browser command must be dropped and concurrent session appends kept: %#v", commands)
	}
	if commands[0].(map[string]any)["method"] != "DOM.getDocument" {
		t.Fatalf("recorder order: %#v", commands)
	}
	for _, cmd := range commands[1:] {
		if cmd.(map[string]any)["method"] != "DOM.disable" {
			t.Fatalf("concurrent appends: %#v", commands)
		}
	}
	if audit["invalidFrames"] != float64(0) {
		t.Fatalf("canonical frames must not raise invalidFrames: %v", audit)
	}
}

func TestGuardGateInvalidIntegralNonIntegerID(t *testing.T) {
	g := newGuardGate(t, "http://unused.invalid", "http://client.test")
	c := &gateConnection{
		sessions:  map[string]string{"client": "T"},
		urls:      map[string][]string{"T": {"http://seven.test/bfcache?n=case", "http://ordinary.test/bfcache?n=case-b"}},
		forwarded: map[string]bool{},
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
	call(`{"action":"arm","targetId":"T","sessionId":"client","ms":1500,"side":"guarded","protectedOrigins":["http://seven.test"],"ordinaryOrigin":"http://ordinary.test"}`)
	before := len(c.ledger)
	c.noteInbound([]byte(`{"id":1.0,"method":"DOM.getDocument","sessionId":"client","params":{"depth":-1,"pierce":true}}`), nil)
	c.noteInbound([]byte(`{"id":1e0,"method":"DOM.disable","sessionId":"client"}`), nil)
	c.noteInbound([]byte(`{"id":3,"method":"DOM.getDocument","sessionId":"client","params":{"depth":-1,"pierce":true}}`), nil)
	if len(c.ledger) != before+1 {
		t.Fatalf("non-integer ids must not enter the ledger; ledger=%#v", c.ledger)
	}
	audit := call(`{"action":"audit","sessionId":"client","probes":[]}`)
	if audit["invalidFrames"] != float64(2) {
		t.Fatalf("want invalidFrames=2 for 1.0 and 1e0; got %v", audit)
	}
	commands := audit["commands"].([]any)
	if len(commands) != 1 || commands[0].(map[string]any)["method"] != "DOM.getDocument" {
		t.Fatalf("only the canonical frame remains: %#v", commands)
	}
}

func TestGuardGateInvalidDuplicateAndCasedMethodKeys(t *testing.T) {
	g := newGuardGate(t, "http://unused.invalid", "http://client.test")
	c := &gateConnection{
		sessions:  map[string]string{"client": "T"},
		urls:      map[string][]string{"T": {"http://seven.test/bfcache?n=case", "http://ordinary.test/bfcache?n=case-b"}},
		forwarded: map[string]bool{},
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
	call(`{"action":"arm","targetId":"T","sessionId":"client","ms":1500,"side":"guarded","protectedOrigins":["http://seven.test"],"ordinaryOrigin":"http://ordinary.test"}`)
	before := len(c.ledger)
	c.noteInbound([]byte(`{"id":1,"method":"Accessibility.getFullAXTree","method":"DOM.getDocument","sessionId":"client","params":{"depth":-1,"pierce":true}}`), nil)
	c.noteInbound([]byte(`{"id":2,"method":"Accessibility.getFullAXTree","METHOD":"DOM.getDocument","sessionId":"client","params":{"depth":-1,"pierce":true}}`), nil)
	c.noteInbound([]byte(`{"id":3,"method":"DOM.disable","sessionId":"client"}`), nil)
	if len(c.ledger) != before+1 {
		t.Fatalf("duplicate/cased method keys must not enter the ledger; ledger=%#v", c.ledger)
	}
	audit := call(`{"action":"audit","sessionId":"client","probes":[]}`)
	if audit["invalidFrames"] != float64(2) {
		t.Fatalf("want invalidFrames=2 for duplicate and METHOD keys; got %v", audit)
	}
	commands := audit["commands"].([]any)
	if len(commands) != 1 || commands[0].(map[string]any)["method"] != "DOM.disable" {
		t.Fatalf("only the canonical frame remains: %#v", commands)
	}
}

func gateControlCall(t *testing.T, g *guardGate, body string) map[string]any {
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

func TestGuardGateInvalidFrameExtendedCases(t *testing.T) {
	g := newGuardGate(t, "http://unused.invalid", "http://client.test")
	c := &gateConnection{
		sessions:  map[string]string{"client": "T"},
		urls:      map[string][]string{"T": {"http://seven.test/bfcache?n=case", "http://ordinary.test/bfcache?n=case-b"}},
		forwarded: map[string]bool{},
	}
	g.connections = append(g.connections, c)
	gateControlCall(t, g, `{"action":"arm","targetId":"T","sessionId":"client","ms":1500,"side":"guarded","protectedOrigins":["http://seven.test"],"ordinaryOrigin":"http://ordinary.test"}`)

	type caseRow struct {
		name string
		raw  string
		pin  string
	}
	cases := []caseRow{
		{name: "neg-float-id", raw: `{"id":-1.0,"method":"DOM.disable","sessionId":"client"}`, pin: ".eE"},
		{name: "lone-METHOD", raw: `{"id":101,"METHOD":"DOM.disable","sessionId":"client"}`, pin: "allowlist"},
		{name: "lone-Method", raw: `{"id":102,"Method":"DOM.disable","sessionId":"client"}`, pin: "allowlist"},
		{name: "lone-SessionId", raw: `{"id":103,"method":"DOM.disable","SessionId":"client"}`, pin: "allowlist"},
		{name: "extra-key", raw: `{"id":104,"method":"DOM.disable","sessionId":"client","extra":1}`, pin: "allowlist"},
		{name: "non-json", raw: `not-json`, pin: "json.Valid"},
		{name: "json-array", raw: `[{"id":105,"method":"DOM.disable","sessionId":"client"}]`, pin: "top-object"},
		{name: "params-null", raw: `{"id":106,"method":"DOM.disable","sessionId":"client","params":null}`, pin: "canon"},
		{name: "trailing", raw: `{"id":107,"method":"DOM.disable","sessionId":"client"} trailing`, pin: "trailing"},
		{name: "params-nested-dup", raw: `{"id":108,"method":"DOM.getDocument","sessionId":"client","params":{"depth":-1,"depth":0,"pierce":true}}`, pin: "params-dup"},
	}
	beforeLedger := len(c.ledger)
	beforeInvalid := c.invalidFrames
	for i, tc := range cases {
		c.noteInbound([]byte(tc.raw), nil)
		wantInvalid := beforeInvalid + i + 1
		if c.invalidFrames != wantInvalid {
			t.Fatalf("%s (%s): invalidFrames=%d want %d", tc.name, tc.pin, c.invalidFrames, wantInvalid)
		}
		if len(c.ledger) != beforeLedger {
			t.Fatalf("%s: must not be ledgered; ledger=%#v", tc.name, c.ledger)
		}
		if len(c.forwarded) != 0 {
			t.Fatalf("%s: forwarded mark must stay unset; forwarded=%v", tc.name, c.forwarded)
		}
		if _, ok := gateParseCommand([]byte(tc.raw)); ok {
			t.Fatalf("%s: gateParseCommand must reject", tc.name)
		}
	}
	c.noteInbound([]byte(`{"id":200,"method":"DOM.disable","sessionId":"client"}`), nil)
	if !c.forwarded[gateKey("client", 200)] {
		t.Fatal("canonical frame must set the forwarded mark")
	}
	audit := gateControlCall(t, g, `{"action":"audit","sessionId":"client","probes":[]}`)
	if audit["invalidFrames"] != float64(len(cases)) {
		t.Fatalf("want invalidFrames=%d; got %v", len(cases), audit["invalidFrames"])
	}
	commands := audit["commands"].([]any)
	if len(commands) != 1 || commands[0].(map[string]any)["method"] != "DOM.disable" {
		t.Fatalf("only the canonical frame remains: %#v", commands)
	}
}

func TestGuardGateInvalidOverlappingCheckPins(t *testing.T) {
	if _, ok := gateParseCommand([]byte(`{"id":1.0,"method":"DOM.disable","sessionId":"client"}`)); ok {
		t.Fatal("1.0 must fail (.eE and ParseInt overlap)")
	}
	if !gateTopKeyConflict([]byte(`{"id":1,"method":"A","method":"B","sessionId":"client"}`)) {
		t.Fatal("exact duplicate method must trip seen (and folded)")
	}
	if !gateTopKeyConflict([]byte(`{"id":1,"method":"A","METHOD":"B","sessionId":"client"}`)) {
		t.Fatal("method+METHOD must trip folded")
	}
	raw := map[string]json.RawMessage{}
	if json.Unmarshal([]byte(`{"id":1,"METHOD":"DOM.disable","sessionId":"client"}`), &raw) != nil {
		t.Fatal("unmarshal")
	}
	if _, has := raw["method"]; has {
		t.Fatal("lone METHOD must not decode as method (allowlist pin)")
	}
	if _, ok := gateParseCommand([]byte(`{"id":1,"METHOD":"DOM.disable","sessionId":"client"}`)); ok {
		t.Fatal("lone METHOD must fail allowlist")
	}
}

func TestGuardGateRecorderProjectionAndConcurrentAudit(t *testing.T) {
	g := newGuardGate(t, "http://unused.invalid", "http://client.test")
	c := &gateConnection{
		sessions:  map[string]string{"client": "T"},
		urls:      map[string][]string{"T": {"http://seven.test/bfcache?n=case", "http://ordinary.test/bfcache?n=case-b"}},
		forwarded: map[string]bool{},
	}
	g.connections = append(g.connections, c)
	gateControlCall(t, g, `{"action":"arm","targetId":"T","sessionId":"client","ms":1500,"side":"guarded","protectedOrigins":["http://seven.test"],"ordinaryOrigin":"http://ordinary.test"}`)
	c.noteInbound([]byte(`{"id":1,"method":"Target.getTargetInfo","params":{"targetId":"T"}}`), nil)
	c.noteInbound([]byte(`{"id":2,"method":"DOM.getDocument","sessionId":"client","params":{"depth":-1,"pierce":true}}`), nil)
	c.noteInbound([]byte(`{"id":3,"method":"DOM.disable","sessionId":"client"}`), nil)
	var ready sync.WaitGroup
	ready.Add(3)
	start := make(chan struct{})
	var audits [2]map[string]any
	var auditErr [2]error
	for i := 0; i < 2; i++ {
		n := i
		go func() {
			defer ready.Done()
			<-start
			req, err := http.NewRequest(http.MethodPost, g.srv.URL+"/gate", strings.NewReader(`{"action":"audit","sessionId":"client","probes":[]}`))
			if err != nil {
				auditErr[n] = err
				return
			}
			req.Header.Set("Origin", g.origin)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				auditErr[n] = err
				return
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				auditErr[n] = errors.New("audit status " + strconv.Itoa(resp.StatusCode))
				return
			}
			var result map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				auditErr[n] = err
				return
			}
			audits[n] = result
		}()
	}
	go func() {
		defer ready.Done()
		<-start
		c.noteInbound([]byte(`{"id":4,"method":"DOM.disable","sessionId":"client"}`), nil)
	}()
	close(start)
	ready.Wait()
	for i, err := range auditErr {
		if err != nil {
			t.Fatalf("concurrent audit %d: %v", i, err)
		}
	}
	for _, audit := range audits {
		commands := audit["commands"].([]any)
		if len(commands) < 2 {
			t.Fatalf("concurrent audit under append: %#v", commands)
		}
		first := commands[0].(map[string]any)
		if first["method"] != "DOM.getDocument" {
			t.Fatalf("projection order: %#v", commands)
		}
		if _, has := first["id"]; has {
			t.Fatalf("commands must strip id: %#v", first)
		}
		if _, has := first["sessionId"]; has {
			t.Fatalf("commands must strip sessionId: %#v", first)
		}
		params, ok := first["params"].(map[string]any)
		if !ok || params["pierce"] != true {
			t.Fatalf("recorder-built getDocument must keep params: %#v", first)
		}
		foundDisable := false
		for _, cmd := range commands[1:] {
			m := cmd.(map[string]any)
			if m["method"] != "DOM.disable" {
				t.Fatalf("unexpected method: %#v", commands)
			}
			if _, has := m["params"]; has {
				t.Fatalf("DOM.disable projection must omit params: %#v", m)
			}
			foundDisable = true
		}
		if !foundDisable {
			t.Fatalf("want DOM.disable in projection: %#v", commands)
		}
	}
}
