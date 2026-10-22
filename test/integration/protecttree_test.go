package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCDPQuarantinedTreeRepliesRedactProtectedDocumentURLs(t *testing.T) {
	var origin string
	env, _ := setupFakeProtect(t, func(seven, _ string) fakeScript {
		origin = seven
		base := guardWaitScript("{}")
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Page.getNavigationHistory":
				return `{"currentIndex":1,"entries":[{"id":1,"url":"` + seven + `/protected?query-marker#fragment-marker"},{"id":2,"url":"about:blank"}]}`, nil
			case "Page.getFrameTree":
				return `{"frameTree":{"frame":{"id":"T1","url":"` + seven + `/path-marker?query-marker","urlFragment":"#fragment-marker","securityOrigin":"` + seven + `"}}}`, nil
			case "Page.getResourceTree":
				return `{"frameTree":{"frame":{"id":"T1","url":"` + seven + `/path-marker?query-marker","urlFragment":"#fragment-marker","securityOrigin":"` + seven + `"},"resources":[{"url":"` + seven + `/script-marker.js?query-marker#fragment-marker","type":"Script"}]}}`, nil
			}
			return base(method, params)
		}
	})
	var result struct {
		Frame struct {
			cdpOutcome
			Result json.RawMessage `json:"result"`
		} `json:"frame"`
		Resource struct {
			cdpOutcome
			Result json.RawMessage `json:"result"`
		} `json:"resource"`
		Script cdpOutcome `json:"script"`
	}
	env.evalJSON(t, "linkTest.protect.historyTrees('chrome-2')", &result)
	if !result.Frame.OK || !result.Resource.OK || !result.Script.refused() {
		t.Fatalf("quarantine controls: %+v", result)
	}
	for _, raw := range []json.RawMessage{result.Frame.Result, result.Resource.Result} {
		if strings.Contains(string(raw), "marker") {
			t.Fatalf("protected tree URL retained a path/query/fragment: %s", raw)
		}
		var tree struct {
			FrameTree struct {
				Frame struct {
					URL      string `json:"url"`
					Fragment string `json:"urlFragment"`
				} `json:"frame"`
				Resources []struct {
					URL string `json:"url"`
				} `json:"resources"`
			} `json:"frameTree"`
		}
		if json.Unmarshal(raw, &tree) != nil || tree.FrameTree.Frame.URL != origin || tree.FrameTree.Frame.Fragment != "" {
			t.Fatalf("protected frame not origin-redacted: %s", raw)
		}
		for _, resource := range tree.FrameTree.Resources {
			if resource.URL != origin {
				t.Fatalf("protected resource not origin-redacted: %s", raw)
			}
		}
	}
}
