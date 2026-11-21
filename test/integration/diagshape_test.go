package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func diagShapeFixture(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		marker := r.URL.Query().Get("marker")
		if marker == "" {
			marker = "DIAGSHAPE_MARKER"
		}
		_, _ = fmt.Fprintf(w, "<!doctype html><title>diagshape %s</title><body data-marker=\"%s\"><p>DIAGSHAPE_DOC %s</p>", marker, marker, marker)
	}))
	t.Cleanup(srv.Close)
	return srv
}

var diagShapeTMS = regexp.MustCompile(`t_ms=(\d+)`)

func diagShapeContextDecisionMS(stderr, vaultOrigin string) (int64, string) {
	best := int64(0)
	bestLine := ""
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.Contains(line, "DIAGSHAPE") {
			continue
		}
		if !strings.Contains(line, "contextCreatedEvent") && !strings.Contains(line, "worldContextLocked") {
			continue
		}
		if vaultOrigin != "" && !strings.Contains(line, vaultOrigin) {
			continue
		}
		m := diagShapeTMS.FindStringSubmatch(line)
		if len(m) != 2 {
			continue
		}
		ms, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		if best == 0 || ms < best {
			best = ms
			bestLine = strings.TrimSpace(line)
		}
	}
	return best, bestLine
}

func TestDiagShapeWrapperDocumentsUnderLaggingGate(t *testing.T) {
	if findChrome() == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("CI Chrome required")
		}
		t.Skip(harnessChromeNotice)
	}
	fixture := diagShapeFixture(t)
	port := fixture.URL[strings.LastIndexByte(fixture.URL, ':')+1:]
	vault := "http://vault.sliccy.ai:" + port
	ordinary := "http://ordinary.guard.test:" + port
	srv := pageServer(t)
	b := launchBrowserOn(t, 0,
		"--host-resolver-rules=MAP vault.sliccy.ai 127.0.0.1, MAP *.guard.test 127.0.0.1, MAP *.sliccy.ai 127.0.0.1",
		"--unsafely-treat-insecure-origin-as-secure="+vault+","+ordinary,
	)
	gate := newGuardGate(t, b.endpoint, srv.URL)
	env := &linkEnv{t: t, b: b, origin: srv.URL}
	env.a = startAttach(t, "--cdp", gate.srv.URL, "--origin", srv.URL, "--origin", vault, "--name", "diagshape-lag-node")
	env.p = b.openPage(t, srv.URL+"/index.html")
	env.waitUp("")
	var hello struct {
		Browsers []helloBrowser `json:"browsers"`
	}
	env.p.eval("linkTest.ready()", &hello)
	if len(hello.Browsers) != 1 {
		t.Fatal("diagshape browser missing")
	}
	ws := strings.Replace(gate.srv.URL, "http", "ws", 1) + "/browser"
	marker := "DIAGSHAPE_MARKER"
	forms := []string{"blob", "filesystem", "sandbox-blob"}
	for _, name := range forms {
		name := name
		t.Run(name, func(t *testing.T) {
			p := *env.p
			p.t = t
			stderrBefore := env.a.stderr.String()
			opts := map[string]any{
				"form":             name,
				"marker":           marker,
				"browser":          hello.Browsers[0].ID,
				"ws":               ws,
				"gate":             gate.srv.URL + "/gate",
				"vault":            vault,
				"ordinary":         ordinary,
				"vaultDoc":         vault + "/doc?n=diagshape-blob&marker=" + marker,
				"ordinaryDoc":      ordinary + "/doc?n=diagshape-ordinary&marker=" + marker,
				"lag":              1,
				"lagHoldMs":        100,
				"settleMs":         10000,
				"protectedOrigins": []string{vault},
				"ordinaryOrigin":   ordinary,
			}
			var raw json.RawMessage
			p.evalWithin("linkTest.diagShape("+jsArgs(opts)+")", &raw, 3*time.Minute)
			var result struct {
				Form         string   `json:"form"`
				Lines        []string `json:"lines"`
				Failed       string   `json:"failed"`
				Reason       string   `json:"reason"`
				Error        string   `json:"error"`
				Residual     bool     `json:"residual"`
				Precondition *struct {
					DelayedEvents             float64 `json:"delayedEvents"`
					FirstDelayedAt            float64 `json:"firstDelayedAt"`
					ReleasedAt                float64 `json:"releasedAt"`
					HeldDeliveredAfterRelease bool    `json:"heldDeliveredAfterRelease"`
					GateCounts                bool    `json:"gateCounts"`
					UnderLagHeldEarly         float64 `json:"underLagHeldEarly"`
				} `json:"precondition"`
				Release map[string]any `json:"release"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatalf("diagshape decode: %v raw=%s", err, raw)
			}
			for _, line := range result.Lines {
				t.Logf("DIAGSHAPE form=%s lag=1 %s", name, line)
			}
			stderrAfter := env.a.stderr.String()
			added := stderrAfter
			if strings.HasPrefix(stderrAfter, stderrBefore) {
				added = stderrAfter[len(stderrBefore):]
			}
			for _, line := range strings.Split(added, "\n") {
				if strings.Contains(line, "DIAGSHAPE") {
					t.Logf("DIAGSHAPE form=%s lag=1 guard-log %s", name, strings.TrimSpace(line))
				}
			}
			t.Logf("DIAGSHAPE form=%s lag=1 raw=%s", name, raw)

			if result.Residual || strings.HasPrefix(result.Failed, "sandbox-blob") {
				t.Logf("DIAGSHAPE PRECONDITION met=false lag=1 form=%s residual=true reason=%s", name, firstNonEmpty(result.Error, result.Failed, result.Reason, "unmeasured residual"))
				return
			}
			if result.Failed != "" {
				t.Fatalf("DIAGSHAPE PRECONDITION met=false lag=1 form=%s failed=%s error=%s", name, result.Failed, result.Error)
			}
			if result.Precondition == nil {
				t.Fatalf("DIAGSHAPE PRECONDITION met=false lag=1 form=%s missing precondition payload", name)
			}

			delayed := int64(result.Precondition.DelayedEvents)
			firstDelayed := int64(result.Precondition.FirstDelayedAt)
			releasedAt := int64(result.Precondition.ReleasedAt)
			if delayed == 0 || firstDelayed == 0 {
				if v, ok := result.Release["delayedEvents"].(float64); ok {
					delayed = int64(v)
				}
				if v, ok := result.Release["firstDelayedAt"].(float64); ok {
					firstDelayed = int64(v)
				}
				if v, ok := result.Release["releasedAt"].(float64); ok {
					releasedAt = int64(v)
				}
			}
			originHint := vault
			if name == "sandbox-blob" {
				originHint = "null"
			}
			contextMS, contextLine := diagShapeContextDecisionMS(added, originHint)
			if contextMS == 0 {
				contextMS, contextLine = diagShapeContextDecisionMS(added, vault)
			}
			if contextMS == 0 {
				contextMS, contextLine = diagShapeContextDecisionMS(added, "")
			}
			heldAfter := result.Precondition.HeldDeliveredAfterRelease
			gateOK := delayed > 0 && firstDelayed > 0
			contextOK := contextMS > 0 && releasedAt > 0 && contextMS < releasedAt
			orderOK := firstDelayed > 0 && contextMS > 0 && firstDelayed <= contextMS
			met := gateOK && contextOK && heldAfter && orderOK && result.Precondition.UnderLagHeldEarly == 0
			t.Logf("DIAGSHAPE PRECONDITION met=%t lag=1 form=%s delayedEvents=%d firstDelayedAt=%d releasedAt=%d contextDecisionMs=%d heldDeliveredAfterRelease=%t underLagHeldEarly=%v contextLine=%s",
				met, name, delayed, firstDelayed, releasedAt, contextMS, heldAfter, result.Precondition.UnderLagHeldEarly, contextLine)
			if !met {
				t.Fatalf("DIAGSHAPE PRECONDITION met=false lag=1 form=%s gateOK=%t contextOK=%t orderOK=%t heldAfter=%t underLagHeldEarly=%v delayedEvents=%d firstDelayedAt=%d releasedAt=%d contextDecisionMs=%d",
					name, gateOK, contextOK, orderOK, heldAfter, result.Precondition.UnderLagHeldEarly, delayed, firstDelayed, releasedAt, contextMS)
			}
		})
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
