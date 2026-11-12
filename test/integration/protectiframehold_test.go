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
		{"targetId": "F-seven-ordinary", "type": "iframe", "url": allowed + "/embed", "parentFrameId": "P-seven"},
		{"targetId": "F-seven-srcdoc", "type": "iframe", "url": "about:srcdoc", "parentFrameId": "P-seven"},
		{"targetId": "F-seven-mid", "type": "iframe", "url": allowed + "/mid", "parentFrameId": "P-seven"},
		{"targetId": "F-seven-nest", "type": "iframe", "url": allowed + "/nest", "parentFrameId": "F-seven-mid"},
		{"targetId": "F-vault-ordinary", "type": "iframe", "url": allowed + "/embed-v", "parentFrameId": "P-vault"},
		{"targetId": "F-vault-srcdoc", "type": "iframe", "url": "about:srcdoc", "parentFrameId": "P-vault"},
		{"targetId": "F-vault-mid", "type": "iframe", "url": "about:srcdoc", "parentFrameId": "P-vault"},
		{"targetId": "F-vault-nest", "type": "iframe", "url": allowed + "/nest-v", "parentFrameId": "F-vault-mid"},
		{"targetId": "F-plain", "type": "iframe", "url": allowed + "/plain", "parentFrameId": "P-ordinary"},
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
	attach := func(session string, info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{
			"method": "Target.attachedToTarget",
			"params": map[string]any{"sessionId": session, "targetInfo": info, "waitingForDebugger": true},
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
			if p.Name != "arm-protected-iframe" || armed {
				return "{}", nil
			}
			armed = true
			events := []string{
				created(page), attach("S-page", page),
				created(sevenPage),
				created(vaultPage),
			}
			for _, info := range shapes {
				events = append(events, created(info), attach("S-"+info["targetId"].(string), info))
				events = append(events,
					`{"sessionId":"S-`+info["targetId"].(string)+`","method":"Runtime.executionContextCreated","params":{"context":{"id":1,"origin":"https://ordinary.test","name":"","auxData":{"isDefault":true,"frameId":"`+info["targetId"].(string)+`"}}}}`,
					`{"sessionId":"S-`+info["targetId"].(string)+`","method":"Runtime.consoleAPICalled","params":{"type":"log","args":[{"type":"string","value":"iframe-secret"}],"executionContextId":1}}`,
				)
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
				if strings.Contains(got, "S-"+id) || strings.Contains(got, id) {
					t.Fatalf("Runtime.evaluate for protected iframe was forwarded: %s", got)
				}
			}
		}
	}
}
