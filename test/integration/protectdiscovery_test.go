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
			case "Target.getTargets":
				infos := make([]string, len(titles))
				for i := range infos {
					infos[i] = info(i)
				}
				return `{"targetInfos":[` + strings.Join(infos, ",") + `]}`, nil
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
		List []struct {
			Title string `json:"title"`
		} `json:"list"`
	}
	env.evalJSON(t, "linkTest.protect.discoveryScrub('chrome-2')", &result)
	want := []string{encodedOrigin, bareHost, "ordinary title", encodedOrigin, bareHost, "", "vault.sliccy.ai", "controller.sliccy.ai", "prefix " + encodedOrigin + " suffix", "prefix " + bareHost + " suffix"}
	if len(result.Events) != 2*len(want) || len(result.Infos) != len(want) || len(result.List) != len(want) {
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
	for i, info := range result.List {
		if info.Title != want[i] {
			t.Errorf("getTargets T%d title %q, want %q", i, info.Title, want[i])
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

func TestCDPDiscoveryCutsProtectedHostsInURLFragments(t *testing.T) {
	type fragmentCase struct {
		prefix, host, raw string
	}
	const fixture = "/doc/path-secret-fixture"
	var cases []fragmentCase
	var events []string
	env, _ := setupFakeProtect(t, func(seven, allowed string) fakeScript {
		for _, host := range []string{"vault.sliccy.ai", "seven.sliccy.ai", "controller.sliccy.ai", strings.TrimPrefix(seven, "http://")} {
			tail := host + fixture
			for _, form := range []struct{ prefix, tail string }{
				{"https://ordinary.example/?a=https://x.example/#", tail},
				{"https://ordinary.example/#", tail},
				{"https://ordinary.example/?a=" + url.QueryEscape("https://x.example/#"), url.QueryEscape(tail)},
				{"https://ordinary.example/#", url.PathEscape(tail)},
				{"https://ordinary.example/?a=https://x.example/#", url.PathEscape(tail)},
			} {
				raw := form.prefix + form.tail
				events = append(events, fmt.Sprintf(`{"method":"Target.targetInfoChanged","params":{"targetInfo":{"targetId":"FRAG%d","type":"page","url":%q,"title":%q}}}`, len(cases), raw, raw))
				cases = append(cases, fragmentCase{form.prefix, host, raw})
			}
		}
		events = append(events, `{"method":"Console.messageAdded","params":{"message":{"text":"fragment-scrub-done"}}}`)
		return func(method string, _ json.RawMessage) (string, []string) {
			if method == "Target.setDiscoverTargets" {
				return "{}", events
			}
			return "{}", nil
		}
	})
	for i, c := range cases {
		var sent struct {
			Params struct {
				TargetInfo struct {
					URL   string `json:"url"`
					Title string `json:"title"`
				} `json:"targetInfo"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(events[i]), &sent); err != nil || sent.Params.TargetInfo.URL != c.raw || sent.Params.TargetInfo.Title != c.raw || !strings.Contains(events[i], "path-secret-fixture") {
			t.Fatalf("the fake's event %d does not carry the fixture in its title and URL: %s", i, events[i])
		}
		u, err := url.Parse(c.raw)
		if err == nil && u.Fragment == "" {
			u, err = url.Parse(u.Query().Get("a"))
		}
		if err != nil || u.Fragment != c.host+fixture {
			t.Fatalf("the fake's URL %q does not put the fixture in a URL fragment, so fragments went untested", c.raw)
		}
	}
	var result struct {
		Infos []struct {
			TargetID string `json:"targetId"`
			Title    string `json:"title"`
			URL      string `json:"url"`
		} `json:"infos"`
		Received string `json:"received"`
	}
	env.evalJSON(t, "linkTest.protect.fragmentDiscovery('chrome-2')", &result)
	if len(result.Infos) != len(cases) {
		t.Fatalf("fragment discovery coverage: got %d of %d events", len(result.Infos), len(cases))
	}
	for _, info := range result.Infos {
		var i int
		if _, err := fmt.Sscanf(info.TargetID, "FRAG%d", &i); err != nil || i < 0 || i >= len(cases) {
			t.Fatalf("unknown fragment target: %+v", info)
		}
		c := cases[i]
		for name, got := range map[string]string{"title": info.Title, "url": info.URL} {
			if rest, ok := strings.CutPrefix(got, c.prefix); !ok || rest != c.host && rest != url.QueryEscape(c.host) {
				t.Errorf("%s for %q: got %q, want %q", name, c.raw, got, c.prefix+c.host)
			}
		}
	}
	for _, leak := range []string{"path-secret-fixture", "/doc", "%2fdoc"} {
		if strings.Contains(strings.ToLower(result.Received), leak) {
			t.Errorf("%q reached the client", leak)
		}
	}
}
