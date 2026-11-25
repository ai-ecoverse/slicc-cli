package cdp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	lhPage         = "LH-PAGE"
	lhGuard        = "LH-GUARD"
	lhClient       = "LH-CLIENT"
	lhOrdinary     = "https://billing.ordinary.test/app"
	lhRedirect     = "https://billing.ordinary.test/redirect"
	lhProtected    = "https://seven.sliccy.ai/secret?n=hop"
	lhVault        = "https://vault.sliccy.ai/doc"
	lhCredential   = "https://ims-na1.adobelogin.com/ims/authorize"
	lhAllowedAfter = "https://billing.ordinary.test/app?back=1"
)

type lhWire struct {
	t      *testing.T
	g      *guard
	chrome []string
	client []string
}

func lhNew(t *testing.T) *lhWire {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	w := &lhWire{t: t, g: newGuard(o, nil)}
	w.g.relaySession = "RELAY"
	w.g.observeAll([]targetInfo{{TargetID: lhPage, Type: "page", URL: lhOrdinary, BrowserContextID: "CTX"}})
	ready := make(chan struct{})
	close(ready)
	nt := &navTarget{target: lhPage, session: lhGuard, ready: ready, done: true, fetchArmed: true, documentURL: lhOrdinary, securityOrigin: "https://billing.ordinary.test", loaderID: "L0"}
	w.g.navTargets[lhPage] = nt
	w.g.navSessions[lhGuard] = nt
	w.g.sessions[lhClient] = &session{id: lhClient, target: lhPage, typ: "page"}
	w.g.sessions[lhGuard] = &session{id: lhGuard, target: lhPage, typ: "page"}
	w.g.mu.Lock()
	w.g.markTouchedLocked(lhPage)
	w.g.mu.Unlock()
	return w
}

func (w *lhWire) take(v browserVerdict) {
	if v.forward != nil {
		w.client = append(w.client, string(v.forward))
	}
	for _, p := range v.page {
		w.client = append(w.client, string(p))
	}
	for _, s := range v.send {
		w.chrome = append(w.chrome, string(s))
	}
}

func (w *lhWire) in(raw string) {
	w.take(w.g.fromBrowser([]byte(raw)))
}

func (w *lhWire) paused(url, requestID, redirectedFrom string) {
	p := map[string]any{
		"requestId": requestID, "networkId": "N-" + requestID, "frameId": lhPage, "resourceType": "Document",
		"request": map[string]any{"url": url, "method": "GET", "headers": map[string]any{}},
	}
	if redirectedFrom != "" {
		p["redirectedRequestId"] = redirectedFrom
	}
	b, _ := json.Marshal(map[string]any{"method": "Fetch.requestPaused", "sessionId": lhGuard, "params": p})
	w.in(string(b))
}

func (w *lhWire) failedProtected(url, requestID, redirectedFrom string) {
	w.t.Helper()
	before := len(w.chrome)
	w.paused(url, requestID, redirectedFrom)
	joined := strings.Join(w.chrome[before:], " ")
	if !strings.Contains(joined, `"Fetch.failRequest"`) || !strings.Contains(joined, requestID) {
		w.t.Fatalf("protected Document %s was not failed: %s", url, joined)
	}
	w.g.mu.Lock()
	locked := w.g.navTargets[lhPage].historyLocked
	w.g.mu.Unlock()
	if !locked {
		w.t.Fatal("history was not locked after a protected Document fail")
	}
}

func (w *lhWire) continued(url, requestID string) {
	w.t.Helper()
	before := len(w.chrome)
	w.paused(url, requestID, "")
	joined := strings.Join(w.chrome[before:], " ")
	if strings.Contains(joined, `"Fetch.failRequest"`) {
		w.t.Fatalf("Document %s was failed: %s", url, joined)
	}
	if !strings.Contains(joined, `"Fetch.continueRequest"`) {
		w.t.Fatalf("Document %s was not continued: %s", url, joined)
	}
}

func TestLeaseHopDocumentsArmedProtectedDocumentFails(t *testing.T) {
	w := lhNew(t)
	w.failedProtected(lhProtected, "R-302", "R-ORD")
	w2 := lhNew(t)
	w2.failedProtected(lhVault, "R-META", "")
	w3 := lhNew(t)
	w3.failedProtected(lhProtected, "R-ASSIGN", "")
	w4 := lhNew(t)
	w4.failedProtected(lhVault, "R-FORM", "")
}

func TestLeaseHopArmedCredentialDocumentContinues(t *testing.T) {
	w := lhNew(t)
	w.continued(lhCredential, "R-SSO")
	w.g.mu.Lock()
	locked := w.g.navTargets[lhPage].historyLocked
	w.g.mu.Unlock()
	if locked {
		t.Fatal("credential Document must not history-lock the tab")
	}
}

func TestLeaseHopArmedSSORoundTripCredentialThenProtected(t *testing.T) {
	w := lhNew(t)
	w.continued(lhCredential, "R-IDP")
	w.failedProtected(lhProtected, "R-BACK", "R-IDP")
}

func TestLeaseHopHistoryLockSurvivesReturnToAllowed(t *testing.T) {
	w := lhNew(t)
	w.failedProtected(lhProtected, "R-LOCK", "")
	w.g.mu.Lock()
	nt := w.g.navTargets[lhPage]
	nt.documentURL = lhAllowedAfter
	nt.errorURL = ""
	nt.securityOrigin = "https://billing.ordinary.test"
	locked := nt.historyLocked
	w.g.mu.Unlock()
	if !locked {
		t.Fatal("history lock cleared")
	}
	pv := w.g.fromCommand(&command{id: 42, method: "Runtime.evaluate", session: lhClient, params: map[string]any{"expression": "1"}}, 42, true)
	if pv.reply == nil || !strings.Contains(string(pv.reply), "history may restore a protected document") {
		t.Fatalf("return to allowed must keep content refused: %+v", pv)
	}
}

func TestLeaseHopCreateTargetTouchesOnlyAfterReply(t *testing.T) {
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	g.browserSessions[""] = true
	var chrome []string
	g.flushAsync = func(send, _ [][]byte) {
		for _, m := range send {
			chrome = append(chrome, string(m))
		}
	}
	pv := g.fromCommand(&command{id: 7, method: "Target.createTarget", params: map[string]any{"url": lhRedirect, "background": false}}, 7, true)
	if pv.reply != nil || pv.forward == nil {
		t.Fatalf("createTarget not admitted: %+v", pv)
	}
	if g.navTargets["NEWTAB"] != nil {
		t.Fatal("nav target existed before createTarget reply")
	}
	joined := strings.Join(chrome, " ")
	if strings.Contains(joined, `"Fetch.enable"`) {
		t.Fatalf("Fetch.enable before createTarget reply: %s", joined)
	}
	v := g.fromBrowser([]byte(`{"id":7,"result":{"targetId":"NEWTAB"}}`))
	for _, s := range v.send {
		chrome = append(chrome, string(s))
	}
	if g.navTargets["NEWTAB"] == nil {
		t.Fatal("createTarget reply did not touch the new tab")
	}
	joined = strings.Join(chrome, " ")
	if !strings.Contains(joined, `"Target.attachToTarget"`) {
		t.Fatalf("touch did not attach: %s", joined)
	}
	if strings.Contains(joined, `"Fetch.enable"`) {
		t.Fatalf("Fetch.enable must wait for the guard attach reply, not the createTarget reply: %s", joined)
	}
}

func TestLeaseHopCreateTargetDoesNotWaitForFetchArm(t *testing.T) {
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	pv := g.fromCommand(&command{id: 9, method: "Target.createTarget", params: map[string]any{"url": lhRedirect, "background": false}}, 9, true)
	if pv.wait != nil {
		t.Fatal("createTarget must not park on the navigation guard")
	}
	if pv.forward == nil {
		t.Fatalf("createTarget not forwarded: %+v", pv)
	}
}
