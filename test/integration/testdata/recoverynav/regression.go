package cdp

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

const (
	rnProtected   = "https://seven.sliccy.ai/missing?q=secret#frag"
	rnOrdinary    = "https://ordinary.test/away"
	rnError       = "chrome-error://chromewebdata/"
	rnUnparseable = "http://exa mple.sliccy.ai/protected"
)

func recoveryNavGuard(t *testing.T) *guard {
	t.Helper()
	o, err := protect.New([]string{"https://seven.sliccy.ai", "https://vault.sliccy.ai"})
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(o, nil)
	g.relaySession = "RELAY"
	g.patterns = g.protectedRequestPatterns()
	g.targets["OPENER"] = &target{id: "OPENER", typ: "page", url: "https://ordinary.test/"}
	g.sessions["S-OPENER"] = &session{id: "S-OPENER", target: "OPENER", typ: "page"}
	_ = g.fromBrowser(encode(map[string]any{
		"method": "Target.attachedToTarget",
		"params": map[string]any{
			"sessionId": "S-PAGE",
			"targetInfo": map[string]any{
				"targetId": "PAGE", "type": "page", "url": rnOrdinary, "title": "away", "openerId": "OPENER",
			},
			"waitingForDebugger": false,
		},
	}))
	if g.sessions["S-PAGE"] == nil {
		t.Fatal("client session missing")
	}
	nt := g.navTargets["PAGE"]
	if nt == nil {
		nt = &navTarget{target: "PAGE", session: "GUARD", ready: make(chan struct{})}
		g.navTargets["PAGE"] = nt
		g.navSessions["GUARD"] = nt
	}
	if nt.session == "" {
		nt.session = "GUARD"
		g.navSessions["GUARD"] = nt
	}
	pop := g.targets["PAGE"]
	pop.url = rnError
	_ = g.fromBrowser(encode(map[string]any{
		"sessionId": "GUARD",
		"method":    "Page.frameNavigated",
		"params": map[string]any{
			"frame": map[string]any{
				"id": "PAGE", "url": rnError, "securityOrigin": "://", "unreachableUrl": rnProtected,
			},
		},
	}))
	if nt.confirm != nil && !nt.confirm.done {
		frame := map[string]any{
			"id": "PAGE", "url": rnError, "securityOrigin": "://", "unreachableUrl": rnProtected,
		}
		raw, _ := json.Marshal(map[string]any{"frameTree": map[string]any{"frame": frame}})
		_ = g.confirmedTargetLocked(nt, nt.confirm, nt.revision, message{Result: raw})
	}
	if !nt.done {
		nt.settle(false)
	}
	nt.failed = false
	if nt.errorURL == "" {
		nt.errorURL = rnProtected
	}
	if len(nt.failedURLs) == 0 {
		nt.failedURLs = []string{rnProtected}
	}
	g.opened["PAGE"] = true
	g.setErrorProofLocked("PAGE", errorProofProtected)
	return g
}

func rnStartedNavigating() []byte {
	return encode(map[string]any{
		"sessionId": "GUARD",
		"method":    "Page.frameStartedNavigating",
		"params": map[string]any{
			"frameId": "PAGE", "loaderId": "L-RELOAD", "url": rnProtected, "navigationType": "differentDocument",
		},
	})
}

func rnFetchPausedProtected() []byte {
	return encode(map[string]any{
		"sessionId": "GUARD",
		"method":    "Fetch.requestPaused",
		"params": map[string]any{
			"requestId": "R1", "networkId": "N1", "frameId": "PAGE", "resourceType": "Document",
			"request": map[string]any{"url": rnProtected},
		},
	})
}

func rnLoadingFailed() []byte {
	return encode(map[string]any{
		"sessionId": "GUARD",
		"method":    "Network.loadingFailed",
		"params": map[string]any{
			"requestId": "R1", "errorText": "BlockedByClient", "canceled": true, "type": "Document",
		},
	})
}

func rnChromeErrorCommitted() []byte {
	return encode(map[string]any{
		"sessionId": "GUARD",
		"method":    "Page.frameNavigated",
		"params": map[string]any{
			"frame": map[string]any{
				"id": "PAGE", "url": rnError, "securityOrigin": "://", "unreachableUrl": rnProtected,
			},
		},
	})
}

func rnTargetInfoChromeError() []byte {
	return encode(map[string]any{
		"method": "Target.targetInfoChanged",
		"params": map[string]any{
			"targetInfo": map[string]any{
				"targetId": "PAGE", "type": "page", "url": rnError, "title": "seven.sliccy.ai",
			},
		},
	})
}

func rnSettleConfirmError(t *testing.T, g *guard) {
	t.Helper()
	nt := g.navTargets["PAGE"]
	if nt == nil || nt.confirm == nil || nt.confirm.done {
		return
	}
	frame := map[string]any{
		"id": "PAGE", "url": rnError, "securityOrigin": "://", "unreachableUrl": rnProtected,
	}
	raw, _ := json.Marshal(map[string]any{"frameTree": map[string]any{"frame": frame}})
	_ = g.confirmedTargetLocked(nt, nt.confirm, nt.revision, message{Result: raw})
}

func rnOpenConfirmFailed(t *testing.T, g *guard) {
	t.Helper()
	_ = g.fromBrowser(rnStartedNavigating())
	_ = g.fromBrowser(rnFetchPausedProtected())
	_ = g.fromBrowser(rnLoadingFailed())
	_ = g.fromBrowser(rnChromeErrorCommitted())
	_ = g.fromBrowser(rnTargetInfoChromeError())
	nt := g.navTargets["PAGE"]
	if nt == nil || nt.confirm == nil {
		t.Fatal("expected confirm after error commit")
	}
	if !nt.confirm.done {
		nt.confirm.settle(true)
	}
}

func rnCommandOutcome(g *guard, method string, params map[string]any, id int64) string {
	v := g.fromCommand(&command{id: id, method: method, session: "S-PAGE", params: params}, id, true)
	if v.reply == nil {
		if v.forward != nil || v.wait != nil || v.history != nil || v.closeProof != nil {
			return "admit"
		}
		return "empty"
	}
	s := string(v.reply)
	switch {
	case strings.Contains(s, refusedGuard):
		return "refusedGuard"
	case strings.Contains(s, refusedErrorPage):
		return "refusedErrorPage"
	case strings.Contains(s, refusedHistory):
		return "refusedHistory"
	case strings.Contains(s, refusedTarget):
		return "refusedTarget"
	case strings.Contains(s, refusedParse):
		return "refusedParse"
	case strings.Contains(s, "refused"):
		return "refusedOther"
	default:
		return "admit"
	}
}

type rnCell struct {
	name   string
	steps  []string
	method string
	params map[string]any
	admit  bool
}

func rnApply(t *testing.T, g *guard, steps []string) {
	t.Helper()
	for _, step := range steps {
		switch step {
		case "reload-start":
			_ = g.fromBrowser(rnStartedNavigating())
		case "fetch-fail":
			_ = g.fromBrowser(rnFetchPausedProtected())
		case "loading-failed":
			_ = g.fromBrowser(rnLoadingFailed())
		case "error-commit":
			_ = g.fromBrowser(rnChromeErrorCommitted())
			_ = g.fromBrowser(rnTargetInfoChromeError())
		case "confirm-settle":
			rnSettleConfirmError(t, g)
		case "confirm-fail":
			nt := g.navTargets["PAGE"]
			if nt != nil && nt.confirm != nil && !nt.confirm.done {
				nt.confirm.settle(true)
			}
		case "history-lock-protected":
			nt := g.navTargets["PAGE"]
			nt.historyLocked = true
			if nt.protectedHistory == nil {
				nt.protectedHistory = map[string]string{}
			}
			nt.protectedHistory["7"] = g.destinationReason(rnProtected)
		case "mark-protected-restored":
			pop := g.targets["PAGE"]
			pop.url = rnProtected
			pop.protected = true
			nt := g.navTargets["PAGE"]
			nt.errorURL = ""
			nt.documentURL = rnProtected
			nt.historyLocked = true
		default:
			t.Fatalf("unknown step %q", step)
		}
	}
}

func TestRecoveryNavOrderMatrix(t *testing.T) {
	afterFail := []string{"reload-start", "fetch-fail", "loading-failed", "error-commit"}
	cells := []rnCell{
		{name: "navigate-before-reload-fail", method: "Page.navigate", params: map[string]any{"url": rnOrdinary}, admit: true},
		{name: "navigate-after-reload-start", steps: []string{"reload-start"}, method: "Page.navigate", params: map[string]any{"url": rnOrdinary}, admit: true},
		{name: "navigate-between-fail-and-commit", steps: []string{"reload-start", "fetch-fail", "loading-failed"}, method: "Page.navigate", params: map[string]any{"url": rnOrdinary}, admit: true},
		{name: "navigate-after-error-commit-before-confirm", steps: afterFail, method: "Page.navigate", params: map[string]any{"url": rnOrdinary}, admit: true},
		{name: "navigate-after-confirm-settle", steps: append(append([]string{}, afterFail...), "confirm-settle"), method: "Page.navigate", params: map[string]any{"url": rnOrdinary}, admit: true},
		{name: "navigate-after-confirm-fail", steps: append(append([]string{}, afterFail...), "confirm-fail"), method: "Page.navigate", params: map[string]any{"url": rnOrdinary}, admit: true},
		{name: "reload-after-error-commit-before-confirm", steps: afterFail, method: "Page.reload", params: map[string]any{}, admit: true},
		{name: "reload-after-confirm-settle", steps: append(append([]string{}, afterFail...), "confirm-settle"), method: "Page.reload", params: map[string]any{}, admit: true},
		{name: "evaluate-after-error-commit-before-confirm", steps: afterFail, method: "Runtime.evaluate", params: map[string]any{"expression": "location.href"}, admit: false},
		{name: "evaluate-after-confirm-settle", steps: append(append([]string{}, afterFail...), "confirm-settle"), method: "Runtime.evaluate", params: map[string]any{"expression": "location.href"}, admit: false},
		{name: "evaluate-after-confirm-fail", steps: append(append([]string{}, afterFail...), "confirm-fail"), method: "Runtime.evaluate", params: map[string]any{"expression": "location.href"}, admit: false},
		{name: "dom-after-confirm-fail", steps: append(append([]string{}, afterFail...), "confirm-fail"), method: "DOM.getDocument", params: map[string]any{}, admit: false},
		{name: "screenshot-after-confirm-fail", steps: append(append([]string{}, afterFail...), "confirm-fail"), method: "Page.captureScreenshot", params: map[string]any{}, admit: false},
		{name: "history-entry-protected", steps: []string{"history-lock-protected"}, method: "Page.navigateToHistoryEntry", params: map[string]any{"entryId": json.Number("7")}, admit: false},
		{name: "navigate-to-protected-url", method: "Page.navigate", params: map[string]any{"url": rnProtected}, admit: false},
		{name: "navigate-to-unparseable-url", method: "Page.navigate", params: map[string]any{"url": rnUnparseable}, admit: false},
		{name: "scripted-reload-on-error-page", method: "Page.reload", params: map[string]any{"scriptToEvaluateOnLoad": "window.__x=1"}, admit: false},
		{name: "reload-restored-protected-document", steps: []string{"mark-protected-restored"}, method: "Page.reload", params: map[string]any{}, admit: false},
		{name: "resource-tree-on-error-page", method: "Page.getResourceTree", params: map[string]any{}, admit: false},
	}
	var red []string
	fmt.Printf("cell\toutcome\tcontract\n")
	for i, cell := range cells {
		t.Run(cell.name, func(t *testing.T) {
			g := recoveryNavGuard(t)
			rnApply(t, g, cell.steps)
			got := rnCommandOutcome(g, cell.method, cell.params, int64(100+i))
			contract := "admit"
			if !cell.admit {
				contract = "refuse"
			}
			fmt.Printf("%s\t%s\t%s\n", cell.name, got, contract)
			if cell.admit {
				if got == "refusedGuard" {
					red = append(red, cell.name)
					t.Errorf("RED own-tab recovery: %s got refusedGuard", cell.name)
				} else if got != "admit" {
					t.Errorf("%s: want admit, got %s", cell.name, got)
				}
			} else if got == "admit" {
				t.Errorf("%s: must stay refused, got admit", cell.name)
			}
		})
	}
	if len(red) > 0 {
		t.Logf("red cells: %s", strings.Join(red, ", "))
	}
}

func TestRecoveryNavConfirmFailedStaysUnreadableAndRecoverable(t *testing.T) {
	g := recoveryNavGuard(t)
	rnOpenConfirmFailed(t, g)
	nt := g.navTargets["PAGE"]
	if nt.confirm == nil || !nt.confirm.done || !nt.confirm.failed {
		t.Fatalf("confirm.failed not set: %+v", nt.confirm)
	}
	for i, method := range []string{"Runtime.evaluate", "DOM.getDocument", "Page.captureScreenshot"} {
		params := map[string]any{}
		if method == "Runtime.evaluate" {
			params["expression"] = "1"
		}
		got := rnCommandOutcome(g, method, params, int64(200+i))
		if got == "admit" {
			t.Errorf("%s admitted on confirm.failed error page", method)
		}
	}
	got := rnCommandOutcome(g, "Page.navigate", map[string]any{"url": rnOrdinary}, 210)
	if got == "refusedGuard" {
		t.Errorf("RED own-tab recovery: navigate-after-confirm-fail got refusedGuard")
	} else if got != "admit" {
		t.Errorf("navigate-after-confirm-fail: want admit, got %s", got)
	}
}

func TestRecoveryNavMutationWidenNavigationControls(t *testing.T) {
	if os.Getenv("SLICC_RECOVERY_NAV_MUTATION") == "" {
		t.Skip("overlay mutation only")
	}
	g := recoveryNavGuard(t)
	got := rnCommandOutcome(g, "Page.getResourceTree", map[string]any{}, 300)
	if got != "admit" {
		t.Fatalf("mutation probe expected widen to admit Page.getResourceTree, got %s", got)
	}
}
