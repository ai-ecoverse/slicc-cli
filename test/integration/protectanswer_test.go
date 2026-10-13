package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

type fakeCall struct {
	result string
	after  []string
}

func bodyRaceScript(seven, allowed string) fakeScript {
	req := func(id, url string) string {
		return netEvent("requestWillBeSent", `{"requestId":"`+id+`","request":{"url":"`+url+`","headers":{}}}`)
	}
	res := func(id, url string) string {
		return netEvent("responseReceived", `{"requestId":"`+id+`","response":{"url":"`+url+`","headers":{}}}`)
	}
	paused := func(id, url string) string {
		return `{"method":"Fetch.requestPaused","params":{"requestId":"` + id + `","request":{"url":"` + url + `","headers":{}},"resourceType":"XHR","responseStatusCode":200}}`
	}
	intercepted := func(id, network, url string) string {
		return netEvent("requestIntercepted", `{"interceptionId":"`+id+`","requestId":"`+network+`","request":{"url":"`+url+`","headers":{}},"resourceType":"XHR","responseStatusCode":200}`)
	}
	cookies := `{"cookies":[{"name":"seven","value":"secret-cookie","domain":"127.0.0.1","path":"/"},{"name":"plain","value":"kept-cookie","domain":"localhost","path":"/"}]}`
	calls := map[string]fakeCall{
		"Network.getResponseBody early":                          {`{"body":"secret-early","base64Encoded":false}`, []string{req("early", seven+"/x"), res("early", seven+"/x")}},
		"Network.getResponseBody plain":                          {`{"body":"kept-plain","base64Encoded":false}`, []string{req("plain", allowed+"/x"), res("plain", allowed+"/x")}},
		"Network.getRequestPostData post":                        {`{"postData":"kept-post"}`, []string{req("post", allowed+"/x")}},
		"Network.getResponseBody ghost":                          {`{"body":"secret-ghost","base64Encoded":false}`, nil},
		"Network.getResponseBodyForInterception intercept":       {`{"body":"secret-intercept","base64Encoded":false}`, []string{intercepted("intercept", "net-intercept", seven+"/x")}},
		"Network.takeResponseBodyForInterceptionAsStream iplain": {`{"stream":"stream-iplain"}`, []string{intercepted("iplain", "net-iplain", allowed+"/x")}},
		"Fetch.takeResponseBodyAsStream fseven":                  {`{"stream":"stream-seven"}`, []string{paused("fseven", seven+"/x")}},
		"Fetch.takeResponseBodyAsStream fplain":                  {`{"stream":"stream-plain"}`, []string{paused("fplain", allowed+"/x")}},
		"IO.read stream-plain":                                   {`{"data":"kept-stream","eof":true}`, nil},
		"IO.read stream-seven":                                   {`{"data":"secret-stream","eof":true}`, nil},
		"IO.read stream-guess":                                   {`{"data":"secret-guess","eof":true}`, nil},
		"Network.getAllCookies ":                                 {cookies, nil},
		"Network.getCookies ":                                    {cookies, nil},
	}
	return func(method string, params json.RawMessage) (string, []string) {
		var p struct {
			RequestID      string `json:"requestId"`
			InterceptionID string `json:"interceptionId"`
			Handle         string `json:"handle"`
		}
		_ = json.Unmarshal(params, &p)
		c, ok := calls[method+" "+p.RequestID+p.InterceptionID+p.Handle]
		if !ok {
			return "{}", nil
		}
		return c.result, c.after
	}
}

type callOutcome struct {
	Method string          `json:"method"`
	MS     int             `json:"ms"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  struct {
		Message string `json:"message"`
	} `json:"error"`
}

func TestCDPChecksBodyAnswersAgainstTheirRequest(t *testing.T) {
	env, fake := setupFakeProtect(t, bodyRaceScript)
	calls := [][]any{
		{"Network.getResponseBody", map[string]any{"requestId": "early"}},
		{"Network.getResponseBody", map[string]any{"requestId": "plain"}},
		{"Network.getRequestPostData", map[string]any{"requestId": "post"}},
		{"Network.getResponseBody", map[string]any{"requestId": "ghost"}},
		{"Network.getResponseBodyForInterception", map[string]any{"interceptionId": "intercept"}},
		{"Network.takeResponseBodyForInterceptionAsStream", map[string]any{"interceptionId": "iplain"}},
		{"Fetch.takeResponseBodyAsStream", map[string]any{"requestId": "fseven"}},
		{"Fetch.takeResponseBodyAsStream", map[string]any{"requestId": "fplain"}},
		{"IO.read", map[string]any{"handle": "stream-seven"}},
		{"IO.read", map[string]any{"handle": "stream-guess"}},
		{"IO.read", map[string]any{"handle": "stream-plain"}},
		{"Network.getAllCookies", map[string]any{}},
		{"Network.getCookies", map[string]any{"urls": []string{env.plain.URL + "/"}}},
		{"Network.replayXHR", map[string]any{"requestId": "plain"}},
		{"Network.replayXHR", map[string]any{"requestId": "replay-guess"}},
		{"Network.replayXHR", map[string]any{"requestId": "early"}},
	}
	var r struct {
		Out []callOutcome `json:"out"`
	}
	env.evalJSON(t, "linkTest.protect.calls("+jsArgs("chrome-2", calls)+")", &r)
	b, _ := json.Marshal(r)
	if len(r.Out) != len(calls) {
		t.Fatalf("calls: %s", b)
	}
	if strings.Contains(string(b), "secret") {
		t.Errorf("an answer carried seven's data: %s", b)
	}
	refused := map[int]string{0: "seven's origin", 3: "hasn't seen this request", 4: "seven's origin", 6: "seven's origin", 8: "didn't hand out this stream", 9: "didn't hand out this stream", 14: "hasn't seen this request", 15: "seven's origin"}
	for i, o := range r.Out {
		want, refuse := refused[i]
		switch {
		case refuse && (o.OK || !strings.HasPrefix(o.Error.Message, "slicc: refused: ") || !strings.Contains(o.Error.Message, want)):
			t.Errorf("call %d %s: want refused with %q, got %+v", i, o.Method, want, o)
		case !refuse && !o.OK:
			t.Errorf("call %d %s: refused: %s", i, o.Method, o.Error.Message)
		}
	}
	if r.Out[5].MS > 1500 {
		t.Errorf("an allowed intercepted stream waited %d ms, as if its interception were unknown", r.Out[5].MS)
	}
	if r.Out[3].MS < 1500 {
		t.Errorf("the answer for an unknown request was refused after %d ms, before waiting for it", r.Out[3].MS)
	}
	for _, kept := range []string{"kept-plain", "kept-post", "stream-iplain", "stream-plain", "kept-stream", "kept-cookie"} {
		if !strings.Contains(string(b), kept) {
			t.Errorf("an allowed answer lost %s: %s", kept, b)
		}
	}
	closed := false
	for _, m := range fake.received() {
		if strings.Contains(m, `"IO.read"`) && (strings.Contains(m, "stream-seven") || strings.Contains(m, "stream-guess")) {
			t.Errorf("the browser got %s", m)
		}
		if strings.Contains(m, `"Network.replayXHR"`) && !strings.Contains(m, `"plain"`) {
			t.Errorf("the browser got %s", m)
		}
		closed = closed || (strings.Contains(m, `"IO.close"`) && strings.Contains(m, "stream-seven"))
	}
	if !closed {
		t.Errorf("the refused stream wasn't closed: %q", fake.received())
	}
}
