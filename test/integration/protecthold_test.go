package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func emptyURLPageHoldScript() fakeScript {
	return func(method string, _ json.RawMessage) (string, []string) {
		switch method {
		case "Browser.getBrowserCommandLine":
			return "{}", []string{
				`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":{"targetId":"H1","type":"page","url":"","title":"","attached":false}}}`,
				`{"method":"Target.attachedToTarget","params":{"sessionId":"HELD","targetInfo":{"targetId":"H1","type":"page","url":"","title":"","attached":true},"waitingForDebugger":false}}`,
			}
		case "Target.attachToTarget":
			return `{"sessionId":"NAV-H1"}`, nil
		case "Target.detachFromTarget":
			return "{}", nil
		case "Browser.getVersion":
			return `{"product":"Chrome/154.0.8037.98","protocolVersion":"1.3"}`, nil
		case "Network.enable", "Fetch.enable", "Page.enable", "Runtime.enable", "Target.setDiscoverTargets", "Target.getTargets", "Target.setAutoAttach":
			return "{}", nil
		}
		return "{}", nil
	}
}

func TestCDPHoldTimeoutHidesAURLLessPage(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, []string{"SLICC_CDP_HOLD_TIMEOUT=500ms"}, func(string, string) fakeScript {
		return emptyURLPageHoldScript()
	})
	var r struct {
		Attached int `json:"attached"`
		MS       int `json:"ms"`
	}
	env.evalJSONWithin(t, "linkTest.protect.holdTimeoutHide('chrome-2')", &r, 15*time.Second)
	if r.Attached != 0 {
		t.Fatalf("client received %d Target.attachedToTarget for the held session, want 0", r.Attached)
	}
	if r.MS < 400 || r.MS > 5000 {
		t.Fatalf("hold timeout took %d ms, want about the shortened 500ms hold", r.MS)
	}
	for _, raw := range fake.received() {
		var m struct {
			Method string `json:"method"`
			ID     *int64 `json:"id"`
		}
		if json.Unmarshal([]byte(raw), &m) != nil {
			continue
		}
		if m.Method == "Target.attachedToTarget" && m.ID == nil {
			t.Fatalf("Chrome received a synthetic Target.attachedToTarget: %s", raw)
		}
	}
	env.a.waitStderr(t, "cdp: an auto-attached target never got a URL", 2*time.Second)
	if strings.Contains(env.a.stderr.String(), "cdp: releasing an auto-attached session with no URL as blank") {
		t.Fatal("hold timeout released the page as blank instead of hiding it")
	}
}
