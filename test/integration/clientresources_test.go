package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCDPClientResourceRequestsRefuseProtectedHopsBeforeSending(t *testing.T) {
	fixture := resourceRequestFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	ordinary := "http://ordinary.guard.test:" + port
	srv := pageServer(t)
	b := launchBrowserOn(t, 0, "--host-resolver-rules=MAP *.guard.test 127.0.0.1,MAP vault.sliccy.ai 127.0.0.1")
	gate := newGuardGate(t, b.endpoint, srv.URL)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", "http://seven.guard.test:"+port, "--name", "client-resource-probe-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatal("client resource browser missing")
	}
	for _, host := range []string{"seven.guard.test", "vault.sliccy.ai"} {
		for _, method := range []string{"Network.loadNetworkResource", "Network.replayXHR", "Fetch.continueRequest", "Network.continueInterceptedRequest", "Fetch.fulfillRequest", "Fetch.continueWithAuth"} {
			for _, redirect := range []bool{false, true} {
				if method == "Network.replayXHR" && !redirect {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/redirect=%t", host, method, redirect), func(t *testing.T) {
					p := *env.p
					p.t = t
					for _, leg := range []string{"control", "guarded"} {
						key := strconv.FormatInt(time.Now().UnixNano(), 36) + "-" + leg
						opts := map[string]any{"method": method, "key": key, "leg": leg, "redirect": redirect, "browser": hello.Browsers[0].ID, "ordinary": ordinary, "protected": "http://" + host + ":" + port, "ws": strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser", "gate": gate.srv.URL + "/gate", "counts": fixture.URL + "/counts", "arm": fixture.URL + "/arm"}
						var raw json.RawMessage
						p.evalWithin("linkTest.clientResource("+jsArgs(opts)+")", &raw, 40*time.Second)
						var row struct {
							Method string     `json:"method"`
							Key    string     `json:"key"`
							Setup  cdpOutcome `json:"setup"`
							Audit  struct {
								Complete bool `json:"complete"`
								Probes   []struct {
									Forwarded bool `json:"forwarded"`
								} `json:"probes"`
							} `json:"audit"`
							Counts map[string]int `json:"counts"`
							Events []struct {
								Method string `json:"method"`
								Params struct {
									ErrorText string `json:"errorText"`
								} `json:"params"`
							} `json:"events"`
						}
						if err := json.Unmarshal(raw, &row); err != nil {
							t.Fatal(err)
						}
						path := filepath.Join("..", "..", "artifacts", "client-resources", host, method, fmt.Sprintf("redirect-%t", redirect))
						if err := os.MkdirAll(path, 0755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(path, leg+".json"), raw, 0600); err != nil {
							t.Fatal(err)
						}
						t.Logf("client %s %s redirect=%t %s: protected server hits=%d", host, method, redirect, leg, row.Counts["protected"])
						if row.Method != method || row.Key != key {
							t.Fatal("client request provenance missing")
						}
						if leg == "control" {
							if !row.Setup.OK && row.Setup.Error.Code == -32601 {
								t.Fatalf("native client API unavailable; this is not zero-hit refusal proof: %s", raw)
							}
							if !row.Setup.OK || row.Counts["protected"] == 0 {
								t.Fatalf("native client request positive control did not follow or reach protected origin: %s", raw)
							}
							continue
						}
						mutation := strings.HasPrefix(method, "Fetch.") || method == "Network.continueInterceptedRequest"
						if mutation && row.Setup.OK {
							blocked := false
							for _, e := range row.Events {
								blocked = blocked || (e.Method == "Network.loadingFailed" && strings.Contains(e.Params.ErrorText, "ERR_BLOCKED_BY_CLIENT"))
							}
							if !blocked || !row.Audit.Complete || len(row.Audit.Probes) != 1 || !row.Audit.Probes[0].Forwarded || row.Counts["protected"] != 0 {
								t.Errorf("mutated request did not terminate before protected hop: %s", raw)
							}
							continue
						}
						if !row.Setup.refused() || !row.Audit.Complete || len(row.Audit.Probes) != 1 || row.Audit.Probes[0].Forwarded || row.Counts["protected"] != 0 {
							t.Errorf("client path sent or could follow protected hop: %s", raw)
						}
					}
				})
			}
		}
	}
}
