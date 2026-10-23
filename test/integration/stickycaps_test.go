package integration

import (
	"encoding/json"
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

var stickyMethods = []string{"Network.setExtraHTTPHeaders", "Network.setUserAgentOverride", "Emulation.setUserAgentOverride", "Emulation.setLocaleOverride", "Emulation.setTimezoneOverride", "Network.setCacheDisabled", "Network.emulateNetworkConditions", "Network.emulateNetworkConditionsByRule", "Network.overrideNetworkState", "Network.setBlockedURLs"}

type stickyProbe struct {
	Origin   string `json:"origin"`
	Eligible bool   `json:"eligible"`
	Effect   *bool  `json:"effect"`
}

type stickyMeasurement struct {
	Method          string      `json:"method"`
	Mode            string      `json:"mode"`
	Leg             string      `json:"leg"`
	Supported       bool        `json:"supported"`
	BoundaryBlocked bool        `json:"boundaryBlocked"`
	Setup           *cdpOutcome `json:"setup"`
	Audit           struct {
		Complete bool `json:"complete"`
		Probes   []struct {
			Forwarded bool `json:"forwarded"`
		} `json:"probes"`
	} `json:"audit"`
	Ordinary  stickyProbe     `json:"ordinaryProbe"`
	Protected *stickyProbe    `json:"protectedProbe"`
	Commit    json.RawMessage `json:"commit"`
}

func stickyFixture(t *testing.T) *httptest.Server {
	t.Helper()
	base := persistentFixture(t)
	var mu sync.Mutex
	counts := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/503-data" {
			base.Config.Handler.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		counts[r.URL.RawQuery]++
		count := counts[r.URL.RawQuery]
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_ = json.NewEncoder(w).Encode(map[string]any{"count": count, "header": r.Header.Get("X-Slicc-503"), "ua": r.UserAgent()})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStickyFixtureRecordsHeaderAgentAndCacheEvidence(t *testing.T) {
	srv := stickyFixture(t)
	for want := 1; want <= 2; want++ {
		req, err := http.NewRequest(http.MethodGet, srv.URL+"/503-data?key=fixture", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Slicc-503", "marker")
		req.Header.Set("User-Agent", "slicc503-marker")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var value struct {
			Count  int    `json:"count"`
			Header string `json:"header"`
			UA     string `json:"ua"`
		}
		err = json.NewDecoder(res.Body).Decode(&value)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if value.Count != want || value.Header != "marker" || value.UA != "slicc503-marker" || res.Header.Get("Cache-Control") != "public, max-age=3600" {
			t.Fatalf("fixture network evidence missing: %+v", value)
		}
	}
}

func TestStickyConfigurationsAreMeasurementOnlyAndAllowCommits(t *testing.T) {
	cmd := exec.Command(requireNode(t), "--input-type=module", "-e", `
import assert from 'node:assert/strict';
import { stickyConfiguration, stickyMethods } from './testdata/link/sticky.js';
assert.equal(stickyMethods.length,10);
assert.equal(new Set(stickyMethods).size,10);
assert.deepEqual(stickyConfiguration('Network.setExtraHTTPHeaders','tag'),{headers:{'X-Slicc-503':'tag'}});
assert.equal(stickyConfiguration('Network.setUserAgentOverride','tag').userAgent,'slicc503-tag');
assert.equal(stickyConfiguration('Emulation.setUserAgentOverride','tag').userAgent,'slicc503-tag');
for(const method of ['Network.emulateNetworkConditions','Network.emulateNetworkConditionsByRule']) assert.equal(stickyConfiguration(method,'tag').offline,false);
assert.deepEqual(stickyConfiguration('Network.setBlockedURLs','tag').urls,['*503-data*']);
assert.throws(()=>stickyConfiguration('Page.setBypassCSP','tag'),/unknown sticky capability/);
`)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sticky configuration: %v\n%s", err, out)
	}
}

func runStickyBoundaryMeasurements(t *testing.T, mode string) {
	t.Helper()
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
	fixture := stickyFixture(t)
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
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", protected, "--name", "sticky-measurement-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatal("sticky measurement browser missing")
	}
	classified := 0
	for _, method := range stickyMethods {
		t.Run(method, func(t *testing.T) {
			p := *env.p
			p.t = t
			rows := map[string]stickyMeasurement{}
			for _, leg := range []string{"control", "baseline", "guarded"} {
				marker := strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + leg
				opts := map[string]any{"method": method, "mode": mode, "leg": leg, "marker": marker, "browser": hello.Browsers[0].ID, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser", "gate": gate.srv.URL + "/gate", "ordinary": ordinary, "protected": protected}
				var raw json.RawMessage
				p.evalWithin("linkTest.sticky("+jsArgs(opts)+")", &raw, 2*time.Minute)
				var row stickyMeasurement
				if err := json.Unmarshal(raw, &row); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join("..", "..", "artifacts", "sticky", mode, strings.ReplaceAll(method, ".", "-"))
				if err := os.MkdirAll(path, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, leg+".json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
				t.Logf("observed %s %s %s: %s", method, mode, leg, raw)
				if row.Method != method || row.Mode != mode || row.Leg != leg {
					t.Fatalf("sticky measurement identity missing: %s", raw)
				}
				if row.Setup != nil && !row.Setup.OK && row.Setup.Error.Code == -32601 {
					if leg != "control" {
						t.Fatal("native support changed between legs")
					}
					t.Skipf("native method unsupported with structured -32601: %s", raw)
				}
				if !row.Supported || !row.Ordinary.Eligible || row.Ordinary.Effect == nil || row.Ordinary.Origin != ordinary || len(row.Commit) == 0 {
					t.Fatalf("ordinary control or commit evidence incomplete: %s", raw)
				}
				if leg != "baseline" && row.Setup == nil {
					t.Fatal("setter terminal reply missing")
				}
				if leg == "control" && (!row.Setup.OK || !*row.Ordinary.Effect) {
					t.Fatalf("native ordinary positive control did not exercise setter: %s", raw)
				}
				if leg == "guarded" {
					if !row.Audit.Complete || len(row.Audit.Probes) != 1 {
						t.Fatal("setter wire audit missing")
					}
					if row.Setup.OK && (!row.Audit.Probes[0].Forwarded || !*row.Ordinary.Effect) {
						t.Fatalf("admitted setter ordinary effect not proven: %s", raw)
					}
					if !row.Setup.OK && (!row.Setup.refused() || row.Audit.Probes[0].Forwarded) {
						t.Fatalf("guard refusal not proven on wire: %s", raw)
					}
				}
				if row.BoundaryBlocked {
					var commit struct {
						Frame struct {
							URL         string `json:"url"`
							Unreachable string `json:"unreachableUrl"`
						} `json:"frame"`
					}
					if err := json.Unmarshal(row.Commit, &commit); err != nil {
						t.Fatal(err)
					}
					if leg != "guarded" || commit.Frame.URL != "chrome-error://chromewebdata/" || !strings.HasPrefix(commit.Frame.Unreachable, protected+"/") || row.Protected != nil {
						t.Fatalf("unverified or fabricated blocked boundary: %s", raw)
					}
				} else {
					if row.Protected == nil || (row.Protected.Eligible && row.Protected.Effect == nil) || (mode != "error" && row.Protected.Origin != protected) {
						t.Fatalf("protected observer evidence incomplete: %s", raw)
					}
				}
				rows[leg] = row
			}
			if *rows["baseline"].Ordinary.Effect {
				t.Fatal("ordinary baseline confounds setter control")
			}
			guarded, baseline := rows["guarded"], rows["baseline"]
			switch {
			case guarded.BoundaryBlocked:
				t.Log("BOUNDARY-BLOCKED: protected document not measured; observed blocking is not a no-effect claim")
			case !guarded.Protected.Eligible || !baseline.Protected.Eligible:
				t.Log("PROBE-UNAVAILABLE: no protected effect attribution or safety claim")
			case *baseline.Protected.Effect:
				t.Log("BASELINE-CONFOUNDED: no setter effect attribution or safety claim")
			case *guarded.Protected.Effect:
				t.Logf("SECURITY-CANDIDATE: sticky protected-side effect %s across %s", method, mode)
			default:
				t.Log("MEASURED: observable protected probe did not show this configured setter value")
			}
			classified++
		})
	}
	if classified == 0 {
		t.Fatal("no supported sticky capability boundary was observed")
	}
}
