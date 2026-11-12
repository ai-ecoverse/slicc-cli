package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func unresolvedWorkerScript(seven, allowed string) fakeScript {
	page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/doc"}
	early := map[string]any{"targetId": "W-early", "type": "worker", "url": "", "parentId": "FRAME-seven"}
	plain := map[string]any{"targetId": "W-plain", "type": "worker", "url": "", "parentFrameId": "FRAME-plain"}
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
	env, fake := setupFakeProtectEnv(t, []string{"SLICC_CDP_HOLD_TIMEOUT=500ms"}, func(seven, allowed string) fakeScript {
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
	resumed, detached := map[string]bool{}, map[string]bool{}
	for _, line := range fake.received() {
		for _, session := range []string{"S-early", "S-plain", "S-ordinary"} {
			if !strings.Contains(line, session) {
				continue
			}
			if strings.Contains(line, "Runtime.runIfWaitingForDebugger") {
				resumed[session] = true
			}
			if strings.Contains(line, "Target.detachFromTarget") {
				detached[session] = true
			}
		}
	}
	if !resumed["S-early"] || !detached["S-early"] || !resumed["S-plain"] || !detached["S-plain"] {
		t.Fatalf("an unresolved worker was not resumed and detached at the hold timeout: resumed=%v detached=%v", resumed, detached)
	}
	if resumed["S-ordinary"] || detached["S-ordinary"] {
		t.Fatalf("the ordinary worker was resumed or detached: resumed=%v detached=%v", resumed, detached)
	}
}

func unshownWorkerScript(seven, allowed string) fakeScript {
	page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/doc"}
	late := map[string]any{"targetId": "W-late", "type": "worker", "url": "", "parentFrameId": "FRAME-seven"}
	plain := map[string]any{"targetId": "W-plain", "type": "worker", "url": "", "parentId": "FRAME-plain"}
	created := func(info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}})
		return string(raw)
	}
	attach := func(session string, info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{
			"method": "Target.attachedToTarget",
			"params": map[string]any{"sessionId": session, "targetInfo": info, "waitingForDebugger": false},
		})
		return string(raw)
	}
	on := func(method string, params any) string {
		raw, _ := json.Marshal(map[string]any{"method": method, "params": params})
		return string(raw)
	}
	frame := func(id, raw string) string {
		rawEvent, _ := json.Marshal(map[string]any{
			"sessionId": "S-page",
			"method":    "Page.frameNavigated",
			"params":    map[string]any{"frame": map[string]any{"id": id, "parentId": "P-ordinary", "url": raw, "securityOrigin": raw}},
		})
		return string(rawEvent)
	}
	tick := func(name string) string {
		return on("Target.targetInfoChanged", map[string]any{"targetInfo": map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/doc", "title": name}})
	}
	destroyed := func(id string) string {
		return on("Target.targetDestroyed", map[string]any{"targetId": id})
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
			case "arm-unshown-worker":
				if armed {
					return "{}", nil
				}
				armed = true
				return "{}", []string{created(late), created(plain), created(page), attach("S-page", page)}
			case "reveal-unshown-frame":
				return "{}", []string{
					frame("FRAME-seven", seven+"/doc"),
					frame("FRAME-plain", allowed+"/frame"),
					on("Target.targetInfoChanged", map[string]any{"targetInfo": map[string]any{"targetId": "W-late", "type": "worker", "url": allowed + "/worker.js"}}),
					tick("revealed"),
				}
			case "destroy-unshown":
				return "{}", []string{destroyed("W-late"), destroyed("W-plain"), tick("destroyed")}
			case "destroy-shown":
				return "{}", []string{destroyed("W-late"), destroyed("W-plain"), tick("after-list")}
			}
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": []any{page, late, plain}})
			return string(raw), nil
		case "Target.attachToTarget":
			return `{"sessionId":"S-explicit"}`, nil
		}
		return "{}", nil
	}
}

func TestCDPReDecidesAWorkerWhenItsCreatorFrameArrives(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return unshownWorkerScript(seven, allowed)
	})
	var result struct {
		Before struct {
			Events []struct {
				TargetID string `json:"targetId"`
			} `json:"events"`
			List []struct {
				TargetID string `json:"targetId"`
			} `json:"list"`
			LateAttach  cdpOutcome `json:"lateAttach"`
			PlainAttach cdpOutcome `json:"plainAttach"`
		} `json:"before"`
		Reveal []struct {
			TargetID string `json:"targetId"`
			Method   string `json:"method"`
		} `json:"reveal"`
		Mid struct {
			LateAttach  cdpOutcome `json:"lateAttach"`
			PlainAttach cdpOutcome `json:"plainAttach"`
		} `json:"mid"`
		Destroyed []struct {
			TargetID string `json:"targetId"`
			Method   string `json:"method"`
		} `json:"destroyed"`
		After struct {
			List []struct {
				TargetID string `json:"targetId"`
			} `json:"list"`
			LateAttach  cdpOutcome `json:"lateAttach"`
			PlainAttach cdpOutcome `json:"plainAttach"`
		} `json:"after"`
		ShownDestroyed []struct {
			TargetID string `json:"targetId"`
			Method   string `json:"method"`
		} `json:"shownDestroyed"`
	}
	env.evalJSONWithin(t, "linkTest.protect.unshownWorkerArrival('chrome-2')", &result, 20*time.Second)
	for _, event := range result.Before.Events {
		if event.TargetID == "W-late" || event.TargetID == "W-plain" {
			t.Errorf("a worker reached the client before its frame: %+v", event)
		}
	}
	for _, item := range result.Before.List {
		if item.TargetID == "W-late" || item.TargetID == "W-plain" {
			t.Errorf("getTargets listed %s before its frame", item.TargetID)
		}
	}
	for _, event := range result.Reveal {
		if event.TargetID == "W-late" {
			t.Errorf("the protected worker was shown once its frame was known: %+v", event)
		}
	}
	for _, got := range []cdpOutcome{result.Before.LateAttach, result.Before.PlainAttach, result.Mid.LateAttach, result.Mid.PlainAttach, result.After.LateAttach} {
		if got.OK || got.Error.Message != refusedText {
			t.Errorf("attach of a worker that was never shown or is protected: %+v", got)
		}
	}
	seenDestroy := map[string]int{}
	for _, event := range result.Destroyed {
		if event.Method == "Target.targetDestroyed" {
			seenDestroy[event.TargetID]++
		}
	}
	if seenDestroy["W-late"] != 0 || seenDestroy["W-plain"] != 0 {
		t.Fatalf("targetDestroyed reached the client for a worker it was never shown: %+v", result.Destroyed)
	}
	listed := map[string]bool{}
	for _, item := range result.After.List {
		listed[item.TargetID] = true
	}
	if listed["W-late"] || !listed["W-plain"] {
		t.Fatalf("listing after the frames were known: %+v", result.After.List)
	}
	if !result.After.PlainAttach.OK {
		t.Fatalf("attach after the ordinary worker was listed: %+v", result.After.PlainAttach)
	}
	shownDestroy := map[string]int{}
	for _, event := range result.ShownDestroyed {
		if event.Method == "Target.targetDestroyed" {
			shownDestroy[event.TargetID]++
		}
	}
	if shownDestroy["W-late"] != 0 || shownDestroy["W-plain"] == 0 {
		t.Fatalf("targetDestroyed after listing: %+v", result.ShownDestroyed)
	}
}

func releaseHeldScript(allowed string) fakeScript {
	page := func(id string) map[string]any {
		return map[string]any{"targetId": id, "type": "page", "url": allowed + "/doc"}
	}
	worker := func(id, frame string) map[string]any {
		return map[string]any{"targetId": id, "type": "worker", "url": "", "parentId": frame}
	}
	created := func(info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}})
		return string(raw)
	}
	attach := func(parent, session string, info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{
			"method":    "Target.attachedToTarget",
			"sessionId": parent,
			"params":    map[string]any{"sessionId": session, "targetInfo": info, "waitingForDebugger": true},
		})
		return string(raw)
	}
	armed := false
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Browser.getHistogram":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(params, &p)
			if p.Name != "arm-held-release" || armed {
				return "{}", nil
			}
			armed = true
			return "{}", []string{
				created(page("P-a")),
				attach("", "S-a", page("P-a")),
				attach("S-a", "S-wa", worker("W-a", "FRAME-a")),
				created(page("P-b")),
				attach("", "S-b", page("P-b")),
				attach("S-b", "S-wb", worker("W-b", "FRAME-b")),
			}
		case "Target.attachToTarget":
			var attached struct {
				TargetID string `json:"targetId"`
			}
			_ = json.Unmarshal(params, &attached)
			if attached.TargetID == "" {
				return "{}", nil
			}
			return `{"sessionId":"G-` + attached.TargetID + `"}`, nil
		case "Page.getNavigationHistory":
			return `{"currentIndex":0,"entries":[{"id":1,"url":"` + allowed + `/doc"}]}`, nil
		case "Target.getTargetInfo":
			var info struct {
				TargetID string `json:"targetId"`
			}
			_ = json.Unmarshal(params, &info)
			return `{"targetInfo":{"targetId":"` + info.TargetID + `","type":"page","url":"` + allowed + `/doc"}}`, nil
		case "Target.getTargets":
			return `{"targetInfos":[]}`, nil
		case "Target.detachFromTarget", "Target.setAutoAttach", "Runtime.runIfWaitingForDebugger":
			return "{}", nil
		}
		return "{}", nil
	}
}

func TestCDPReleasesHeldWorkersWhenThePageSessionStopsAutoAttach(t *testing.T) {
	env, fake := setupFakeProtectPage(t, func(_, allowed string) fakeScript {
		return releaseHeldScript(allowed)
	})
	var result struct {
		Events []struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			TargetID  string `json:"targetId"`
		} `json:"events"`
		MS int `json:"ms"`
	}
	env.evalJSONWithin(t, "linkTest.protect.releaseHeldWorkers('chrome-2')", &result, 20*time.Second)
	if result.MS > 2000 {
		t.Fatalf("releasing held workers took %d ms; the hold timeout must not be what unpauses them", result.MS)
	}
	for _, event := range result.Events {
		if event.TargetID == "W-a" || event.TargetID == "W-b" || event.SessionID == "S-wa" || event.SessionID == "S-wb" {
			t.Errorf("a held worker reached the client: %+v", event)
		}
	}
	got := fake.received()
	index := func(session, method string) int {
		needle := `"` + session + `"`
		for i, line := range got {
			if strings.Contains(line, needle) && strings.Contains(line, method) {
				return i
			}
		}
		return -1
	}
	detachPage := -1
	for i, line := range got {
		if strings.Contains(line, "Target.detachFromTarget") && strings.Contains(line, `"sessionId":"S-a"`) && !strings.Contains(line, `"S-wa"`) {
			detachPage = i
			break
		}
	}
	resumeA := index("S-wa", "Runtime.runIfWaitingForDebugger")
	detachA := index("S-wa", "Target.detachFromTarget")
	autoOff := -1
	for i, line := range got {
		if strings.Contains(line, "Target.setAutoAttach") && strings.Contains(line, `"S-b"`) {
			autoOff = i
			break
		}
	}
	resumeB := index("S-wb", "Runtime.runIfWaitingForDebugger")
	detachB := index("S-wb", "Target.detachFromTarget")
	if resumeA < 0 || detachA < 0 || detachPage < 0 || resumeA > detachPage || detachA > detachPage {
		t.Fatalf("detaching the page session did not resume and detach its held worker first: resume=%d detach=%d page=%d", resumeA, detachA, detachPage)
	}
	if resumeB < 0 || detachB < 0 || autoOff < 0 || resumeB > autoOff || detachB > autoOff || resumeB < detachPage {
		t.Fatalf("turning auto-attach off did not resume and detach the other held worker: resume=%d detach=%d auto=%d pageDetach=%d", resumeB, detachB, autoOff, detachPage)
	}
}
