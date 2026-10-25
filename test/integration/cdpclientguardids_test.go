package integration

import (
	"encoding/json"
	"testing"
)

func TestCDPBiosGuardIDs(t *testing.T) {
	env, fake := setupFakeProtect(t, func(string, string) fakeScript {
		base := guardWaitScript("{}")
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Page.getFrameTree":
				return `{"frameTree":{"frame":{"id":"T1","url":"about:blank","securityOrigin":"://"}}}`, nil
			case "Runtime.evaluate":
				return `{"result":{"type":"number","value":1}}`, nil
			}
			return base(method, params)
		}
	})
	var result struct {
		Attach struct {
			cdpOutcome
			Result json.RawMessage `json:"result"`
		} `json:"attach"`
		Fetch    cdpOutcome `json:"fetch"`
		Tree     cdpOutcome `json:"tree"`
		Plain    cdpOutcome `json:"plain"`
		Boundary cdpOutcome `json:"boundary"`
	}
	env.evalJSON(t, "linkTest.protect.biosGuardIDs('chrome-2')", &result)
	if !result.Attach.OK || !result.Fetch.OK || !result.Tree.OK || !result.Plain.OK || !result.Boundary.OK {
		t.Fatalf("client guard command outcomes: %+v", result)
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result.Attach.Result, &attached); err != nil || attached.SessionID == "" {
		t.Fatalf("attach result: %s (%v)", result.Attach.Result, err)
	}
	want := map[int64]string{
		1 << 30:        "Target.attachToTarget",
		1<<30 + 1:      "Fetch.enable",
		1<<30 + 2:      "Page.getFrameTree",
		1<<30 + 3:      "Runtime.evaluate",
		0x70000000 - 1: "Runtime.evaluate",
	}
	for _, raw := range fake.received() {
		var m struct {
			ID        int64  `json:"id"`
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		if method, ok := want[m.ID]; ok && method == m.Method {
			if m.Method != "Target.attachToTarget" && m.SessionID != attached.SessionID {
				t.Fatalf("command %d session = %q, want %q", m.ID, m.SessionID, attached.SessionID)
			}
			delete(want, m.ID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("client guard commands not forwarded: %v", want)
	}
}
