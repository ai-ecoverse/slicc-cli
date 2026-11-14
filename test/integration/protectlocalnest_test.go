package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func localNestEvent(session, method string, params map[string]any) string {
	m := map[string]any{"method": method, "params": params}
	if session != "" {
		m["sessionId"] = session
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

func localNestProtectedPageScript(midURL, allowed string) fakeScript {
	page := map[string]any{"targetId": "P-top", "type": "page", "url": allowed + "/nest"}
	srcdoc := map[string]any{
		"targetId": "F-srcdoc", "type": "iframe", "url": "about:srcdoc",
		"parentId": "P-top", "parentFrameId": "FRAME-MID", "attached": true,
	}
	bodies := map[string]map[string]any{"P-top": page, "F-srcdoc": srcdoc}
	armed := false
	attaches := 0
	return func(method string, params json.RawMessage) (string, []string) {
		var p struct {
			Name     string `json:"name"`
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(params, &p)
		switch method {
		case "Browser.getHistogram":
			if p.Name != "arm-local-nest" || armed {
				return "{}", nil
			}
			armed = true
			return "{}", []string{
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": page}),
				localNestEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-top", "waitingForDebugger": false, "targetInfo": page}),
				localNestEvent("S-top", "Page.frameAttached", map[string]any{"frameId": "FRAME-MID", "parentFrameId": "P-top"}),
				localNestEvent("S-top", "Page.frameNavigated", map[string]any{"frame": map[string]any{
					"id": "FRAME-MID", "parentId": "P-top", "url": midURL, "securityOrigin": midURL,
				}}),
				localNestEvent("S-top", "Runtime.executionContextCreated", map[string]any{"context": map[string]any{
					"id": 2, "origin": midURL, "name": "", "uniqueId": "mid-main",
					"auxData": map[string]any{"isDefault": true, "type": "default", "frameId": "FRAME-MID"},
				}}),
				localNestEvent("S-top", "Page.frameAttached", map[string]any{"frameId": "FRAME-GC", "parentFrameId": "FRAME-MID"}),
				localNestEvent("S-top", "Page.frameNavigated", map[string]any{"frame": map[string]any{
					"id": "FRAME-GC", "parentId": "FRAME-MID", "url": allowed + "/inner", "securityOrigin": allowed,
				}}),
				localNestEvent("S-top", "Runtime.executionContextCreated", map[string]any{"context": map[string]any{
					"id": 3, "origin": allowed, "name": "", "uniqueId": "gc-main",
					"auxData": map[string]any{"isDefault": true, "type": "default", "frameId": "FRAME-GC"},
				}}),
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": srcdoc}),
				localNestEvent("S-top", "Target.attachedToTarget", map[string]any{
					"sessionId": "S-srcdoc", "waitingForDebugger": true, "targetInfo": srcdoc,
				}),
			}
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": []any{page, srcdoc}})
			return string(raw), nil
		case "Target.getTargetInfo":
			if body, ok := bodies[p.TargetID]; ok {
				raw, _ := json.Marshal(map[string]any{"targetInfo": body})
				return string(raw), nil
			}
		case "Target.attachToTarget":
			attaches++
			if p.TargetID == "P-top" {
				return `{"sessionId":"S-top-client"}`, nil
			}
			if p.TargetID == "F-srcdoc" {
				return `{"sessionId":"S-srcdoc-explicit"}`, nil
			}
			return fmt.Sprintf(`{"sessionId":"S-x%d"}`, attaches), nil
		case "Page.getFrameTree":
			raw, _ := json.Marshal(map[string]any{"frameTree": map[string]any{
				"frame": map[string]any{"id": "P-top", "url": allowed + "/nest", "securityOrigin": allowed},
				"childFrames": []any{
					map[string]any{
						"frame": map[string]any{"id": "FRAME-MID", "parentId": "P-top", "url": midURL, "securityOrigin": midURL},
						"childFrames": []any{
							map[string]any{"frame": map[string]any{"id": "FRAME-GC", "parentId": "FRAME-MID", "url": allowed + "/inner", "securityOrigin": allowed}},
							map[string]any{"frame": map[string]any{"id": "F-srcdoc", "parentId": "FRAME-MID", "url": "about:srcdoc", "securityOrigin": midURL}},
						},
					},
				},
			}})
			return string(raw), nil
		case "Runtime.evaluate":
			return `{"result":{"type":"string","value":"leaked"}}`, nil
		case "Page.enable", "Runtime.enable", "Runtime.runIfWaitingForDebugger", "Target.detachFromTarget", "Target.setAutoAttach", "Target.setDiscoverTargets":
			return "{}", nil
		}
		return "{}", nil
	}
}

func localNestOrdinaryNestedScript(allowed string) fakeScript {
	page := map[string]any{"targetId": "P-top", "type": "page", "url": allowed + "/nest"}
	mid := map[string]any{
		"targetId": "F-mid", "type": "iframe", "url": "https://cross.test/mid",
		"parentId": "P-top", "parentFrameId": "P-top", "attached": true,
	}
	gc := map[string]any{
		"targetId": "F-gc", "type": "iframe", "url": allowed + "/inner",
		"parentId": "P-top", "parentFrameId": "F-mid", "attached": true,
	}
	other := map[string]any{"targetId": "P-other", "type": "page", "url": allowed + "/other"}
	disputed := map[string]any{
		"targetId": "F-disputed", "type": "iframe", "url": allowed + "/disputed",
		"parentId": "P-top", "parentFrameId": "P-top", "attached": true,
	}
	protectedMid := map[string]any{
		"targetId": "F-seven-mid", "type": "iframe", "url": "https://seven.sliccy.ai/mid",
		"parentId": "P-top", "parentFrameId": "P-top", "attached": true,
	}
	protectedChild := map[string]any{
		"targetId": "F-under-seven", "type": "iframe", "url": allowed + "/under",
		"parentId": "P-top", "parentFrameId": "F-seven-mid", "attached": true,
	}
	bodies := map[string]map[string]any{
		"P-top": page, "F-mid": mid, "F-gc": gc, "P-other": other,
		"F-disputed": disputed, "F-seven-mid": protectedMid, "F-under-seven": protectedChild,
	}
	armed := false
	attaches := 0
	return func(method string, params json.RawMessage) (string, []string) {
		var p struct {
			Name     string `json:"name"`
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(params, &p)
		switch method {
		case "Browser.getHistogram":
			if p.Name != "arm-ordinary-nested" || armed {
				return "{}", nil
			}
			armed = true
			return "{}", []string{
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": page}),
				localNestEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-top", "waitingForDebugger": false, "targetInfo": page}),
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": other}),
				localNestEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-other", "waitingForDebugger": false, "targetInfo": other}),
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": mid}),
				localNestEvent("S-top", "Target.attachedToTarget", map[string]any{"sessionId": "S-mid", "waitingForDebugger": false, "targetInfo": mid}),
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": gc}),
				localNestEvent("S-mid", "Target.attachedToTarget", map[string]any{"sessionId": "S-gc", "waitingForDebugger": false, "targetInfo": gc}),
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": disputed}),
				localNestEvent("S-other", "Target.attachedToTarget", map[string]any{"sessionId": "S-disputed", "waitingForDebugger": true, "targetInfo": disputed}),
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": protectedMid}),
				localNestEvent("S-top", "Target.attachedToTarget", map[string]any{"sessionId": "S-seven-mid", "waitingForDebugger": true, "targetInfo": protectedMid}),
				localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": protectedChild}),
				localNestEvent("S-seven-mid", "Target.attachedToTarget", map[string]any{"sessionId": "S-under-seven", "waitingForDebugger": true, "targetInfo": protectedChild}),
			}
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": []any{page, other, mid, gc, disputed, protectedMid, protectedChild}})
			return string(raw), nil
		case "Target.getTargetInfo":
			if body, ok := bodies[p.TargetID]; ok {
				raw, _ := json.Marshal(map[string]any{"targetInfo": body})
				return string(raw), nil
			}
		case "Target.attachToTarget":
			attaches++
			switch p.TargetID {
			case "P-top":
				return `{"sessionId":"S-top-client"}`, nil
			case "F-mid":
				return `{"sessionId":"S-mid-explicit"}`, nil
			case "F-gc":
				return `{"sessionId":"S-gc-explicit"}`, nil
			case "F-disputed":
				return `{"sessionId":"S-disputed-explicit"}`, nil
			case "F-under-seven":
				return `{"sessionId":"S-under-explicit"}`, nil
			case "F-seven-mid":
				return `{"sessionId":"S-seven-explicit"}`, nil
			}
			return fmt.Sprintf(`{"sessionId":"S-x%d"}`, attaches), nil
		case "Runtime.evaluate", "Page.enable", "Runtime.enable":
			return "{}", nil
		case "Runtime.runIfWaitingForDebugger", "Target.detachFromTarget", "Target.setAutoAttach", "Target.setDiscoverTargets", "Page.getFrameTree":
			return "{}", nil
		}
		return "{}", nil
	}
}

func localNestRefusedAnswerScript(seven, allowed string) fakeScript {
	page := map[string]any{"targetId": "P-top", "type": "page", "url": allowed + "/nest"}
	mid := map[string]any{
		"targetId": "F-mid", "type": "iframe", "url": seven + "/mid",
		"parentId": "P-top", "parentFrameId": "P-top", "attached": true,
	}
	bodies := map[string]map[string]any{"P-top": page, "F-mid": mid}
	armed := false
	evaluatePending := false
	return func(method string, params json.RawMessage) (string, []string) {
		var p struct {
			Name     string `json:"name"`
			TargetID string `json:"targetId"`
		}
		_ = json.Unmarshal(params, &p)
		switch method {
		case "Browser.getHistogram":
			switch p.Name {
			case "arm-refused-answer":
				if armed {
					return "{}", nil
				}
				armed = true
				return "{}", []string{
					localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": page}),
					localNestEvent("", "Target.attachedToTarget", map[string]any{"sessionId": "S-top", "waitingForDebugger": false, "targetInfo": page}),
					localNestEvent("", "Target.targetCreated", map[string]any{"targetInfo": mid}),
					localNestEvent("S-top", "Target.attachedToTarget", map[string]any{"sessionId": "S-mid", "waitingForDebugger": false, "targetInfo": mid}),
				}
			case "commit-mid-protected":
				changed := map[string]any{
					"targetId": "F-mid", "type": "iframe", "url": seven + "/mid",
					"parentId": "P-top", "parentFrameId": "P-top", "attached": true,
				}
				bodies["F-mid"] = changed
				return "{}", []string{
					localNestEvent("", "Target.targetInfoChanged", map[string]any{"targetInfo": changed}),
					localNestEvent("S-top", "Target.targetInfoChanged", map[string]any{"targetInfo": changed}),
				}
			}
			return "{}", nil
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": []any{page, bodies["F-mid"]}})
			return string(raw), nil
		case "Target.getTargetInfo":
			if body, ok := bodies[p.TargetID]; ok {
				raw, _ := json.Marshal(map[string]any{"targetInfo": body})
				return string(raw), nil
			}
		case "Target.attachToTarget":
			if p.TargetID == "F-mid" {
				return `{"sessionId":"S-mid-client"}`, nil
			}
			if p.TargetID == "P-top" {
				return `{"sessionId":"S-top-client"}`, nil
			}
			return `{"sessionId":"S-x"}`, nil
		case "Runtime.evaluate":
			if !evaluatePending {
				evaluatePending = true
				return "#pending:mid-eval|{\"result\":{\"type\":\"string\",\"value\":\"late\"}}", nil
			}
			return `{"result":{"type":"string","value":"late"}}`, nil
		case "Page.enable", "Runtime.enable", "Runtime.runIfWaitingForDebugger", "Target.detachFromTarget", "Target.setAutoAttach", "Target.setDiscoverTargets":
			return "{}", nil
		}
		return "{}", nil
	}
}

func TestCDPHidesGrandchildUnderProtectedLocalMid(t *testing.T) {
	for _, mid := range []struct {
		name string
		url  func(seven string) string
	}{
		{"seven", func(seven string) string { return seven + "/embed" }},
		{"vault", func(string) string { return "https://vault.sliccy.ai/embed" }},
	} {
		t.Run(mid.name, func(t *testing.T) {
			env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
				return localNestProtectedPageScript(mid.url(seven), allowed)
			})
			var result struct {
				List         []string   `json:"list"`
				SrcdocInfo   cdpOutcome `json:"srcdocInfo"`
				SrcdocAttach cdpOutcome `json:"srcdocAttach"`
				ContextSeen  bool       `json:"contextSeen"`
				ContextEval  cdpOutcome `json:"contextEval"`
				FrameTreeHit bool       `json:"frameTreeHit"`
				Events       []string   `json:"events"`
				ChildEnable  cdpOutcome `json:"childEnable"`
				TargetEvents bool       `json:"targetEvents"`
				ChildAttach  bool       `json:"childAttach"`
			}
			env.evalJSONWithin(t, "linkTest.protect.localNestProtected("+jsArgs("chrome-2")+")", &result, 30*time.Second)
			raw, _ := json.Marshal(result)
			for _, id := range result.List {
				if id == "F-srcdoc" {
					t.Fatalf("srcdoc grandchild listed: %s", raw)
				}
			}
			if result.SrcdocInfo.OK {
				t.Fatalf("getTargetInfo answered for srcdoc grandchild: %s", raw)
			}
			if result.SrcdocAttach.OK {
				t.Fatalf("attach to srcdoc grandchild succeeded: %s", raw)
			}
			if result.ChildAttach || result.TargetEvents {
				t.Fatalf("srcdoc grandchild target events reached the client: %s", raw)
			}
			if result.ContextSeen {
				t.Fatalf("contextCreated for grandchild under protected local mid reached the client: %s", raw)
			}
			if result.ContextEval.OK || !strings.HasPrefix(result.ContextEval.Error.Message, "slicc: refused: ") || result.ContextEval.Error.Code != -32000 {
				t.Fatalf("evaluate under protected local mid: %+v %s", result.ContextEval, raw)
			}
			if result.FrameTreeHit {
				t.Fatalf("frame tree exposed a protected local mid descendant: %s", raw)
			}
		})
	}
}

func TestCDPDeliversOrdinaryNestedOOPIFFromMidSession(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return localNestOrdinaryNestedScript(allowed)
	})
	var result struct {
		List             []string   `json:"list"`
		MidInfo          cdpOutcome `json:"midInfo"`
		MidAttach        cdpOutcome `json:"midAttach"`
		GcInfo           cdpOutcome `json:"gcInfo"`
		GcAttach         cdpOutcome `json:"gcAttach"`
		GcEnable         cdpOutcome `json:"gcEnable"`
		ChildAttach      bool       `json:"childAttach"`
		DisputedInfo     cdpOutcome `json:"disputedInfo"`
		DisputedAttach   cdpOutcome `json:"disputedAttach"`
		UnderSevenInfo   cdpOutcome `json:"underSevenInfo"`
		UnderSevenAttach cdpOutcome `json:"underSevenAttach"`
		Events           []string   `json:"events"`
	}
	env.evalJSONWithin(t, "linkTest.protect.localNestOrdinary("+jsArgs("chrome-2")+")", &result, 30*time.Second)
	raw, _ := json.Marshal(result)
	listed := map[string]bool{}
	for _, id := range result.List {
		listed[id] = true
	}
	if !listed["F-mid"] || !listed["F-gc"] {
		t.Fatalf("ordinary nested OOPIFs missing from listing: %s", raw)
	}
	if !result.MidInfo.OK || !result.MidAttach.OK || !result.GcInfo.OK || !result.GcAttach.OK || !result.GcEnable.OK {
		t.Fatalf("ordinary nested OOPIF was not delivered and answered: %s", raw)
	}
	if !result.ChildAttach {
		t.Fatalf("grandchild auto-attach from mid session missing: %s", raw)
	}
	if result.DisputedAttach.OK || result.DisputedInfo.OK && listed["F-disputed"] && result.DisputedAttach.Error.Message == "" {
		if result.DisputedAttach.OK {
			t.Fatalf("different-page owner still delivered disputed iframe: %s", raw)
		}
	}
	if result.DisputedAttach.OK {
		t.Fatalf("different-page owner did not dispute: %s", raw)
	}
	if result.UnderSevenAttach.OK || listed["F-under-seven"] {
		t.Fatalf("protected mid still leaked a descendant: %s", raw)
	}
}

func TestCDPAnswersCommandsOnRefusedChildSession(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return localNestRefusedAnswerScript(seven, allowed)
	})
	var result struct {
		Enable cdpOutcome `json:"enable"`
		Eval   cdpOutcome `json:"eval"`
		MS     int        `json:"ms"`
	}
	env.evalJSONWithin(t, "linkTest.protect.localNestRefusedAnswer("+jsArgs("chrome-2")+")", &result, 30*time.Second)
	raw, _ := json.Marshal(result)
	for name, o := range map[string]cdpOutcome{"Page.enable": result.Enable, "Runtime.evaluate": result.Eval} {
		if o.OK || o.Error.Code != -32000 || !strings.HasPrefix(o.Error.Message, "slicc: refused: ") {
			t.Fatalf("%s on refused child session: %+v %s", name, o, raw)
		}
	}
	if result.MS > 10000 {
		t.Fatalf("refused child session answer parked past the client timeout: %s", raw)
	}
}
