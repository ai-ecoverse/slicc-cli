package browser

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/protect"
)

type sevenRoleSpelling struct {
	name, url, cookie string
}

var sevenRolePilot = []sevenRoleSpelling{
	{"Exact", "https://lease-pilot.sliccy.ai/", "lease-pilot.sliccy.ai"},
	{"Upper", "https://LEASE-PILOT.SLICCY.AI/", "LEASE-PILOT.SLICCY.AI"},
	{"TrailingDot", "https://lease-pilot.sliccy.ai./", "lease-pilot.sliccy.ai."},
	{"Port443", "https://lease-pilot.sliccy.ai:443/", ".lease-pilot.sliccy.ai"},
	{"SubDomain", "https://a.lease-pilot.sliccy.ai/", "a.lease-pilot.sliccy.ai"},
	{"Port8443", "https://lease-pilot.sliccy.ai:8443/", "lease-pilot.sliccy.ai"},
	{"HTTP", "http://lease-pilot.sliccy.ai/", "lease-pilot.sliccy.ai"},
}

func TestSevenRolePilotHost(t *testing.T) {
	o := protect.MustNew(nil)
	for _, s := range sevenRolePilot {
		t.Run(s.name, func(t *testing.T) {
			t.Run("Protected", func(t *testing.T) {
				if !o.Loaded(s.url) {
					t.Errorf("Loaded(%q) = false, want protected", s.url)
				}
				if !o.URL(s.url) {
					t.Errorf("URL(%q) = false, want protected", s.url)
				}
				if !o.Target(s.url, false, protect.NoOpener) {
					t.Errorf("Target(%q) = false, want protected", s.url)
				}
				if !o.CookieDomain(s.cookie) {
					t.Errorf("CookieDomain(%q) = false, want protected", s.cookie)
				}
				key := strings.TrimSuffix(s.url, "/")
				if !o.StorageKey(key) {
					t.Errorf("StorageKey(%q) = false, want protected", key)
				}
				if part := "https://example.com/^0" + key; !o.StorageKey(part) {
					t.Errorf("StorageKey(%q) = false, want protected", part)
				}
			})
			t.Run("OrdinarySeven", func(t *testing.T) {
				if origin := OriginOf(s.url); IsSevenOrigin(origin, nil) {
					t.Errorf("IsSevenOrigin(OriginOf(%q) = %q) = true, want not a seven origin", s.url, origin)
				}
				if raw := strings.TrimSuffix(s.url, "/"); IsSevenOrigin(raw, nil) {
					t.Errorf("IsSevenOrigin(%q) = true, want not a seven origin", raw)
				}
			})
		})
	}
}

func TestSevenRoleControls(t *testing.T) {
	for _, c := range []struct {
		label string
		seven bool
	}{
		{"seven", true},
		{"www", true},
		{"lease", true},
		{"lease-pilot-x", true},
		{"x-lease-pilot", true},
		{"vault", false},
		{"vault-preview", false},
		{"vault-main", false},
		{"echo", false},
	} {
		t.Run(c.label, func(t *testing.T) {
			raw := "https://" + c.label + ".sliccy.ai/"
			if got := IsSevenOrigin(OriginOf(raw), nil); got != c.seven {
				t.Errorf("IsSevenOrigin(OriginOf(%q)) = %v, want %v", raw, got, c.seven)
			}
			if got := catalog.Seven.IsSevenLabel(c.label); got != c.seven {
				t.Errorf("IsSevenLabel(%q) = %v, want %v", c.label, got, c.seven)
			}
		})
	}
}

type sevenRoleBrowser struct {
	list string
}

func (b sevenRoleBrowser) RoundTrip(r *http.Request) (*http.Response, error) {
	body := ""
	switch r.URL.Path {
	case "/json/version":
		body = `{"Browser":"Chrome/140.0.7339.0","webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/sevenrole"}`
	case "/json/list":
		body = b.list
	default:
		return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}

func sevenRoleTabs(t *testing.T, list string) []string {
	t.Helper()
	ep, err := ProbeEndpoint(context.Background(), "http://127.0.0.1:9222", Options{Transport: sevenRoleBrowser{list: list}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tab := range ep.SevenTabs {
		ids = append(ids, tab.ID)
	}
	if ep.SevenOpen != (len(ids) > 0) {
		t.Errorf("SevenOpen = %v with tabs %v", ep.SevenOpen, ids)
	}
	return ids
}

func TestSevenRoleTabSelection(t *testing.T) {
	pilot := `{"id":"PILOT","type":"page","url":"https://lease-pilot.sliccy.ai/","title":"pilot"}`
	seven := `{"id":"SEVEN","type":"page","url":"https://seven.sliccy.ai/","title":"seven"}`
	t.Run("PilotAndSeven", func(t *testing.T) {
		if got := sevenRoleTabs(t, "["+pilot+","+seven+"]"); len(got) != 1 || got[0] != "SEVEN" {
			t.Errorf("seven tabs = %v, want only [SEVEN]", got)
		}
	})
	t.Run("PilotOnly", func(t *testing.T) {
		if got := sevenRoleTabs(t, "["+pilot+"]"); len(got) != 0 {
			t.Errorf("seven tabs = %v, want none", got)
		}
	})
	t.Run("SevenOnly", func(t *testing.T) {
		if got := sevenRoleTabs(t, "["+seven+"]"); len(got) != 1 || got[0] != "SEVEN" {
			t.Errorf("seven tabs = %v, want [SEVEN]", got)
		}
	})
}
