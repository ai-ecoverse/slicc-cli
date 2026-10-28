package integration

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestCDPDiscoveryScrubsEncodedAndUnschemedProtectedTitles(t *testing.T) {
	var encodedOrigin, bareHost string
	env, _ := setupFakeProtect(t, func(seven, allowed string) fakeScript {
		encodedOrigin = seven
		bareHost = strings.TrimPrefix(seven, "http://")
		failed := seven + "/doc/path-secret-fixture?query-secret=fixture#fragment-secret-fixture"
		var full strings.Builder
		for _, b := range []byte(failed) {
			fmt.Fprintf(&full, "%%%02X", b)
		}
		deep := failed
		for range 6 {
			deep = url.QueryEscape(deep)
		}
		titles := []string{url.QueryEscape(full.String()), strings.TrimPrefix(failed, "http://"), "ordinary title", seven, bareHost, deep, "vault.sliccy.ai/doc/path-secret-fixture", "controller.sliccy.ai/tray/path-secret-fixture", "prefix " + url.QueryEscape(full.String()) + " suffix", "prefix " + strings.TrimPrefix(failed, "http://") + " suffix"}
		info := func(i int) string {
			return fmt.Sprintf(`{"targetId":"T%d","type":"page","url":%q,"title":%q}`, i, allowed+"/blank", titles[i])
		}
		return func(method string, params json.RawMessage) (string, []string) {
			switch method {
			case "Target.setDiscoverTargets":
				var events []string
				for i := range titles {
					for _, name := range []string{"Target.targetCreated", "Target.targetInfoChanged"} {
						events = append(events, fmt.Sprintf(`{"method":%q,"params":{"targetInfo":%s}}`, name, info(i)))
					}
				}
				for _, text := range []string{titles[0], titles[1], titles[5], titles[6], titles[7]} {
					events = append(events, fmt.Sprintf(`{"method":"Page.frameNavigated","params":{"frame":{"id":"F1","url":%q,"name":%q}}}`, allowed+"/blank", text), fmt.Sprintf(`{"method":"Console.messageAdded","params":{"message":{"text":%q}}}`, text))
				}
				return "{}", events
			case "Target.getTargetInfo":
				var p struct {
					TargetID string `json:"targetId"`
				}
				_ = json.Unmarshal(params, &p)
				for i := range titles {
					if p.TargetID == fmt.Sprintf("T%d", i) {
						return `{"targetInfo":` + info(i) + `}`, nil
					}
				}
			}
			return "{}", nil
		}
	})
	var result struct {
		Events []struct {
			Method string `json:"method"`
			Info   struct {
				TargetID string `json:"targetId"`
				Title    string `json:"title"`
				URL      string `json:"url"`
			} `json:"info"`
		} `json:"events"`
		Infos []struct {
			Title string `json:"title"`
		} `json:"infos"`
		Text []string `json:"text"`
	}
	env.evalJSON(t, "linkTest.protect.discoveryScrub('chrome-2')", &result)
	want := []string{encodedOrigin, bareHost, "ordinary title", encodedOrigin, bareHost, "", "vault.sliccy.ai", "controller.sliccy.ai", "prefix " + encodedOrigin + " suffix", "prefix " + bareHost + " suffix"}
	if len(result.Events) != 2*len(want) || len(result.Infos) != len(want) {
		t.Fatalf("discovery coverage: %+v", result)
	}
	for _, event := range result.Events {
		var i int
		if _, err := fmt.Sscanf(event.Info.TargetID, "T%d", &i); err != nil || i < 0 || i >= len(want) {
			t.Fatalf("unknown discovery target: %+v", event)
		}
		if event.Info.Title != want[i] || event.Info.URL != env.allowed+"/blank" {
			t.Errorf("%s target %s: got %+v, want title %q and ordinary URL", event.Method, event.Info.TargetID, event.Info, want[i])
		}
	}
	for i, info := range result.Infos {
		if info.Title != want[i] {
			t.Errorf("getTargetInfo T%d title %q, want %q", i, info.Title, want[i])
		}
	}
	textWant := []string{encodedOrigin, encodedOrigin, bareHost, bareHost, "", "", "vault.sliccy.ai", "vault.sliccy.ai", "controller.sliccy.ai", "controller.sliccy.ai"}
	if len(result.Text) != len(textWant) {
		t.Fatalf("frame name and console text coverage: %v", result.Text)
	}
	for i, text := range result.Text {
		if text != textWant[i] {
			t.Errorf("frame name or console text %d: %q, want %q", i, text, textWant[i])
		}
	}
}
