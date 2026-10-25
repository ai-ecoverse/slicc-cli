package integration

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCDPCallBookkeeping(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "settled", true: "continued"}[reuse], func(t *testing.T) {
			env, fake := setupFakeProtect(t, func(string, string) fakeScript { return guardWaitScript("{}") })
			var value any
			env.p.eval("window.callWork = linkTest.protect.callBookkeeping('chrome-2'); true", &value)
			env.p.waitFor("window.callReady === true", 10*time.Second)
			fake.gate("Page.getNavigationHistory", "Browser.getVersion")
			env.p.eval("window.callStart(); true", &value)
			env.p.waitFor("window.callHistory !== undefined", 20*time.Second)
			var probe struct {
				ID        int64  `json:"id"`
				SessionID string `json:"sessionId"`
			}
			for _, raw := range fake.received() {
				var m struct {
					ID        int64  `json:"id"`
					Method    string `json:"method"`
					SessionID string `json:"sessionId"`
				}
				_ = json.Unmarshal([]byte(raw), &m)
				if m.Method == "Page.getNavigationHistory" {
					probe.ID, probe.SessionID = m.ID, m.SessionID
				}
			}
			if probe.ID < 1<<30 || probe.SessionID == "" {
				t.Fatalf("missing call: %+v", probe)
			}
			env.p.eval("window.callFinish("+jsArgs(probe.ID, reuse)+"); true", &value)
			env.p.waitFor("window.callResult !== undefined", 10*time.Second)
			var result struct {
				History  cdpOutcome `json:"history"`
				Reserved cdpOutcome `json:"reserved"`
				Ordinary cdpOutcome `json:"ordinary"`
				Messages []struct {
					ID     int64           `json:"id"`
					Result json.RawMessage `json:"result"`
				} `json:"messages"`
			}
			env.p.eval("window.callResult", &result)
			if !result.History.refused() || !result.Ordinary.OK || reuse && !result.Reserved.refused() {
				t.Fatalf("call outcomes: %+v", result)
			}
			for _, m := range result.Messages {
				if m.ID == probe.ID && len(m.Result) != 0 {
					t.Fatal("unexpected call answer")
				}
			}
		})
	}
}

func TestCDPClientCallBookkeeping(t *testing.T) {
	env, fake := setupFakeProtect(t, func(string, string) fakeScript {
		base := guardWaitScript("{}")
		return func(method string, params json.RawMessage) (string, []string) {
			if method == "Browser.getHistograms" {
				return "{}", []string{`{"id":77,"sessionId":"OTHER","result":{}}`, `{"id":88,"result":{}}`}
			}
			return base(method, params)
		}
	})
	fake.gate("Browser.getVersion", "Browser.getHistograms")
	var result struct {
		Reserved  cdpOutcome `json:"reserved"`
		Duplicate cdpOutcome `json:"duplicate"`
		Original  cdpOutcome `json:"original"`
		Ordinary  cdpOutcome `json:"ordinary"`
		Unmatched int        `json:"unmatched"`
	}
	env.evalJSON(t, "linkTest.protect.clientCallBookkeeping('chrome-2')", &result)
	if !result.Reserved.refused() || !result.Duplicate.refused() || !result.Original.OK || !result.Ordinary.OK || result.Unmatched != 0 {
		t.Fatalf("call outcomes: %+v", result)
	}
}
