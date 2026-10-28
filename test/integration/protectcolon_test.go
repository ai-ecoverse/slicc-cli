package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCDPKeepsOrdinaryColonChainsAndScrubsProtectedSuffixes(t *testing.T) {
	unicode := strings.Repeat("é:", 64) + "/x " + strings.Repeat("é:", 6400) + "/x "
	type row struct {
		text string
		want string
	}
	tests := []row{
		{unicode + "https://vault.sliccy.ai/scrub-secret-fixture", unicode + "https://vault.sliccy.ai"},
		{strings.Repeat("catalog.example.org:443/fixture ", 80) + "https://docs.example.com/", strings.Repeat("catalog.example.org:443/fixture ", 80) + "https://docs.example.com/"},
		{"seven.sliccy.ai:443/scrub-secret-fixture", "seven.sliccy.ai:443"},
		{"prefix:seven.sliccy.ai:443/scrub-secret-fixture", "prefix:seven.sliccy.ai:443"},
		{"prefix:ｓｅｖｅｎ．ｓｌｉｃｃｙ．ａｉ:443/scrub-secret-fixture", "prefix:ｓｅｖｅｎ．ｓｌｉｃｃｙ．ａｉ:443"},
		{"prefix:se\u200bven.sliccy.ai:443/scrub-secret-fixture", "prefix:se\u200bven.sliccy.ai:443"},
		{"prefix:%73even.sliccy.ai:443/scrub-secret-fixture", "prefix:%73even.sliccy.ai:443"},
	}
	env, _ := setupFakeProtect(t, func(seven, allowed string) fakeScript {
		for _, alias := range []string{"2130706433", "0x7f.1"} {
			host := "prefix:" + alias + seven[strings.LastIndexByte(seven, ':'):]
			tests = append(tests, row{host + "/scrub-secret-fixture", host})
		}
		return func(method string, _ json.RawMessage) (string, []string) {
			if method != "Target.setDiscoverTargets" {
				return "{}", nil
			}
			var events []string
			for i, tc := range tests {
				events = append(events,
					fmt.Sprintf(`{"method":"Target.targetInfoChanged","params":{"targetInfo":{"targetId":"BOUND%d","type":"page","url":%q,"title":%q}}}`, i, allowed+"/blank", tc.text),
					fmt.Sprintf(`{"method":"Console.messageAdded","params":{"message":{"text":%q}}}`, tc.text),
				)
			}
			return "{}", append(events, `{"method":"Console.messageAdded","params":{"message":{"text":"scrub-bound-done"}}}`)
		}
	})
	var result struct {
		Titles  []string `json:"titles"`
		Console []string `json:"console"`
	}
	env.evalJSONWithin(t, "linkTest.protect.scrubBoundedDiscovery('chrome-2')", &result, 20*time.Second)
	for name, values := range map[string][]string{"title": result.Titles, "console": result.Console} {
		if len(values) != len(tests) {
			t.Fatalf("missing %s metadata events: %d", name, len(values))
		}
		for i, tc := range tests {
			if values[i] != tc.want {
				t.Errorf("%s %d: got %q, want %q", name, i, values[i], tc.want)
			}
		}
	}
}
