package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var nativePlaceholders = map[string]string{
	"url":     "<pairing-url>",
	"device":  "<fingerprint>",
	"tab":     "<origin>",
	"retryAt": "<time>",
	"wait":    "<ms>",
}

func normalizeEvent(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("not JSON: %q", raw)
	}
	for k, v := range nativePlaceholders {
		if _, ok := m[k]; ok {
			m[k] = v
		}
	}
	if res, ok := m["resources"].(map[string]any); ok {
		for _, key := range []string{"mounts", "rejectedMounts"} {
			list, _ := res[key].([]any)
			for _, item := range list {
				if mm, ok := item.(map[string]any); ok {
					mm["path"] = "<" + filepath.Base(fmt.Sprint(mm["path"])) + ">"
				}
			}
		}
		list, _ := res["browsers"].([]any)
		for _, item := range list {
			if b, ok := item.(map[string]any); ok {
				b["name"] = "<name>"
			}
		}
	}
	return m
}

func nativeEvents(t *testing.T, f *flowProc) []byte {
	t.Helper()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	for _, e := range f.seen {
		if e.Event == "not-json" {
			t.Fatalf("stdout carried a line that isn't JSON: %q", e.raw)
		}
		if err := enc.Encode(normalizeEvent(t, e.raw)); err != nil {
			t.Fatal(err)
		}
	}
	return out.Bytes()
}

func (f *flowProc) rest(t *testing.T) int {
	t.Helper()
	for {
		select {
		case e, ok := <-f.events:
			if !ok {
				return f.wait(t)
			}
			f.seen = append(f.seen, e)
		case <-time.After(45 * time.Second):
			t.Fatalf("slicc did not exit; events %v", f.kinds())
		}
	}
}

func lastIsClosed(t *testing.T, f *flowProc) {
	t.Helper()
	if len(f.seen) == 0 || f.seen[len(f.seen)-1].Event != "closed" {
		t.Fatalf("the last event isn't closed: %v", f.kinds())
	}
	if n := slices.Index(f.kinds(), "closed"); n != len(f.seen)-1 {
		t.Fatalf("closed came before the end: %v", f.kinds())
	}
}

type linkedResources struct {
	Mode      string `json:"mode"`
	Resources *struct {
		Browsers []struct {
			ID    string `json:"id"`
			Role  string `json:"role"`
			Owned bool   `json:"owned"`
		} `json:"browsers"`
		Mounts []struct {
			Name     string `json:"name"`
			Path     string `json:"path"`
			ReadOnly bool   `json:"readOnly"`
		} `json:"mounts"`
		RejectedMounts []struct {
			Path   string `json:"path"`
			Reason string `json:"reason"`
		} `json:"rejectedMounts"`
		Network struct {
			Exit   bool   `json:"exit"`
			Policy string `json:"policy"`
		} `json:"network"`
		KernelPort int `json:"kernelPort"`
	} `json:"resources"`
}

func mountDirs(t *testing.T) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real, filepath.Join(t.TempDir(), "missing")
}

func checkResources(t *testing.T, raw, mode, mount, missing string, browsers int) {
	t.Helper()
	var l linkedResources
	if err := json.Unmarshal([]byte(raw), &l); err != nil || l.Resources == nil || l.Mode != mode {
		t.Fatalf("linked %s", raw)
	}
	r := l.Resources
	if len(r.Mounts) != 1 || r.Mounts[0].Name != "proj" || r.Mounts[0].Path != mount || !r.Mounts[0].ReadOnly {
		t.Fatalf("mounts in %s", raw)
	}
	if len(r.RejectedMounts) != 1 || r.RejectedMounts[0].Path != missing || r.RejectedMounts[0].Reason != "not-a-folder" {
		t.Fatalf("rejected mounts in %s", raw)
	}
	if !r.Network.Exit || r.Network.Policy == "" || len(r.Browsers) != browsers {
		t.Fatalf("resources in %s", raw)
	}
	for _, b := range r.Browsers {
		if b.Owned || b.Role == "" {
			t.Fatalf("browsers in %s", raw)
		}
	}
}

func TestAttachJSONReportsTheLinkLifecycle(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	mount, missing := mountDirs(t)
	f := startFlow(t, os.Environ(), "attach", "--json", "--cdp", b.endpoint, "--origin", srv.URL, "--name", "it-node", "--no-kernel", "--mount", mount+":proj:ro", "--mount", missing)
	if e := f.next(t, "connecting"); !strings.Contains(e.raw, `"mode":"cdp"`) {
		t.Fatalf("connecting %s", e.raw)
	}
	f.waitStderr(t, "waiting for a seven tab")
	b.openPage(t, srv.URL+"/index.html")
	f.next(t, "connecting")
	le := f.next(t, "linked")
	checkResources(t, le.raw, "cdp", mount, missing, 1)
	if strings.Contains(le.raw, "/devtools/") || strings.Contains(le.raw, strconv.Itoa(b.port)) {
		t.Fatalf("linked names the CDP endpoint: %s", le.raw)
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.rest(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lastIsClosed(t, f)
	nativeFixture(t, "attach.ndjson", nativeEvents(t, f))
}

func TestAttachJSONReportsAnOfferItCannotAnswer(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	f := startFlow(t, os.Environ(), "attach", "--json", "--cdp", b.endpoint, "--origin", srv.URL, "--no-kernel")
	f.next(t, "connecting")
	p := b.openPage(t, srv.URL+"/index.html")
	f.next(t, "connecting")
	f.next(t, "linked")
	var st struct {
		Nonce string `json:"nonce"`
	}
	p.eval("linkTest.status()", &st)
	var n any
	p.eval(`globalThis.sliccLinkSend(JSON.stringify({t: 'offer', nonce: `+jsArgs(st.Nonce)+`, sdp: 'not an sdp'}))`, &n)
	for {
		e := f.nextAny(t)
		if e.Event != "error" {
			continue
		}
		var ev struct {
			Message   string `json:"message"`
			Retryable *bool  `json:"retryable"`
			Mode      string `json:"mode"`
		}
		if json.Unmarshal([]byte(e.raw), &ev) != nil || ev.Message == "" || ev.Retryable == nil || !*ev.Retryable || ev.Mode != "cdp" {
			t.Fatalf("error %s", e.raw)
		}
		break
	}
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.rest(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lastIsClosed(t, f)
}

func TestAttachJSONRefusesStdioSignalling(t *testing.T) {
	_, errOut, code := slicc(t, "attach", "--json", "--signal", "stdio")
	if code != 2 || !strings.Contains(errOut, "--json and --signal stdio don't go together") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}

func TestAttachJSONEndsAFailedSetupWithErrorAndClosed(t *testing.T) {
	f := startFlow(t, os.Environ(), "attach", "--json", "--cdp", closedAddr(t), "--no-kernel")
	if code := f.rest(t); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if got := f.kinds(); !slices.Equal(got, []string{"connecting", "error", "closed"}) {
		t.Fatalf("events %v", got)
	}
	if !strings.Contains(f.seen[1].raw, `"retryable":false`) || !strings.Contains(f.stderr.String(), "slicc: ") {
		t.Fatalf("error %s, stderr %s", f.seen[1].raw, f.stderr.String())
	}
}

func TestPairJSONReportsThePairingAndTheLink(t *testing.T) {
	b := launchBrowser(t)
	srv := pageServer(t)
	e := newLoopEnv(t)
	mount, missing := mountDirs(t)
	f := startFlow(t, e.env, "pair", "--json", "--page", srv.URL+"/index.html?ports="+e.ports(), "--name", "it-pair", "--no-kernel", "--mount", mount+":proj:ro", "--mount", missing)
	pe := f.next(t, "pairing")
	var pairing struct {
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(pe.raw), &pairing) != nil || !pairingURL.MatchString(pairing.URL) {
		t.Fatalf("pairing %s", pe.raw)
	}
	p := b.openPage(t, pairing.URL)
	if r := confirm(t, p, ""); !r.Linked {
		t.Fatalf("pairing: %+v", r)
	}
	if c := f.next(t, "connecting"); !strings.Contains(c.raw, `"mode":"loopback"`) {
		t.Fatalf("connecting %s", c.raw)
	}
	le := f.next(t, "linked")
	checkResources(t, le.raw, "loopback", mount, missing, 0)
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.rest(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lastIsClosed(t, f)
	nativeFixture(t, "pair.ndjson", nativeEvents(t, f))
}

func TestOpenJSONEndsAFailedSetupWithErrorAndClosed(t *testing.T) {
	e := newLoopEnv(t)
	ln, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(e.first))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	env := overrideEnv(e.env, map[string]string{"SLICC_LOOPBACK_PORTS": fmt.Sprintf("%d-%d", e.first, e.first)})
	f := startFlow(t, env, "open", "--json", "--no-kernel")
	if code := f.rest(t); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if got := f.kinds(); !slices.Equal(got, []string{"error", "closed"}) || !strings.Contains(f.seen[0].raw, `"retryable":false`) || !strings.Contains(f.stderr.String(), "no free port") {
		t.Fatalf("events %v %v, stderr %s", got, f.seen, f.stderr.String())
	}
}

func TestFollowJSONReportsTheLinkLifecycle(t *testing.T) {
	env, h, trays, p, _ := followSetup(t, 1)
	mount, missing := mountDirs(t)
	join := h.joinURL(trays[0])
	f := startFlow(t, env, join, "follow", "--json", "--exit", "--mount", mount+":proj:ro", "--mount", missing)
	if c := f.next(t, "connecting"); !strings.Contains(c.raw, `"mode":"tray"`) || strings.Contains(c.raw, "attempt") {
		t.Fatalf("connecting %s", c.raw)
	}
	le := f.next(t, "linked")
	checkResources(t, le.raw, "tray", mount, missing, 0)
	first := p.waitUp(t, "")
	var dropped map[string]any
	p.eval("linkTest.drop()", &dropped)
	f.next(t, "link-lost")
	re := f.next(t, "reconnecting")
	var rec struct {
		Attempt int       `json:"attempt"`
		RetryAt time.Time `json:"retryAt"`
		Wait    int64     `json:"wait"`
	}
	if json.Unmarshal([]byte(re.raw), &rec) != nil || rec.Attempt != 1 || rec.RetryAt.IsZero() || rec.Wait <= 0 {
		t.Fatalf("reconnecting %s", re.raw)
	}
	if c := f.next(t, "connecting"); !strings.Contains(c.raw, `"attempt":1`) {
		t.Fatalf("connecting %s", c.raw)
	}
	f.next(t, "linked")
	p.waitUp(t, first.Nonce)
	if err := interrupt(f.cmd); err != nil {
		t.Fatal(err)
	}
	if code := f.rest(t); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lastIsClosed(t, f)
	var all strings.Builder
	for _, e := range f.seen {
		all.WriteString(e.raw)
	}
	assertNoSecret(t, all.String(), join)
	nativeFixture(t, "follow.ndjson", nativeEvents(t, f))
}

func TestFollowJSONReportsATerminalFailure(t *testing.T) {
	env := append(configEnv(t), "SLICC_NO_TELEMETRY=1")
	h := newFakeHub(t)
	expired := h.addTray("expired")
	h.with(func() { expired.Expired = true })
	join := h.joinURL(expired)
	f := startFlow(t, env, join, "follow", "--json")
	if code := f.rest(t); code != 1 {
		t.Fatalf("exit %d", code)
	}
	lastIsClosed(t, f)
	var all strings.Builder
	for _, e := range f.seen {
		all.WriteString(e.raw)
	}
	assertNoSecret(t, all.String(), join)
	if !strings.Contains(f.stderr.String(), "(TRAY_EXPIRED)") {
		t.Fatalf("stderr %s", f.stderr.String())
	}
	nativeFixture(t, "follow-error.ndjson", nativeEvents(t, f))
	if _, errOut, code := slicc(t, join, "follow", "--json", "--signal-only"); code != 2 || !strings.Contains(errOut, "--json and --signal-only don't go together") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
}
