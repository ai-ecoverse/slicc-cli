package integration

import (
	"encoding/json"
	"testing"
)

func TestCDPListsAProtectedIframeAtItsBareOrigin(t *testing.T) {
	var origin, full string
	env, _ := setupFakeProtect(t, func(seven, _ string) fakeScript {
		origin = seven
		full = seven + "/doc?n=frame-secret&q=1"
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
			}
			return "{}", nil
		}
	})
	var r struct {
		Listed struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
			Title    string `json:"title"`
		} `json:"listed"`
		Info struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"info"`
		Events []struct {
			Method string `json:"method"`
			URL    string `json:"url"`
			Title  string `json:"title"`
		} `json:"events"`
	}
	env.evalJSON(t, "linkTest.protect.iframeListingScrub('chrome-2')", &r)
	if r.Listed.TargetID != "IFRAME" || r.Listed.Type != "iframe" || r.Listed.URL != origin || r.Listed.Title != origin {
		t.Fatalf("getTargets iframe listing: %+v want url/title %s", r.Listed, origin)
	}
	if r.Info.URL != origin || r.Info.Title != origin {
		t.Fatalf("getTargetInfo iframe: %+v want url/title %s", r.Info, origin)
	}
	if len(r.Events) == 0 {
		t.Fatal("no targetCreated/targetInfoChanged for the protected iframe")
	}
	for _, e := range r.Events {
		if e.URL != origin || e.Title != origin {
			t.Fatalf("%s iframe event: %+v want url/title %s", e.Method, e, origin)
		}
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
