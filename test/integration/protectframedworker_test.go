package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCDPHidesFramedProtectedWorkerSession(t *testing.T) {
	env, _ := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		blob := "blob:" + allowed + "/0b9e0f6c-1f0e-4c1a-9a43-5d3f6e2c7a10"
		scriptURL := seven + "/worker.js?secret=framed"
		page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/doc"}
		iframe := map[string]any{"targetId": "F-vault", "type": "iframe", "url": seven + "/doc?secret=framed", "openerId": "P-ordinary"}
		framed := map[string]any{"targetId": "W-framed", "type": "worker", "url": blob, "openerId": "P-ordinary", "openerFrameId": "F-vault"}
		framedChild := map[string]any{"targetId": "W-framed", "type": "worker", "url": blob, "openerId": "P-ordinary"}
		shared := map[string]any{"targetId": "W-shared", "type": "shared_worker", "url": "data:text/javascript,postMessage('secret-framed')", "openerFrameId": "F-vault"}
		service := map[string]any{"targetId": "W-service", "type": "service_worker", "url": "", "openerId": "P-ordinary"}
		ordinary := map[string]any{"targetId": "W-ordinary", "type": "worker", "url": allowed + "/worker.js", "openerId": "P-ordinary"}
		attach := func(parent, session string, info map[string]any) string {
			msg := map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": session, "targetInfo": info, "waitingForDebugger": false}}
			if parent != "" {
				msg["sessionId"] = parent
			}
			raw, _ := json.Marshal(msg)
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
		sessionEvents := func(session string) []string {
			return []string{
				on(session, "Runtime.executionContextCreated", map[string]any{"context": map[string]any{"id": 1, "origin": seven, "name": "", "auxData": map[string]any{"isDefault": true}}}),
				on(session, "Runtime.consoleAPICalled", map[string]any{"type": "log", "args": []any{map[string]any{"type": "string", "value": "secret-framed"}}, "executionContextId": 1}),
				on(session, "Log.entryAdded", map[string]any{"entry": map[string]any{"source": "worker", "level": "info", "text": "secret-framed", "url": blob}}),
				on(session, "Network.requestWillBeSent", map[string]any{"requestId": "req-" + session, "request": map[string]any{"url": scriptURL}, "timestamp": 1}),
				on(session, "Network.responseReceived", map[string]any{"requestId": "req-" + session, "response": map[string]any{"url": scriptURL, "status": 200}}),
			}
		}
		bodies := map[string]map[string]any{
			"P-ordinary": page, "F-vault": iframe, "W-framed": framed, "W-shared": shared, "W-service": service, "W-ordinary": ordinary,
		}
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Target.setDiscoverTargets":
				events := []string{
					created(page),
					attach("", "S-iframe", iframe),
					attach("S-iframe", "S-framed", framedChild),
				}
				events = append(events, sessionEvents("S-framed")...)
				events = append(events, created(framed), attach("", "S-framed-browser", framed))
				events = append(events, sessionEvents("S-framed-browser")...)
				events = append(events, created(shared), attach("", "S-shared", shared))
				events = append(events, sessionEvents("S-shared")...)
				events = append(events, attach("S-iframe", "S-service", service))
				events = append(events, sessionEvents("S-service")...)
				events = append(events,
					attach("", "S-ordinary", ordinary),
					on("S-ordinary", "Runtime.consoleAPICalled", map[string]any{"type": "log", "args": []any{map[string]any{"type": "string", "value": "ordinary-worker-ok"}}, "executionContextId": 1}),
					`{"method":"Console.messageAdded","params":{"message":{"text":"framed-worker-done"}}}`,
				)
				return "{}", events
			case "Target.getTargets":
				list := []any{page, iframe, framed, shared, service, ordinary}
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
			}
			return "{}", nil
		}
	})
	var result struct {
		List []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"list"`
		FramedAttach  cdpOutcome `json:"framedAttach"`
		SharedAttach  cdpOutcome `json:"sharedAttach"`
		ServiceAttach cdpOutcome `json:"serviceAttach"`
		FramedInfo    cdpOutcome `json:"framedInfo"`
		OrdinaryInfo  struct {
			OK     bool `json:"ok"`
			Result struct {
				TargetInfo struct {
					TargetID string `json:"targetId"`
					Type     string `json:"type"`
					URL      string `json:"url"`
				} `json:"targetInfo"`
			} `json:"result"`
		} `json:"ordinaryInfo"`
		Events []struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			TargetID  string `json:"targetId"`
			Type      string `json:"type"`
			URL       string `json:"url"`
			Text      string `json:"text"`
		} `json:"events"`
	}
	env.evalJSON(t, "linkTest.protect.framedWorker('chrome-2')", &result)
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "secret-framed") || strings.Contains(string(raw), "S-framed") || strings.Contains(string(raw), "S-shared") || strings.Contains(string(raw), "S-service") {
		t.Fatalf("a protected worker session reached the client: %s", raw)
	}
	protected := map[string]bool{"W-framed": true, "W-shared": true, "W-service": true}
	protectedSessions := map[string]bool{"S-iframe": true, "S-framed": true, "S-framed-browser": true, "S-shared": true, "S-service": true}
	var ordinaryListed bool
	for _, item := range result.List {
		if protected[item.TargetID] {
			t.Errorf("getTargets lists %s as %+v", item.TargetID, item)
		}
		if item.TargetID == "W-ordinary" {
			ordinaryListed = true
			if item.Type != "worker" || item.URL != env.allowed+"/worker.js" {
				t.Errorf("ordinary worker listed as %+v", item)
			}
		}
	}
	if !ordinaryListed {
		t.Fatalf("ordinary worker missing from getTargets: %+v", result.List)
	}
	for _, got := range []cdpOutcome{result.FramedAttach, result.SharedAttach, result.ServiceAttach} {
		if got.OK || got.Error.Message != refusedText {
			t.Errorf("attach to a protected worker: %+v", got)
		}
	}
	if result.FramedInfo.OK || result.FramedInfo.Error.Code != -32602 || result.FramedInfo.Error.Message != "No target with given id found" {
		t.Fatalf("getTargetInfo on the framed worker: %+v", result.FramedInfo)
	}
	info := result.OrdinaryInfo.Result.TargetInfo
	if !result.OrdinaryInfo.OK || info.TargetID != "W-ordinary" || info.Type != "worker" || info.URL != env.allowed+"/worker.js" {
		t.Fatalf("getTargetInfo on the ordinary worker: %+v", result.OrdinaryInfo)
	}
	var ordinaryConsole bool
	for _, event := range result.Events {
		if protected[event.TargetID] || protectedSessions[event.SessionID] || event.Type == "worker" && event.TargetID != "W-ordinary" {
			t.Errorf("protected worker event: %+v", event)
		}
		switch event.Method {
		case "Runtime.executionContextCreated", "Runtime.consoleAPICalled", "Log.entryAdded":
			if event.SessionID != "S-ordinary" && event.SessionID != "" && event.Text != "framed-worker-done" && event.Text != "ordinary-worker-ok" {
				t.Errorf("worker runtime event: %+v", event)
			}
		}
		if strings.HasPrefix(event.Method, "Network.") {
			t.Errorf("worker network event: %+v", event)
		}
		if event.SessionID == "S-ordinary" && event.Method == "Runtime.consoleAPICalled" && event.Text == "ordinary-worker-ok" {
			ordinaryConsole = true
		}
	}
	if !ordinaryConsole {
		t.Fatalf("ordinary worker console did not reach the client: %+v", result.Events)
	}
}
