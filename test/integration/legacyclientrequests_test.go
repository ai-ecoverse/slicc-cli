package integration

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

const legacyInterceptionRefusal = "slicc: refused: the guard doesn’t support the legacy Network interception methods; intercept requests through Fetch instead"

func TestCDPLegacyClientURLAndRawResponseAreNeverForwarded(t *testing.T) {
	env, fake := setupFakeProtect(t, bodyRaceScript)
	calls := [][]any{
		{"Network.setRequestInterception", map[string]any{"patterns": []map[string]string{{"urlPattern": "*"}}}},
		{"Network.setRequestInterception", map[string]any{"patterns": []map[string]string{}}},
		{"Network.getResponseBodyForInterception", map[string]any{"interceptionId": "intercept"}},
		{"Network.takeResponseBodyForInterceptionAsStream", map[string]any{"interceptionId": "iplain"}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "iplain"}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "iplain", "headers": map[string]string{"X-Legacy": "1"}}},
		{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "iplain", "errorReason": "Failed"}},
	}
	for _, url := range []string{env.plain.URL + "/ordinary", env.seven + "/protected", "https://vault.sliccy.ai/protected", "not-a-url", ""} {
		calls = append(calls, []any{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "legacy-probe", "url": url}})
		raw := base64.StdEncoding.EncodeToString([]byte("HTTP/1.1 302 Found\r\nLocation: " + url + "\r\nContent-Length: 0\r\n\r\n"))
		calls = append(calls, []any{"Network.continueInterceptedRequest", map[string]any{"interceptionId": "legacy-probe", "rawResponse": raw}})
	}
	var result struct {
		Out []callOutcome `json:"out"`
	}
	env.evalJSON(t, "linkTest.protect.calls("+jsArgs("chrome-2", calls)+")", &result)
	if len(result.Out) != len(calls) {
		t.Fatal("missing terminal legacy refusals")
	}
	for _, o := range result.Out {
		if o.OK || o.Error.Message != legacyInterceptionRefusal {
			t.Errorf("legacy interception command was not refused: %+v", o)
		}
	}
	for _, wire := range fake.received() {
		var command struct {
			Method string `json:"method"`
		}
		if json.Unmarshal([]byte(wire), &command) != nil {
			t.Fatal("invalid captured wire")
		}
		switch command.Method {
		case "Network.setRequestInterception", "Network.continueInterceptedRequest", "Network.getResponseBodyForInterception", "Network.takeResponseBodyForInterceptionAsStream":
			t.Errorf("legacy client request reached browser wire: %s", wire)
		}
	}
	if strings.Contains(strings.Join(fake.received(), "\n"), "legacy-probe") {
		t.Error("a legacy probe reached the browser")
	}
}
