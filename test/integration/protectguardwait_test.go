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

func TestCDPHoldsATargetsCommandsWhileItsGuardIsSlowToSetUp(t *testing.T) {
	env, fake := setupFakeProtect(t, func(string, string) fakeScript { return guardWaitScript("{}") })
	fake.delay("Network.enable", 20*time.Second)
	var r guardWaitResult
	env.evalJSON(t, "linkTest.protect.guardWait('chrome-2')", &r)
	if !r.Enable.OK || r.Enable.MS < 19000 {
		t.Errorf("Page.enable on the target: %+v, want it to succeed once the guard is up, after about 20 s", r.Enable)
	}
	if !r.Other.OK || r.Other.MS > 10000 {
		t.Errorf("a browser command on the same stream: %+v, want it answered without waiting for the target's guard", r.Other)
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
