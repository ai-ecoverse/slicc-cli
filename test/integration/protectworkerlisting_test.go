package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCDPHidesProtectedWorkersFromListings(t *testing.T) {
	env, fake := setupFakeProtectPage(t, func(seven, allowed string) fakeScript {
		info := func(id, typ, raw string) string {
			return fmt.Sprintf(`{"targetId":%q,"type":%q,"url":%q,"title":%q}`, id, typ, raw, raw)
		}
		protected := info("W-protected", "worker", seven+"/worker.js?secret=fixture")
		shared := info("W-shared", "shared_worker", seven+"/shared-worker.js?secret=fixture")
		service := info("W-service", "service_worker", seven+"/sw.js?secret=fixture")
		ordinary := info("W-ordinary", "worker", allowed+"/worker.js")
		page := info("P-seven", "page", seven+"/doc?secret=fixture")
		tab := info("T-seven", "tab", seven+"/tab?secret=fixture")
		event := func(method, body string) string {
			return fmt.Sprintf(`{"method":%q,"params":{"targetInfo":%s}}`, method, body)
		}
		gone := func(id string) string {
			return fmt.Sprintf(`{"method":"Target.targetDestroyed","params":{"targetId":%q}}`, id)
		}
		bodies := map[string]string{
			"W-protected": protected, "W-shared": shared, "W-service": service,
			"W-ordinary": ordinary, "P-seven": page, "T-seven": tab,
		}
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Target.setDiscoverTargets":
				var events []string
				for _, id := range []string{"W-protected", "W-shared", "W-service", "W-ordinary", "P-seven", "T-seven"} {
					events = append(events, event("Target.targetCreated", bodies[id]), event("Target.targetInfoChanged", bodies[id]))
				}
				events = append(events, gone("W-protected"), gone("W-ordinary"), `{"method":"Console.messageAdded","params":{"message":{"text":"worker-listing-done"}}}`)
				return "{}", events
			case "Target.getTargets":
				return `{"targetInfos":[` + strings.Join([]string{protected, shared, service, ordinary, page, tab}, ",") + `]}`, nil
			case "Target.getTargetInfo":
				var p struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(params, &p)
				if body, ok := bodies[p.TargetID]; ok {
					return `{"targetInfo":` + body + `}`, nil
				}
			}
			return "{}", nil
		}
	})
	var result struct {
		List []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
			Title    string `json:"title"`
		} `json:"list"`
		ProtectedInfo cdpOutcome `json:"protectedInfo"`
		OrdinaryInfo  struct {
			OK     bool `json:"ok"`
			Result struct {
				TargetInfo struct {
					TargetID string `json:"targetId"`
					Type     string `json:"type"`
					URL      string `json:"url"`
					Title    string `json:"title"`
				} `json:"targetInfo"`
			} `json:"result"`
		} `json:"ordinaryInfo"`
		PageInfo struct {
			OK     bool `json:"ok"`
			Result struct {
				TargetInfo struct {
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"targetInfo"`
			} `json:"result"`
		} `json:"pageInfo"`
		TabInfo struct {
			OK     bool `json:"ok"`
			Result struct {
				TargetInfo struct {
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"targetInfo"`
			} `json:"result"`
		} `json:"tabInfo"`
		Attach cdpOutcome `json:"attach"`
		Events []struct {
			Method   string `json:"method"`
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
			Title    string `json:"title"`
		} `json:"events"`
	}
	env.evalJSON(t, "linkTest.protect.workerListing('chrome-2')", &result)
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "secret=fixture") || strings.Contains(string(raw), "/worker.js?") || strings.Contains(string(raw), "/shared-worker.js") || strings.Contains(string(raw), "/sw.js") {
		t.Fatalf("a protected worker reached the client: %s", raw)
	}
	var ordinaryListed, pageListed, tabListed bool
	for _, item := range result.List {
		switch item.TargetID {
		case "W-protected", "W-shared", "W-service":
			t.Errorf("getTargets lists %s as %+v", item.TargetID, item)
		case "W-ordinary":
			ordinaryListed = true
			if item.Type != "worker" || item.URL != env.allowed+"/worker.js" || item.Title != env.allowed+"/worker.js" {
				t.Errorf("ordinary worker listed as %+v", item)
			}
		case "P-seven":
			pageListed = true
			if item.Type != "page" || item.URL != env.seven || item.Title != env.seven {
				t.Errorf("protected page listed as %+v, want origin %s", item, env.seven)
			}
		case "T-seven":
			tabListed = true
			if item.Type != "tab" || item.URL != env.seven || item.Title != env.seven {
				t.Errorf("protected tab listed as %+v, want origin %s", item, env.seven)
			}
		}
	}
	if !ordinaryListed || !pageListed || !tabListed {
		t.Fatalf("listing missing an ordinary worker or a protected page or tab: %+v", result.List)
	}
	if result.ProtectedInfo.OK || result.ProtectedInfo.Error.Code != -32602 || result.ProtectedInfo.Error.Message != "No target with given id found" {
		t.Fatalf("getTargetInfo on the protected worker: %+v", result.ProtectedInfo)
	}
	info := result.OrdinaryInfo.Result.TargetInfo
	if !result.OrdinaryInfo.OK || info.TargetID != "W-ordinary" || info.Type != "worker" || info.URL != env.allowed+"/worker.js" {
		t.Fatalf("getTargetInfo on the ordinary worker: %+v", result.OrdinaryInfo)
	}
	if !result.PageInfo.OK || result.PageInfo.Result.TargetInfo.URL != env.seven || result.PageInfo.Result.TargetInfo.Title != env.seven {
		t.Fatalf("getTargetInfo on the protected page: %+v", result.PageInfo)
	}
	if !result.TabInfo.OK || result.TabInfo.Result.TargetInfo.URL != env.seven || result.TabInfo.Result.TargetInfo.Title != env.seven {
		t.Fatalf("getTargetInfo on the protected tab: %+v", result.TabInfo)
	}
	if result.Attach.OK || result.Attach.Error.Message != refusedText {
		t.Fatalf("attach to the protected worker: %+v", result.Attach)
	}
	var sawOrdinaryCreated, sawOrdinaryChanged, sawOrdinaryGone bool
	for _, event := range result.Events {
		switch event.TargetID {
		case "W-protected", "W-shared", "W-service":
			t.Errorf("discovery event for a protected worker: %+v", event)
		case "W-ordinary":
			switch event.Method {
			case "Target.targetCreated":
				sawOrdinaryCreated = true
				if event.Type != "worker" || event.URL != env.allowed+"/worker.js" || event.Title != env.allowed+"/worker.js" {
					t.Errorf("ordinary worker event: %+v", event)
				}
			case "Target.targetInfoChanged":
				sawOrdinaryChanged = true
				if event.Type != "worker" || event.URL != env.allowed+"/worker.js" || event.Title != env.allowed+"/worker.js" {
					t.Errorf("ordinary worker event: %+v", event)
				}
			case "Target.targetDestroyed":
				sawOrdinaryGone = true
			default:
				t.Errorf("ordinary worker event: %+v", event)
			}
		case "P-seven", "T-seven":
			if event.Method == "Target.targetDestroyed" || event.URL != env.seven || event.Title != env.seven {
				t.Errorf("protected page or tab event: %+v", event)
			}
		}
	}
	if !sawOrdinaryCreated || !sawOrdinaryChanged || !sawOrdinaryGone {
		t.Fatalf("ordinary worker discovery: %+v", result.Events)
	}
	for _, got := range fake.received() {
		if strings.Contains(got, "W-protected") && strings.Contains(got, "Target.getTargetInfo") {
			t.Fatalf("getTargetInfo for the protected worker was forwarded: %s", got)
		}
	}
}
