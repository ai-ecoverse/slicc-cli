package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func protectedIframeShapesScript(seven, vault, allowed string, blindSevenFrame bool) fakeScript {
	page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/doc"}
	sevenPage := map[string]any{"targetId": "P-seven", "type": "page", "url": seven + "/doc"}
	vaultPage := map[string]any{"targetId": "P-vault", "type": "page", "url": vault + "/doc"}
	shapes := []map[string]any{
		{"targetId": "F-seven-ordinary", "type": "iframe", "url": allowed + "/embed", "parentFrameId": "FRAME-seven"},
		{"targetId": "F-seven-srcdoc", "type": "iframe", "url": "about:srcdoc", "parentFrameId": "FRAME-seven"},
		{"targetId": "F-seven-mid", "type": "iframe", "url": allowed + "/mid", "parentFrameId": "FRAME-seven"},
		{"targetId": "F-seven-nest", "type": "iframe", "url": allowed + "/nest", "parentFrameId": "F-seven-mid"},
		{"targetId": "F-vault-ordinary", "type": "iframe", "url": allowed + "/embed-v", "parentFrameId": "FRAME-vault"},
		{"targetId": "F-vault-srcdoc", "type": "iframe", "url": "about:srcdoc", "parentFrameId": "FRAME-vault"},
		{"targetId": "F-vault-mid", "type": "iframe", "url": "about:srcdoc", "parentFrameId": "FRAME-vault"},
		{"targetId": "F-vault-nest", "type": "iframe", "url": allowed + "/nest-v", "parentFrameId": "F-vault-mid"},
		{"targetId": "F-plain", "type": "iframe", "url": allowed + "/plain", "parentFrameId": "FRAME-plain"},
	}
	bodies := map[string]map[string]any{
		"P-ordinary": page, "P-seven": sevenPage, "P-vault": vaultPage,
	}
	for _, info := range shapes {
		bodies[info["targetId"].(string)] = info
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
	frame := func(session, id, parent, raw string) string {
		event, _ := json.Marshal(map[string]any{
			"sessionId": session,
			"method":    "Page.frameNavigated",
			"params":    map[string]any{"frame": map[string]any{"id": id, "parentId": parent, "url": raw, "securityOrigin": raw}},
		})
		return string(event)
	}
	armed := false
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Browser.getHistogram":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(params, &p)
			if p.Name != "arm-protected-iframe" || armed {
				return "{}", nil
			}
			armed = true
			events := []string{
				created(page), attach("", "S-page", page),
				created(sevenPage), attach("", "S-seven", sevenPage),
				created(vaultPage), attach("", "S-vault", vaultPage),
				frame("S-page", "FRAME-plain", "P-ordinary", allowed+"/frame"),
			}
			if !blindSevenFrame {
				events = append(events, frame("S-seven", "FRAME-seven", "P-seven", seven+"/frame"))
			}
			events = append(events, frame("S-vault", "FRAME-vault", "P-vault", vault+"/frame"))
			parentOf := map[string]string{
				"F-seven-ordinary": "S-seven", "F-seven-srcdoc": "S-seven", "F-seven-mid": "S-seven", "F-seven-nest": "S-F-seven-mid",
				"F-vault-ordinary": "S-vault", "F-vault-srcdoc": "S-vault", "F-vault-mid": "S-vault", "F-vault-nest": "S-F-vault-mid",
				"F-plain": "S-page",
			}
			for _, info := range shapes {
				id := info["targetId"].(string)
				events = append(events, created(info), attach(parentOf[id], "S-"+id, info))
				if id != "F-plain" {
					events = append(events,
						`{"sessionId":"S-`+id+`","method":"Runtime.executionContextCreated","params":{"context":{"id":1,"origin":"https://ordinary.test","name":"","auxData":{"isDefault":true,"frameId":"`+id+`"}}}}`,
						`{"sessionId":"S-`+id+`","method":"Runtime.consoleAPICalled","params":{"type":"log","args":[{"type":"string","value":"iframe-secret"}],"executionContextId":1}}`,
					)
				}
			}
			return "{}", events
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			list := []any{page, sevenPage, vaultPage}
			for _, info := range shapes {
				list = append(list, info)
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": list})
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
		case "Runtime.evaluate":
			return `{"result":{"type":"string","value":"evaluated"}}`, nil
		case "Target.detachFromTarget", "Runtime.runIfWaitingForDebugger":
			return "{}", nil
		}
		return "{}", nil
	}
}

func assertProtectedIframeShapes(t *testing.T, env *protectEnv, fake *fakeCDPBrowser) {
	t.Helper()
	var result struct {
		Events []struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			TargetID  string `json:"targetId"`
			Type      string `json:"type"`
			URL       string `json:"url"`
			Text      string `json:"text"`
		} `json:"events"`
		List []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"list"`
		Infos  map[string]cdpOutcome `json:"infos"`
		Attach map[string]cdpOutcome `json:"attach"`
		Evals  map[string]cdpOutcome `json:"evals"`
		Plain  struct {
			Info   cdpOutcome `json:"info"`
			Attach cdpOutcome `json:"attach"`
		} `json:"plain"`
	}
	env.evalJSONWithin(t, "linkTest.protect.protectedIframeShapes('chrome-2')", &result, 20*time.Second)
	hidden := []string{
		"F-seven-ordinary", "F-seven-srcdoc", "F-seven-mid", "F-seven-nest",
		"F-vault-ordinary", "F-vault-srcdoc", "F-vault-mid", "F-vault-nest",
	}
	listed := map[string]bool{}
	for _, item := range result.List {
		listed[item.TargetID] = true
		for _, id := range hidden {
			if item.TargetID == id {
				t.Errorf("getTargets listed protected iframe %s as %+v", id, item)
			}
		}
	}
	if !listed["F-plain"] {
		t.Fatalf("ordinary iframe missing from listing: %+v", result.List)
	}
	for _, id := range hidden {
		info := result.Infos[id]
		if info.OK || info.Error.Code != -32602 || info.Error.Message != "No target with given id found" {
			t.Errorf("getTargetInfo on %s: %+v", id, info)
		}
		attach := result.Attach[id]
		if attach.OK || attach.Error.Message != refusedText {
			t.Errorf("attach to %s: %+v", id, attach)
		}
		eval := result.Evals[id]
		if eval.OK || eval.Error.Message != refusedText {
			t.Errorf("Runtime.evaluate on %s: %+v", id, eval)
		}
	}
	if !result.Plain.Info.OK {
		t.Fatalf("getTargetInfo on ordinary iframe: %+v", result.Plain.Info)
	}
	if !result.Plain.Attach.OK {
		t.Fatalf("attach to ordinary iframe: %+v", result.Plain.Attach)
	}
	for _, event := range result.Events {
		for _, id := range hidden {
			if event.TargetID == id || event.SessionID == "S-"+id {
				t.Errorf("protected iframe reached the client: %+v", event)
			}
		}
		if event.Text == "iframe-secret" {
			t.Errorf("console from a protected iframe reached the client: %+v", event)
		}
	}
	for _, got := range fake.received() {
		if strings.Contains(got, "Runtime.evaluate") {
			for _, id := range hidden {
				if strings.Contains(got, `"sessionId":"S-`+id+`"`) {
					t.Fatalf("Runtime.evaluate for protected iframe was forwarded: %s", got)
				}
			}
		}
	}
}

func TestCDPHidesIframesInsideProtectedDocuments(t *testing.T) {
	env, fake := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return protectedIframeShapesScript(seven, "https://vault.sliccy.ai", allowed, false)
	})
	assertProtectedIframeShapes(t, env, fake)
}

func TestCDPHidesIframesInsideProtectedDocumentsBlindSevenFrame(t *testing.T) {
	env, fake := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return protectedIframeShapesScript(seven, "https://vault.sliccy.ai", allowed, true)
	})
	assertProtectedIframeShapes(t, env, fake)
}

func lateIframeParentScript(seven, vault, allowed string) fakeScript {
	page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/doc"}
	sevenPage := map[string]any{"targetId": "P-seven", "type": "page", "url": seven + "/doc"}
	vaultPage := map[string]any{"targetId": "P-vault", "type": "page", "url": vault + "/doc"}
	shapes := []map[string]any{
		{"targetId": "F-seven-x", "type": "iframe", "url": allowed + "/seven-x", "parentFrameId": "FRAME-seven-late"},
		{"targetId": "F-seven-child", "type": "iframe", "url": allowed + "/seven-child", "parentFrameId": "F-seven-x"},
		{"targetId": "F-vault-x", "type": "iframe", "url": allowed + "/vault-x", "parentFrameId": "FRAME-vault-late"},
		{"targetId": "F-vault-child", "type": "iframe", "url": allowed + "/vault-child", "parentFrameId": "F-vault-x"},
		{"targetId": "F-plain-x", "type": "iframe", "url": allowed + "/plain-x", "parentFrameId": "FRAME-plain-late"},
		{"targetId": "F-plain-child", "type": "iframe", "url": allowed + "/plain-child", "parentFrameId": "F-plain-x"},
	}
	bodies := map[string]map[string]any{
		"P-ordinary": page, "P-seven": sevenPage, "P-vault": vaultPage,
	}
	for _, info := range shapes {
		bodies[info["targetId"].(string)] = info
	}
	created := func(info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}})
		return string(raw)
	}
	attach := func(parent, session string, info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{
			"method":    "Target.attachedToTarget",
			"sessionId": parent,
			"params":    map[string]any{"sessionId": session, "targetInfo": info, "waitingForDebugger": false},
		})
		return string(raw)
	}
	frameAttached := func(session, id, parent string) string {
		event, _ := json.Marshal(map[string]any{
			"sessionId": session,
			"method":    "Page.frameAttached",
			"params":    map[string]any{"frameId": id, "parentFrameId": parent},
		})
		return string(event)
	}
	armed := false
	revealed := false
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Browser.getHistogram":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(params, &p)
			switch p.Name {
			case "arm-late-iframe-parent":
				if armed {
					return "{}", nil
				}
				armed = true
				events := []string{
					created(page), attach("", "S-page", page),
					created(sevenPage),
					created(vaultPage),
				}
				for _, info := range shapes {
					events = append(events, created(info))
				}
				return "{}", events
			case "reveal-late-iframe-parent":
				if !armed || revealed {
					return "{}", nil
				}
				revealed = true
				return "{}", []string{
					frameAttached("S-page", "FRAME-seven-late", "P-seven"),
					frameAttached("S-page", "FRAME-vault-late", "P-vault"),
					frameAttached("S-page", "FRAME-plain-late", "P-ordinary"),
				}
			}
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			list := []any{page, sevenPage, vaultPage}
			for _, info := range shapes {
				list = append(list, info)
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": list})
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

func TestCDPRedecidesIframesWhenParentAncestryArrivesLate(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return lateIframeParentScript(seven, "https://vault.sliccy.ai", allowed)
	})
	var result struct {
		Before struct {
			List   []string              `json:"list"`
			Infos  map[string]cdpOutcome `json:"infos"`
			Attach map[string]cdpOutcome `json:"attach"`
		} `json:"before"`
		After struct {
			List   []string              `json:"list"`
			Infos  map[string]cdpOutcome `json:"infos"`
			Attach map[string]cdpOutcome `json:"attach"`
		} `json:"after"`
	}
	env.evalJSONWithin(t, "linkTest.protect.lateIframeParent('chrome-2')", &result, 20*time.Second)
	listed := func(ids []string) map[string]bool {
		out := map[string]bool{}
		for _, id := range ids {
			out[id] = true
		}
		return out
	}
	before := listed(result.Before.List)
	after := listed(result.After.List)
	hidden := []string{"F-seven-x", "F-seven-child", "F-vault-x", "F-vault-child"}
	ancestryText := "slicc: refused: unresolved ancestry"
	for _, id := range hidden {
		if before[id] {
			t.Errorf("before late parent, %s was listed", id)
		}
		if after[id] {
			t.Errorf("after late protected parent, %s was listed", id)
		}
		info := result.After.Infos[id]
		if info.OK || info.Error.Code != -32602 || info.Error.Message != "No target with given id found" {
			t.Errorf("after late protected parent, getTargetInfo(%s): %+v", id, info)
		}
		attach := result.After.Attach[id]
		if attach.OK || attach.Error.Message != refusedText {
			t.Errorf("after late protected parent, attach(%s): %+v want %q", id, attach, refusedText)
		}
		if attach.Error.Message == ancestryText {
			t.Errorf("after late protected parent, attach(%s) stayed on unresolved ancestry", id)
		}
	}
	for _, id := range []string{"F-plain-x", "F-plain-child"} {
		if before[id] {
			t.Errorf("before late parent, ordinary %s was listed", id)
		}
		if !after[id] {
			t.Errorf("after ordinary late parent, %s stayed hidden", id)
		}
		if !result.After.Infos[id].OK {
			t.Errorf("after ordinary late parent, getTargetInfo(%s): %+v", id, result.After.Infos[id])
		}
		if !result.After.Attach[id].OK {
			t.Errorf("after ordinary late parent, attach(%s): %+v", id, result.After.Attach[id])
		}
	}
}

func lateIframeWorkerCascadeScript(seven, vault, allowed string) fakeScript {
	page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/doc"}
	sevenPage := map[string]any{"targetId": "P-seven", "type": "page", "url": seven + "/doc"}
	vaultPage := map[string]any{"targetId": "P-vault", "type": "page", "url": vault + "/doc"}
	frames := []map[string]any{
		{"targetId": "F-seven", "type": "iframe", "url": allowed + "/seven-x", "parentFrameId": "FRAME-seven-late"},
		{"targetId": "F-vault", "type": "iframe", "url": allowed + "/vault-x", "parentFrameId": "FRAME-vault-late"},
		{"targetId": "F-plain", "type": "iframe", "url": allowed + "/plain-x", "parentFrameId": "FRAME-plain-late"},
	}
	workers := []map[string]any{
		{"targetId": "W-seven", "type": "worker", "url": allowed + "/seven-worker.js", "openerFrameId": "F-seven"},
		{"targetId": "W-seven-2", "type": "worker", "url": allowed + "/seven-worker-2.js", "openerId": "W-seven"},
		{"targetId": "W-vault", "type": "worker", "url": allowed + "/vault-worker.js", "openerFrameId": "F-vault"},
		{"targetId": "W-vault-2", "type": "worker", "url": allowed + "/vault-worker-2.js", "openerId": "W-vault"},
		{"targetId": "W-plain", "type": "worker", "url": allowed + "/plain-worker.js", "openerFrameId": "F-plain"},
		{"targetId": "W-plain-2", "type": "worker", "url": allowed + "/plain-worker-2.js", "openerId": "W-plain"},
	}
	bodies := map[string]map[string]any{
		"P-ordinary": page, "P-seven": sevenPage, "P-vault": vaultPage,
	}
	for _, info := range frames {
		bodies[info["targetId"].(string)] = info
	}
	for _, info := range workers {
		bodies[info["targetId"].(string)] = info
	}
	created := func(info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}})
		return string(raw)
	}
	attach := func(parent, session string, info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{
			"method":    "Target.attachedToTarget",
			"sessionId": parent,
			"params":    map[string]any{"sessionId": session, "targetInfo": info, "waitingForDebugger": false},
		})
		return string(raw)
	}
	frameAttached := func(session, id, parent string) string {
		event, _ := json.Marshal(map[string]any{
			"sessionId": session,
			"method":    "Page.frameAttached",
			"params":    map[string]any{"frameId": id, "parentFrameId": parent},
		})
		return string(event)
	}
	armed := false
	revealed := false
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Browser.getHistogram":
			var p struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(params, &p)
			switch p.Name {
			case "arm-late-iframe-worker":
				if armed {
					return "{}", nil
				}
				armed = true
				events := []string{
					created(page), attach("", "S-page", page),
					created(sevenPage),
					created(vaultPage),
				}
				for _, info := range frames {
					events = append(events, created(info))
				}
				for _, info := range workers {
					id := info["targetId"].(string)
					events = append(events, created(info), attach("", "S-"+id, info))
				}
				return "{}", events
			case "reveal-late-iframe-worker":
				if !armed || revealed {
					return "{}", nil
				}
				revealed = true
				return "{}", []string{
					frameAttached("S-page", "FRAME-seven-late", "P-seven"),
					frameAttached("S-page", "FRAME-vault-late", "P-vault"),
					frameAttached("S-page", "FRAME-plain-late", "P-ordinary"),
				}
			}
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			list := []any{page, sevenPage, vaultPage}
			for _, info := range frames {
				list = append(list, info)
			}
			for _, info := range workers {
				list = append(list, info)
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": list})
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

func TestCDPCascadesLateIframeProtectionIntoWorkers(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return lateIframeWorkerCascadeScript(seven, "https://vault.sliccy.ai", allowed)
	})
	var result struct {
		Before struct {
			List   []string              `json:"list"`
			Infos  map[string]cdpOutcome `json:"infos"`
			Attach map[string]cdpOutcome `json:"attach"`
		} `json:"before"`
		After struct {
			List   []string              `json:"list"`
			Infos  map[string]cdpOutcome `json:"infos"`
			Attach map[string]cdpOutcome `json:"attach"`
		} `json:"after"`
	}
	env.evalJSONWithin(t, "linkTest.protect.lateIframeWorkerCascade('chrome-2')", &result, 20*time.Second)
	listed := func(ids []string) map[string]bool {
		out := map[string]bool{}
		for _, id := range ids {
			out[id] = true
		}
		return out
	}
	before := listed(result.Before.List)
	after := listed(result.After.List)
	for _, id := range []string{"W-seven", "W-seven-2", "W-vault", "W-vault-2"} {
		if !before[id] {
			t.Errorf("before late parent, worker %s was not listed", id)
		}
		if !result.Before.Attach[id].OK {
			t.Errorf("before late parent, attach(%s): %+v", id, result.Before.Attach[id])
		}
		if after[id] {
			t.Errorf("after late protected parent, worker %s was listed", id)
		}
		attach := result.After.Attach[id]
		if attach.OK || attach.Error.Message != refusedText {
			t.Errorf("after late protected parent, attach(%s): %+v want %q", id, attach, refusedText)
		}
		info := result.After.Infos[id]
		if info.OK || info.Error.Code != -32602 || info.Error.Message != "No target with given id found" {
			t.Errorf("after late protected parent, getTargetInfo(%s): %+v", id, info)
		}
	}
	for _, id := range []string{"W-plain", "W-plain-2"} {
		if !before[id] || !after[id] {
			t.Errorf("ordinary late-parent worker %s listing before=%v after=%v", id, before[id], after[id])
		}
		if !result.After.Attach[id].OK {
			t.Errorf("ordinary late-parent worker attach(%s): %+v", id, result.After.Attach[id])
		}
		if !result.After.Infos[id].OK {
			t.Errorf("ordinary late-parent worker getTargetInfo(%s): %+v", id, result.After.Infos[id])
		}
	}
}
