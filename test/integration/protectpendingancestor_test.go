package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func pendingAncestorIframeScript(seven, allowed, settle string) fakeScript {
	vault := "https://vault.sliccy.ai"
	tab := map[string]any{"targetId": "T-ordinary", "type": "tab", "url": allowed + "/framed", "browserContextId": "C-ordinary"}
	page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/framed", "browserContextId": "C-ordinary"}
	frameURL := allowed + "/inner"
	info := func(url string) map[string]any {
		return map[string]any{"targetId": "F-plain", "type": "iframe", "url": url, "parentId": "P-ordinary", "parentFrameId": "P-ordinary", "attached": true, "browserContextId": "C-ordinary"}
	}
	bodies := map[string]map[string]any{"T-ordinary": tab, "P-ordinary": page, "F-plain": info("")}
	protectedURL := seven + "/doc"
	protectedOrigin := seven
	if strings.HasPrefix(settle, "vault-") {
		protectedURL = vault + "/doc"
		protectedOrigin = vault
	}
	confirmTree, _ := json.Marshal(map[string]any{"frameTree": map[string]any{"frame": map[string]any{
		"id": "P-ordinary", "url": protectedURL, "securityOrigin": protectedOrigin,
	}}})
	ordinaryTree, _ := json.Marshal(map[string]any{"frameTree": map[string]any{"frame": map[string]any{
		"id": "P-ordinary", "url": allowed + "/framed", "securityOrigin": allowed,
	}}})
	event := func(session, method string, params map[string]any) string {
		m := map[string]any{"method": method, "params": params}
		if session != "" {
			m["sessionId"] = session
		}
		raw, _ := json.Marshal(m)
		return string(raw)
	}
	created := func(session string, body map[string]any) string {
		return event(session, "Target.targetCreated", map[string]any{"targetInfo": body})
	}
	attach := func(parent, session string, body map[string]any, waiting bool) string {
		return event(parent, "Target.attachedToTarget", map[string]any{"sessionId": session, "targetInfo": body, "waitingForDebugger": waiting})
	}
	armed, framed, committed, confirming := false, false, false, false
	attaches := 0
	pendingConfirm := false
	return func(method string, params json.RawMessage) (string, []string) {
		var p struct {
			Name     string `json:"name"`
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(params, &p)
		switch method {
		case "Browser.getHistogram":
			switch {
			case p.Name == "arm-pending-pages" && !armed:
				armed = true
				return "{}", []string{
					created("RELAY", tab), created("", tab), attach("", "S-tab", tab, false),
					created("RELAY", page), created("", page), attach("", "S-page", page, false),
				}
			case p.Name == "arm-pending-iframes" && armed && !framed:
				framed = true
				body := info("")
				return "{}", []string{
					created("RELAY", body), created("", body),
					attach("S-tab", "S-F-plain-tab", map[string]any{"targetId": "F-plain", "type": "iframe", "url": "", "attached": true, "browserContextId": "C-ordinary"}, false),
					attach("S-page", "S-F-plain", info(""), true),
					event("S-page", "Page.frameAttached", map[string]any{"frameId": "F-plain", "parentFrameId": "P-ordinary"}),
					event("S-page", "Page.frameDetached", map[string]any{"frameId": "F-plain", "reason": "swap"}),
				}
			case p.Name == "arm-page-confirm" && framed && !confirming:
				confirming = true
				pendingConfirm = true
				return "{}", []string{
					event("S-page", "Page.frameNavigated", map[string]any{
						"type":  "Navigation",
						"frame": map[string]any{"id": "P-ordinary", "url": "about:blank", "securityOrigin": "://"},
					}),
				}
			case p.Name == "commit-pending-iframes" && framed && !committed:
				committed = true
				bodies["F-plain"] = info(frameURL)
				changed := info(frameURL)
				return "{}", []string{
					event("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": changed}),
					event("", "Target.targetInfoChanged", map[string]any{"targetInfo": changed}),
				}
			case p.Name == "settle-owner-url" && framed:
				bodies["P-ordinary"] = map[string]any{"targetId": "P-ordinary", "type": "page", "url": protectedURL, "browserContextId": "C-ordinary"}
				changed := bodies["P-ordinary"]
				pendingConfirm = true
				return "{}", []string{
					event("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": changed}),
					event("", "Target.targetInfoChanged", map[string]any{"targetInfo": changed}),
				}
			case p.Name == "settle-owner-nav" && framed:
				bodies["P-ordinary"] = map[string]any{"targetId": "P-ordinary", "type": "page", "url": protectedURL, "browserContextId": "C-ordinary"}
				return "{}", []string{
					event("S-page", "Page.frameNavigated", map[string]any{
						"type":  "Navigation",
						"frame": map[string]any{"id": "P-ordinary", "url": protectedURL, "securityOrigin": protectedOrigin},
					}),
				}
			}
			return "{}", nil
		case "Page.getFrameTree":
			tree := confirmTree
			if settle == "ordinary" || settle == "ordinary-confirm" {
				tree = ordinaryTree
			}
			if pendingConfirm {
				pendingConfirm = false
				return "#pending:page-confirm|" + string(tree), nil
			}
			return string(tree), nil
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			list := []any{tab, bodies["P-ordinary"]}
			if framed {
				list = append(list, bodies["F-plain"])
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": list})
			return string(raw), nil
		case "Target.getTargetInfo":
			if body, ok := bodies[p.TargetID]; ok {
				raw, _ := json.Marshal(map[string]any{"targetInfo": body})
				return string(raw), nil
			}
		case "Target.attachToTarget":
			attaches++
			return fmt.Sprintf(`{"sessionId":"S-x%d-%s"}`, attaches, p.TargetID), nil
		case "Runtime.evaluate":
			return `{"result":{"type":"string","value":"evaluated"}}`, nil
		}
		return "{}", nil
	}
}

func TestCDPDeliversOrdinaryIframeAfterDelayedPageConfirm(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return pendingAncestorIframeScript(seven, allowed, "ordinary-confirm")
	})
	var result struct {
		Rows map[string]struct {
			Info struct {
				cdpOutcome
				URL string `json:"url"`
			} `json:"info"`
			Attach cdpOutcome `json:"attach"`
		} `json:"rows"`
		List   []string `json:"list"`
		Events []struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			Child     string `json:"child"`
			TargetID  string `json:"targetId"`
			URL       string `json:"url"`
			Waiting   *bool  `json:"waiting"`
			Reason    string `json:"reason"`
		} `json:"events"`
	}
	env.evalJSONWithin(t, "linkTest.pendingAncestorIframes("+jsArgs(map[string]any{"browser": "chrome-2", "frames": []string{"F-plain"}, "settle": "ordinary-confirm"})+")", &result, 60*time.Second)
	raw, _ := json.Marshal(result)
	plain := result.Rows["F-plain"]
	if !plain.Info.OK || plain.Info.URL != env.allowed+"/inner" {
		t.Fatalf("Target.getTargetInfo after ordinary settle: %+v\n%s", plain.Info, raw)
	}
	if !plain.Attach.OK {
		t.Fatalf("Target.attachToTarget after ordinary settle: %+v\n%s", plain.Attach, raw)
	}
	pageAt, tabAt, pageN, tabN := -1, -1, 0, 0
	for i, e := range result.Events {
		if e.Method == "Target.attachedToTarget" && e.Child == "S-F-plain" {
			pageN++
			pageAt = i
			if e.SessionID != "S-page" || e.URL != env.allowed+"/inner" || e.Waiting == nil || *e.Waiting {
				t.Fatalf("page-parent auto-attach after ordinary settle wrong: %+v", e)
			}
		}
		if e.Method == "Target.attachedToTarget" && e.Child == "S-F-plain-tab" {
			tabN++
			tabAt = i
			if e.SessionID != "S-tab" || e.URL != env.allowed+"/inner" || e.Waiting == nil || *e.Waiting {
				t.Fatalf("tab-parent auto-attach after ordinary settle wrong: %+v", e)
			}
		}
		if e.Method == "Target.detachedFromTarget" && (e.Child == "S-F-plain" || e.Child == "S-F-plain-tab") {
			t.Fatalf("ordinary iframe was detached: %+v\n%s", e, raw)
		}
	}
	if pageN != 1 || tabN != 1 {
		t.Fatalf("auto-attach delivery counts page=%d tab=%d want 1 each: %s", pageN, tabN, raw)
	}
	if pageAt < 0 || tabAt < 0 {
		t.Fatalf("missing dual-parent attaches page=%d tab=%d %s", pageAt, tabAt, raw)
	}
	listed := false
	for _, id := range result.List {
		if id == "F-plain" {
			listed = true
		}
	}
	if !listed {
		t.Fatalf("ordinary iframe missing from listing after settle: %s", raw)
	}
}

func assertHeldIframeHiddenAfterProtectedSettle(t *testing.T, env *protectEnv, settle string) {
	t.Helper()
	var result struct {
		Rows map[string]struct {
			Info struct {
				cdpOutcome
				URL string `json:"url"`
			} `json:"info"`
			Attach cdpOutcome `json:"attach"`
		} `json:"rows"`
		List   []string `json:"list"`
		Events []struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			Child     string `json:"child"`
			TargetID  string `json:"targetId"`
			URL       string `json:"url"`
			Waiting   *bool  `json:"waiting"`
			Reason    string `json:"reason"`
		} `json:"events"`
	}
	env.evalJSONWithin(t, "linkTest.pendingAncestorIframes("+jsArgs(map[string]any{"browser": "chrome-2", "frames": []string{"F-plain"}, "settle": settle})+")", &result, 60*time.Second)
	raw, _ := json.Marshal(result)
	plain := result.Rows["F-plain"]
	if plain.Info.OK || plain.Info.Error.Code != -32602 || plain.Info.Error.Message != "No target with given id found" {
		t.Fatalf("Target.getTargetInfo after %s settle: %+v\n%s", settle, plain.Info, raw)
	}
	if plain.Attach.OK || plain.Attach.Error.Message != refusedText {
		t.Fatalf("Target.attachToTarget after %s settle: %+v\n%s", settle, plain.Attach, raw)
	}
	for _, id := range result.List {
		if id == "F-plain" {
			t.Fatalf("protected iframe listed after %s settle: %s", settle, raw)
		}
	}
	delivered, detached := 0, 0
	for _, e := range result.Events {
		if e.Method == "Target.attachedToTarget" && (e.Child == "S-F-plain" || e.Child == "S-F-plain-tab") {
			delivered++
		}
		if e.Method == "Target.detachedFromTarget" && (e.Child == "S-F-plain" || e.Child == "S-F-plain-tab") {
			detached++
		}
		if e.TargetID == "F-plain" && e.Method != "Target.detachedFromTarget" && e.URL != "" {
			t.Fatalf("held iframe reached the client under %s settle: %+v", settle, e)
		}
	}
	if delivered != 0 {
		t.Fatalf("held iframe was delivered under %s settle: delivered=%d %s", settle, delivered, raw)
	}
	if detached == 0 {
		t.Fatalf("held iframe sessions were not detached under %s settle: %s", settle, raw)
	}
}

func TestCDPProtectsHeldIframeWhenOwnerURLChangesToSeven(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return pendingAncestorIframeScript(seven, allowed, "seven-url")
	})
	assertHeldIframeHiddenAfterProtectedSettle(t, env, "seven-url")
}

func TestCDPProtectsHeldIframeWhenOwnerURLChangesToVault(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return pendingAncestorIframeScript(seven, allowed, "vault-url")
	})
	assertHeldIframeHiddenAfterProtectedSettle(t, env, "vault-url")
}

func TestCDPProtectsHeldIframeWhenOwnerFrameNavigatesToSeven(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return pendingAncestorIframeScript(seven, allowed, "seven-nav")
	})
	assertHeldIframeHiddenAfterProtectedSettle(t, env, "seven-nav")
}

func TestCDPProtectsHeldIframeWhenOwnerFrameNavigatesToVault(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return pendingAncestorIframeScript(seven, allowed, "vault-nav")
	})
	assertHeldIframeHiddenAfterProtectedSettle(t, env, "vault-nav")
}

func TestCDPProtectsHeldIframeWhenPendingPageConfirmsOnSeven(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return pendingAncestorIframeScript(seven, allowed, "seven-confirm")
	})
	assertHeldIframeHiddenAfterProtectedSettle(t, env, "seven-confirm")
}

func TestCDPProtectsHeldIframeWhenPendingPageConfirmsOnVault(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return pendingAncestorIframeScript(seven, allowed, "vault-confirm")
	})
	assertHeldIframeHiddenAfterProtectedSettle(t, env, "vault-confirm")
}
