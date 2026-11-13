package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func dualParentIframeScript(seven, vault, allowed string) fakeScript {
	ctx := "C-ordinary"
	tab := map[string]any{"targetId": "T-ordinary", "type": "tab", "url": allowed + "/framed", "browserContextId": ctx}
	page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/framed", "browserContextId": ctx}
	sevenPage := map[string]any{"targetId": "P-seven", "type": "page", "url": seven + "/doc"}
	vaultPage := map[string]any{"targetId": "P-vault", "type": "page", "url": vault + "/doc"}
	type shape struct {
		id, url string
		parents []struct {
			session, child string
			waiting        bool
			parentFrame    string
		}
	}
	shapes := []shape{
		{
			id: "F-plain", url: allowed + "/inner",
			parents: []struct {
				session, child string
				waiting        bool
				parentFrame    string
			}{
				{"S-tab", "S-F-plain-tab", false, ""},
				{"S-page", "S-F-plain", true, "P-ordinary"},
			},
		},
		{
			id: "F-to-seven", url: seven + "/embed",
			parents: []struct {
				session, child string
				waiting        bool
				parentFrame    string
			}{
				{"S-tab", "S-F-to-seven-tab", false, ""},
				{"S-page", "S-F-to-seven", true, "P-ordinary"},
			},
		},
		{
			id: "F-in-seven", url: allowed + "/inner-seven",
			parents: []struct {
				session, child string
				waiting        bool
				parentFrame    string
			}{
				{"S-seven", "S-F-in-seven", true, "P-seven"},
			},
		},
		{
			id: "F-in-vault", url: allowed + "/inner-vault",
			parents: []struct {
				session, child string
				waiting        bool
				parentFrame    string
			}{
				{"S-vault", "S-F-in-vault", true, "P-vault"},
			},
		},
	}
	info := func(id, parent, url, frameCtx string) map[string]any {
		out := map[string]any{"targetId": id, "type": "iframe", "url": url, "attached": true}
		if frameCtx != "" {
			out["browserContextId"] = frameCtx
		}
		if parent != "" {
			out["parentId"] = parent
			out["parentFrameId"] = parent
		}
		return out
	}
	frameCtxOf := func(id string) string {
		switch id {
		case "F-in-seven":
			return ""
		case "F-in-vault":
			return ""
		default:
			return ctx
		}
	}
	bodies := map[string]map[string]any{
		"T-ordinary": tab, "P-ordinary": page, "P-seven": sevenPage, "P-vault": vaultPage,
	}
	parentOf := func(id string) string {
		switch id {
		case "F-in-seven":
			return "P-seven"
		case "F-in-vault":
			return "P-vault"
		default:
			return "P-ordinary"
		}
	}
	for _, s := range shapes {
		bodies[s.id] = info(s.id, parentOf(s.id), "", frameCtxOf(s.id))
	}
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
	armed, framed, committed := false, false, false
	attaches := 0
	return func(method string, params json.RawMessage) (string, []string) {
		var p struct {
			Name     string `json:"name"`
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(params, &p)
		switch method {
		case "Browser.getHistogram":
			switch {
			case p.Name == "arm-urlless-pages" && !armed:
				armed = true
				return "{}", []string{
					created("RELAY", tab), created("", tab), attach("", "S-tab", tab, false),
					created("RELAY", page), created("", page), attach("", "S-page", page, false),
					created("RELAY", sevenPage), created("", sevenPage), attach("", "S-seven", sevenPage, false),
					created("RELAY", vaultPage), created("", vaultPage), attach("", "S-vault", vaultPage, false),
				}
			case p.Name == "arm-urlless-iframes" && armed && !framed:
				framed = true
				var events []string
				for _, s := range shapes {
					body := info(s.id, parentOf(s.id), "", frameCtxOf(s.id))
					events = append(events, created("RELAY", body), created("", body))
					for _, a := range s.parents {
						events = append(events, attach(a.session, a.child, info(s.id, a.parentFrame, "", frameCtxOf(s.id)), a.waiting))
					}
				}
				events = append(events,
					event("S-page", "Page.frameAttached", map[string]any{"frameId": "F-plain", "parentFrameId": "P-ordinary"}),
					event("S-page", "Page.frameDetached", map[string]any{"frameId": "F-plain", "reason": "swap"}),
				)
				return "{}", events
			case p.Name == "commit-urlless-iframes" && framed && !committed:
				committed = true
				var events []string
				for _, s := range shapes {
					bodies[s.id] = info(s.id, parentOf(s.id), s.url, frameCtxOf(s.id))
					changed := info(s.id, parentOf(s.id), s.url, frameCtxOf(s.id))
					events = append(events,
						event("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": changed}),
						event("", "Target.targetInfoChanged", map[string]any{"targetInfo": changed}),
					)
				}
				return "{}", events
			}
			return "{}", nil
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			list := []any{tab, page, sevenPage, vaultPage}
			if framed {
				for _, s := range shapes {
					list = append(list, bodies[s.id])
				}
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

func TestCDPDeliversOrdinaryIframeUnderTabAndPageAttachParents(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return dualParentIframeScript(seven, "https://vault.sliccy.ai", allowed)
	})
	frames := []string{"F-plain", "F-to-seven", "F-in-seven", "F-in-vault"}
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
	env.evalJSONWithin(t, "linkTest.urlLessIframes("+jsArgs(map[string]any{"browser": "chrome-2", "frames": frames})+")", &result, 60*time.Second)
	raw, _ := json.Marshal(result)
	plain := result.Rows["F-plain"]
	if !plain.Info.OK || plain.Info.URL != env.allowed+"/inner" {
		t.Fatalf("Target.getTargetInfo on an ordinary dual-parent iframe: %+v\n%s", plain.Info, raw)
	}
	if !plain.Attach.OK {
		t.Fatalf("Target.attachToTarget on an ordinary dual-parent iframe: %+v\n%s", plain.Attach, raw)
	}
	pageDelivered, tabDelivered, swapped, swapAt, pageAt := 0, 0, false, -1, -1
	for i, e := range result.Events {
		if e.Method == "Page.frameDetached" && e.SessionID == "S-page" && e.Reason == "swap" {
			swapped, swapAt = true, i
		}
		if e.Method == "Target.attachedToTarget" && e.Child == "S-F-plain" {
			pageDelivered++
			pageAt = i
			if e.SessionID != "S-page" || e.URL != env.allowed+"/inner" || e.Waiting == nil || *e.Waiting {
				t.Fatalf("page-parent auto-attach reached the client wrong: %+v", e)
			}
		}
		if e.Method == "Target.attachedToTarget" && e.Child == "S-F-plain-tab" {
			tabDelivered++
			if e.SessionID != "S-tab" || e.URL != env.allowed+"/inner" || e.Waiting == nil || *e.Waiting {
				t.Fatalf("tab-parent auto-attach reached the client wrong: %+v", e)
			}
		}
		if e.Method == "Target.detachedFromTarget" && (e.Child == "S-F-plain" || e.Child == "S-F-plain-tab") {
			t.Fatalf("ordinary dual-parent iframe session was detached: %+v\n%s", e, raw)
		}
	}
	if !swapped {
		t.Fatalf("the page session's frame swap never reached the client: %s", raw)
	}
	if pageDelivered != 1 || pageAt < swapAt {
		t.Fatalf("page-parent auto-attach reached the client %d times (at %d, swap at %d), want once after the swap: %s", pageDelivered, pageAt, swapAt, raw)
	}
	if tabDelivered != 1 {
		t.Fatalf("tab-parent auto-attach reached the client %d times, want once: %s", tabDelivered, raw)
	}
	listed := map[string]bool{}
	for _, id := range result.List {
		listed[id] = true
	}
	if !listed["F-plain"] {
		t.Fatalf("the ordinary dual-parent iframe is missing from the listing: %s", raw)
	}
	for _, id := range frames[1:] {
		row := result.Rows[id]
		if row.Info.OK || row.Info.Error.Code != -32602 || row.Info.Error.Message != "No target with given id found" {
			t.Fatalf("Target.getTargetInfo on %s: %+v", id, row.Info)
		}
		if row.Attach.OK || row.Attach.Error.Message != refusedText {
			t.Fatalf("Target.attachToTarget on %s: %+v", id, row.Attach)
		}
		if listed[id] {
			t.Fatalf("%s was listed", id)
		}
		for _, e := range result.Events {
			announced := e.Method == "Target.targetCreated" && e.URL == "" && strings.HasPrefix(id, "F-to-")
			if !announced && (e.TargetID == id || strings.HasPrefix(e.Child, "S-"+id) || e.SessionID == "S-"+id) {
				t.Fatalf("%s reached the client: %+v", id, e)
			}
			if strings.Contains(e.URL, "/embed") || strings.Contains(e.URL, "/inner-") {
				t.Fatalf("a protected iframe's URL reached the client: %+v", e)
			}
		}
	}
}
