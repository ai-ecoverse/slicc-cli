package integration

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func protectedPauseEvent(method, id, url, resourceType string) string {
	switch method {
	case "Fetch.requestPaused":
		return `{"method":"Fetch.requestPaused","params":{"requestId":"` + id + `","request":{"url":"` + url + `","headers":{}},"resourceType":"` + resourceType + `","responseStatusCode":200}}`
	case "Fetch.authRequired":
		return `{"method":"Fetch.authRequired","params":{"requestId":"` + id + `","request":{"url":"` + url + `","headers":{}},"authChallenge":{"source":"Server","origin":"` + url + `","scheme":"basic","realm":"x"}}}`
	case "Network.requestIntercepted":
		return netEvent("requestIntercepted", `{"interceptionId":"`+id+`","requestId":"net-`+id+`","request":{"url":"`+url+`","headers":{}},"resourceType":"`+resourceType+`","responseStatusCode":200}`)
	}
	return ""
}

func protectedPauseScript(seven, allowed string) fakeScript {
	vault := "https://vault.sliccy.ai/x"
	events := []string{
		protectedPauseEvent("Fetch.requestPaused", "sdoc", seven+"/doc", "Document"),
		protectedPauseEvent("Fetch.requestPaused", "sxhr", seven+"/xhr", "XHR"),
		protectedPauseEvent("Fetch.authRequired", "sauth", seven+"/auth", "XHR"),
		protectedPauseEvent("Network.requestIntercepted", "sleg", seven+"/leg", "XHR"),
		protectedPauseEvent("Network.requestIntercepted", "slegdoc", seven+"/legdoc", "Document"),
		protectedPauseEvent("Fetch.requestPaused", "vdoc", vault+"/doc", "Document"),
		protectedPauseEvent("Fetch.requestPaused", "vxhr", vault+"/xhr", "XHR"),
		protectedPauseEvent("Fetch.authRequired", "vauth", vault+"/auth", "XHR"),
		protectedPauseEvent("Network.requestIntercepted", "vleg", vault+"/leg", "XHR"),
		protectedPauseEvent("Fetch.requestPaused", "odoc", allowed+"/doc", "Document"),
		protectedPauseEvent("Fetch.requestPaused", "oxhr", allowed+"/xhr", "XHR"),
		protectedPauseEvent("Fetch.requestPaused", "oxhr2", allowed+"/xhr2", "XHR"),
		protectedPauseEvent("Fetch.requestPaused", "oxhr3", allowed+"/xhr3", "XHR"),
		protectedPauseEvent("Fetch.requestPaused", "oxhr4", allowed+"/xhr4", "XHR"),
		protectedPauseEvent("Fetch.requestPaused", "oxhr5", allowed+"/xhr5", "XHR"),
		protectedPauseEvent("Fetch.authRequired", "oauth", allowed+"/auth", "XHR"),
		protectedPauseEvent("Network.requestIntercepted", "oleg", allowed+"/leg", "XHR"),
	}
	return afterEnable(events)
}

func TestCDPRefusesClientWritesOnPausedProtectedRequests(t *testing.T) {
	env, fake := setupFakeProtect(t, protectedPauseScript)
	plantCookie := "sid=PLANTED; Domain=.sliccy.ai; HttpOnly"
	plantBody := base64.StdEncoding.EncodeToString([]byte("<html>planted</html>"))
	plantBin := base64.StdEncoding.EncodeToString([]byte("Set-Cookie: sid=PLANTED; Domain=.sliccy.ai"))
	plantRaw := base64.StdEncoding.EncodeToString([]byte("HTTP/1.1 200 OK\r\nSet-Cookie: sid=PLANTED; Domain=.sliccy.ai\r\nContent-Length: 0\r\n\r\n"))
	setCookie := []map[string]string{{"name": "Set-Cookie", "value": plantCookie}}
	calls := [][]any{
		{"Network.enable", map[string]any{}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "sdoc", "responseCode": 200, "responseHeaders": setCookie, "body": plantBody}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "sxhr", "responseCode": 200, "responseHeaders": setCookie, "body": plantBody}},
		{"Fetch.continueResponse", map[string]any{"requestId": "sxhr", "responseCode": 200, "responseHeaders": setCookie}},
		{"Fetch.continueResponse", map[string]any{"requestId": "sxhr", "responseCode": 200, "binaryResponseHeaders": plantBin}},
		{"Fetch.continueRequest", map[string]any{"requestId": "sxhr"}},
		{"Fetch.failRequest", map[string]any{"requestId": "sxhr", "errorReason": "Failed"}},
		{"Fetch.continueWithAuth", map[string]any{"requestId": "sauth", "authChallengeResponse": map[string]string{"response": "CancelAuth"}}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "sleg", "rawResponse": plantRaw}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "sleg", "url": env.allowed + "/hijack"}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "sleg"}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "slegdoc"}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "vdoc", "responseCode": 200, "responseHeaders": setCookie, "body": plantBody}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "vxhr", "responseCode": 200, "responseHeaders": setCookie, "body": plantBody}},
		{"Fetch.continueResponse", map[string]any{"requestId": "vxhr", "responseCode": 200, "responseHeaders": setCookie}},
		{"Fetch.continueResponse", map[string]any{"requestId": "vxhr", "responseCode": 200, "binaryResponseHeaders": plantBin}},
		{"Fetch.continueRequest", map[string]any{"requestId": "vxhr"}},
		{"Fetch.failRequest", map[string]any{"requestId": "vxhr", "errorReason": "Failed"}},
		{"Fetch.continueWithAuth", map[string]any{"requestId": "vauth", "authChallengeResponse": map[string]string{"response": "CancelAuth"}}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "vleg", "rawResponse": plantRaw}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "vleg", "url": env.allowed + "/hijack"}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "vleg"}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "odoc", "responseCode": 200, "responseHeaders": []map[string]string{{"name": "Set-Cookie", "value": "a=kept; Path=/"}}, "body": base64.StdEncoding.EncodeToString([]byte("ok"))}},
		{"Fetch.continueResponse", map[string]any{"requestId": "oxhr", "responseCode": 200, "responseHeaders": []map[string]string{{"name": "Set-Cookie", "value": "a=kept; Path=/"}}}},
		{"Fetch.continueRequest", map[string]any{"requestId": "oxhr2"}},
		{"Fetch.failRequest", map[string]any{"requestId": "oxhr3", "errorReason": "Failed"}},
		{"Fetch.continueWithAuth", map[string]any{"requestId": "oauth", "authChallengeResponse": map[string]string{"response": "Default"}}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "oleg"}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "oxhr4", "responseCode": 200, "responseHeaders": []map[string]string{{"name": "Content-Type", "value": "text/plain"}}, "body": base64.StdEncoding.EncodeToString([]byte("kept"))}},
		{"Fetch.continueResponse", map[string]any{"requestId": "oxhr5", "responseCode": 200, "responseHeaders": []map[string]string{{"name": "X-Kept", "value": "1"}}}},
	}
	var r struct {
		Out []callOutcome `json:"out"`
	}
	env.evalJSON(t, "linkTest.protect.calls("+jsArgs("chrome-2", calls)+")", &r)
	b, _ := json.Marshal(r)
	if len(r.Out) != len(calls) {
		t.Fatalf("calls: %s", b)
	}
	refusedThrough := 21
	for i, o := range r.Out {
		switch {
		case i == 0:
			if !o.OK {
				t.Errorf("Network.enable refused: %s", b)
			}
		case i <= refusedThrough:
			if o.OK || o.Error.Message == "" || !strings.HasPrefix(o.Error.Message, "slicc: refused: ") {
				t.Errorf("call %d %s: want refused protected write, got %+v (%s)", i, o.Method, o, b)
			}
		default:
			if !o.OK {
				t.Errorf("call %d %s: ordinary control refused: %s", i, o.Method, b)
			}
		}
	}
	for _, m := range fake.received() {
		if strings.Contains(m, "PLANTED") {
			t.Errorf("a planted cookie or body reached the browser: %s", m)
		}
		for _, id := range []string{"sdoc", "sxhr", "vdoc", "vxhr"} {
			if strings.Contains(m, `"Fetch.fulfillRequest"`) && strings.Contains(m, `"requestId":"`+id+`"`) {
				t.Errorf("client fulfill of protected pause reached the browser: %s", m)
			}
			if strings.Contains(m, `"Fetch.continueResponse"`) && strings.Contains(m, `"requestId":"`+id+`"`) {
				t.Errorf("client continueResponse of protected pause reached the browser: %s", m)
			}
		}
		for _, id := range []string{"sleg", "vleg"} {
			if strings.Contains(m, `"Network.continueInterceptedRequest"`) && strings.Contains(m, `"interceptionId":"`+id+`"`) && (strings.Contains(m, `"rawResponse"`) || strings.Contains(m, `"url"`)) {
				t.Errorf("client legacy mutation of protected pause reached the browser: %s", m)
			}
		}
		for _, id := range []string{"sauth", "vauth"} {
			if strings.Contains(m, `"Fetch.continueWithAuth"`) && strings.Contains(m, `"requestId":"`+id+`"`) && strings.Contains(m, "CancelAuth") {
				t.Errorf("client continueWithAuth of protected pause reached the browser: %s", m)
			}
		}
	}
	forwarded := map[string]bool{}
	for _, m := range fake.received() {
		for _, id := range []string{"odoc", "oxhr", "oxhr2", "oxhr3", "oxhr4", "oxhr5", "oauth", "oleg"} {
			if strings.Contains(m, `"`+id+`"`) && (strings.Contains(m, `"Fetch.`) || strings.Contains(m, `"Network.continueInterceptedRequest"`)) {
				forwarded[id] = true
			}
		}
	}
	for _, id := range []string{"odoc", "oxhr", "oxhr2", "oxhr3", "oxhr4", "oxhr5", "oauth", "oleg"} {
		if !forwarded[id] {
			t.Errorf("ordinary control %s was not forwarded: %q", id, fake.received())
		}
	}
}
