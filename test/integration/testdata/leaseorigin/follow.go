package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/ctl"
	"github.com/ai-ecoverse/slicc-cli/internal/node"
	"github.com/ai-ecoverse/slicc-cli/internal/policy"
	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

type originHub struct {
	srv     *httptest.Server
	mu      sync.Mutex
	next    map[string]string
	expired map[string]bool
	counts  map[string]int
	attach  int
	hold    chan struct{}
	polled  chan struct{}
	n       int
}

func (h *originHub) holdPolls() (func(), <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hold, h.polled = make(chan struct{}), make(chan struct{}, 1)
	var once sync.Once
	hold := h.hold
	return func() { once.Do(func() { close(hold) }) }, h.polled
}

func (h *originHub) attaches() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.attach
}

func newOriginHub(t *testing.T, host string) *originHub {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &originHub{next: map[string]string{}, expired: map[string]bool{}, counts: map[string]int{}}
	h.srv = &httptest.Server{Listener: ln, Config: &http.Server{Handler: h}}
	h.srv.Start()
	t.Cleanup(h.srv.Close)
	if host != "127.0.0.1" {
		h.srv.URL = strings.Replace(h.srv.URL, "127.0.0.1", host, 1)
	}
	return h
}

func (h *originHub) join(token string) string { return h.srv.URL + "/join/" + token }

func (h *originHub) moves(token, next string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next[token] = next
}

func (h *originHub) expire(token string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expired[token] = true
}

func (h *originHub) total() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.counts {
		n += c
	}
	return n
}

func (h *originHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	action, _ := body["action"].(string)
	token := strings.TrimPrefix(r.URL.Path, "/join/")
	h.mu.Lock()
	h.counts[token]++
	h.n++
	id := fmt.Sprintf("boot-%d", h.n)
	next, moved := h.next[token]
	expired := h.expired[token]
	if action == "" {
		h.attach++
	}
	hold, polled := h.hold, h.polled
	h.mu.Unlock()
	if hold != nil && action == "poll" {
		select {
		case polled <- struct{}{}:
		default:
		}
		select {
		case <-hold:
		case <-r.Context().Done():
			return
		}
		writeOrigin(w, http.StatusOK, map[string]any{"role": "follower", "bootstrap": map[string]any{"bootstrapId": "boot-held", "attempt": 1, "state": "waiting", "cursor": 0}, "events": []any{}})
		return
	}
	out := map[string]any{}
	status := http.StatusOK
	boot := map[string]any{"bootstrapId": id, "attempt": 1, "state": "waiting", "cursor": 0, "retriesRemaining": 1}
	switch {
	case expired:
		status = http.StatusGone
		out = map[string]any{"code": tray.CodeTrayExpired, "error": "expired"}
	case moved:
		status = http.StatusConflict
		out = map[string]any{"role": "follower", "result": map[string]any{"action": "fail", "code": tray.CodeSuperseded, "error": "moved", "joinUrl": next}}
	case action == "poll":
		out = map[string]any{"role": "follower", "bootstrap": boot, "events": []map[string]any{{"sequence": 1, "type": "bootstrap.offer", "offer": map[string]any{"type": "offer", "sdp": "v=0\r\n"}}}}
	default:
		out = map[string]any{"role": "follower", "trust": "full", "result": map[string]any{"action": "signal", "code": "LEADER_CONNECTED", "bootstrap": boot}}
	}
	writeOrigin(w, status, out)
}

func writeOrigin(w http.ResponseWriter, status int, out any) {
	payload, _ := json.Marshal(out)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(payload)
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func originHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	rt, err := os.MkdirTemp("/tmp", "sl-org-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(rt) })
	t.Setenv("XDG_RUNTIME_DIR", rt)
	t.Setenv("TMPDIR", rt)
	path, err := tray.DefaultStatePath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func remembered(t *testing.T, path, typed, want string) {
	t.Helper()
	if got := tray.OpenState(path).Current(typed); got != want {
		t.Fatalf("the ordinary join state remembers %q, want the successor", got)
	}
}

func TestLeaseFollowerStaysOnTheReviewedOrigin(t *testing.T) {
	t.Run("SupervisorStopsOnRefusal", func(t *testing.T) {
		for _, order := range []string{"EvFailedFirst", "EvDownFirst", "EvDownAfterUp", "ServeNilFirst"} {
			t.Run(order, func(t *testing.T) { supervisorStops(t, order) })
		}
	})
	t.Run("NoSuccessorURLInOutput", func(t *testing.T) {
		originHome(t)
		reviewed := newOriginHub(t, "127.0.0.1")
		foreign := newOriginHub(t, "localhost")
		successor := foreign.join("farhub.s3cr3tfar")
		typed := reviewed.join("typed.s3cr3ttyped")
		reviewed.moves("typed.s3cr3ttyped", successor)
		var debug lockedBuffer
		cfg := followerConfig(typed, followFlags{leaseBrowser: "http://127.0.0.1:9"}, nil, func(format string, a ...any) { fmt.Fprintf(&debug, format+"\n", a...) })
		if cfg.PinnedJoinURL != typed || cfg.State != nil {
			t.Fatal("the lease-only follower is not pinned to the typed join URL")
		}
		f, err := tray.NewFollower(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var stderr lockedBuffer
		r := &followRun{f: f, joinURL: typed, hub: tray.HubOrigin(typed), events: make(chan followEvent, 16), ctl: ctl.NewHolder("follow", version)}
		r.status = func(format string, a ...any) { fmt.Fprintf(&stderr, "slicc: "+format+"\n", a...) }
		done := make(chan int, 1)
		go func() { done <- r.run(t.Context()) }()
		var code int
		select {
		case code = <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("the refused follower did not stop; reviewed hub requests=%d", reviewed.total())
		}
		snap, _ := json.Marshal(r.ctl.Snapshot())
		out := stderr.String() + "\n" + debug.String() + "\n" + string(snap)
		if code != 1 || !strings.Contains(stderr.String(), tray.CodeSupersededForeignOrigin) || r.ctl.Snapshot().Code != tray.CodeSupersededForeignOrigin || r.ctl.Snapshot().State != ctl.StateStopped {
			t.Fatalf("exit %d, output:\n%s", code, out)
		}
		for _, secret := range []string{successor, "farhub", "s3cr3tfar", strings.TrimPrefix(foreign.srv.URL, "http://")} {
			if strings.Contains(out, secret) {
				t.Fatalf("the output names the refused successor (%q):\n%s", secret, out)
			}
		}
		if foreign.total() != 0 || reviewed.total() != 1 {
			t.Fatalf("hub requests reviewed=%d foreign=%d", reviewed.total(), foreign.total())
		}
	})
}

func supervisorStops(t *testing.T, order string) {
	originHome(t)
	reviewed := newOriginHub(t, "127.0.0.1")
	release, polled := reviewed.holdPolls()
	t.Cleanup(release)
	typed := reviewed.join("typed.s3cr3ttyped")
	f, err := tray.NewFollower(followerConfig(typed, followFlags{leaseBrowser: "http://127.0.0.1:9"}, nil, func(string, ...any) {}))
	if err != nil {
		t.Fatal(err)
	}
	var stderr lockedBuffer
	r := &followRun{f: f, joinURL: typed, hub: tray.HubOrigin(typed), events: make(chan followEvent, 16), ctl: ctl.NewHolder("follow", version), node: node.New(node.Config{Version: version, Mode: "remote"})}
	foreign := "http://localhost:1/join/farhub.s3cr3tfar"
	var atBackoff error
	backoff := false
	r.status = func(format string, a ...any) {
		fmt.Fprintf(&stderr, "slicc: "+format+"\n", a...)
		if order == "ServeNilFirst" && !backoff && strings.HasPrefix(format, "reconnecting in") {
			backoff = true
			atBackoff = r.f.Rejoin(foreign)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- r.run(ctx) }()
	select {
	case <-polled:
	case <-time.After(10 * time.Second):
		t.Fatal("the follower never polled the reviewed hub")
	}
	conn := r.currentConn()
	refuse := func() {
		if err := r.f.Rejoin(foreign); tray.ErrorCode(err) != tray.CodeSupersededForeignOrigin {
			t.Fatalf("the refusal was not recorded: %v", err)
		}
	}
	generic := errors.New("lease peer did not establish an exclusive link")
	switch order {
	case "EvFailedFirst":
		refuse()
		r.emit(followEvent{kind: evFailed, conn: conn, nonce: conn.Session().BootstrapID(), err: generic})
	case "EvDownFirst":
		refuse()
		r.emit(followEvent{kind: evDown, conn: conn})
	case "EvDownAfterUp":
		refuse()
		r.emit(followEvent{kind: evUp, conn: conn, name: "paired lease peer"})
		r.emit(followEvent{kind: evDown, conn: conn})
	case "ServeNilFirst":
		_ = conn.Session().Close()
		release()
	}
	var code int
	select {
	case code = <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("the refused follower kept running: attaches=%d\n%s", reviewed.attaches(), stderr.String())
	}
	if order == "ServeNilFirst" && (!backoff || tray.ErrorCode(atBackoff) != tray.CodeSupersededForeignOrigin) {
		t.Fatalf("the refusal was not recorded at the backoff boundary: %v", atBackoff)
	}
	snap := r.ctl.Snapshot()
	if code != 1 || snap.Code != tray.CodeSupersededForeignOrigin || snap.State != ctl.StateStopped || reviewed.attaches() != 1 {
		t.Fatalf("exit %d code %q state %q attaches %d\n%s", code, snap.Code, snap.State, reviewed.attaches(), stderr.String())
	}
	if order != "ServeNilFirst" && strings.Contains(stderr.String(), "reconnecting") {
		t.Fatalf("the supervisor scheduled a reconnect after the refusal:\n%s", stderr.String())
	}
}

func TestOrdinaryFollowUnchanged(t *testing.T) {
	setup := func(t *testing.T, expired bool) (string, string, string, *originHub) {
		path := originHome(t)
		reviewed := newOriginHub(t, "127.0.0.1")
		foreign := newOriginHub(t, "localhost")
		successor := foreign.join("farhub.s3cr3tfar")
		reviewed.moves("typed.s3cr3ttyped", successor)
		if expired {
			foreign.expire("farhub.s3cr3tfar")
		}
		return path, reviewed.join("typed.s3cr3ttyped"), successor, foreign
	}
	t.Run("ForeignSuccessorStillFollowedAndRemembered", func(t *testing.T) {
		path, typed, successor, foreign := setup(t, true)
		var stderr lockedBuffer
		rt := followRuntime{
			guard: func(context.Context, *policy.Policy, browser.Options, []uint16, []uint16, func(string, ...any)) error {
				return nil
			},
			screen: func(followFlags, *followRun) error { return nil },
		}
		r, err := newFollowRunUsing(typed, followFlags{}, &stopper{}, &stderr, rt)
		if err != nil {
			t.Fatal(err)
		}
		defer r.closeSSH()
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		if _, err := r.f.Dial(ctx); tray.ErrorCode(err) != tray.CodeTrayExpired {
			t.Fatalf("ordinary follow did not reach the successor: %v", err)
		}
		if foreign.total() != 1 || !r.f.Redirected() {
			t.Fatalf("ordinary follow did not follow the successor: %d", foreign.total())
		}
		remembered(t, path, typed, successor)
	})
	t.Run("SignalOnlyUnchanged", func(t *testing.T) {
		path, typed, successor, foreign := setup(t, false)
		var stdout, stderr lockedBuffer
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		if code := signalOnlyRun(ctx, typed, &stdout, &stderr); code != 0 {
			t.Fatalf("exit %d\n%s\n%s", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "join url: superseded, the replacement is remembered") || !strings.Contains(stdout.String(), "offer:") || foreign.total() < 1 {
			t.Fatalf("signal-only did not follow the successor:\n%s", stdout.String())
		}
		remembered(t, path, typed, successor)
	})
	t.Run("AgentLinkUnchanged", func(t *testing.T) {
		path, typed, successor, foreign := setup(t, true)
		quiet := func(string, ...any) {}
		rl, err := newRemoteLinks(typed, quiet, quiet)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		if _, err := rl.f.Dial(ctx); tray.ErrorCode(err) != tray.CodeTrayExpired {
			t.Fatalf("the agent link did not reach the successor: %v", err)
		}
		if foreign.total() != 1 {
			t.Fatalf("the agent link did not follow the successor: %d", foreign.total())
		}
		remembered(t, path, typed, successor)
	})
}
