package integration

import (
	"encoding/json"
	"testing"
)

func TestCDPHidesAProtectedIframeFromListings(t *testing.T) {
	env, _ := setupFakeProtect(t, func(seven, _ string) fakeScript {
		full := seven + "/doc?n=frame-secret&q=1"
		info := `{"targetId":"IFRAME","type":"iframe","url":` + jsonQuote(full) + `,"title":"secret frame title","attached":false}`
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Target.setDiscoverTargets":
				return "{}", []string{
					`{"method":"Target.targetCreated","params":{"targetInfo":` + info + `}}`,
					`{"method":"Target.targetInfoChanged","params":{"targetInfo":` + info + `}}`,
				}
			case "Target.getTargets":
				return `{"targetInfos":[` + info + `]}`, nil
			case "Target.getTargetInfo":
				var p struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(params, &p)
				if p.TargetID == "IFRAME" {
					return `{"targetInfo":` + info + `}`, nil
				}
			case "Target.attachToTarget":
				return `{"sessionId":"S-iframe"}`, nil
			}
			return "{}", nil
		}
	})
	var r struct {
		Listed any        `json:"listed"`
		Info   cdpOutcome `json:"info"`
		Attach cdpOutcome `json:"attach"`
		Events []struct {
			Method string `json:"method"`
			URL    string `json:"url"`
			Title  string `json:"title"`
		} `json:"events"`
	}
	env.evalJSON(t, "linkTest.protect.iframeListingScrub('chrome-2')", &r)
	if r.Listed != nil {
		t.Fatalf("getTargets listed a protected iframe: %+v", r.Listed)
	}
	if r.Info.OK || r.Info.Error.Code != -32602 || r.Info.Error.Message != "No target with given id found" {
		t.Fatalf("getTargetInfo on a protected iframe: %+v", r.Info)
	}
	if r.Attach.OK || r.Attach.Error.Message != refusedText {
		t.Fatalf("attach to a protected iframe: %+v", r.Attach)
	}
	if len(r.Events) != 0 {
		t.Fatalf("discovery events for a protected iframe: %+v", r.Events)
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
