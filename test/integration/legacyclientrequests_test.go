package integration

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestCDPLegacyClientURLAndRawResponseAreNeverForwarded(t *testing.T) {
	env, fake := setupFakeProtect(t, bodyRaceScript)
	calls := [][]any{{"Network.takeResponseBodyForInterceptionAsStream", map[string]any{"interceptionId": "iplain"}}, {"Network.continueInterceptedRequest", map[string]any{"interceptionId": "iplain"}}}
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
	if !result.Out[0].OK || !result.Out[1].OK {
		t.Fatal("unmodified page continuation no longer allowed")
	}
	for _, o := range result.Out[2:] {
		if o.OK || !strings.Contains(o.Error.Message, "Network.continueInterceptedRequest with url or rawResponse") {
			t.Errorf("legacy mutation did not fail closed: %+v", o)
		}
	}
	unmodifiedForwarded := false
	for _, wire := range fake.received() {
		var command struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if json.Unmarshal([]byte(wire), &command) != nil {
			t.Fatal("invalid captured wire")
		}
		if command.Method == "Network.continueInterceptedRequest" && command.Params["interceptionId"] == "iplain" {
			unmodifiedForwarded = true
		}
		if command.Method == "Network.continueInterceptedRequest" && command.Params["interceptionId"] == "legacy-probe" {
			t.Fatalf("legacy client request reached browser wire: %s", wire)
		}
	}
	if !unmodifiedForwarded {
		t.Fatal("C5 unmodified legacy continuation was not forwarded")
	}
}
