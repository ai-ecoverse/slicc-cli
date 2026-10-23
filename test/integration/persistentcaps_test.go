package integration

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const refusedLaterText = "slicc: refused: scripts and bindings for later documents could run in seven’s pages or the vault, so the guard doesn’t allow them; run a script in the page you see with Runtime.evaluate instead"

type persistentMeasurement struct {
	BoundaryBlocked bool        `json:"boundaryBlocked"`
	Method          string      `json:"method"`
	Mode            string      `json:"mode"`
	Leg             string      `json:"leg"`
	Supported       bool        `json:"supported"`
	Setup           *cdpOutcome `json:"setup"`
	Audit           struct {
		Complete bool `json:"complete"`
		Probes   []struct {
			Forwarded bool `json:"forwarded"`
		} `json:"probes"`
	} `json:"audit"`
	OrdinaryProbe struct {
		Origin string `json:"origin"`
		Effect *bool  `json:"effect"`
	} `json:"ordinaryProbe"`
	ProtectedProbe struct {
		Origin string `json:"origin"`
		Effect *bool  `json:"effect"`
	} `json:"protectedProbe"`
	Commit json.RawMessage `json:"commit"`
}

func persistentFixture(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.URL.Path == "/fail" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("error fixture: %v", err)
				return
			}
			_ = conn.Close()
			return
		}
		if r.URL.Path == "/503-data" {
			mu.Lock()
			counts[r.URL.RawQuery]++
			count := counts[r.URL.RawQuery]
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			_ = json.NewEncoder(w).Encode(map[string]any{"count": count, "header": r.Header.Get("X-Slicc-503"), "ua": r.UserAgent()})
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Security-Policy", "script-src 'none'")
		marker := html.EscapeString(r.URL.Query().Get("marker"))
		_, _ = fmt.Fprintf(w, "<!doctype html><title>measurement %s</title><body data-marker=\"%s\"><p>measurement %s</p>", marker, marker, marker)
		if r.URL.Path == "/parent" {
			if r.URL.Query().Get("kind") == "srcdoc" {
				_, _ = fmt.Fprintf(w, "<iframe name=\"inherited503\" srcdoc=\"%s\"></iframe>", html.EscapeString("<body data-marker=\""+marker+"\"><p>inherited</p>"))
			} else {
				_, _ = fmt.Fprint(w, "<iframe name=\"inherited503\"></iframe>")
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestPersistentFixtureCSPAndNetworkEvidence(t *testing.T) {
	srv := persistentFixture(t)
	res, err := http.Get(srv.URL + "/doc?marker=fixture")
	if err != nil {
		t.Fatal(err)
	}
	if res.Header.Get("Content-Security-Policy") != "script-src 'none'" {
		t.Error("fixture did not enforce script CSP")
	}
	_ = res.Body.Close()
	for want := 1; want <= 2; want++ {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/503-data?key=fixture", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Slicc-503", "fixture")
		req.Header.Set("User-Agent", "slicc503-fixture")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var data struct {
			Count  int    `json:"count"`
			Header string `json:"header"`
			UA     string `json:"ua"`
		}
		err = json.NewDecoder(res.Body).Decode(&data)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if data.Count != want || data.Header != "fixture" || data.UA != "slicc503-fixture" || res.Header.Get("Cache-Control") != "public, max-age=3600" {
			t.Fatalf("fixture evidence missing: %+v", data)
		}
	}
}

func TestPersistentSetterAuditRequiresNumericIDsAndTerminalWireEvidence(t *testing.T) {
	cmd := exec.Command(requireNode(t), "--input-type=module", "-e", `
import assert from 'node:assert/strict';
import { auditSetter } from './testdata/link/persistent.js';
let request;
globalThis.fetch = async (url, opts) => { request = JSON.parse(opts.body); return { ok: true, json: async () => ({ probes: [{ id: 1, forwarded: true }] }) }; };
const audit = await auditSetter('gate', 'session', 37, { ok: true });
assert.deepEqual(request, { action: 'audit', sessionId: 'session', probes: [{ id: 1, wireId: 37 }] });
assert.equal(audit.complete, true);
globalThis.fetch = async () => ({ ok: false, status: 400, text: async () => 'invalid control', json: () => { throw new Error('must not parse error body'); } });
await assert.rejects(auditSetter('gate', 'session', 37, { ok: true }), /setter audit HTTP 400: invalid control/);
for (const probes of [[], [{ id: 'wrong', forwarded: true }], [{ id: 1 }], [{ id: 1, forwarded: false }]]) {
  globalThis.fetch = async () => ({ ok: true, json: async () => ({ probes }) });
  await assert.rejects(auditSetter('gate', 'session', 37, { ok: true }), /missing or inconsistent/);
}
globalThis.fetch = async () => ({ ok: true, json: async () => ({ probes: [{ id: 1, forwarded: false }] }) });
assert.equal((await auditSetter('gate', 'session', 37, { ok: false, error: { code: -32000 } })).complete, true);
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("setter audit regression: %v\n%s", err, out)
	}
}

func TestCDPDocumentPersistentCapabilityMeasurements(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("no Chrome found; set SLICC_TEST_CHROME")
		}
		t.Skip(harnessChromeNotice)
	}
	methods := []string{"Page.addScriptToEvaluateOnNewDocument", "Page.addScriptToEvaluateOnLoad", "Runtime.addBinding", "Page.setBypassCSP"}
	modes := []string{"plain", "history", "bfcache", "inherited-blank", "inherited-srcdoc", "error"}
	measured := 0
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			fixture := persistentFixture(t)
			port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
			ordinary := "http://ordinary.guard.test:" + port
			protected := "http://seven.guard.test:" + port
			srv := pageServer(t)
			extra := []string{"--host-resolver-rules=MAP *.guard.test 127.0.0.1"}
			if mode == "history" {
				extra = append(extra, "--disable-features=BackForwardCache")
			}
			b := launchBrowserOn(t, 0, extra...)
			gate := newGuardGate(t, b.endpoint, srv.URL)
			env := &linkEnv{t: t, b: b, origin: srv.URL}
			env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "measurement-node")
			env.p = b.openPage(t, srv.URL+"/index.html")
			env.waitUp("")
			var hello struct {
				Browsers []helloBrowser `json:"browsers"`
			}
			env.p.eval("linkTest.ready()", &hello)
			if len(hello.Browsers) != 1 {
				t.Fatalf("measurement browser missing: %+v", hello)
			}
			for _, method := range methods {
				t.Run(method, func(t *testing.T) {
					p := *env.p
					p.t = t
					rows := map[string]persistentMeasurement{}
					for _, leg := range []string{"control", "baseline", "guarded"} {
						marker := strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + leg
						opts := map[string]any{"method": method, "mode": mode, "leg": leg, "marker": marker, "browser": hello.Browsers[0].ID, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser", "gate": gate.srv.URL + "/gate", "ordinary": ordinary, "protected": protected}
						var raw json.RawMessage
						p.evalWithin("linkTest.persistent("+jsArgs(opts)+")", &raw, 90*time.Second)
						var row persistentMeasurement
						if err := json.Unmarshal(raw, &row); err != nil {
							t.Fatal(err)
						}
						if row.Method != method || row.Mode != mode || row.Leg != leg {
							t.Fatalf("measurement identity unavailable: %s", raw)
						}
						if row.Setup != nil && !row.Setup.OK && row.Setup.Error.Code == -32601 {
							if leg != "control" {
								t.Fatalf("support changed between legs: %s", raw)
							}
							t.Skipf("native Chrome reports unsupported method with structured -32601: %s", raw)
						}
						if !row.Supported || row.OrdinaryProbe.Effect == nil || (!row.BoundaryBlocked && row.ProtectedProbe.Effect == nil) || len(row.Commit) == 0 || row.OrdinaryProbe.Origin != ordinary {
							t.Fatalf("incomplete measurement: %s", raw)
						}
						if row.BoundaryBlocked {
							var blocked struct {
								Frame struct {
									URL         string `json:"url"`
									Unreachable string `json:"unreachableUrl"`
								} `json:"frame"`
							}
							if err := json.Unmarshal(row.Commit, &blocked); err != nil {
								t.Fatal(err)
							}
							if leg != "guarded" || blocked.Frame.URL != "chrome-error://chromewebdata/" || !strings.HasPrefix(blocked.Frame.Unreachable, protected+"/") {
								t.Fatalf("blocked boundary not independently verified: %s", raw)
							}
						}
						if !row.BoundaryBlocked && mode != "error" && row.ProtectedProbe.Origin != protected {
							t.Fatalf("protected origin not confirmed: %s", raw)
						}
						if leg != "baseline" && row.Setup == nil {
							t.Fatalf("terminal setter evidence missing: %s", raw)
						}
						if leg == "guarded" && (!row.Audit.Complete || len(row.Audit.Probes) != 1) {
							t.Fatalf("setter forwarding ledger missing: %s", raw)
						}
						if leg == "control" && (!row.Setup.OK || !*row.OrdinaryProbe.Effect) {
							t.Fatalf("ordinary positive control did not exercise capability: %s", raw)
						}
						if leg == "guarded" {
							if row.Setup.OK && (!row.Audit.Probes[0].Forwarded || !*row.OrdinaryProbe.Effect) {
								t.Fatalf("admitted capability did not work on ordinary page: %s", raw)
							}
							if row.Setup.OK || !row.Setup.refused() || row.Setup.Error.Message != refusedLaterText || row.Audit.Probes[0].Forwarded {
								t.Fatalf("exact bios refusal and non-forwarding not proven: %s", raw)
							}
						}
						rows[leg] = row
						path := filepath.Join("..", "..", "artifacts", "persistent", mode, strings.ReplaceAll(method, ".", "-"))
						if err := os.MkdirAll(path, 0755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(path, leg+".json"), raw, 0600); err != nil {
							t.Fatal(err)
						}
						t.Logf("measured %s %s %s: %s", method, mode, leg, raw)
					}
					if *rows["baseline"].OrdinaryProbe.Effect {
						t.Fatal("ordinary baseline confounds positive control")
					}
					if rows["guarded"].BoundaryBlocked {
						t.Log("BOUNDARY-BLOCKED: protected document/restore not measured; setter refusal and blocking independently verified")
					} else if *rows["baseline"].ProtectedProbe.Effect {
						t.Log("CONFOUNDED-PROBE: protected probe also succeeds without setter; no capability attribution or no-effect claim")
					} else if *rows["guarded"].ProtectedProbe.Effect {
						t.Errorf("protected-side effect remains after exact refusal for %s across %s", method, mode)
					}
					measured++
				})
			}
		})
	}
	if measured == 0 {
		t.Fatal("no supported persistent capability was measured")
	}
}
