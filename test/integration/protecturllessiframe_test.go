package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func urlLessIframeScript(seven, vault, allowed string) fakeScript {
	page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/framed"}
	sevenPage := map[string]any{"targetId": "P-seven", "type": "page", "url": seven + "/doc"}
	vaultPage := map[string]any{"targetId": "P-vault", "type": "page", "url": vault + "/doc"}
	type shape struct {
		id, session, parent, url string
	}
	shapes := []shape{
		{"F-plain", "S-page", "P-ordinary", allowed + "/inner"},
		{"F-to-seven", "S-page", "P-ordinary", seven + "/embed"},
		{"F-to-vault", "S-page", "P-ordinary", vault + "/embed"},
		{"F-in-seven", "S-seven", "P-seven", allowed + "/inner-seven"},
		{"F-in-vault", "S-vault", "P-vault", allowed + "/inner-vault"},
	}
	info := func(s shape, url string) map[string]any {
		return map[string]any{"targetId": s.id, "type": "iframe", "url": url, "parentId": s.parent, "parentFrameId": s.parent, "attached": true}
	}
	bodies := map[string]map[string]any{"P-ordinary": page, "P-seven": sevenPage, "P-vault": vaultPage}
	for _, s := range shapes {
		bodies[s.id] = info(s, "")
	}
	event := func(session, method string, params map[string]any) string {
		m := map[string]any{"method": method, "params": params}
		if session != "" {
			m["sessionId"] = session
		}
		raw, _ := json.Marshal(m)
		return string(raw)
	}
	created := func(session string, info map[string]any) string {
		return event(session, "Target.targetCreated", map[string]any{"targetInfo": info})
	}
	attach := func(parent, session string, info map[string]any, waiting bool) string {
		return event(parent, "Target.attachedToTarget", map[string]any{"sessionId": session, "targetInfo": info, "waitingForDebugger": waiting})
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
				var events []string
				for _, pg := range []struct {
					session string
					info    map[string]any
				}{{"S-page", page}, {"S-seven", sevenPage}, {"S-vault", vaultPage}} {
					events = append(events, created("RELAY", pg.info), created("", pg.info), attach("", pg.session, pg.info, false))
				}
				return "{}", events
			case p.Name == "arm-urlless-iframes" && armed && !framed:
				framed = true
				var events []string
				for _, s := range shapes {
					events = append(events, created("RELAY", info(s, "")), created("", info(s, "")), attach(s.session, "S-"+s.id, info(s, ""), true))
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
					bodies[s.id] = info(s, s.url)
					events = append(events, event("RELAY", "Target.targetInfoChanged", map[string]any{"targetInfo": info(s, s.url)}), event("", "Target.targetInfoChanged", map[string]any{"targetInfo": info(s, s.url)}))
				}
				return "{}", events
			}
			return "{}", nil
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			list := []any{page, sevenPage, vaultPage}
			if framed {
				for _, s := range shapes {
					list = append(list, bodies[s.id])
				}
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": list})
			return string(raw), nil
		case "Target.getTargetInfo":
			if info, ok := bodies[p.TargetID]; ok {
				raw, _ := json.Marshal(map[string]any{"targetInfo": info})
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

func TestCDPAnswersLookupsOfAURLLessIframeOnceItsHoldSettles(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return urlLessIframeScript(seven, "https://vault.sliccy.ai", allowed)
	})
	frames := []string{"F-plain", "F-to-seven", "F-to-vault", "F-in-seven", "F-in-vault"}
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
		t.Errorf("Target.getTargetInfo on an ordinary iframe asked during its URL hold: %+v\n%s", plain.Info, raw)
	}
	if !plain.Attach.OK {
		t.Errorf("Target.attachToTarget on an ordinary iframe asked during its URL hold: %+v\n%s", plain.Attach, raw)
	}
	delivered, swapped, swapAt, attachAt := 0, false, -1, -1
	for i, e := range result.Events {
		if e.Method == "Page.frameDetached" && e.SessionID == "S-page" && e.Reason == "swap" {
			swapped, swapAt = true, i
		}
		if e.Method == "Target.attachedToTarget" && e.Child == "S-F-plain" {
			delivered++
			attachAt = i
			if e.SessionID != "S-page" || e.URL != env.allowed+"/inner" || e.Waiting == nil || *e.Waiting {
				t.Errorf("the ordinary iframe's auto-attach reached the client wrong: %+v", e)
			}
		}
	}
	if !swapped {
		t.Errorf("the page session's frame swap never reached the client: %s", raw)
	}
	if delivered != 1 || attachAt < swapAt {
		t.Errorf("the ordinary iframe's auto-attach reached the client %d times (at %d, swap at %d), want once after the swap: %s", delivered, attachAt, swapAt, raw)
	}
	listed := map[string]bool{}
	for _, id := range result.List {
		listed[id] = true
	}
	if !listed["F-plain"] {
		t.Errorf("the ordinary iframe is missing from the listing: %s", raw)
	}
	for _, id := range frames[1:] {
		row := result.Rows[id]
		if row.Info.OK || row.Info.Error.Code != -32602 || row.Info.Error.Message != "No target with given id found" {
			t.Errorf("Target.getTargetInfo on %s: %+v", id, row.Info)
		}
		if row.Attach.OK || row.Attach.Error.Message != refusedText {
			t.Errorf("Target.attachToTarget on %s: %+v", id, row.Attach)
		}
		if listed[id] {
			t.Errorf("%s was listed", id)
		}
		for _, e := range result.Events {
			announced := e.Method == "Target.targetCreated" && e.URL == "" && strings.HasPrefix(id, "F-to-")
			if !announced && (e.TargetID == id || e.Child == "S-"+id || e.SessionID == "S-"+id) {
				t.Errorf("%s reached the client: %+v", id, e)
			}
			if strings.Contains(e.URL, "/embed") || strings.Contains(e.URL, "/inner-") {
				t.Errorf("a protected iframe's URL reached the client: %+v", e)
			}
		}
	}
}
