package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func nullOriginWorkerScript(seven, allowed string) fakeScript {
	page := map[string]any{"targetId": "P-ordinary", "type": "page", "url": allowed + "/doc"}
	sevenPage := map[string]any{"targetId": "P-seven", "type": "page", "url": seven + "/doc"}
	frame := map[string]any{"targetId": "F-null", "type": "iframe", "url": "about:srcdoc", "parentFrameId": "P-ordinary"}
	sevenFrame := map[string]any{"targetId": "F-null-seven", "type": "iframe", "url": "about:srcdoc", "parentFrameId": "P-seven"}
	ordinary := map[string]any{"targetId": "W-null-ordinary", "type": "worker", "url": allowed + "/worker.js?null-origin-worker=1", "parentFrameId": "F-null"}
	hidden := map[string]any{"targetId": "W-null-seven", "type": "worker", "url": allowed + "/worker.js?null-origin-worker=seven", "parentFrameId": "F-null-seven"}
	bodies := map[string]map[string]any{
		"P-ordinary": page, "P-seven": sevenPage, "F-null": frame, "F-null-seven": sevenFrame,
		"W-null-ordinary": ordinary, "W-null-seven": hidden,
	}
	created := func(info map[string]any) string {
		raw, _ := json.Marshal(map[string]any{"method": "Target.targetCreated", "params": map[string]any{"targetInfo": info}})
		return string(raw)
	}
	attach := func(parent, session string, info map[string]any) string {
		msg := map[string]any{"method": "Target.attachedToTarget", "params": map[string]any{"sessionId": session, "targetInfo": info, "waitingForDebugger": true}}
		if parent != "" {
			msg["sessionId"] = parent
		}
		raw, _ := json.Marshal(msg)
		return string(raw)
	}
	on := func(session, method string, params any) string {
		raw, _ := json.Marshal(map[string]any{"sessionId": session, "method": method, "params": params})
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
			if p.Name != "arm-null-origin-worker" || armed {
				return "{}", nil
			}
			armed = true
			return "{}", []string{
				created(page), attach("", "S-page", page),
				created(sevenPage), attach("", "S-seven", sevenPage),
				created(frame), attach("S-page", "S-null", frame),
				on("S-null", "Runtime.executionContextCreated", map[string]any{"context": map[string]any{"id": 1, "origin": "null", "name": "", "auxData": map[string]any{"isDefault": true, "frameId": "F-null"}}}),
				created(sevenFrame), attach("S-seven", "S-null-seven", sevenFrame),
				on("S-null-seven", "Runtime.executionContextCreated", map[string]any{"context": map[string]any{"id": 1, "origin": "null", "name": "", "auxData": map[string]any{"isDefault": true, "frameId": "F-null-seven"}}}),
				created(ordinary), attach("S-null", "S-null-ordinary", ordinary),
				created(hidden), attach("S-null-seven", "S-null-seven-worker", hidden),
				on("S-null-seven-worker", "Runtime.consoleAPICalled", map[string]any{"type": "log", "args": []any{map[string]any{"type": "string", "value": "secret-null-seven"}}, "executionContextId": 1}),
			}
		case "Target.getTargets":
			if !armed {
				return `{"targetInfos":[]}`, nil
			}
			raw, _ := json.Marshal(map[string]any{"targetInfos": []any{page, sevenPage, frame, sevenFrame, ordinary, hidden}})
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

func TestCDPDeliversAWorkerFromANullOriginFrame(t *testing.T) {
	env, fake := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		return nullOriginWorkerScript(seven, allowed)
	})
	var result struct {
		Events []struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			TargetID  string `json:"targetId"`
			Type      string `json:"type"`
			URL       string `json:"url"`
		} `json:"events"`
		List []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"list"`
		OrdinaryAttach cdpOutcome `json:"ordinaryAttach"`
		SevenAttach    cdpOutcome `json:"sevenAttach"`
		OrdinaryInfo   struct {
			OK     bool `json:"ok"`
			Result struct {
				TargetInfo struct {
					TargetID string `json:"targetId"`
					URL      string `json:"url"`
				} `json:"targetInfo"`
			} `json:"result"`
		} `json:"ordinaryInfo"`
		SevenInfo cdpOutcome `json:"sevenInfo"`
	}
	env.evalJSONWithin(t, "linkTest.protect.nullOriginWorker('chrome-2')", &result, 20*time.Second)
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "secret-null-seven") || strings.Contains(string(raw), "S-null-seven-worker") {
		t.Fatalf("null-origin worker under seven reached the client: %s", raw)
	}
	var ordinarySeen bool
	for _, event := range result.Events {
		if event.SessionID == "S-null-ordinary" && event.Method == "Target.attachedToTarget" {
			ordinarySeen = true
		}
		if event.TargetID == "W-null-seven" || event.SessionID == "S-null-seven-worker" {
			t.Errorf("seven null-origin worker event: %+v", event)
		}
	}
	if !ordinarySeen {
		t.Fatalf("ordinary null-origin worker was not delivered: %+v", result.Events)
	}
	listed := map[string]bool{}
	for _, item := range result.List {
		listed[item.TargetID] = true
		if item.TargetID == "W-null-seven" {
			t.Errorf("getTargets listed the seven null-origin worker: %+v", item)
		}
	}
	if !listed["W-null-ordinary"] {
		t.Fatalf("ordinary null-origin worker missing from listing: %+v", result.List)
	}
	if !result.OrdinaryAttach.OK {
		t.Fatalf("attach of ordinary null-origin worker: %+v", result.OrdinaryAttach)
	}
	if result.SevenAttach.OK || result.SevenAttach.Error.Message != refusedText {
		t.Fatalf("attach of seven null-origin worker: %+v", result.SevenAttach)
	}
	info := result.OrdinaryInfo.Result.TargetInfo
	if !result.OrdinaryInfo.OK || info.TargetID != "W-null-ordinary" || !strings.Contains(info.URL, "null-origin-worker=1") {
		t.Fatalf("getTargetInfo on ordinary null-origin worker: %+v", result.OrdinaryInfo)
	}
	if result.SevenInfo.OK || result.SevenInfo.Error.Code != -32602 {
		t.Fatalf("getTargetInfo on seven null-origin worker: %+v", result.SevenInfo)
	}
	detachedSeven := false
	for _, line := range fake.received() {
		if strings.Contains(line, "Target.detachFromTarget") && strings.Contains(line, "S-null-seven-worker") {
			detachedSeven = true
		}
	}
	if !detachedSeven {
		t.Fatal("seven null-origin worker was not detached")
	}
}

func TestCDPSandboxedSrcdocNullOriginWorker(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/worker.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "text/javascript")
		_, _ = w.Write([]byte("/* null-origin-worker */"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, "<!doctype html><title>%s</title><body>", r.URL.Query().Get("n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	page := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/?n=null-origin"
	worker := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/worker.js?null-origin-worker=1"
	env := setupProtect(t)
	var ordinary struct {
		Worker *struct {
			TargetID string `json:"targetId"`
			URL      string `json:"url"`
			Type     string `json:"type"`
		} `json:"worker"`
		Attached []struct {
			SessionID     string `json:"sessionId"`
			URL           string `json:"url"`
			ParentFrameID string `json:"parentFrameId"`
		} `json:"attached"`
	}
	env.evalJSONWithin(t, "linkTest.protect.sandboxedNullOriginWorker("+jsArgs("chrome", page, worker)+")", &ordinary, 30*time.Second)
	if ordinary.Worker == nil || ordinary.Worker.Type != "worker" || !strings.Contains(ordinary.Worker.URL, "null-origin-worker=1") {
		t.Fatalf("sandboxed null-origin worker on an ordinary page was not delivered: %+v", ordinary)
	}
	sevenPage := env.seven + "/index.html"
	var seven struct {
		Worker   any `json:"worker"`
		Attached []struct {
			URL string `json:"url"`
		} `json:"attached"`
	}
	env.evalJSONWithin(t, "linkTest.protect.sandboxedNullOriginWorker("+jsArgs("chrome", sevenPage, worker)+")", &seven, 30*time.Second)
	if seven.Worker != nil {
		t.Fatalf("sandboxed null-origin worker under seven was listed: %+v", seven)
	}
	for _, a := range seven.Attached {
		if strings.Contains(a.URL, "null-origin-worker") {
			t.Fatalf("sandboxed null-origin worker under seven was attached: %+v", seven)
		}
	}
}
