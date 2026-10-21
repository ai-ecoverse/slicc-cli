package integration

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func outlivedScript(seven string) fakeScript {
	base := guardWaitScript("{}")
	return func(method string, params json.RawMessage) (string, []string) {
		switch method {
		case "Page.captureScreenshot":
			return `{"data":"c2V2ZW4ncyBlcnJvciBwYWdl"}`, nil
		case "Runtime.evaluate":
			return `{"result":{"type":"number","value":1}}`, []string{`{"method":"Page.frameNavigated","sessionId":"S1","params":{"type":"Navigation","frame":{"id":"T1","loaderId":"L1","url":"chrome-error://chromewebdata/","unreachableUrl":"` + seven + `/doc?code=fake-code-0001","securityOrigin":"://"}}}`}
		}
		return base(method, params)
	}
}

func TestCDPDropsAnAnswerThatOutlivesAProtectedErrorPageCommit(t *testing.T) {
	env, fake := setupFakeProtect(t, func(seven, _ string) fakeScript { return outlivedScript(seven) })
	fake.delay("Page.captureScreenshot", 2*time.Second)
	var r struct {
		Shot struct {
			cdpOutcome
			Result struct {
				Data string `json:"data"`
			} `json:"result"`
		} `json:"shot"`
		Trigger cdpOutcome `json:"trigger"`
	}
	env.evalJSON(t, "linkTest.protect.outlivedCapture('chrome-2')", &r)
	if !r.Trigger.OK {
		t.Fatalf("the command that commits the error page: %+v", r.Trigger)
	}
	if r.Shot.Result.Data != "" || !r.Shot.refused() || !strings.Contains(r.Shot.Error.Message, "committed Chrome's error page for a protected URL while this command ran") {
		t.Fatalf("a screenshot answered after the error page for seven's URL committed reached the page: %+v", r.Shot)
	}
}
