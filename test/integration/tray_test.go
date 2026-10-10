package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

type logBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (l *logBuffer) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, strings.TrimSpace(sprintf(format, args...)))
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func fastConfig(joinURL string, logs *logBuffer) tray.Config {
	cfg := tray.Config{
		JoinURL:        joinURL,
		RequestTimeout: 2 * time.Second,
		RetryBudget:    time.Second,
		RetryBackoff:   10 * time.Millisecond,
		PollInterval:   10 * time.Millisecond,
		OfferTimeout:   2 * time.Second,
	}
	if logs != nil {
		cfg.Logf = logs.logf
	}
	return cfg
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func dial(t *testing.T, cfg tray.Config) (*tray.Follower, *tray.Session) {
	t.Helper()
	f, err := tray.NewFollower(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s, err := f.Dial(testCtx(t))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return f, s
}

func recv(t *testing.T, s *tray.Session) tray.Event {
	t.Helper()
	ev, err := s.Recv(testCtx(t))
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	return ev
}

func attachRequests(h *fakeHub, token string) []hubRequest {
	var out []hubRequest
	for _, r := range h.requestLog() {
		if r.Token == token && r.Action == "" {
			out = append(out, r)
		}
	}
	return out
}

func TestTrayAttachSignalsWithICEServers(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	_, s := dial(t, fastConfig(h.joinURL(tr), nil))
	ice := s.ICEServers()
	if len(ice) != 2 || ice[1].Username != "turn-user-secret" || ice[1].Credential != "turn-credential-secret" || len(ice[1].URLs) != 2 {
		t.Fatalf("ice servers %+v", ice)
	}
	if s.Attempt() != 1 {
		t.Fatalf("attempt %d", s.Attempt())
	}
	reqs := attachRequests(h, tr.Token)
	if len(reqs) != 1 || reqs[0].Body["runtime"] != tray.DefaultRuntime || reqs[0].Body["controllerId"] == "" {
		t.Fatalf("attach requests %+v", reqs)
	}
}

func TestTrayAttachWaitsForTheLeader(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	h.with(func() { tr.WaitAttaches = 3 })
	logs := &logBuffer{}
	_, s := dial(t, fastConfig(h.joinURL(tr), logs))
	if ev := recv(t, s); ev.Kind != tray.EventOffer {
		t.Fatalf("event %v", ev.Kind)
	}
	reqs := attachRequests(h, tr.Token)
	if len(reqs) != 4 {
		t.Fatalf("attaches %d", len(reqs))
	}
	for _, r := range reqs[1:] {
		if r.Body["controllerId"] != reqs[0].Body["controllerId"] {
			t.Fatal("controller id changed while waiting")
		}
	}
	if !strings.Contains(logs.String(), "LEADER_NOT_CONNECTED") {
		t.Fatalf("logs %q", logs.String())
	}
}

func TestTrayAttachWaitHonoursCancellation(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	h.with(func() { tr.NoLeader = true })
	f, err := tray.NewFollower(fastConfig(h.joinURL(tr), nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = f.Dial(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
}

func TestTrayOfferAnswerAndCandidateExchange(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	mid, idx := "0", 0
	h.with(func() {
		tr.LeaderCandidates = []hubCandidate{
			{Candidate: "candidate:1 1 udp 2122260223 192.0.2.10 54321 typ host", SDPMid: &mid, SDPMLineIndex: &idx},
			{Candidate: "candidate:2 1 udp 1686052607 198.51.100.7 61000 typ srflx raddr 0.0.0.0 rport 0"},
		}
	})
	_, s := dial(t, fastConfig(h.joinURL(tr), nil))
	offer := recv(t, s)
	if offer.Kind != tray.EventOffer || offer.SDP != testOfferSDP || offer.Attempt != 1 {
		t.Fatalf("offer %+v", offer)
	}
	c1 := recv(t, s)
	if c1.Kind != tray.EventCandidate || !strings.Contains(c1.Candidate.Candidate, "typ host") || c1.Candidate.SDPMid == nil || *c1.Candidate.SDPMid != "0" || c1.Candidate.SDPMLineIndex == nil || *c1.Candidate.SDPMLineIndex != 0 {
		t.Fatalf("candidate 1 %+v", c1)
	}
	c2 := recv(t, s)
	if c2.Kind != tray.EventCandidate || c2.Candidate.SDPMid != nil || c2.Candidate.SDPMLineIndex != nil {
		t.Fatalf("candidate 2 %+v", c2)
	}
	ctx := testCtx(t)
	if err := s.SendAnswer(ctx, "v=0\r\nanswer\r\n"); err != nil {
		t.Fatal(err)
	}
	var one uint16 = 1
	ours := "candidate:9 1 udp 2122260223 203.0.113.5 50000 typ host"
	if err := s.SendCandidate(ctx, tray.Candidate{Candidate: ours, SDPMid: &mid, SDPMLineIndex: &one}); err != nil {
		t.Fatal(err)
	}
	h.with(func() {
		if len(tr.answers) != 1 || tr.answers[0].SDP != "v=0\r\nanswer\r\n" || tr.answers[0].BootstrapID != tr.bootstraps[0].ID {
			t.Errorf("answers %+v", tr.answers)
		}
		if len(tr.candidates) != 1 || tr.candidates[0].Candidate.Candidate != ours || *tr.candidates[0].Candidate.SDPMLineIndex != 1 || *tr.candidates[0].Candidate.SDPMid != "0" {
			t.Errorf("candidates %+v", tr.candidates)
		}
		if tr.answers[0].ControllerID != tr.bootstraps[0].ControllerID {
			t.Error("answer posted under a different controller")
		}
	})
	if err := s.SendAnswer(ctx, "  "); tray.ErrorCode(err) != "INVALID_ANSWER" {
		t.Fatalf("empty answer: %v", err)
	}
}

func TestTrayLateLeaderCandidatesArriveAfterTheAnswer(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	_, s := dial(t, fastConfig(h.joinURL(tr), nil))
	if ev := recv(t, s); ev.Kind != tray.EventOffer {
		t.Fatal(ev.Kind)
	}
	if err := s.SendAnswer(testCtx(t), "v=0\r\n"); err != nil {
		t.Fatal(err)
	}
	h.with(func() {
		appendEvent(tr.bootstraps[0], hubEvent{Type: "bootstrap.ice_candidate", Candidate: &hubCandidate{Candidate: "candidate:late"}})
	})
	if ev := recv(t, s); ev.Kind != tray.EventCandidate || ev.Candidate.Candidate != "candidate:late" {
		t.Fatalf("late candidate %+v", ev)
	}
	_ = s.Close()
	if _, err := s.Recv(testCtx(t)); !errors.Is(err, tray.ErrClosed) {
		t.Fatalf("recv after close: %v", err)
	}
}

func TestTrayRetryableFailureMintsAFreshBootstrap(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	h.with(func() { tr.FailBootstraps = 2 })
	_, s := dial(t, fastConfig(h.joinURL(tr), nil))
	for want := 2; want <= 3; want++ {
		ev := recv(t, s)
		if ev.Kind != tray.EventRestart || ev.Attempt != want || len(ev.ICEServers) != 2 {
			t.Fatalf("restart %d: %+v", want, ev)
		}
	}
	offer := recv(t, s)
	if offer.Kind != tray.EventOffer || offer.Attempt != 3 || s.Attempt() != 3 {
		t.Fatalf("offer %+v attempt %d", offer, s.Attempt())
	}
	if err := s.SendAnswer(testCtx(t), "v=0\r\n"); err != nil {
		t.Fatal(err)
	}
	h.with(func() {
		if len(tr.bootstraps) != 3 || len(tr.answers) != 1 || tr.answers[0].BootstrapID != tr.bootstraps[2].ID {
			t.Errorf("bootstraps %d answers %+v", len(tr.bootstraps), tr.answers)
		}
	})
	retries := 0
	for _, r := range h.requestLog() {
		if r.Action == "retry" {
			retries++
			if r.Body["runtime"] != tray.DefaultRuntime {
				t.Errorf("retry runtime %v", r.Body["runtime"])
			}
		}
	}
	if retries != 2 {
		t.Fatalf("retries %d", retries)
	}
}

func TestTrayRetriesStopWhenExhausted(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	h.with(func() { tr.FailBootstraps = 100 })
	_, s := dial(t, fastConfig(h.joinURL(tr), nil))
	restarts := 0
	for {
		ev, err := s.Recv(testCtx(t))
		if err != nil {
			if tray.ErrorCode(err) != "BOOTSTRAP_TIMEOUT" || !tray.IsRetryable(err) {
				t.Fatalf("err %v", err)
			}
			break
		}
		if ev.Kind != tray.EventRestart {
			t.Fatalf("event %v", ev.Kind)
		}
		restarts++
	}
	if restarts != 3 {
		t.Fatalf("restarts %d", restarts)
	}
}

func TestTrayNonRetryableFailureEndsTheSession(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	h.with(func() {
		tr.FailBootstraps = 1
		tr.FailRetryable = false
		tr.FailCode = "LEADER_REJECTED"
	})
	_, s := dial(t, fastConfig(h.joinURL(tr), nil))
	if _, err := s.Recv(testCtx(t)); tray.ErrorCode(err) != "LEADER_REJECTED" {
		t.Fatalf("err %v", err)
	}
	for _, r := range h.requestLog() {
		if r.Action == "retry" {
			t.Fatal("retried a non-retryable failure")
		}
	}
}

func supersedeChain(h *fakeHub, hops int) []*fakeTray {
	trays := make([]*fakeTray, hops+1)
	for i := range trays {
		trays[i] = h.addTray("chain")
	}
	h.with(func() {
		for i := 0; i < hops; i++ {
			trays[i].SupersededBy = h.srv.URL + "/join/" + trays[i+1].Token
		}
	})
	return trays
}

func TestTraySupersedeChainIsFollowedAndPersisted(t *testing.T) {
	h := newFakeHub(t)
	trays := supersedeChain(h, 3)
	state := tray.OpenState(filepath.Join(t.TempDir(), "slicc", "tray-join.json"))
	var changed []string
	cfg := fastConfig(h.joinURL(trays[0]), nil)
	cfg.State = state
	cfg.OnJoinURLChanged = func(u string) { changed = append(changed, u) }
	f, s := dial(t, cfg)
	if ev := recv(t, s); ev.Kind != tray.EventOffer {
		t.Fatal(ev.Kind)
	}
	if !f.Redirected() || len(changed) != 3 || changed[2] != h.joinURL(trays[3]) {
		t.Fatalf("changed %d", len(changed))
	}
	controllers := map[any]bool{}
	for i, tr := range trays {
		reqs := attachRequests(h, tr.Token)
		if len(reqs) != 1 {
			t.Fatalf("tray %d attaches %d", i, len(reqs))
		}
		controllers[reqs[0].Body["controllerId"]] = true
	}
	if len(controllers) != 4 {
		t.Fatal("controller id reused across a supersede hop")
	}
	if got := state.Current(h.joinURL(trays[0])); got != h.joinURL(trays[3]) {
		t.Fatal("state does not hold the final join URL")
	}
	if last, ok := state.Last(); !ok || last != h.joinURL(trays[3]) {
		t.Fatal("state Last is not the final join URL")
	}
	f2, s2 := dial(t, cfg)
	_ = s2
	if f2.Redirected() != true {
		t.Fatal("second follower did not start from the persisted URL")
	}
	if len(attachRequests(h, trays[0].Token)) != 1 || len(attachRequests(h, trays[3].Token)) != 2 {
		t.Fatal("second dial walked the chain again")
	}
}

func TestTraySupersedeFiveHopsSucceed(t *testing.T) {
	h := newFakeHub(t)
	trays := supersedeChain(h, tray.MaxRedirects)
	_, s := dial(t, fastConfig(h.joinURL(trays[0]), nil))
	if ev := recv(t, s); ev.Kind != tray.EventOffer {
		t.Fatal(ev.Kind)
	}
}

func TestTraySupersedeSixHopsExhaustTheChain(t *testing.T) {
	h := newFakeHub(t)
	trays := supersedeChain(h, tray.MaxRedirects+1)
	state := tray.OpenState(filepath.Join(t.TempDir(), "tray-join.json"))
	cfg := fastConfig(h.joinURL(trays[0]), nil)
	cfg.State = state
	f, err := tray.NewFollower(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Dial(testCtx(t))
	if tray.ErrorCode(err) != tray.CodeSupersedeExhausted {
		t.Fatalf("err %v", err)
	}
	assertRedacted(t, err.Error(), h, trays...)
	attached := 0
	for _, tr := range trays {
		attached += len(attachRequests(h, tr.Token))
	}
	if attached != tray.MaxRedirects+1 || len(attachRequests(h, trays[tray.MaxRedirects+1].Token)) != 0 {
		t.Fatalf("attached %d", attached)
	}
	if state.Current(h.joinURL(trays[0])) != h.joinURL(trays[tray.MaxRedirects]) {
		t.Fatal("state does not hold the last followed hop")
	}
	if _, err := f.Dial(testCtx(t)); err != nil {
		t.Fatalf("redial from the persisted hop: %v", err)
	}
}

func TestTraySupersedeLoopIsBounded(t *testing.T) {
	h := newFakeHub(t)
	a, b := h.addTray("loop"), h.addTray("loop")
	h.with(func() {
		a.SupersededBy = h.joinURL(b)
		b.SupersededBy = h.joinURL(a)
	})
	f, _ := tray.NewFollower(fastConfig(h.joinURL(a), nil))
	if _, err := f.Dial(testCtx(t)); tray.ErrorCode(err) != tray.CodeSupersedeExhausted {
		t.Fatalf("err %v", err)
	}
}

func TestTraySupersedeWithoutAJoinURL(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	h.with(func() { tr.SupersedeMissing = true })
	f, _ := tray.NewFollower(fastConfig(h.joinURL(tr), nil))
	if _, err := f.Dial(testCtx(t)); tray.ErrorCode(err) != tray.CodeSupersededMissingJoin {
		t.Fatalf("err %v", err)
	}
}

func TestTraySupersedeToAnInsecureURLIsRefused(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	bad := "http://hub.example.test/join/insecure.0123456789abcdef"
	h.with(func() { tr.SupersededBy = bad })
	f, _ := tray.NewFollower(fastConfig(h.joinURL(tr), nil))
	_, err := f.Dial(testCtx(t))
	if tray.ErrorCode(err) != tray.CodeBadResponse || strings.Contains(err.Error(), "insecure.0123456789abcdef") {
		t.Fatalf("err %v", err)
	}
	for _, r := range h.requestLog() {
		if strings.Contains(r.Token, "insecure") {
			t.Fatal("followed an insecure redirect")
		}
	}
}

func TestTraySupersedeDuringBootstrapIsPersisted(t *testing.T) {
	h := newFakeHub(t)
	a, b := h.addTray("mid"), h.addTray("mid")
	h.with(func() {
		a.OfferSDP = ""
		a.SupersedeOnPoll = h.joinURL(b)
	})
	state := tray.OpenState(filepath.Join(t.TempDir(), "tray-join.json"))
	cfg := fastConfig(h.joinURL(a), nil)
	cfg.State = state
	f, s := dial(t, cfg)
	_, err := s.Recv(testCtx(t))
	if tray.ErrorCode(err) != tray.CodeSuperseded || !tray.IsRetryable(err) {
		t.Fatalf("err %v", err)
	}
	if state.Current(h.joinURL(a)) != h.joinURL(b) {
		t.Fatal("mid-session supersede not persisted")
	}
	s2, err := f.Dial(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if ev := recv(t, s2); ev.Kind != tray.EventOffer {
		t.Fatal(ev.Kind)
	}
}

func TestTrayTerminalAttachFailures(t *testing.T) {
	h := newFakeHub(t)
	expired := h.addTray("expired")
	h.with(func() { expired.Expired = true })
	cases := map[string]string{
		h.srv.URL + "/join/unknown.00112233445566778899": tray.CodeInvalidJoin,
		h.joinURL(expired):                 tray.CodeTrayExpired,
		h.srv.URL + "/join/malformedtoken": "MALFORMED_CAPABILITY",
	}
	for joinURL, code := range cases {
		f, err := tray.NewFollower(fastConfig(joinURL, nil))
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Dial(testCtx(t))
		if tray.ErrorCode(err) != code || tray.IsRetryable(err) {
			t.Fatalf("%s: err %v", code, err)
		}
		assertNoSecret(t, err.Error(), joinURL)
	}
}

func TestTrayBootstrapNotFoundIsRetryable(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	h.with(func() { tr.OfferSDP = "" })
	_, s := dial(t, fastConfig(h.joinURL(tr), nil))
	h.with(func() { tr.bootstraps = nil })
	_, err := s.Recv(testCtx(t))
	if tray.ErrorCode(err) != tray.CodeBootstrapNotFound || !tray.IsRetryable(err) {
		t.Fatalf("err %v", err)
	}
}

func TestTrayRequestTimeout(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	h.with(func() { h.hang = true })
	cfg := fastConfig(h.joinURL(tr), nil)
	cfg.RequestTimeout = 200 * time.Millisecond
	f, _ := tray.NewFollower(cfg)
	start := time.Now()
	_, err := f.Dial(testCtx(t))
	if tray.ErrorCode(err) != tray.CodeRequestTimeout || !tray.IsRetryable(err) {
		t.Fatalf("err %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	assertRedacted(t, err.Error(), h, tr)
}

func TestTrayRequestTimeoutAppliesToACustomClient(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	_, s := dial(t, func() tray.Config {
		c := fastConfig(h.joinURL(tr), nil)
		c.HTTPClient = &http.Client{}
		c.RequestTimeout = 200 * time.Millisecond
		return c
	}())
	h.with(func() { h.hang = true })
	if err := s.SendAnswer(testCtx(t), "v=0\r\n"); tray.ErrorCode(err) != tray.CodeRequestTimeout {
		t.Fatalf("err %v", err)
	}
}

func TestTrayOfferTimeout(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	h.with(func() { tr.OfferSDP = "" })
	cfg := fastConfig(h.joinURL(tr), nil)
	cfg.OfferTimeout = 300 * time.Millisecond
	_, s := dial(t, cfg)
	_, err := s.Recv(testCtx(t))
	if tray.ErrorCode(err) != tray.CodeOfferTimeout || !tray.IsRetryable(err) {
		t.Fatalf("err %v", err)
	}
}

func TestTrayRecvHonoursCancellationAndClose(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	h.with(func() { tr.OfferSDP = "" })
	cfg := fastConfig(h.joinURL(tr), nil)
	cfg.PollInterval = time.Second
	_, s := dial(t, cfg)
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = s.Close()
	}()
	if _, err := s.Recv(testCtx(t)); !errors.Is(err, tray.ErrClosed) {
		t.Fatalf("err %v", err)
	}
}

func TestTrayJoinURLValidation(t *testing.T) {
	good := []string{
		"https://www.sliccy.ai/join/tray1.abcdef0123",
		"  https://www.sliccy.ai/join/tray1.abcdef0123\n",
		"http://127.0.0.1:8787/join/tray1.abcdef0123",
		"http://localhost:8787/join/tray1.abcdef0123",
		"http://[::1]:8787/join/tray1.abcdef0123",
	}
	for _, u := range good {
		if !tray.IsJoinURL(u) {
			t.Errorf("rejected %q", u)
		}
	}
	bad := []string{
		"",
		"www.sliccy.ai/join/tray1.abc",
		"http://www.sliccy.ai/join/tray1.abcdef0123",
		"https://www.sliccy.ai/controller/tray1.abcdef0123",
		"https://www.sliccy.ai/join/",
		"https://www.sliccy.ai/join/a/b",
		"https://user:pw@www.sliccy.ai/join/tray1.abcdef0123",
		"ftp://www.sliccy.ai/join/tray1.abcdef0123",
	}
	for _, u := range bad {
		if tray.IsJoinURL(u) {
			t.Errorf("accepted %q", u)
		}
		if _, err := tray.NewFollower(tray.Config{JoinURL: u}); err == nil || (u != "" && strings.Contains(err.Error(), u)) {
			t.Errorf("NewFollower(%q) = %v", u, err)
		}
	}
}

func TestTrayStateFilePermissionsAndContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg", "slicc")
	path := filepath.Join(dir, "tray-join.json")
	state := tray.OpenState(path)
	origin := "https://www.sliccy.ai/join/origin.0011223344556677"
	next := "https://www.sliccy.ai/join/next.8899aabbccddeeff"
	if got := state.Current(origin); got != origin {
		t.Fatal("empty state should return the origin")
	}
	if _, ok := state.Last(); ok {
		t.Fatal("empty state has a last URL")
	}
	if err := state.Save(origin, next); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Fatalf("file mode %o", fi.Mode().Perm())
		}
		di, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if di.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode %o", di.Mode().Perm())
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "origin.0011223344556677") || !strings.Contains(string(data), next) {
		t.Fatalf("state content %s", data)
	}
	if tray.OpenState(path).Current(origin) != next {
		t.Fatal("state did not survive a reopen")
	}
	if err := state.Save(origin, "http://evil.example/join/x.y"); err == nil {
		t.Fatal("saved an insecure join URL")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files %v", entries)
	}
	if err := state.Forget(origin); err != nil || state.Current(origin) != origin {
		t.Fatal("forget did not drop the entry")
	}
}

func TestTrayStateTightensAnExistingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := filepath.Join(t.TempDir(), "slicc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "tray-join.json")
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := tray.OpenState(path)
	origin := "https://www.sliccy.ai/join/o.0011223344556677"
	if state.Current(origin) != origin {
		t.Fatal("corrupt state not ignored")
	}
	if err := state.Save(origin, origin); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	di, _ := os.Stat(dir)
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Fatalf("modes %o %o", fi.Mode().Perm(), di.Mode().Perm())
	}
}

func TestTrayRedactionOfErrorsAndLogs(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("red")
	logs := &logBuffer{}
	h.with(func() { h.garbage = "<html>bad gateway for /join/" + tr.Token + "</html>" })
	f, _ := tray.NewFollower(fastConfig(h.joinURL(tr), logs))
	_, err := f.Dial(testCtx(t))
	if tray.ErrorCode(err) != tray.CodeBadResponse || !tray.IsRetryable(err) {
		t.Fatalf("err %v", err)
	}
	assertRedacted(t, err.Error(), h, tr)

	closed := httptestClosedURL(t) + "/join/" + tr.Token
	f, _ = tray.NewFollower(fastConfig(closed, logs))
	_, err = f.Dial(testCtx(t))
	if tray.ErrorCode(err) != tray.CodeNetwork || !tray.IsRetryable(err) {
		t.Fatalf("err %v", err)
	}
	assertNoSecret(t, err.Error(), closed)

	h.with(func() {
		h.garbage = ""
		tr.WaitAttaches = 1
		tr.FailBootstraps = 1
	})
	f, _ = tray.NewFollower(fastConfig(h.joinURL(tr), logs))
	s, err := f.Dial(testCtx(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	recv(t, s)
	assertRedacted(t, logs.String(), h, tr)
	if logs.String() == "" {
		t.Fatal("no logs")
	}
	if f.Redact("x "+h.joinURL(tr)+" y "+tr.Token) != "x <redacted> y <redacted>" {
		t.Fatalf("redact %q", f.Redact(h.joinURL(tr)))
	}
}

func assertRedacted(t *testing.T, s string, h *fakeHub, trays ...*fakeTray) {
	t.Helper()
	for _, tr := range trays {
		assertNoSecret(t, s, h.joinURL(tr))
	}
}

func assertNoSecret(t *testing.T, s, joinURL string) {
	t.Helper()
	token := joinURL[strings.LastIndex(joinURL, "/")+1:]
	secret := token[strings.LastIndex(token, ".")+1:]
	for _, needle := range []string{joinURL, token, secret} {
		if strings.Contains(s, needle) {
			t.Fatalf("output leaks %q: %q", needle, s)
		}
	}
}

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func httptestClosedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	u := srv.URL
	srv.Close()
	return u
}
