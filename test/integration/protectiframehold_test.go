package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func protectedIframeShapesScript(seven, vault, allowed string) fakeScript {
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
				frame("S-seven", "FRAME-seven", "P-seven", seven+"/frame"),
				frame("S-vault", "FRAME-vault", "P-vault", vault+"/frame"),
			}
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

func TestCDPHidesIframesInsideProtectedDocuments(t *testing.T) {
	env, fake := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return protectedIframeShapesScript(seven, "https://vault.sliccy.ai", allowed)
	})
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
