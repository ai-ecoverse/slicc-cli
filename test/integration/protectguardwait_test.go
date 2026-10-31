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

const guardSessionEvent = `{"method":"Network.dataReceived","sessionId":"S1","params":{"requestId":"r1","timestamp":1,"dataLength":1,"encodedDataLength":1}}`

func TestCDPHoldsATargetsCommandsPastAMinuteWhileItsGuardIsLive(t *testing.T) {
	env, fake := setupFakeProtect(t, func(string, string) fakeScript { return guardWaitScript("{}") })
	fake.delay("Network.enable", 90*time.Second)
	fake.every(guardSessionEvent)
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

func TestCDPRefusesASilentGuardAtTheIdleBoundWhileChromeAnswersOtherCommands(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, guardBounds("2s", "60s"), func(string, string) fakeScript { return guardWaitScript("{}") })
	fake.delay("Network.enable", 10*time.Minute)
	var r guardWaitResult
	env.evalJSON(t, "linkTest.protect.guardWait('chrome-2')", &r)
	if !r.Enable.refused() || !strings.Contains(r.Enable.Error.Message, "its guard made no progress in 2s") || r.Enable.MS > 15000 {
		t.Errorf("Page.enable on a target whose guard is silent while the browser answers: %+v, want slicc's refusal at the 2 s idle bound", r.Enable)
	}
	if !r.Other.OK || r.Other.MS > 10000 {
		t.Errorf("a browser command on the same stream: %+v, want it answered", r.Other)
	}
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

func TestCDPGuardBoundsFromTheEnvironmentNeverWidenTheGuard(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, guardBounds("1h", "1h"), func(string, string) fakeScript { return guardWaitScript("{}") })
	fake.delay("Network.enable", 10*time.Minute)
	fake.delay("Browser.getVersion", 10*time.Minute)
	var r guardHoldResult
	env.evalJSON(t, "linkTest.protect.guardHold('chrome-2')", &r)
	if !r.Enable.refused() || !strings.HasSuffix(r.Enable.Error.Message, ": its guard made no progress in 30s") || r.Enable.MS < 29000 || r.Enable.MS > 60000 {
		t.Errorf("Page.enable on the target with %s=1h: %+v, want slicc's refusal at the default 30 s idle bound", "SLICC_CDP_GUARD_IDLE", r.Enable)
	}
}

func unannounced(script fakeScript, sessionID string) fakeScript {
	return func(method string, params json.RawMessage) (string, []string) {
		result, after := script(method, params)
		if method == "Target.attachToTarget" && strings.Contains(result, `"sessionId":"`+sessionID+`"`) {
			return result, nil
		}
		return result, after
	}
}

func TestCDPHoldsATargetsCommandsBeforeChromeAnnouncesTheirSession(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, guardBounds("2s", "60s"), func(string, string) fakeScript { return unannounced(guardWaitScript("{}"), "S2") })
	fake.delay("Network.enable", 10*time.Minute)
	fake.delay("Browser.getVersion", 10*time.Minute)
	var r guardHoldResult
	env.evalJSON(t, "linkTest.protect.guardHold('chrome-2')", &r)
	if !r.Enable.refused() || !strings.Contains(r.Enable.Error.Message, "slicc can't keep seven's origin out of this target: its guard made no progress in 2s") || r.Enable.MS > 15000 {
		t.Errorf("Page.enable on a session Chrome answered but never announced: %+v, want it held for the target's guard and refused at the 2 s idle bound", r.Enable)
	}
}

type lateSessionResult struct {
	Enable struct {
		cdpOutcome
		MS int `json:"ms"`
	} `json:"enable"`
	Attach *cdpOutcome `json:"attach"`
}

func pageCommandsOn(fake *fakeCDPBrowser, sessionID string) []int {
	var at []int
	for i, raw := range fake.received() {
		var m struct {
			SessionID string `json:"sessionId"`
		}
		if json.Unmarshal([]byte(raw), &m) == nil && m.SessionID == sessionID {
			at = append(at, i)
		}
	}
	return at
}

func TestCDPRefusesCommandsOnASessionThatNeverRegisters(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, guardBounds("2s", "60s"), func(string, string) fakeScript { return guardWaitScript("{}") })
	fake.every(`{"method":"Target.targetInfoChanged","sessionId":"RELAY","params":{"targetInfo":{"targetId":"T1","type":"page","url":"about:blank","title":"","attached":false}}}`)
	var r lateSessionResult
	env.evalJSON(t, "linkTest.protect.lateSession('chrome-2', 'NOSUCH', null)", &r)
	if !r.Enable.refused() || r.Enable.Error.Message != "slicc: refused: slicc doesn't know which target this session belongs to: its guard made no progress in 2s" || r.Enable.MS < 1500 || r.Enable.MS > 15000 {
		t.Errorf("Page.enable on a session nobody attached: %+v, want it held and refused at the 2 s idle bound", r.Enable)
	}
	if at := pageCommandsOn(fake, "NOSUCH"); len(at) > 0 {
		t.Errorf("the browser got %d commands on the unknown session", len(at))
	}
}

func lateAttachScript(url string) fakeScript {
	var attaches atomic.Int32
	return func(method string, _ json.RawMessage) (string, []string) {
		switch method {
		case "Target.attachToTarget":
			if attaches.Add(1) == 1 {
				return `{"sessionId":"LATE"}`, []string{`{"method":"Target.attachedToTarget","params":{"sessionId":"LATE","targetInfo":{"targetId":"T9","type":"page","url":"` + url + `/doc","title":"","attached":true},"waitingForDebugger":false}}`}
			}
			return `{"sessionId":"NAV9"}`, nil
		case "Browser.getVersion":
			return `{"product":"Chrome/154.0.8037.98","protocolVersion":"1.3"}`, nil
		}
		return "{}", nil
	}
}

func TestCDPDecidesAHeldSessionsCommandsOnceItRegisters(t *testing.T) {
	for _, tc := range []struct {
		name      string
		protected bool
	}{
		{"on seven's origin", true},
		{"elsewhere", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, fake := setupFakeProtect(t, func(seven, allowed string) fakeScript {
				if tc.protected {
					return lateAttachScript(seven)
				}
				return lateAttachScript(allowed)
			})
			var r lateSessionResult
			env.evalJSON(t, "linkTest.protect.lateSession('chrome-2', 'LATE', 'T9')", &r)
			at := pageCommandsOn(fake, "LATE")
			if tc.protected {
				if !r.Enable.refused() || !strings.Contains(r.Enable.Error.Message, "target is on seven's origin") || len(at) > 0 {
					t.Errorf("Page.enable on a session that registers on seven's origin: %+v, %d commands reached the browser; want it refused", r.Enable, len(at))
				}
				return
			}
			attach := -1
			for i, raw := range fake.received() {
				if strings.Contains(raw, `"Target.attachToTarget"`) && strings.Contains(raw, `"T9"`) {
					attach = i
					break
				}
			}
			if !r.Enable.OK || len(at) != 1 || attach < 0 || at[0] < attach {
				t.Errorf("Page.enable on a session that registers elsewhere: %+v, sent at %v, the attach at %d; want it held until the session registered and then answered", r.Enable, at, attach)
			}
		})
	}
}

func TestCDPRefusesAHeldTargetAtTheOuterBoundWhileItsGuardIsLive(t *testing.T) {
	env, fake := setupFakeProtectEnv(t, guardBounds("2s", "8s"), func(string, string) fakeScript { return guardWaitScript("{}") })
	fake.delay("Network.enable", 10*time.Minute)
	fake.every(guardSessionEvent)
	var r guardHoldResult
	env.evalJSON(t, "linkTest.protect.guardHold('chrome-2')", &r)
	if !r.Enable.refused() || !strings.Contains(r.Enable.Error.Message, "slicc can't keep seven's origin out of this target: its guard wasn't ready in 8s") || r.Enable.MS < 6000 || r.Enable.MS > 30000 {
		t.Errorf("Page.enable on the target: %+v, want it held past the 2 s idle bound and refused at the 8 s outer bound", r.Enable)
	}
}

func TestCDPCountsOnlyItsGuardSessionAsAGuardsProgress(t *testing.T) {
	targetEvent := func(id string) string {
		return `{"method":"Target.targetInfoChanged","sessionId":"RELAY","params":{"targetInfo":{"targetId":"` + id + `","type":"page","url":"about:blank","title":"","attached":true}}}`
	}
	for _, tc := range []struct {
		name, event, want string
	}{
		{"an event on the guard's session", guardSessionEvent, "its guard wasn't ready in 8s"},
		{"a Target event for the target", targetEvent("T1"), "its guard made no progress in 2s"},
		{"a Target event for another target", targetEvent("T9"), "its guard made no progress in 2s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, fake := setupFakeProtectEnv(t, guardBounds("2s", "8s"), func(string, string) fakeScript { return guardWaitScript("{}") })
			fake.delay("Network.enable", 10*time.Minute)
			fake.delay("Browser.getVersion", 10*time.Minute)
			fake.every(tc.event)
			var r guardHoldResult
			env.evalJSON(t, "linkTest.protect.guardHold('chrome-2')", &r)
			if !r.Enable.refused() || !strings.HasSuffix(r.Enable.Error.Message, ": "+tc.want) || r.Enable.MS > 30000 {
				t.Errorf("Page.enable on the target: %+v, want a refusal with %q", r.Enable, tc.want)
			}
		})
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

func pausedServiceWorkerScript(allowed string) fakeScript {
	return func(method string, _ json.RawMessage) (string, []string) {
		switch method {
		case "Target.setAutoAttach":
			return "{}", []string{`{"method":"Target.attachedToTarget","params":{"sessionId":"PSW","targetInfo":{"targetId":"SW1","type":"service_worker","url":"` + allowed + `/sw.js","title":"","attached":true},"waitingForDebugger":true}}`}
		case "Target.attachToTarget":
			return `{"sessionId":"G1"}`, nil
		case "Browser.getVersion":
			return `{"product":"Chrome/154.0.8037.98","protocolVersion":"1.3"}`, nil
		}
		return "{}", nil
	}
}

func TestCDPLetsAPausedServiceWorkerStartBeforeItsGuard(t *testing.T) {
	for _, tc := range []struct {
		name   string
		attach time.Duration
	}{
		{"the guard attaches first", 0},
		{"the worker runs before the guard attaches", time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, fake := setupFakeProtectEnv(t, guardBounds("2s", "8s"), func(_, allowed string) fakeScript { return pausedServiceWorkerScript(allowed) })
			fake.gate("Network.enable", "Runtime.runIfWaitingForDebugger")
			if tc.attach > 0 {
				fake.delay("Target.attachToTarget", tc.attach)
			}
			var r struct {
				Out []struct {
					cdpOutcome
					Method string `json:"method"`
					MS     int    `json:"ms"`
				} `json:"out"`
			}
			env.evalJSON(t, "linkTest.protect.calls("+jsArgs("chrome-2", [][]any{
				{"Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}},
				{"Runtime.runIfWaitingForDebugger", map[string]any{}, "PSW"},
				{"Debugger.enable", map[string]any{}, "PSW"},
			})+")", &r)
			if len(r.Out) != 3 || !r.Out[1].OK || r.Out[1].MS > 1500 || !r.Out[2].refused() {
				t.Errorf("a service worker paused until its debugger runs it, whose guard Chrome answers only after that: %+v, want runIfWaitingForDebugger answered at once and worker Debugger refused", r.Out)
			}
			for _, raw := range fake.received() {
				var m struct {
					Method string `json:"method"`
				}
				_ = json.Unmarshal([]byte(raw), &m)
				if m.Method == "Debugger.enable" {
					t.Error("worker Debugger.enable reached Chrome")
				}
			}
		})
	}
}

func slowPageScript() fakeScript {
	script := guardWaitScript("{}")
	pad := strings.Repeat("x", 64<<10)
	var flood []string
	for range 8 {
		flood = append(flood, `{"method":"Tracing.dataCollected","params":{"value":[{"pad":"`+pad+`"}]}}`)
	}
	return func(method string, params json.RawMessage) (string, []string) {
		if method == "SystemInfo.getInfo" {
			return "{}", flood
		}
		return script(method, params)
	}
}

func TestCDPLeavesTheTimeAPageTakesToReadOutOfAGuardsIdleBound(t *testing.T) {
	for _, tc := range []struct {
		name   string
		enable time.Duration
		ok     bool
	}{
		{"Chrome answers the guard while the page reads nothing", time.Second, true},
		{"Chrome never answers the guard", 10 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, fake := setupFakeProtectEnv(t, guardBounds("2s", "60s"), func(string, string) fakeScript { return slowPageScript() })
			fake.delay("Network.enable", tc.enable)
			var r struct {
				Enable struct {
					cdpOutcome
					MS int `json:"ms"`
				} `json:"enable"`
				Flood bool `json:"flood"`
			}
			env.evalJSON(t, "linkTest.protect.guardBehindSlowPage('chrome-2', 6000)", &r)
			if !r.Flood {
				t.Fatalf("the page never got the flood that fills its window: %+v", r)
			}
			if tc.ok && (!r.Enable.OK || r.Enable.MS < 5500) {
				t.Errorf("Page.enable on a target whose guard Chrome answered while slicc waited 6 s on the page: %+v, want it answered once the page reads again", r.Enable)
			}
			if !tc.ok && (!r.Enable.refused() || !strings.HasSuffix(r.Enable.Error.Message, ": its guard made no progress in 2s") || r.Enable.MS < 7000 || r.Enable.MS > 20000) {
				t.Errorf("Page.enable on a target whose guard Chrome never answers, while slicc waited 6 s on the page: %+v, want slicc's refusal at the 2 s idle bound after the page reads again", r.Enable)
			}
		})
	}
}
