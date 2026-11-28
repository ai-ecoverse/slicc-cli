package tray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/signal"
)

const (
	pinHub     = "https://hub.example"
	pinForeign = "https://evil.example"
)

type pinRoute struct {
	carrier string
	next    string
}

type pinLate struct {
	kind   string
	alt    string
	hold   chan struct{}
	served chan struct{}
	once   sync.Once
}

func (l *pinLate) release() { l.once.Do(func() { close(l.hold) }) }

type pinWorld struct {
	mu     sync.Mutex
	routes map[string]pinRoute
	late   map[string]*pinLate
	seen   []string
}

func (w *pinWorld) lateFor(t *testing.T, joinURL, kind, alt string) *pinLate {
	l := &pinLate{kind: kind, alt: alt, hold: make(chan struct{}), served: make(chan struct{}, 1)}
	t.Cleanup(l.release)
	w.mu.Lock()
	defer w.mu.Unlock()
	w.late[pinKeyOf(joinURL)] = l
	return l
}

func (l *pinLate) signal() {
	select {
	case l.served <- struct{}{}:
	default:
	}
}

func (l *pinLate) reached(t *testing.T) {
	t.Helper()
	select {
	case <-l.served:
	case <-time.After(10 * time.Second):
		t.Fatal("the follow-up poll never reached the hub")
	}
}

func pinKeyOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Scheme + "://" + u.Host + u.EscapedPath()
}

func pinOriginOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Scheme + "://" + u.Host
}

func newPinWorld(routes map[string]pinRoute) *pinWorld {
	w := &pinWorld{routes: map[string]pinRoute{}, late: map[string]*pinLate{}}
	for k, v := range routes {
		w.routes[pinKeyOf(k)] = v
	}
	return w
}

func (w *pinWorld) requests() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.seen)
}

func (w *pinWorld) to(origin string) int {
	n := 0
	for _, s := range w.requests() {
		if pinOriginOf(s) == origin {
			n++
		}
	}
	return n
}

func (w *pinWorld) RoundTrip(r *http.Request) (*http.Response, error) {
	var body map[string]any
	if r.Body != nil {
		data, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		_ = json.Unmarshal(data, &body)
	}
	action, _ := body["action"].(string)
	key := r.URL.Scheme + "://" + r.URL.Host + r.URL.EscapedPath()
	w.mu.Lock()
	w.seen = append(w.seen, key)
	route, ok := w.routes[key]
	late := w.late[key]
	w.mu.Unlock()
	rec := httptest.NewRecorder()
	if !ok {
		writePinJSON(rec, http.StatusForbidden, map[string]any{"code": CodeInvalidJoin, "error": "unknown tray"})
		return rec.Result(), nil
	}
	if late != nil && late.kind == "wait" && action == "" {
		late.signal()
		writePinJSON(rec, http.StatusOK, map[string]any{"role": "follower", "result": map[string]any{"action": actionWait, "code": "LEADER_NOT_CONNECTED", "retryAfterMs": 30000}})
		return rec.Result(), nil
	}
	if late != nil && (late.kind == "answerhold" && action == "answer" || late.kind == "attachhold" && action == "" || late.kind == "offerhold" && action == "poll") {
		late.signal()
		<-late.hold
		late = nil
	}
	if cursor, _ := body["cursor"].(float64); late != nil && late.kind != "answerhold" && action == "poll" && cursor >= 1 {
		late.signal()
		boot := map[string]any{"bootstrapId": "boot-1", "attempt": 1, "state": "failed", "cursor": 1, "retriesRemaining": 1, "failure": map[string]any{"code": "BOOTSTRAP_TIMEOUT", "message": "timeout", "retryable": true, "retryAfterMs": 30000}}
		switch late.kind {
		case "retry":
			writePinJSON(rec, http.StatusOK, map[string]any{"role": "follower", "bootstrap": boot})
			return rec.Result(), nil
		case "net":
			<-late.hold
			return nil, errors.New("connection reset by peer")
		default:
			<-late.hold
			writePinJSON(rec, http.StatusConflict, map[string]any{"role": "follower", "result": map[string]any{"action": actionFail, "code": CodeSuperseded, "error": "moved", "joinUrl": late.alt}})
			return rec.Result(), nil
		}
	}
	carrier := route.carrier
	if at, ok := map[string]string{"poll": "poll", "answer": "answer", "candidate": "ice-candidate"}[carrier]; ok {
		carrier = ""
		if action == at {
			carrier = "json"
		}
	}
	switch carrier {
	case "":
		boot := map[string]any{"bootstrapId": "boot-1", "attempt": 1, "state": "waiting", "cursor": 0, "retriesRemaining": 1}
		switch action {
		case "poll":
			events := []map[string]any{}
			if cursor, _ := body["cursor"].(float64); cursor < 1 {
				events = append(events, map[string]any{"sequence": 1, "type": eventOffer, "offer": map[string]any{"type": "offer", "sdp": "v=0\r\n"}})
			}
			writePinJSON(rec, http.StatusOK, map[string]any{"role": "follower", "bootstrap": boot, "events": events})
		case "answer", "ice-candidate":
			writePinJSON(rec, http.StatusOK, map[string]any{"role": "follower", "bootstrap": boot})
		default:
			writePinJSON(rec, http.StatusOK, map[string]any{"role": "follower", "trust": "full", "result": map[string]any{"action": actionSignal, "code": "LEADER_CONNECTED", "bootstrap": boot}})
		}
	case "expired":
		writePinJSON(rec, http.StatusGone, map[string]any{"code": CodeTrayExpired, "error": "expired"})
	case "location":
		rec.Header().Set("Location", route.next+"?json=true")
		writePinJSON(rec, http.StatusPermanentRedirect, map[string]any{})
	case "link":
		rec.Header().Add("Link", `<`+route.next+`>; rel="successor-version"`)
		rec.Header().Set("Content-Type", "text/plain")
		rec.WriteHeader(http.StatusConflict)
		_, _ = rec.WriteString("moved")
	case "json":
		writePinJSON(rec, http.StatusConflict, map[string]any{"role": "follower", "result": map[string]any{"action": actionFail, "code": CodeSuperseded, "error": "moved", "joinUrl": route.next}})
	case "manual", "manualbody":
		rec.Header().Add("Link", `<`+pinHub+`/status>; rel="status"; type="application/json"`)
		if carrier == "manual" {
			rec.Header().Add("Link", `<`+route.next+`>; rel="successor-version"`)
		}
		writePinJSON(rec, http.StatusConflict, map[string]any{"role": "follower", "result": map[string]any{"action": actionFail, "code": CodeSuperseded, "error": "moved", "joinUrl": route.next}})
	}
	return rec.Result(), nil
}

func writePinJSON(w http.ResponseWriter, status int, v any) {
	data, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

type pinRun struct {
	w       *pinWorld
	f       *Follower
	state   string
	mu      sync.Mutex
	changed []string
	logs    []string
}

func (p *pinRun) changes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.changed)
}

func (p *pinRun) logText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.logs, "\n")
}

func newPinRun(t *testing.T, typed string, pinned, persisted bool, routes map[string]pinRoute) *pinRun {
	t.Helper()
	p := &pinRun{w: newPinWorld(routes), state: filepath.Join(t.TempDir(), "join.json")}
	cfg := Config{
		JoinURL:        typed,
		HTTPClient:     &http.Client{Transport: p.w},
		RequestTimeout: 2 * time.Second,
		RetryBudget:    2 * time.Second,
		RetryBackoff:   10 * time.Millisecond,
		PollInterval:   10 * time.Millisecond,
		OfferTimeout:   5 * time.Second,
		OnJoinURLChanged: func(u string) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.changed = append(p.changed, u)
		},
		Logf: func(format string, args ...any) {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.logs = append(p.logs, fmt.Sprintf(format, args...))
		},
	}
	if pinned {
		cfg.PinnedJoinURL = typed
	}
	if persisted {
		cfg.State = OpenState(p.state)
	}
	f, err := NewFollower(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p.f = f
	return p
}

func (p *pinRun) dial(t *testing.T) (*Session, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	s, err := p.f.Dial(ctx)
	if s != nil {
		t.Cleanup(func() { _ = s.Close() })
	}
	return s, err
}

func (p *pinRun) stateAbsent(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(p.state); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a join state file was written: %v", err)
	}
}

func waitBlockedIn(t *testing.T, frame string) {
	t.Helper()
	buf := make([]byte, 1<<20)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		for _, g := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(g, frame) && strings.Contains(g, "(*Mutex).Lock") {
				return
			}
		}
	}
	t.Fatalf("no goroutine blocked on a mutex in %s", frame)
}

func pinJoin(origin, token string) string { return origin + "/join/" + token + ".s3cr3t" + token }

func (p *pinRun) refused(t *testing.T, err error, successor string, admitted []string) {
	t.Helper()
	if ErrorCode(err) != CodeSupersededForeignOrigin || IsRetryable(err) {
		t.Fatalf("want a fatal %s, got %v", CodeSupersededForeignOrigin, err)
	}
	if n := p.w.to(pinOriginOf(successor)); n != 0 {
		t.Fatalf("the successor origin received %d requests: %v", n, p.w.requests())
	}
	if got := p.changes(); !slices.Equal(got, admitted) {
		t.Fatalf("OnJoinURLChanged %v, want %v", got, admitted)
	}
	want := p.f.origin
	if len(admitted) > 0 {
		want = admitted[len(admitted)-1]
	}
	if cur := p.f.currentURL(); cur != want {
		t.Fatalf("current join URL moved to %q, want %q", p.f.Redact(cur), p.f.Redact(want))
	}
	if strings.Contains(p.logText(), "superseded during the bootstrap") {
		t.Fatalf("a refused bootstrap supersession was logged as followed:\n%s", p.logText())
	}
	if n := strings.Count(p.logText(), "following redirect"); n != len(admitted) {
		t.Fatalf("%d redirect log lines for %d admitted hops:\n%s", n, len(admitted), p.logText())
	}
	u, _ := url.Parse(successor)
	token := strings.TrimPrefix(u.EscapedPath(), "/join/")
	for _, out := range []string{err.Error(), p.logText()} {
		if strings.Contains(out, successor) || strings.Contains(out, token) || strings.Contains(out, u.Host) {
			t.Fatalf("the refused successor leaked: %q", out)
		}
	}
	if data, err := os.ReadFile(p.state); err == nil && (strings.Contains(string(data), u.Host) || strings.Contains(string(data), token)) {
		t.Fatal("the refused successor was persisted")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func (p *pinRun) followed(t *testing.T, s *Session, err error, chain []string) {
	t.Helper()
	if err != nil || s == nil {
		t.Fatalf("same-origin rotation not followed: %v", err)
	}
	if got := p.w.requests(); !slices.Equal(got, keysOf(chain)) {
		t.Fatalf("requests %v, want %v", got, keysOf(chain))
	}
	if got := p.changes(); !slices.Equal(got, chain[1:]) {
		t.Fatalf("OnJoinURLChanged %v, want %v", got, chain[1:])
	}
	if p.f.currentURL() != chain[len(chain)-1] || !p.f.Redirected() {
		t.Fatal("the follower did not move to the same-origin successor in memory")
	}
	p.stateAbsent(t)
}

func keysOf(urls []string) []string {
	out := make([]string, len(urls))
	for i, u := range urls {
		out[i] = pinKeyOf(u)
	}
	return out
}

func sameChain(n int) []string {
	out := make([]string, n+1)
	for i := range out {
		out[i] = pinJoin(pinHub, "t"+strings.Repeat("0", i+1))
	}
	return out
}

func chainRoutes(chain []string, carrier string) map[string]pinRoute {
	routes := map[string]pinRoute{}
	for i := range len(chain) - 1 {
		routes[chain[i]] = pinRoute{carrier: carrier, next: chain[i+1]}
	}
	routes[chain[len(chain)-1]] = pinRoute{}
	return routes
}

func TestLeaseFollowerStaysOnTheReviewedOrigin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	defaultState, err := DefaultStatePath()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, p := range []string{defaultState, defaultState + ".lease"} {
			if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("a default join state file exists: %s %v", p, err)
			}
		}
	})
	typed := pinJoin(pinHub, "typed")
	foreign := pinJoin(pinForeign, "far")
	for _, row := range []struct{ name, carrier string }{
		{"Location308Foreign", "location"},
		{"LinkSuccessorForeign", "link"},
		{"JSONSupersededForeign", "json"},
		{"Manual409Foreign", "manual manualbody"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			for _, carrier := range strings.Fields(row.carrier) {
				p := newPinRun(t, typed, true, true, map[string]pinRoute{typed: {carrier, foreign}, foreign: {}})
				_, err := p.dial(t)
				p.refused(t, err, foreign, nil)
				p.stateAbsent(t)
				if got := p.w.requests(); !slices.Equal(got, []string{pinKeyOf(typed)}) {
					t.Fatalf("%s: requests %v", carrier, got)
				}
			}
		})
	}
	for _, row := range []struct{ name, carrier string }{
		{"Location308Same", "location"},
		{"LinkSame", "link"},
		{"JSONSame", "json"},
		{"Manual409Same", "manual manualbody"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			for _, carrier := range strings.Fields(row.carrier) {
				chain := sameChain(1)
				p := newPinRun(t, chain[0], true, false, chainRoutes(chain, carrier))
				s, err := p.dial(t)
				p.followed(t, s, err, chain)
			}
		})
	}
	t.Run("ForeignMidChain", func(t *testing.T) {
		t.Parallel()
		chain := sameChain(1)
		after := pinJoin(pinHub, "after")
		routes := chainRoutes(chain, "json")
		routes[chain[1]] = pinRoute{"json", foreign}
		routes[foreign] = pinRoute{"json", after}
		routes[after] = pinRoute{}
		p := newPinRun(t, chain[0], true, true, routes)
		_, err := p.dial(t)
		p.refused(t, err, foreign, chain[1:])
		if p.w.to(pinHub) != 2 {
			t.Fatalf("requests %v", p.w.requests())
		}
	})
	t.Run("ForeignAtEnd", func(t *testing.T) {
		t.Parallel()
		chain := sameChain(MaxRedirects - 1)
		routes := chainRoutes(chain, "location")
		routes[chain[len(chain)-1]] = pinRoute{"location", foreign}
		routes[foreign] = pinRoute{}
		p := newPinRun(t, chain[0], true, true, routes)
		_, err := p.dial(t)
		p.refused(t, err, foreign, chain[1:])
	})
	t.Run("ForeignSixthHop", func(t *testing.T) {
		t.Parallel()
		chain := sameChain(MaxRedirects)
		routes := chainRoutes(chain, "location")
		routes[chain[len(chain)-1]] = pinRoute{"location", foreign}
		routes[foreign] = pinRoute{}
		p := newPinRun(t, chain[0], true, true, routes)
		_, err := p.dial(t)
		p.refused(t, err, foreign, chain[1:])
	})
	t.Run("FiveHopsSameOrigin", func(t *testing.T) {
		t.Parallel()
		chain := sameChain(MaxRedirects)
		p := newPinRun(t, chain[0], true, false, chainRoutes(chain, "location"))
		s, err := p.dial(t)
		p.followed(t, s, err, chain)
	})
	t.Run("SixthHopStillExhausts", func(t *testing.T) {
		t.Parallel()
		chain := sameChain(MaxRedirects + 1)
		p := newPinRun(t, chain[0], true, false, chainRoutes(chain, "location"))
		_, err := p.dial(t)
		if ErrorCode(err) != CodeSupersedeExhausted {
			t.Fatalf("want %s, got %v", CodeSupersedeExhausted, err)
		}
		if got := p.w.requests(); !slices.Equal(got, keysOf(chain[:MaxRedirects+1])) {
			t.Fatalf("requests %v", got)
		}
	})
	for _, row := range []struct{ name, typed, next string }{
		{"PortOnly", "http://127.0.0.1:41000", "http://127.0.0.1:41001"},
		{"SchemeOnly", "http://127.0.0.1:41000", "https://127.0.0.1:41000"},
		{"HttpsToHttpSameHost", "https://localhost:41443", "http://localhost:41443"},
		{"TrailingDotHost", pinHub, "https://hub.example."},
		{"UnicodeHostSpelling", "https://bücher.example", "https://xn--bcher-kva.example"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			from, to := pinJoin(row.typed, "from"), pinJoin(row.next, "to")
			p := newPinRun(t, from, true, true, map[string]pinRoute{from: {"location", to}, to: {}})
			_, err := p.dial(t)
			p.refused(t, err, to, nil)
			p.stateAbsent(t)
			if got := p.w.requests(); !slices.Equal(got, []string{pinKeyOf(from)}) {
				t.Fatalf("requests %v", got)
			}
		})
	}
	t.Run("BootstrapSupersedeForeign", func(t *testing.T) {
		t.Parallel()
		p := newPinRun(t, typed, true, true, map[string]pinRoute{typed: {"poll", foreign}, foreign: {}})
		s, err := p.dial(t)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		_, err = s.Recv(ctx)
		p.refused(t, err, foreign, nil)
	})
	for _, row := range []struct{ name, carrier string }{
		{"AnswerSupersedeForeign", "answer"},
		{"CandidateSupersedeForeign", "candidate"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			p := newPinRun(t, typed, true, true, map[string]pinRoute{typed: {row.carrier, foreign}, foreign: {}})
			p.f.cfg.PollInterval = 30 * time.Second
			s, err := p.dial(t)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			c := NewConn(s)
			offer, err := c.Recv(ctx)
			if err != nil || offer.T != signal.TypeOffer {
				t.Fatalf("no offer: %v", err)
			}
			sent := c.Send(ctx, signal.Message{T: signal.TypeAnswer, Nonce: offer.Nonce, SDP: "v=0\r\n"})
			if row.carrier == "candidate" {
				if sent != nil {
					t.Fatal(sent)
				}
				c.Settle()
				sent = c.Send(ctx, signal.Message{T: signal.TypeCandidate, Nonce: offer.Nonce, Candidate: &signal.Candidate{Candidate: "candidate:1 1 udp 1 127.0.0.1 9 typ host"}})
			}
			if ErrorCode(sent) != CodeSupersededForeignOrigin {
				t.Fatalf("send: %v", sent)
			}
			_, err = c.Recv(ctx)
			p.refused(t, err, foreign, nil)
			before := len(p.w.requests())
			_ = c.Send(ctx, signal.Message{T: signal.TypeAnswer, Nonce: offer.Nonce, SDP: "v=0\r\n"})
			_ = c.Send(ctx, signal.Message{T: signal.TypeCandidate, Nonce: offer.Nonce, Candidate: &signal.Candidate{Candidate: "candidate:2 1 udp 1 127.0.0.1 9 typ host"}})
			time.Sleep(100 * time.Millisecond)
			if after := p.w.requests(); len(after) != before {
				t.Fatalf("the refused session kept talking to the hub: %v", after)
			}
		})
	}
	live := func(t *testing.T, kind string) (*pinRun, *Conn, signal.Message, *pinLate, chan error, context.Context) {
		t.Helper()
		p := newPinRun(t, typed, true, true, map[string]pinRoute{typed: {"answer", foreign}, foreign: {}, pinJoin(pinHub, "allowed"): {}})
		late := p.w.lateFor(t, typed, kind, pinJoin(pinHub, "allowed"))
		s, err := p.dial(t)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		t.Cleanup(cancel)
		c := NewConn(s)
		offer, err := c.Recv(ctx)
		if err != nil || offer.T != signal.TypeOffer {
			t.Fatalf("no offer: %v", err)
		}
		got := make(chan error, 1)
		go func() {
			_, err := c.Recv(ctx)
			got <- err
		}()
		late.reached(t)
		for deadline := time.Now().Add(10 * time.Second); kind == "retry" && !strings.Contains(p.logText(), "retrying in"); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal("the live Recv never reached the bootstrap retry sleep")
			}
		}
		if sent := c.Send(ctx, signal.Message{T: signal.TypeAnswer, Nonce: offer.Nonce, SDP: "v=0\r\n"}); ErrorCode(sent) != CodeSupersededForeignOrigin {
			t.Fatalf("answer: %v", sent)
		}
		return p, c, offer, late, got, ctx
	}
	settle := func(t *testing.T, got chan error) error {
		t.Helper()
		select {
		case err := <-got:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("the live Recv did not return after the refusal")
			return nil
		}
	}
	t.Run("StickyBlocksLaterAllowedMove", func(t *testing.T) {
		t.Parallel()
		p, _, _, late, got, _ := live(t, "same")
		late.release()
		err := settle(t, got)
		p.refused(t, err, foreign, nil)
		if slices.Contains(p.w.requests(), pinKeyOf(pinJoin(pinHub, "allowed"))) {
			t.Fatal("the late allowed move was dialled")
		}
	})
	t.Run("RecvInRetrySleepGetsRefusal", func(t *testing.T) {
		t.Parallel()
		p, _, _, _, got, _ := live(t, "retry")
		err := settle(t, got)
		if errors.Is(err, io.EOF) || errors.Is(err, ErrClosed) {
			t.Fatalf("the sleeping Recv ended with %v", err)
		}
		p.refused(t, err, foreign, nil)
	})
	t.Run("CompetingNetworkErrorLoses", func(t *testing.T) {
		t.Parallel()
		p, _, _, late, got, _ := live(t, "net")
		before := len(p.w.requests())
		late.release()
		err := settle(t, got)
		p.refused(t, err, foreign, nil)
		if after := p.w.requests(); len(after) != before {
			t.Fatalf("the held poll was retried after the refusal: %v", after[before:])
		}
	})
	t.Run("DialSignalAfterRefusal", func(t *testing.T) {
		t.Parallel()
		p := newPinRun(t, typed, true, true, map[string]pinRoute{typed: {}})
		late := p.w.lateFor(t, typed, "attachhold", "")
		type dialed struct {
			s   *Session
			err error
		}
		got := make(chan dialed, 1)
		go func() {
			s, err := p.f.Dial(t.Context())
			got <- dialed{s, err}
		}()
		late.reached(t)
		if err := p.f.Rejoin(foreign); ErrorCode(err) != CodeSupersededForeignOrigin {
			t.Fatalf("Rejoin: %v", err)
		}
		late.release()
		var d dialed
		select {
		case d = <-got:
		case <-time.After(10 * time.Second):
			t.Fatal("Dial did not return")
		}
		if d.s != nil {
			_ = d.s.Close()
		}
		if d.s != nil || ErrorCode(d.err) != CodeSupersededForeignOrigin || len(p.w.requests()) != 1 {
			t.Fatalf("Dial after a late signal: session=%v %v requests %v", d.s != nil, d.err, p.w.requests())
		}
		p.stateAbsent(t)
	})
	t.Run("RecvOfferAfterRefusal", func(t *testing.T) {
		t.Parallel()
		p := newPinRun(t, typed, true, true, map[string]pinRoute{typed: {}})
		late := p.w.lateFor(t, typed, "offerhold", "")
		s, err := p.dial(t)
		if err != nil {
			t.Fatal(err)
		}
		c := NewConn(s)
		got := make(chan error, 1)
		go func() {
			m, err := c.Recv(t.Context())
			if err == nil {
				err = fmt.Errorf("delivered %s", m.T)
			}
			got <- err
		}()
		late.reached(t)
		if err := p.f.Rejoin(foreign); ErrorCode(err) != CodeSupersededForeignOrigin {
			t.Fatalf("Rejoin: %v", err)
		}
		late.release()
		err = settle(t, got)
		s.recvMu.Lock()
		queued := len(s.queue)
		s.recvMu.Unlock()
		if ErrorCode(err) != CodeSupersededForeignOrigin || queued != 0 {
			t.Fatalf("Recv after a late offer: %v queued=%d", err, queued)
		}
	})
	t.Run("AttachWaitStopsOnLatch", func(t *testing.T) {
		t.Parallel()
		p := newPinRun(t, typed, true, false, map[string]pinRoute{typed: {}})
		late := p.w.lateFor(t, typed, "wait", "")
		got := make(chan error, 1)
		go func() {
			_, err := p.f.Dial(t.Context())
			got <- err
		}()
		late.reached(t)
		if err := p.f.Rejoin(foreign); ErrorCode(err) != CodeSupersededForeignOrigin {
			t.Fatalf("Rejoin: %v", err)
		}
		err := settle(t, got)
		if ErrorCode(err) != CodeSupersededForeignOrigin || len(p.w.requests()) != 1 || p.w.to(pinForeign) != 0 {
			t.Fatalf("Dial after the refusal: %v, requests %v", err, p.w.requests())
		}
	})
	t.Run("SettledRejoinWakes", func(t *testing.T) {
		t.Parallel()
		p := newPinRun(t, typed, true, false, map[string]pinRoute{typed: {}})
		s, err := p.dial(t)
		if err != nil {
			t.Fatal(err)
		}
		c := NewConn(s)
		offer, err := c.Recv(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Send(t.Context(), signal.Message{T: signal.TypeAnswer, Nonce: offer.Nonce, SDP: "v=0\r\n"}); err != nil {
			t.Fatal(err)
		}
		c.Settle()
		got := make(chan error, 1)
		go func() {
			_, err := c.Recv(t.Context())
			got <- err
		}()
		if err := p.f.Rejoin(foreign); ErrorCode(err) != CodeSupersededForeignOrigin {
			t.Fatalf("Rejoin: %v", err)
		}
		if err := settle(t, got); ErrorCode(err) != CodeSupersededForeignOrigin {
			t.Fatalf("the settled Recv returned %v", err)
		}
	})
	t.Run("CandidateRacingRefusedAnswer", func(t *testing.T) {
		t.Parallel()
		p := newPinRun(t, typed, true, true, map[string]pinRoute{typed: {"answer", foreign}, foreign: {}})
		late := p.w.lateFor(t, typed, "answerhold", "")
		s, err := p.dial(t)
		if err != nil {
			t.Fatal(err)
		}
		c := NewConn(s)
		offer, err := c.Recv(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		answered, candidate := make(chan error, 1), make(chan error, 1)
		go func() {
			answered <- c.Send(t.Context(), signal.Message{T: signal.TypeAnswer, Nonce: offer.Nonce, SDP: "v=0\r\n"})
		}()
		late.reached(t)
		go func() {
			candidate <- c.Send(t.Context(), signal.Message{T: signal.TypeCandidate, Nonce: offer.Nonce, Candidate: &signal.Candidate{Candidate: "candidate:1 1 udp 1 127.0.0.1 9 typ host"}})
		}()
		waitBlockedIn(t, "(*Session).SendCandidate")
		late.release()
		a, b := settle(t, answered), settle(t, candidate)
		s.trickleMu.Lock()
		queued := len(s.pending)
		s.trickleMu.Unlock()
		if ErrorCode(a) != CodeSupersededForeignOrigin || ErrorCode(b) != CodeSupersededForeignOrigin || queued != 0 {
			t.Fatalf("answer %v candidate %v queued %d", a, b, queued)
		}
	})
	t.Run("AfterRefusalNoCandidateQueued", func(t *testing.T) {
		t.Parallel()
		p := newPinRun(t, typed, true, true, map[string]pinRoute{typed: {"answer", foreign}, foreign: {}})
		s, err := p.dial(t)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		c := NewConn(s)
		offer, err := c.Recv(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if sent := c.Send(ctx, signal.Message{T: signal.TypeAnswer, Nonce: offer.Nonce, SDP: "v=0\r\n"}); ErrorCode(sent) != CodeSupersededForeignOrigin {
			t.Fatalf("answer: %v", sent)
		}
		before := len(p.w.requests())
		sent := c.Send(ctx, signal.Message{T: signal.TypeCandidate, Nonce: offer.Nonce, Candidate: &signal.Candidate{Candidate: "candidate:1 1 udp 1 127.0.0.1 9 typ host"}})
		s.trickleMu.Lock()
		queued := len(s.pending)
		s.trickleMu.Unlock()
		if ErrorCode(sent) != CodeSupersededForeignOrigin || queued != 0 || len(p.w.requests()) != before {
			t.Fatalf("candidate after the refusal: %v queued=%d", sent, queued)
		}
		_, err = c.Recv(ctx)
		p.refused(t, err, foreign, nil)
	})
	t.Run("ZoneIDSpelling", func(t *testing.T) {
		t.Parallel()
		from := pinJoin("https://[fe80::1%25lan0]:8443", "from")
		for _, next := range []string{"https://[fe80::1%25LAN0]:8443", "https://[fe80::1%25Lan0]:8443", "https://[fe80::1]:8443", "https://[fe80::1%25lan1]:8443"} {
			to := pinJoin(next, "to")
			p := newPinRun(t, from, true, true, map[string]pinRoute{from: {"location", to}, to: {}})
			_, err := p.dial(t)
			p.refused(t, err, to, nil)
			p.stateAbsent(t)
		}
		same := pinJoin("https://[fe80::1%25lan0]:8443", "rotated")
		p := newPinRun(t, from, true, false, map[string]pinRoute{from: {"location", same}, same: {}})
		s, err := p.dial(t)
		p.followed(t, s, err, []string{from, same})
		f, err := NewFollower(Config{JoinURL: pinJoin("https://[fe80::1]:8443", "a"), PinnedJoinURL: pinJoin("https://[fe80::1]:8443", "a")})
		if err != nil || f.admit(pinJoin("https://[fe80::1%25lan0]:8443", "b")) == nil {
			t.Fatalf("a zoned successor of an unzoned pin was admitted: %v", err)
		}
	})
	t.Run("RestoredForeignStateIgnored", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "join.json.lease")
		if err := OpenState(path).Save(typed, foreign); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(before), "evil.example") {
			t.Fatalf("seed: %v %s", err, before)
		}
		w := newPinWorld(map[string]pinRoute{typed: {carrier: "expired"}, foreign: {}})
		f, err := NewFollower(Config{JoinURL: typed, PinnedJoinURL: typed, State: OpenState(path), HTTPClient: &http.Client{Transport: w}, RetryBudget: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if f.currentURL() != typed {
			t.Fatal("the restored foreign join URL became current")
		}
		if _, err := f.Dial(t.Context()); ErrorCode(err) != CodeTrayExpired {
			t.Fatalf("want the reviewed hub's answer, got %v", err)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(before) {
			t.Fatalf("the restored state changed: %v", err)
		}
		if got := w.requests(); !slices.Equal(got, []string{pinKeyOf(typed)}) || w.to(pinForeign) != 0 {
			t.Fatalf("requests %v", got)
		}
	})
	t.Run("RejoinForeignRefused", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ typed, next string }{
			{pinJoin(pinHub, "typed"), pinJoin(pinForeign, "far")},
			{pinJoin("https://sub.example", "typed"), pinJoin("https://\u017fub.example", "far")},
			{pinJoin("https://\u03c3.example", "typed"), pinJoin("https://\u03c2.example", "far")},
			{pinJoin(pinHub, "typed"), pinJoin("https://hub.example:8443", "far")},
			{pinJoin("https://localhost:8443", "typed"), pinJoin("http://localhost:8443", "far")},
			{pinJoin("https://[fe80::1%25lan0]:8443", "typed"), pinJoin("https://[fe80::1%25LAN0]:8443", "far")},
		} {
			p := newPinRun(t, c.typed, true, true, map[string]pinRoute{c.typed: {}})
			if err := p.f.Rejoin(c.next); ErrorCode(err) != CodeSupersededForeignOrigin || IsRetryable(err) {
				t.Fatalf("Rejoin %q for %q: %v", c.next, c.typed, err)
			}
			if p.f.currentURL() != c.typed || len(p.changes()) != 0 {
				t.Fatalf("Rejoin moved the follower to %q", c.next)
			}
			p.stateAbsent(t)
			if _, err := p.dial(t); ErrorCode(err) != CodeSupersededForeignOrigin || len(p.w.requests()) != 0 || ErrorCode(p.f.Refused()) != CodeSupersededForeignOrigin {
				t.Fatalf("after a refused Rejoin the follower dialled %v: %v", p.w.requests(), err)
			}
		}
	})
	t.Run("RejoinSameAllowed", func(t *testing.T) {
		t.Parallel()
		next := pinJoin("https://HUB.example", "rotated")
		p := newPinRun(t, typed, true, false, map[string]pinRoute{next: {}})
		if err := p.f.Rejoin(next); err != nil {
			t.Fatal(err)
		}
		if p.f.currentURL() != next || !slices.Equal(p.changes(), []string{next}) {
			t.Fatal("a same-origin Rejoin was not followed")
		}
		if _, err := p.dial(t); err != nil || !slices.Equal(p.w.requests(), []string{pinKeyOf(next)}) {
			t.Fatalf("requests %v: %v", p.w.requests(), err)
		}
		p.stateAbsent(t)
	})
	t.Run("OriginSpellingsAgree", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			name, pin, next  string
			same, key, admit bool
		}{
			{"HostCase", pinHub, "https://HUB.EXAMPLE", true, true, true},
			{"SchemeCase", pinHub, "HTTPS://hub.example", true, true, true},
			{"LoopbackCase", "http://localhost:8080", "http://LOCALHOST:8080", true, true, true},
			{"UnicodeCase", "https://bücher.example", "https://BÜCHER.example", true, true, true},
			{"DefaultPortHTTPS", pinHub, "https://hub.example:443", true, true, true},
			{"DefaultPortHTTP", "http://[::1]:80", "http://[::1]", true, true, true},
			{"LeadingZeroPort", "https://hub.example:0443", "https://hub.example:443", false, false, false},
			{"TrailingDot", pinHub, "https://hub.example.", false, false, false},
			{"IDNPunycode", "https://bücher.example", "https://xn--bcher-kva.example", false, false, false},
			{"IPv6Expanded", "http://[::1]:8080", "http://[0:0:0:0:0:0:0:1]:8080", false, false, false},
			{"IPv4Mapped", "http://127.0.0.1:8080", "http://[::ffff:127.0.0.1]:8080", false, false, false},
			{"PortOnly", pinHub, "https://hub.example:8443", false, false, false},
			{"SchemeOnly", "https://localhost:8443", "http://localhost:8443", false, false, false},
			{"KelvinSign", "https://key.example", "https://\u212aey.example", true, true, true},
			{"LongS", "https://sub.example", "https://\u017fub.example", true, false, false},
			{"FinalSigma", "https://\u03c3.example", "https://\u03c2.example", true, false, false},
			{"DottedCapitalI", "https://iub.example", "https://\u0130ub.example", false, true, false},
		} {
			pin, next := pinJoin(c.pin, "a"), pinJoin(c.next, "b")
			pu, err := ParseJoinURL(pin)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			nu, err := ParseJoinURL(next)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			same, key := sameOrigin(pu, nu), HubOrigin(pin) == HubOrigin(next)
			f, err := NewFollower(Config{JoinURL: pin, PinnedJoinURL: pin})
			if err != nil {
				t.Fatal(err)
			}
			admitted := f.admit(next) == nil
			t.Logf("%s sameOrigin=%v hubOrigin=%v admitted=%v", c.name, same, key, admitted)
			if same != c.same || key != c.key || admitted != c.admit || admitted != (same && key) {
				t.Errorf("%s: sameOrigin=%v hubOrigin=%v admitted=%v, want %v %v %v", c.name, same, key, admitted, c.same, c.key, c.admit)
			}
		}
		for _, next := range []string{"https://u:p@hub.example/join/b.c", "https://hub%2eexample/join/b.c", "https://hub.example/join/b.c#f", ""} {
			f, err := NewFollower(Config{JoinURL: pinJoin(pinHub, "a"), PinnedJoinURL: pinJoin(pinHub, "a")})
			if err != nil {
				t.Fatal(err)
			}
			if f.admit(next) == nil {
				t.Errorf("admitted an unparseable successor %q", next)
			}
		}
	})
}
