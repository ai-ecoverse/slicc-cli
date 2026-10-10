package integration

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ai-ecoverse/slicc-cli/internal/signal/tray"
)

func TestTraySupersedeShapes(t *testing.T) {
	for _, shape := range []string{"legacy-409", "link-only"} {
		t.Run(shape, func(t *testing.T) {
			h := newFakeHub(t)
			trays := supersedeChain(h, 2)
			h.with(func() {
				for _, tr := range trays {
					tr.LegacySupersede = shape == "legacy-409"
					tr.LinkOnly = shape == "link-only"
				}
			})
			f, s := dial(t, fastConfig(h.joinURL(trays[0]), nil))
			if ev := recv(t, s); ev.Kind != tray.EventOffer || !f.Redirected() {
				t.Fatalf("event %v", ev.Kind)
			}
			for i, tr := range trays {
				if n := len(attachRequests(h, tr.Token)); n != 1 {
					t.Fatalf("tray %d attaches %d", i, n)
				}
			}
		})
	}
}

func TestTrayDoesNotFollowHTTPRedirectsOrMutateTheCallerClient(t *testing.T) {
	h := newFakeHub(t)
	trays := supersedeChain(h, 1)
	caller := &http.Client{}
	cfg := fastConfig(h.joinURL(trays[0]), nil)
	cfg.HTTPClient = caller
	var changed []string
	cfg.OnJoinURLChanged = func(u string) { changed = append(changed, u) }
	_, _ = dial(t, cfg)
	if caller.CheckRedirect != nil {
		t.Fatal("caller client mutated")
	}
	if len(changed) != 1 || changed[0] != h.joinURL(trays[1]) || strings.Contains(changed[0], "json=") {
		t.Fatalf("changed %v", changed)
	}
}

func TestTrayRetriesTransientHubErrors(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("flaky")
	logs := &logBuffer{}
	h.with(func() { h.flaky = 2 })
	_, s := dial(t, fastConfig(h.joinURL(tr), logs))
	if !strings.Contains(logs.String(), "error code: 1101") {
		t.Fatalf("logs %q", logs.String())
	}
	h.with(func() { h.flaky = 3 })
	if ev := recv(t, s); ev.Kind != tray.EventOffer {
		t.Fatal(ev.Kind)
	}
	if n := len(attachRequests(h, tr.Token)); n != 3 {
		t.Fatalf("attaches %d", n)
	}
	h.with(func() { h.flaky = 100 })
	err := s.SendAnswer(testCtx(t), "v=0\r\n")
	if tray.ErrorCode(err) != tray.CodeBadResponse || !tray.IsRetryable(err) || !strings.Contains(err.Error(), "error code: 1101") {
		t.Fatalf("err %v", err)
	}
	answers := 0
	for _, r := range h.requestLog() {
		if r.Action == "answer" {
			answers++
		}
	}
	if answers != 4 {
		t.Fatalf("answer attempts %d", answers)
	}
}

func TestTrayClientErrorsAreNotRetried(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("a")
	_, s := dial(t, fastConfig(h.joinURL(tr), nil))
	h.with(func() { tr.bootstraps = nil })
	if err := s.SendAnswer(testCtx(t), "v=0\r\n"); tray.ErrorCode(err) != tray.CodeBootstrapNotFound {
		t.Fatalf("err %v", err)
	}
	answers := 0
	for _, r := range h.requestLog() {
		if r.Action == "answer" {
			answers++
		}
	}
	if answers != 1 {
		t.Fatalf("answer attempts %d", answers)
	}
}

func TestTrayHoldsLocalCandidatesUntilTheAnswer(t *testing.T) {
	h := newFakeHub(t)
	tr := h.addTray("trickle")
	_, s := dial(t, fastConfig(h.joinURL(tr), nil))
	if s.Trust() != "full" {
		t.Fatalf("trust %q", s.Trust())
	}
	recv(t, s)
	ctx := testCtx(t)
	for _, c := range []string{"candidate:early-1", "", "candidate:early-2"} {
		if err := s.SendCandidate(ctx, tray.Candidate{Candidate: c}); err != nil {
			t.Fatal(err)
		}
	}
	h.with(func() {
		if len(tr.candidates) != 0 {
			t.Error("candidates posted before the answer")
		}
	})
	if err := s.SendAnswer(ctx, "v=0\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.SendCandidate(ctx, tray.Candidate{Candidate: "candidate:late"}); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, r := range h.requestLog() {
		switch r.Action {
		case "answer":
			order = append(order, "answer")
		case "ice-candidate":
			order = append(order, r.Body["candidate"].(map[string]any)["candidate"].(string))
		}
	}
	if strings.Join(order, ",") != "answer,candidate:early-1,candidate:early-2,candidate:late" {
		t.Fatalf("order %v", order)
	}
}

func TestTraySuccessorVersionVectors(t *testing.T) {
	vectors := []struct{ header, want string }{
		{`<https://www.sliccy.ai/join/fresh-tray.deadbeef>; rel="successor-version"`, "https://www.sliccy.ai/join/fresh-tray.deadbeef"},
		{`<https://www.sliccy.ai/join/fresh.beef>; rel="successor-version", <https://www.sliccy.ai/.well-known/api-catalog>; rel="api-catalog", <https://www.sliccy.ai/status>; rel="status"; type="application/json"`, "https://www.sliccy.ai/join/fresh.beef"},
		{`<https://www.sliccy.ai/status>; rel="status", <https://www.sliccy.ai/join/fresh.beef>; rel="successor-version"`, "https://www.sliccy.ai/join/fresh.beef"},
		{`<https://www.sliccy.ai/join/a.b>; rel=successor-version`, "https://www.sliccy.ai/join/a.b"},
		{`<https://www.sliccy.ai/join/a.b>; rel="alternate successor-version"`, "https://www.sliccy.ai/join/a.b"},
		{`<https://www.sliccy.ai/join/a.b>; REL="Successor-Version"`, "https://www.sliccy.ai/join/a.b"},
		{`<https://www.sliccy.ai/x>; rel="alternate"; title="one, two", <https://www.sliccy.ai/join/a.b>; rel="successor-version"`, "https://www.sliccy.ai/join/a.b"},
		{`<https://www.sliccy.ai/x>; title="q; rel=successor-version"`, ""},
		{`<https://www.sliccy.ai/join/old.b>; rel="predecessor-version"`, ""},
		{`<https://www.sliccy.ai/join/a.b>; rel="successor-version-2"`, ""},
		{`</join/a.b>; rel="successor-version"`, ""},
		{`<https://www.sliccy.ai/join/fresh%3Eevil.deadbeef>; rel="successor-version"`, "https://www.sliccy.ai/join/fresh%3Eevil.deadbeef"},
		{"", ""},
		{"not a link header", ""},
	}
	for _, v := range vectors {
		header := http.Header{}
		if v.header != "" {
			header.Set("Link", v.header)
		}
		if got := tray.SuccessorFromLinkHeader(header); got != v.want {
			t.Errorf("%q: got %q, want %q", v.header, got, v.want)
		}
	}
	header := http.Header{}
	header.Add("Link", `<https://www.sliccy.ai/status>; rel="status"`)
	header.Add("Link", `<https://www.sliccy.ai/join/a.b>; rel="successor-version"`)
	if got := tray.SuccessorFromLinkHeader(header); got != "https://www.sliccy.ai/join/a.b" {
		t.Errorf("repeated headers: %q", got)
	}
	if tray.SuccessorFromLinkHeader(nil) != "" {
		t.Error("nil header")
	}
}
