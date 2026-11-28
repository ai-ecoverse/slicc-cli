package flow

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

const pageFlagFullwidthPilot = "\uff4c\uff45\uff41\uff53\uff45-\uff50\uff49\uff4c\uff4f\uff54.sliccy.ai"

func pageFlagRefusal(origin string) string {
	return "--page " + origin + " is on a protected host and is not a seven page"
}

func TestPageFlagRefusesProtected(t *testing.T) {
	for _, c := range []struct {
		name, page, origin string
		extra              []string
	}{
		{"Vault", "https://vault.sliccy.ai/", "https://vault.sliccy.ai", nil},
		{"VaultPreview", "https://vault-preview.sliccy.ai/", "https://vault-preview.sliccy.ai", nil},
		{"VaultMain", "https://vault-main.sliccy.ai/unlock", "https://vault-main.sliccy.ai", nil},
		{"PilotExact", "https://lease-pilot.sliccy.ai/", "https://lease-pilot.sliccy.ai", nil},
		{"PilotUpper", "https://LEASE-PILOT.SLICCY.AI/", "https://lease-pilot.sliccy.ai", nil},
		{"PilotTrailingDot", "https://lease-pilot.sliccy.ai./", "https://lease-pilot.sliccy.ai.", nil},
		{"Pilot443", "https://lease-pilot.sliccy.ai:443/", "https://lease-pilot.sliccy.ai", nil},
		{"PilotDeeperName", "https://a.lease-pilot.sliccy.ai/", "https://a.lease-pilot.sliccy.ai", nil},
		{"PilotHTTP", "http://lease-pilot.sliccy.ai/", "http://lease-pilot.sliccy.ai", nil},
		{"PilotFullwidth", "https://" + pageFlagFullwidthPilot + "/", "https://" + pageFlagFullwidthPilot, nil},
		{"UserinfoPilot", "https://seven.sliccy.ai@lease-pilot.sliccy.ai/", "https://lease-pilot.sliccy.ai", nil},
		{"OtherProtectedZoneHost", "https://mcpcat.sliccy.ai/", "https://mcpcat.sliccy.ai", nil},
		{"Apex", "https://sliccy.ai/", "https://sliccy.ai", nil},
		{"SevenHTTP", "http://seven.sliccy.ai/", "http://seven.sliccy.ai", nil},
		{"SevenOtherPort", "https://seven.sliccy.ai:8443/", "https://seven.sliccy.ai:8443", nil},
		{"SevenTrailingDot", "https://seven.sliccy.ai./", "https://seven.sliccy.ai.", nil},
		{"UnparseableZoneHost", "https://a.xn--1ug.sliccy.ai/", "https://a.xn--1ug.sliccy.ai", nil},
		{"VaultAlsoOrigin", "https://vault.sliccy.ai/", "https://vault.sliccy.ai", []string{"https://vault.sliccy.ai"}},
		{"VaultPort99999", "https://vault.sliccy.ai:99999/", "https://vault.sliccy.ai:99999", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := PageOrigins(c.page, c.extra)
			if err == nil || err.Error() != pageFlagRefusal(c.origin) || got != nil {
				t.Fatalf("PageOrigins(%q, %q) = %q, %v; want the refusal %q", c.page, c.extra, got, err, pageFlagRefusal(c.origin))
			}
			if !strings.HasPrefix(err.Error(), "--page ") {
				t.Fatalf("PageOrigins error %q must name --page, not --origin", err)
			}
		})
	}
}

func TestPageFlagStillAccepts(t *testing.T) {
	for _, c := range []struct {
		name, page string
		extra      []string
		want       []string
	}{
		{"SevenPage", "https://seven.sliccy.ai/", nil, nil},
		{"DefaultPage", DefaultPage, nil, nil},
		{"SixPage", SixPage, nil, nil},
		{"SevenUpper", "https://SEVEN.SLICCY.AI/os/", nil, nil},
		{"Seven443", "https://seven.sliccy.ai:443/", nil, nil},
		{"SevenWithOrigin", "https://seven.sliccy.ai/", []string{"http://localhost:3000"}, []string{"http://localhost:3000"}},
		{"LocalhostOrdinary", "http://localhost:5173/", nil, []string{"http://localhost:5173"}},
		{"LocalhostWithOrigin", "http://127.0.0.1:8080/index.html", []string{"https://example.org"}, []string{"https://example.org", "http://127.0.0.1:8080"}},
		{"NonSliccyHTTPS", "https://example.com/app/", nil, []string{"https://example.com"}},
		{"SliccyLookalikeOtherZone", "https://sliccy.ai.example.com/", nil, []string{"https://sliccy.ai.example.com"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := PageOrigins(c.page, c.extra)
			if err != nil || !slices.Equal(got, c.want) {
				t.Fatalf("PageOrigins(%q, %q) = %q, %v; want %q", c.page, c.extra, got, err, c.want)
			}
		})
	}
}

type pageFlagRecorder struct {
	mu       sync.Mutex
	events   []Event
	register int
}

func (r *pageFlagRecorder) emit(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *pageFlagRecorder) registered(context.Context, *node.Node, *policy.Policy, []Browser) (Caps, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.register++
	return Caps{}, nil
}

func (r *pageFlagRecorder) run(t *testing.T, page string) ([]Event, int, error) {
	t.Helper()
	entry := browser.Entry{ID: "cdp:pageflag", Kind: browser.KindEndpoint, Name: "pageflag", Endpoint: &browser.Endpoint{CDP: "pageflag:never"}}
	err := Go(t.Context(), Config{Page: page, Emit: r.emit, Register: r.registered}, []browser.Entry{entry})
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events), r.register, err
}

func TestPageFlagFlowRefusesBeforeLaunch(t *testing.T) {
	t.Run("Refused", func(t *testing.T) {
		want := pageFlagRefusal("https://vault-preview.sliccy.ai")
		events, register, err := (&pageFlagRecorder{}).run(t, "https://vault-preview.sliccy.ai/")
		if err == nil || err.Error() != want {
			t.Fatalf("Go = %v, want %q", err, want)
		}
		if len(events) != 2 || events[0].Event != Error || events[0].Message != want || events[1].Event != Closed || len(events[1].Browsers) != 0 || register != 0 {
			t.Fatalf("events %+v, register %d; want only the refusal and an empty close", events, register)
		}
	})
	t.Run("SevenReachesAttach", func(t *testing.T) {
		events, register, err := (&pageFlagRecorder{}).run(t, "https://seven.sliccy.ai/")
		if err == nil || !strings.HasPrefix(err.Error(), "attach to pageflag: ") {
			t.Fatalf("Go = %v, want the attach to the pageflag endpoint to fail", err)
		}
		if len(events) == 0 || events[0].Event != Error || events[0].Message != err.Error() || register != 0 {
			t.Fatalf("events %+v, register %d", events, register)
		}
	})
}

func originFlagRefusal(origin string) string {
	return "--origin " + origin + " is on a protected host and is not a seven page"
}

const originFlagExtID = "abcdefghijklmnopqrstuvwxyzabcdef"

func TestOriginFlagRefusesProtected(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []string
		want string
	}{
		{"Pilot", []string{"https://lease-pilot.sliccy.ai"}, originFlagRefusal("https://lease-pilot.sliccy.ai")},
		{"Vault", []string{"https://vault.sliccy.ai"}, originFlagRefusal("https://vault.sliccy.ai")},
		{"Deeper", []string{"https://a.lease-pilot.sliccy.ai"}, originFlagRefusal("https://a.lease-pilot.sliccy.ai")},
		{"Apex", []string{"https://sliccy.ai"}, originFlagRefusal("https://sliccy.ai")},
		{"PilotHTTP", []string{"http://lease-pilot.sliccy.ai"}, originFlagRefusal("http://lease-pilot.sliccy.ai")},
		{"SevenHTTP", []string{"http://seven.sliccy.ai"}, originFlagRefusal("http://seven.sliccy.ai")},
		{"PilotWS", []string{"ws://lease-pilot.sliccy.ai"}, originFlagRefusal("ws://lease-pilot.sliccy.ai")},
		{"VaultWSS", []string{"wss://vault.sliccy.ai"}, originFlagRefusal("wss://vault.sliccy.ai")},
		{"DeeperFTP", []string{"ftp://a.lease-pilot.sliccy.ai"}, originFlagRefusal("ftp://a.lease-pilot.sliccy.ai")},
		{"ApexFTP", []string{"ftp://sliccy.ai"}, originFlagRefusal("ftp://sliccy.ai")},
		{"PilotUpper", []string{"https://LEASE-PILOT.SLICCY.AI"}, originFlagRefusal("https://lease-pilot.sliccy.ai")},
		{"PilotTrailingDot", []string{"https://lease-pilot.sliccy.ai."}, originFlagRefusal("https://lease-pilot.sliccy.ai.")},
		{"Pilot443", []string{"https://lease-pilot.sliccy.ai:443"}, originFlagRefusal("https://lease-pilot.sliccy.ai")},
		{"PilotOtherPort", []string{"https://lease-pilot.sliccy.ai:8443"}, originFlagRefusal("https://lease-pilot.sliccy.ai:8443")},
		{"PilotFullwidth", []string{"https://" + pageFlagFullwidthPilot}, originFlagRefusal("https://" + pageFlagFullwidthPilot)},
		{"SevenOtherPort", []string{"https://seven.sliccy.ai:8443"}, originFlagRefusal("https://seven.sliccy.ai:8443")},
		{"SevenTrailingDot", []string{"https://seven.sliccy.ai."}, originFlagRefusal("https://seven.sliccy.ai.")},
		{"ExtZoneHost", []string{"chrome-extension://lease-pilot.sliccy.ai"}, originFlagRefusal("chrome-extension://lease-pilot.sliccy.ai")},
		{"UnparseableZone", []string{"https://a.xn--1ug.sliccy.ai"}, originFlagRefusal("https://a.xn--1ug.sliccy.ai")},
		{"VaultPort99999", []string{"https://vault.sliccy.ai:99999"}, originFlagRefusal("https://vault.sliccy.ai:99999")},
		{"FileZonePort", []string{"file://lease-pilot.sliccy.ai:443"}, originFlagRefusal("file://lease-pilot.sliccy.ai:443")},
		{"WSPort99999", []string{"ws://lease-pilot.sliccy.ai:99999"}, originFlagRefusal("ws://lease-pilot.sliccy.ai:99999")},
		{"WSSUnparseableZone", []string{"wss://xn--1ug.sliccy.ai"}, originFlagRefusal("wss://xn--1ug.sliccy.ai")},
		{"WSSUnparseableZoneSep", []string{"wss://xn--1ug.sliccy\u3002ai"}, originFlagRefusal("wss://xn--1ug.sliccy\u3002ai")},
		{"WSSApexIdeographicDot", []string{"wss://sliccy\u3002ai"}, originFlagRefusal("wss://sliccy\u3002ai")},
		{"FileUnparseableZone", []string{"file://xn--1ug.sliccy.ai:443"}, originFlagRefusal("file://xn--1ug.sliccy.ai:443")},
		{"EmptyHostWS", []string{"ws://:443"}, originFlagRefusal("ws://:443")},
		{"SecondExtra", []string{"http://localhost:3000", "https://vault.sliccy.ai"}, originFlagRefusal("https://vault.sliccy.ai")},
		{"Userinfo", []string{"https://user:pass@lease-pilot.sliccy.ai"}, `signal: "https://user:pass@lease-pilot.sliccy.ai" is not an origin (scheme://host[:port])`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := signal.NewOrigins(c.in)
			if err == nil || err.Error() != c.want || got != nil {
				t.Fatalf("NewOrigins(%q) = %v, %v; want %q", c.in, got, err, c.want)
			}
		})
	}
}

func TestOriginFlagStillAccepts(t *testing.T) {
	for _, c := range []struct {
		name string
		in   []string
	}{
		{"Seven", []string{"https://seven.sliccy.ai"}},
		{"WWW", []string{"https://www.sliccy.ai"}},
		{"Localhost", []string{"http://localhost:3000"}},
		{"NonSliccy", []string{"https://example.com"}},
		{"ChromeExtension", []string{"chrome-extension://" + originFlagExtID}},
		{"FileLocalhost", []string{"file://localhost"}},
		{"Devtools", []string{"devtools://devtools"}},
		{"DevtoolsOpaqueHost", []string{"devtools://host.123"}},
		{"WSNumericLabel", []string{"ws://1.2.3.4"}},
		{"WSNonZone", []string{"ws://example.com"}},
		{"Seven443", []string{"https://seven.sliccy.ai:443"}},
		{"SevenUpper", []string{"https://SEVEN.SLICCY.AI"}},
		{"PlacementMarker", []string{"chrome-extension://" + originFlagExtID}},
		{"OwnerAdmission", []string{"chrome-extension://" + originFlagExtID}},
		{"BiosLiveCheck", []string{"chrome-extension://" + originFlagExtID}},
		{"EvaluateResultUnmarked", []string{"chrome-extension://" + originFlagExtID}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := signal.NewOrigins(c.in)
			if err != nil || got == nil {
				t.Fatalf("NewOrigins(%q) = %v, %v", c.in, got, err)
			}
			if !got.Allowed(browser.NormalizeOrigin(c.in[0])) {
				t.Fatalf("NewOrigins(%q) did not admit the origin", c.in)
			}
		})
	}
}

func TestOriginFlagAdmitsAccepted(t *testing.T) {
	in := []string{"http://localhost:3000", "https://example.com", "ws://example.com", "chrome-extension://" + originFlagExtID}
	o, err := signal.NewOrigins(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range in {
		origin := browser.NormalizeOrigin(raw)
		if !o.Allowed(origin) {
			t.Fatalf("Allowed(%q) = false after NewOrigins accepted it", origin)
		}
	}
}

func TestPageFlagRefusalNamesPageNotOrigin(t *testing.T) {
	_, err := PageOrigins("https://vault.sliccy.ai/", nil)
	if err == nil || err.Error() != pageFlagRefusal("https://vault.sliccy.ai") {
		t.Fatalf("got %v, want the --page refusal", err)
	}
	_, err = PageOrigins("https://seven.sliccy.ai/", []string{"https://vault.sliccy.ai"})
	if err == nil || err.Error() != originFlagRefusal("https://vault.sliccy.ai") {
		t.Fatalf("got %v, want the --origin refusal from NewOrigins", err)
	}
}
