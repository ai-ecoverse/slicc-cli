package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func unresolvedWorkerScript(seven, allowed string) fakeScript {
	page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/doc"}
	early := map[string]any{"targetId": "W-early", "type": "worker", "url": "", "openerId": "P-ordinary", "openerFrameId": "FRAME-seven"}
	plain := map[string]any{"targetId": "W-plain", "type": "worker", "url": "", "openerId": "P-ordinary", "openerFrameId": "FRAME-plain"}
	ordinary := map[string]any{"targetId": "W-ordinary", "type": "worker", "url": allowed + "/worker.js", "openerId": "P-ordinary"}
	bodies := map[string]map[string]any{"P-ordinary": page, "W-early": early, "W-plain": plain, "W-ordinary": ordinary}
	attach := func(session string, info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{
			"method": "Target.attachedToTarget",
			"params": map[string]any{"sessionId": session, "targetInfo": info, "waitingForDebugger": true},
		})
		return string(raw)
	}
	created := func(info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}})
		return string(raw)
	}
	on := func(session, method string, params any) string {
		raw, _ := json.Marshal(map[string]any{"sessionId": session, "method": method, "params": params})
		return string(raw)
	}
	frame := func(id, raw string) string {
		return on("S-page", "Page.frameNavigated", map[string]any{"frame": map[string]any{"id": id, "parentId": "P-ordinary", "url": raw, "securityOrigin": raw}})
	}
	armed := false
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Browser.getHistogram":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(params, &p)
			switch p.Name {
			case "arm-unresolved-worker":
				if armed {
					return "{}", nil
				}
				armed = true
				return "{}", []string{
					created(page),
					attach("S-page", page),
					created(early),
					attach("S-early", early),
					on("S-early", "Runtime.executionContextCreated", map[string]any{"context": map[string]any{"id": 1, "origin": seven, "name": "", "auxData": map[string]any{"isDefault": true, "frameId": "FRAME-seven"}}}),
					on("S-early", "Runtime.consoleAPICalled", map[string]any{"type": "log", "args": []any{map[string]any{"type": "string", "value": "secret-early"}}, "executionContextId": 1}),
					created(plain),
					attach("S-plain", plain),
					created(ordinary),
					attach("S-ordinary", ordinary),
				}
			case "reveal-frame":
				return "{}", []string{frame("FRAME-seven", seven+"/doc"), frame("FRAME-plain", allowed+"/frame")}
			}
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": []any{page, early, plain, ordinary}})
			return string(raw), nil
		case "Target.getTargetInfo":
			var p struct {
				TargetID string `json:"targetId"`
			}
			_ = json.Unmarshal(params, &p)
			if info, ok := bodies[p.TargetID]; ok {
				raw, _ := json.Marshal(map[string]any{"targetInfo": info})
				return string(raw), nil
			}
		case "Target.attachToTarget":
			return `{"sessionId":"S-explicit"}`, nil
		case "Target.detachFromTarget", "Runtime.runIfWaitingForDebugger":
			return "{}", nil
		}
		return "{}", nil
	}
}

func TestCDPHoldsAWorkerUntilItsCreatorFrameIsKnown(t *testing.T) {
	env, fake := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return unresolvedWorkerScript(seven, allowed)
	})
	var result struct {
		Held struct {
			Events []struct {
				Method    string `json:"method"`
				SessionID string `json:"sessionId"`
				TargetID  string `json:"targetId"`
				Text      string `json:"text"`
			} `json:"events"`
			List []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				URL      string `json:"url"`
			} `json:"list"`
			EarlyAttach    cdpOutcome `json:"earlyAttach"`
			PlainAttach    cdpOutcome `json:"plainAttach"`
			OrdinaryAttach cdpOutcome `json:"ordinaryAttach"`
			EarlyInfo      cdpOutcome `json:"earlyInfo"`
			OrdinaryInfo   struct {
				OK     bool `json:"ok"`
				Result struct {
					TargetInfo struct {
						TargetID string `json:"targetId"`
						URL      string `json:"url"`
					} `json:"targetInfo"`
				} `json:"result"`
			} `json:"ordinaryInfo"`
		} `json:"held"`
		PlainWaiting bool `json:"plainWaiting"`
		List         []struct {
			TargetID string `json:"targetId"`
		} `json:"list"`
		Events []struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			TargetID  string `json:"targetId"`
			Text      string `json:"text"`
		} `json:"events"`
	}
	env.evalJSONWithin(t, "linkTest.protect.unresolvedWorkerHold('chrome-2')", &result, 20*time.Second)
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "secret-early") || strings.Contains(string(raw), "S-early") {
		t.Fatalf("the unresolved worker reached the client: %s", raw)
	}
	var ordinarySeen, plainSeen bool
	for _, event := range result.Held.Events {
		switch event.SessionID {
		case "S-ordinary":
			ordinarySeen = event.Method == "Target.attachedToTarget"
		case "S-plain", "S-early":
			t.Errorf("held window forwarded %s %s", event.SessionID, event.Method)
		}
		if event.TargetID == "W-early" || event.TargetID == "W-plain" {
			t.Errorf("held window listed an unresolved worker event %+v", event)
		}
	}
	if !ordinarySeen {
		t.Fatalf("ordinary worker was held with the unresolved ones: %+v", result.Held.Events)
	}
	listed := map[string]bool{}
	for _, item := range result.Held.List {
		listed[item.TargetID] = true
		if item.TargetID == "W-early" || item.TargetID == "W-plain" {
			t.Errorf("getTargets listed %s while its frame was unknown", item.TargetID)
		}
	}
	if !listed["W-ordinary"] || result.Held.List == nil {
		t.Fatalf("ordinary worker missing from the held-window listing: %+v", result.Held.List)
	}
	for _, got := range []cdpOutcome{result.Held.EarlyAttach, result.Held.PlainAttach} {
		if got.OK || got.Error.Message != refusedText {
			t.Errorf("attach while the creator frame is unknown: %+v", got)
		}
	}
	if result.Held.EarlyInfo.OK || result.Held.EarlyInfo.Error.Code != -32602 || result.Held.EarlyInfo.Error.Message != "No target with given id found" {
		t.Fatalf("getTargetInfo while the creator frame is unknown: %+v", result.Held.EarlyInfo)
	}
	if !result.Held.OrdinaryAttach.OK {
		t.Fatalf("ordinary worker attach: %+v", result.Held.OrdinaryAttach)
	}
	info := result.Held.OrdinaryInfo.Result.TargetInfo
	if !result.Held.OrdinaryInfo.OK || info.TargetID != "W-ordinary" || info.URL != env.allowed+"/worker.js" {
		t.Fatalf("ordinary worker info: %+v", result.Held.OrdinaryInfo)
	}
	if !result.PlainWaiting {
		t.Fatal("the worker whose frame was ordinary was resumed before the page saw it")
	}
	after := map[string]bool{}
	for _, item := range result.List {
		after[item.TargetID] = true
	}
	if after["W-early"] || !after["W-plain"] || !after["W-ordinary"] {
		t.Fatalf("listing after the frames were known: %+v", result.List)
	}
	for _, event := range result.Events {
		if event.SessionID == "S-early" || event.TargetID == "W-early" {
			t.Errorf("protected frame still forwarded the worker: %+v", event)
		}
		if event.SessionID == "S-plain" && event.Method == "Target.attachedToTarget" {
			plainSeen = true
		}
	}
	if !plainSeen {
		t.Fatal("the ordinary frame's worker was not delivered after its frame was known")
	}
	got := fake.received()
	reveal := -1
	for i, line := range got {
		if strings.Contains(line, "reveal-frame") {
			reveal = i
		}
	}
	if reveal < 0 {
		t.Fatal("the reveal command never reached Chrome")
	}
	resumed := map[string]bool{}
	detached := map[string]bool{}
	for i, line := range got {
		for _, session := range []string{"S-early", "S-plain"} {
			if !strings.Contains(line, session) {
				continue
			}
			if strings.Contains(line, "Runtime.runIfWaitingForDebugger") || strings.Contains(line, "Target.detachFromTarget") {
				if i < reveal {
					t.Fatalf("Chrome was resumed or detached before the frame was known: %s", line)
				}
			}
			if strings.Contains(line, "Runtime.runIfWaitingForDebugger") {
				resumed[session] = true
			}
			if strings.Contains(line, "Target.detachFromTarget") {
				detached[session] = true
			}
		}
	}
	if !resumed["S-early"] || !detached["S-early"] {
		t.Fatalf("the protected frame's worker was not resumed and detached after the reveal: resumed=%v detached=%v", resumed, detached)
	}
	if resumed["S-plain"] || detached["S-plain"] {
		t.Fatalf("the ordinary frame's worker was resumed or detached: resumed=%v detached=%v", resumed, detached)
	}
}

func TestCDPHoldTimeoutHidesAnUnresolvedWorker(t *testing.T) {
	env, _ := setupFakeProtectEnv(t, []string{"SLICC_CDP_HOLD_TIMEOUT=500ms"}, func(seven, allowed string) fakeScript {
		return unresolvedWorkerScript(seven, allowed)
	})
	var r struct {
		Early    int `json:"early"`
		Plain    int `json:"plain"`
		Ordinary int `json:"ordinary"`
		MS       int `json:"ms"`
	}
	env.evalJSONWithin(t, "linkTest.protect.unresolvedWorkerTimeout('chrome-2')", &r, 15*time.Second)
	if r.Early != 0 || r.Plain != 0 {
		t.Fatalf("client received unresolved workers early=%d plain=%d, want 0", r.Early, r.Plain)
	}
	if r.Ordinary != 1 {
		t.Fatalf("ordinary worker attachments = %d, want 1", r.Ordinary)
	}
	if r.MS < 400 || r.MS > 5000 {
		t.Fatalf("hold timeout took %d ms, want about the shortened 500ms hold", r.MS)
	}
	env.a.waitStderr(t, "cdp: an auto-attached target never got a URL", 2*time.Second)
}
