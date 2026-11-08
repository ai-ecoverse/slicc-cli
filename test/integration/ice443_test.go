package integration

import (
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

func TestPrepareICEServersAddsCloudflareTLS443(t *testing.T) {
	in := []tray.ICEServer{{
		URLs:       []string{"turn:turn.cloudflare.com:3478?transport=udp", "turns:turn.cloudflare.com:5349?transport=tcp"},
		Username:   "user",
		Credential: "secret",
	}}
	out, added := tray.PrepareICEServers(in)
	if !added || len(out) != 1 || len(out[0].URLs) != 3 || out[0].URLs[2] != "turns:turn.cloudflare.com:443?transport=tcp" {
		t.Fatalf("got added=%v urls=%v", added, out[0].URLs)
	}
	if out[0].Username != "user" || out[0].Credential != "secret" {
		t.Fatalf("credentials changed: %#v", out[0])
	}
	if len(in[0].URLs) != 2 {
		t.Fatalf("input mutated: %v", in[0].URLs)
	}
	again, added := tray.PrepareICEServers(out)
	if added || len(again[0].URLs) != 3 {
		t.Fatalf("duplicated 443: added=%v urls=%v", added, again[0].URLs)
	}
}

func TestPrepareICEServersLeavesOtherRelaysAlone(t *testing.T) {
	cases := [][]tray.ICEServer{
		{{URLs: []string{"stun:stun.cloudflare.com:3478"}}},
		{{URLs: []string{"turn:turn.cloudflare.com:3478?transport=udp"}, Username: "", Credential: ""}},
		{{URLs: []string{"turn:turn.example.test:3478?transport=udp", "turns:turn.example.test:5349?transport=tcp"}, Username: "user", Credential: "secret"}},
		{{URLs: []string{"turns:turn.cloudflare.com:443?transport=tcp"}, Username: "user", Credential: "secret"}},
		{{URLs: []string{"TURNS:TURN.cloudflare.com:443?transport=tcp"}, Username: "user", Credential: "secret"}},
	}
	for _, in := range cases {
		out, added := tray.PrepareICEServers(in)
		if added || len(out[0].URLs) != len(in[0].URLs) {
			t.Fatalf("changed %v into added=%v %v", in[0].URLs, added, out[0].URLs)
		}
	}
	if out, added := tray.PrepareICEServers(nil); added || out != nil {
		t.Fatalf("empty: %#v %v", out, added)
	}
}

func TestFollowSignalOnlyAddsCloudflareTURNOverTLS443(t *testing.T) {
	env := configEnv(t)
	h := newFakeHub(t)
	tr := h.addTray("cli")
	h.with(func() {
		tr.WaitAttaches = 1
		tr.IceServers = []map[string]any{
			{"urls": []string{"stun:stun.cloudflare.com:3478"}, "username": "", "credential": ""},
			{"urls": []string{"turn:turn.cloudflare.com:3478?transport=udp", "turns:turn.cloudflare.com:5349?transport=tcp"}, "username": "turn-user-secret", "credential": "turn-credential-secret"},
		}
	})
	out, errOut, code := sliccEnv(t, env, h.joinURL(tr), "follow", "--signal-only")
	if code != 0 {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	want := "  turn:turn.cloudflare.com:3478?transport=udp turns:turn.cloudflare.com:5349?transport=tcp turns:turn.cloudflare.com:443?transport=tcp (with credentials)\n"
	if !strings.Contains(out, want) {
		t.Fatalf("stdout lacks %q:\n%s", want, out)
	}
	if strings.Count(out, "turns:turn.cloudflare.com:443?transport=tcp") != 1 {
		t.Fatalf("443 URL repeated:\n%s", out)
	}
	if strings.Contains(out+errOut, "turn-user-secret") || strings.Contains(out+errOut, "turn-credential-secret") {
		t.Fatal("credentials printed")
	}
}
