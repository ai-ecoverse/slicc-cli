package integration

import (
	"encoding/json"
	"strings"
	"testing"
)

func quarantinedTreeScript(tree func(seven string) (frame, resource string)) func(seven, _ string) fakeScript {
	return func(seven, _ string) fakeScript {
		base := guardWaitScript("{}")
		frame, resource := tree(seven)
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Page.getNavigationHistory":
				return `{"currentIndex":1,"entries":[{"id":1,"url":"` + seven + `/protected?query-marker#fragment-marker","title":"title-marker"},{"id":2,"url":"about:blank","title":""}]}`, nil
			case "Page.getFrameTree":
				return frame, nil
			case "Page.getResourceTree":
				return resource, nil
			}
			return base(method, params)
		}
	}
}

func TestCDPQuarantinedTreeRepliesRedactProtectedDocumentURLs(t *testing.T) {
	var origin string
	env, _ := setupFakeProtect(t, quarantinedTreeScript(func(seven string) (string, string) {
		origin = seven
		child := `{"frame":{"id":"C1","parentId":"T1","url":"` + seven + `/path-marker?query-marker","urlFragment":"#fragment-marker","securityOrigin":"` + seven + `","name":"name-marker","mimeType":"text/html"}}`
		root := `"frame":{"id":"T1","url":"about:blank","securityOrigin":"://","name":"","mimeType":"text/html"}`
		frame := `{"frameTree":{` + root + `,"childFrames":[` + child + `]}}`
		resourceChild := strings.TrimSuffix(child, "}") + `,"resources":[{"url":"` + seven + `/script-marker.js?query-marker#fragment-marker","type":"Script","mimeType":"text/javascript"}]}`
		resource := `{"frameTree":{` + root + `,"childFrames":[` + resourceChild + `],"resources":[]}}`
		return frame, resource
	}))
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
		t.Fatalf("quarantine controls on an ordinary document: %+v", result)
	}
	for _, raw := range []json.RawMessage{result.Frame.Result, result.Resource.Result} {
		if strings.Contains(string(raw), "marker") {
			t.Fatalf("protected child frame kept a path, query, fragment or name: %s", raw)
		}
		var tree struct {
			FrameTree struct {
				Frame struct {
					URL string `json:"url"`
				} `json:"frame"`
				ChildFrames []struct {
					Frame struct {
						URL      string `json:"url"`
						Fragment string `json:"urlFragment"`
						Name     string `json:"name"`
					} `json:"frame"`
					Resources []struct {
						URL string `json:"url"`
					} `json:"resources"`
				} `json:"childFrames"`
			} `json:"frameTree"`
		}
		if json.Unmarshal(raw, &tree) != nil || tree.FrameTree.Frame.URL != "about:blank" || len(tree.FrameTree.ChildFrames) != 1 {
			t.Fatalf("ordinary root or protected child missing: %s", raw)
		}
		child := tree.FrameTree.ChildFrames[0]
		if child.Frame.URL != origin || child.Frame.Fragment != "" || child.Frame.Name != "" {
			t.Fatalf("protected child frame not cut to its origin with fragment and name blanked: %s", raw)
		}
		for _, resource := range child.Resources {
			if resource.URL != origin {
				t.Fatalf("protected resource not origin-redacted: %s", raw)
			}
		}
	}
}

func TestCDPQuarantinedTreeRefusesProtectedMainFrame(t *testing.T) {
	env, _ := setupFakeProtect(t, quarantinedTreeScript(func(seven string) (string, string) {
		root := `"frame":{"id":"T1","url":"` + seven + `/path-marker?query-marker","urlFragment":"#fragment-marker","securityOrigin":"` + seven + `","name":"name-marker"}`
		return `{"frameTree":{` + root + `}}`, `{"frameTree":{` + root + `,"resources":[{"url":"` + seven + `/script-marker.js","type":"Script"}]}}`
	}))
	var result struct {
		Frame    cdpOutcome `json:"frame"`
		Resource cdpOutcome `json:"resource"`
		History  cdpOutcome `json:"history"`
		Close    cdpOutcome `json:"close"`
	}
	env.evalJSON(t, "linkTest.protect.historyTreesLatch('chrome-2')", &result)
	if !result.Frame.refused() || !result.Resource.refused() {
		t.Fatalf("a tree answer whose main frame is protected must be refused: %+v", result)
	}
	if !result.History.refused() {
		t.Fatalf("the session must be refused after a protected main frame answered: %+v", result)
	}
	if !result.Close.OK {
		t.Fatalf("Target.closeTarget must still work: %+v", result)
	}
}
