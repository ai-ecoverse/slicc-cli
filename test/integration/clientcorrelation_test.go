package integration

import (
	"strings"
	"testing"
)

func TestCDPClientMutationWithoutNetworkCorrelationIsNotForwarded(t *testing.T) {
	env, fake := setupFakeProtect(t, bodyRaceScript)
	calls := [][]any{
		{"Fetch.takeResponseBodyAsStream", map[string]any{"requestId": "fplain"}},
		{"Fetch.continueRequest", map[string]any{"requestId": "fplain", "url": env.allowed + "/ordinary"}},
		{"Fetch.fulfillRequest", map[string]any{"requestId": "fplain", "responseCode": 302, "responseHeaders": []map[string]string{{"name": "Location", "value": env.allowed + "/ordinary"}}}},
		{"Fetch.continueWithAuth", map[string]any{"requestId": "fplain", "authChallengeResponse": map[string]string{"response": "Default"}}},
		{"Fetch.continueRequest", map[string]any{"requestId": "fplain"}},
	}
	var result struct {
		Out []callOutcome `json:"out"`
	}
	env.evalJSON(t, "linkTest.protect.calls("+jsArgs("chrome-2", calls)+")", &result)
	if len(result.Out) != len(calls) || !result.Out[0].OK || !result.Out[4].OK {
		t.Fatalf("unmodified page continuation failed: %+v", result.Out)
	}
	for _, o := range result.Out[1:4] {
		if o.OK || !strings.Contains(o.Error.Message, "classify every redirect") {
			t.Errorf("uncorrelated client mutation escaped: %+v", o)
		}
	}
	for _, wire := range fake.received() {
		if strings.Contains(wire, `"Fetch.fulfillRequest"`) || strings.Contains(wire, `"Fetch.continueWithAuth"`) || strings.Contains(wire, `"Fetch.continueRequest"`) && strings.Contains(wire, `"url"`) {
			t.Errorf("uncorrelated client mutation reached wire: %s", wire)
		}
	}
}
