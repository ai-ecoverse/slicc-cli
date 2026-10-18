package integration

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func guardWaitScript(networkEnable string) fakeScript {
	var sessions atomic.Int32
	return func(method string, _ json.RawMessage) (string, []string) {
		switch method {
		case "Target.createTarget":
			return `{"targetId":"T1"}`, []string{`{"method":"Target.targetCreated","sessionId":"RELAY","params":{"targetInfo":{"targetId":"T1","type":"page","url":"about:blank","title":"","attached":false}}}`}
		case "Target.attachToTarget":
			n := sessions.Add(1)
			parent := ""
			if n == 1 {
				parent = `"sessionId":"RELAY",`
			}
			id := "S" + strconv.Itoa(int(n))
			return `{"sessionId":"` + id + `"}`, []string{`{"method":"Target.attachedToTarget",` + parent + `"params":{"sessionId":"` + id + `","targetInfo":{"targetId":"T1","type":"page","url":"about:blank","title":"","attached":true},"waitingForDebugger":false}}`}
		case "Network.enable":
			return networkEnable, nil
		case "Browser.getVersion":
			return `{"product":"Chrome/154.0.8037.98","protocolVersion":"1.3"}`, nil
		}
		return "{}", nil
	}
}

type guardWaitResult struct {
	Enable struct {
		cdpOutcome
		MS int `json:"ms"`
	} `json:"enable"`
	Other struct {
		cdpOutcome
		MS int `json:"ms"`
	} `json:"other"`
}

func TestCDPHoldsATargetsCommandsPastAMinuteWhileChromeIsLive(t *testing.T) {
	env, fake := setupFakeProtect(t, func(string, string) fakeScript { return guardWaitScript("{}") })
	fake.delay("Network.enable", 90*time.Second)
	var r guardWaitResult
	env.evalJSONWithin(t, "linkTest.protect.guardWait('chrome-2')", &r, 150*time.Second)
	if !r.Enable.OK || r.Enable.MS < 89000 {
		t.Errorf("Page.enable on the target: %+v, want it to succeed once the guard is up, after about 90 s", r.Enable)
	}
	if !r.Other.OK || r.Other.MS > 10000 {
		t.Errorf("a browser command on the same stream: %+v, want it answered without waiting for the target's guard", r.Other)
	}
}

type guardHoldResult struct {
	Enable struct {
		cdpOutcome
		MS int `json:"ms"`
	} `json:"enable"`
}

func guardBounds(idle, limit string) []string {
	return []string{"SLICC_CDP_GUARD_IDLE=" + idle, "SLICC_CDP_GUARD_LIMIT=" + limit}
}

func TestCDPRefusesAHeldTargetAtTheIdleBoundWhenChromeIsSilent(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, guardBounds("2s", "60s"), func(string, string) fakeScript { return guardWaitScript("{}") })
	fake.delay("Network.enable", 10*time.Minute)
	fake.delay("Browser.getVersion", 10*time.Minute)
	var r guardHoldResult
	env.evalJSON(t, "linkTest.protect.guardHold('chrome-2')", &r)
	if !r.Enable.refused() || !strings.Contains(r.Enable.Error.Message, "slicc can't keep seven's origin out of this target: its guard made no progress in 2s") || r.Enable.MS > 15000 {
		t.Errorf("Page.enable on the target: %+v, want slicc's refusal at the 2 s idle bound", r.Enable)
	}
}

func TestCDPRefusesAHeldTargetAtTheOuterBoundWhileChromeIsLive(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, guardBounds("2s", "8s"), func(string, string) fakeScript { return guardWaitScript("{}") })
	fake.delay("Network.enable", 10*time.Minute)
	var r guardHoldResult
	env.evalJSON(t, "linkTest.protect.guardHold('chrome-2')", &r)
	if !r.Enable.refused() || !strings.Contains(r.Enable.Error.Message, "slicc can't keep seven's origin out of this target: its guard wasn't ready in 8s") || r.Enable.MS < 6000 || r.Enable.MS > 30000 {
		t.Errorf("Page.enable on the target: %+v, want it held past the 2 s idle bound and refused at the 8 s outer bound", r.Enable)
	}
}

func TestCDPResetsAStreamWhoseHeldCommandsPassTheCaps(t *testing.T) {
	for _, tc := range []struct {
		name        string
		count, size int
	}{
		{"4097 commands", 4097, 0},
		{"over 64 MiB", 17, 4 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, fake := setupFakeProtect(t, func(string, string) fakeScript { return guardWaitScript("{}") })
			fake.delay("Network.enable", 10*time.Minute)
			var r struct {
				Stream struct {
					Reset  string `json:"reset"`
					Detail string `json:"detail"`
				} `json:"stream"`
				Failed string `json:"failed"`
			}
			env.evalJSON(t, "linkTest.protect.guardFlood("+jsArgs("chrome-2", tc.count, tc.size)+")", &r)
			if r.Stream.Reset != "ENOBUFS" {
				t.Errorf("the stream: %+v, want RESET ENOBUFS", r)
			}
		})
	}
}

func TestCDPRefusesATargetsCommandsAtOnceWhenItsGuardFails(t *testing.T) {
	env, _ := setupFakeProtect(t, func(string, string) fakeScript { return guardWaitScript("!Network.enable failed") })
	var r guardWaitResult
	env.evalJSON(t, "linkTest.protect.guardWait('chrome-2')", &r)
	if r.Enable.OK || !strings.Contains(r.Enable.Error.Message, "slicc can't keep seven's origin out of this target") || r.Enable.MS > 5000 {
		t.Errorf("Page.enable on the target: %+v, want slicc's refusal at once", r.Enable)
	}
}
