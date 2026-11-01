package integration

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func unpairJSON(t *testing.T, env []string, args ...string) (map[string]any, string) {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"unpair", "--json"}, args...)...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("unpair %v: %v\n%s", args, err, stderr.String())
	}
	if strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("unpair %v: stdout %q", args, stdout.String())
	}
	var m map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &m); err != nil || m["schema"] != float64(1) {
		t.Fatalf("unpair %v: %q", args, stdout.String())
	}
	return m, stderr.String()
}

func normalizePeers(m map[string]any, fp string) map[string]any {
	swap := func(v any) any {
		if v == fp {
			return "<fingerprint>"
		}
		return v
	}
	for _, key := range []string{"removed", "closed"} {
		if list, ok := m[key].([]any); ok {
			for i := range list {
				list[i] = swap(list[i])
			}
		}
	}
	if list, ok := m["peers"].([]any); ok {
		for _, item := range list {
			p := item.(map[string]any)
			p["fingerprint"], p["origin"], p["node"], p["firstSeen"] = swap(p["fingerprint"]), "<origin>", "<node>", "<time>"
		}
	}
	return m
}

func TestUnpairJSONListsPeersAndClosesTheLiveLink(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	e := newLoopEnv(t)
	f := startFlow(t, e.env, "pair", "--json", "--no-kernel", "--page", srv.URL+"/index.html?ports="+e.ports())
	pe := f.next(t, "pairing")
	var pairing struct {
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(pe.raw), &pairing) != nil {
		t.Fatal(pe.raw)
	}
	p := b.openPage(t, pairing.URL)
	if r := confirm(t, p, ""); !r.Linked {
		t.Fatalf("pairing: %+v", r)
	}
	f.next(t, "connecting")
	f.next(t, "linked")
	var fp string
	p.eval("linkTest.loopback.fingerprint()", &fp)
	var log bytes.Buffer
	enc := json.NewEncoder(&log)
	enc.SetEscapeHTML(false)

	list, _ := unpairJSON(t, e.env)
	peers, _ := list["peers"].([]any)
	if len(peers) != 1 {
		t.Fatalf("peers %v", list)
	}
	peer := peers[0].(map[string]any)
	if peer["fingerprint"] != fp || peer["live"] != true || peer["origin"] != srv.URL || peer["node"] == "" || peer["firstSeen"] == "" {
		t.Fatalf("peer %v, page %s", peer, fp)
	}
	_ = enc.Encode(normalizePeers(list, fp))

	removed, stderr := unpairJSON(t, e.env, fp[:12])
	if r, _ := removed["removed"].([]any); len(r) != 1 || r[0] != fp {
		t.Fatalf("removed %v", removed)
	}
	if c, _ := removed["closed"].([]any); len(c) != 1 || c[0] != fp {
		t.Fatalf("closed %v; stderr %s", removed, stderr)
	}
	_ = enc.Encode(normalizePeers(removed, fp))
	if lost := f.next(t, "link-lost"); !strings.Contains(lost.raw, fp) {
		t.Fatalf("link-lost %s", lost.raw)
	}
	p.waitFor("!linkTest.status().up", 20*time.Second)

	after, _ := unpairJSON(t, e.env)
	if l, _ := after["peers"].([]any); len(l) != 0 {
		t.Fatalf("peers after unpair %v", after)
	}
	_ = enc.Encode(after)
	none, _ := unpairJSON(t, e.env, "--all")
	if r, _ := none["removed"].([]any); len(r) != 0 {
		t.Fatalf("--all %v", none)
	}
	_ = enc.Encode(none)
	nativeFixture(t, "unpair.ndjson", log.Bytes())
}
