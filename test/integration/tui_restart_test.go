package integration

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/tui"
)

type restartHarness struct {
	*tuiHarness
	detached []bool
	updates  int
}

func newRestartTUI(t *testing.T, entries []browser.Entry, update bool) *restartHarness {
	h := &restartHarness{tuiHarness: &tuiHarness{t: t, now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}}
	cfg := tui.Config{
		Entries: entries,
		Start:   func(c tui.Choice) { h.started = append(h.started, c) },
		Stop:    func() { h.stopped++ },
		Detach:  func(restart bool) { h.detached = append(h.detached, restart) },
		Now:     func() time.Time { return h.now },
	}
	if update {
		cfg.Update = func() { h.updates++ }
	}
	h.m = tui.New(cfg)
	return h
}

func TestTUIDashboardDetachKeyLeavesTheBrowsersRunning(t *testing.T) {
	h := newRestartTUI(t, tuiEntries(), false)
	h.keys("enter")
	h.send(tui.EventMsg(flow.Event{Event: flow.Launched, Browser: "chrome", Name: "Google Chrome", Role: flow.RoleSeven, CDP: "http://127.0.0.1:50000"}))
	v := h.view()
	if !strings.Contains(v, "d detach") || strings.Contains(v, "u update") {
		t.Fatalf("key bar without an update to offer:\n%s", v)
	}
	if isQuit(h.keys("d")) || !slices.Equal(h.detached, []bool{false}) || h.stopped != 0 || h.model().Mode() != tui.ModeClosing || !strings.Contains(h.view(), "detaching…") {
		t.Fatalf("d: detached %v, stopped %d, mode %s:\n%s", h.detached, h.stopped, h.model().Mode(), h.view())
	}
	h.keys("q", "d", "ctrl+c")
	if len(h.detached) != 1 || h.stopped != 0 {
		t.Fatalf("keys while detaching: detached %v, stopped %d", h.detached, h.stopped)
	}
	h.send(tui.EventMsg(flow.Event{Event: flow.Closed, Kept: []string{"chrome"}}))
	if cmd := h.send(tui.DoneMsg{}); !isQuit(cmd) {
		t.Fatal("DoneMsg did not quit")
	}
}

func TestTUIDashboardUpdateKeyRestartsOnceTheBinaryIsReplaced(t *testing.T) {
	h := newRestartTUI(t, tuiEntries(), true)
	h.send(tui.NoticeMsg("slicc 9.9.9 is available (you have 1.0.0); run `slicc update`"))
	h.keys("enter")
	if v := h.view(); !strings.Contains(v, "d detach") || !strings.Contains(v, "u update") {
		t.Fatalf("key bar:\n%s", v)
	}
	h.keys("u", "u")
	if h.updates != 1 || !h.model().Updating() || !strings.Contains(h.view(), "u updating…") || !strings.Contains(h.view(), "looking for a newer slicc") {
		t.Fatalf("u: %d updates, updating %v:\n%s", h.updates, h.model().Updating(), h.view())
	}
	h.send(tui.UpdateMsg{Line: "update: GitHub's API rate limit is used up", Failed: true})
	if h.model().Updating() || h.model().Mode() != tui.ModeDashboard || len(h.detached) != 0 || !strings.Contains(h.view(), "rate limit is used up") {
		t.Fatalf("after a failed update: mode %s, detached %v:\n%s", h.model().Mode(), h.detached, h.view())
	}
	h.keys("u")
	h.send(tui.UpdateMsg{Line: "updated to slicc 9.9.9 (SHA256SUMS and attestation checked); restarting", Restart: true})
	if h.updates != 2 || !slices.Equal(h.detached, []bool{true}) || h.model().Mode() != tui.ModeClosing || !strings.Contains(h.view(), "restarting…") || strings.Contains(h.view(), "9.9.9 is available") {
		t.Fatalf("after the update: %d updates, detached %v, mode %s:\n%s", h.updates, h.detached, h.model().Mode(), h.view())
	}
}

func TestTUIDashboardUpdateAfterQuitDoesNotRestart(t *testing.T) {
	h := newRestartTUI(t, tuiEntries(), true)
	h.keys("enter", "u", "q")
	h.send(tui.UpdateMsg{Line: "updated to slicc 9.9.9; restarting", Restart: true})
	if h.stopped != 1 || len(h.detached) != 0 || !strings.Contains(h.view(), "closing…") {
		t.Fatalf("stopped %d, detached %v:\n%s", h.stopped, h.detached, h.view())
	}
}

func TestTUIPairDashboardOffersUpdateButNotDetach(t *testing.T) {
	o := browser.Options{ProfilesDir: t.TempDir()}
	h := newRestartTUI(t, flow.WithPair(o, nil), true)
	h.keys("enter")
	v := h.view()
	if !strings.Contains(v, "u update") || strings.Contains(v, "d detach") {
		t.Fatalf("pair key bar:\n%s", v)
	}
	h.keys("d")
	if len(h.detached) != 0 || h.model().Mode() != tui.ModeDashboard {
		t.Fatalf("d on the pair dashboard: detached %v, mode %s", h.detached, h.model().Mode())
	}
	h.keys("u")
	h.send(tui.UpdateMsg{Line: "updated; restarting", Restart: true})
	if h.updates != 1 || !slices.Equal(h.detached, []bool{true}) || !strings.Contains(h.view(), "restarting…") {
		t.Fatalf("u on the pair dashboard: %d updates, detached %v:\n%s", h.updates, h.detached, h.view())
	}
}

func TestTUIGoMsgSkipsThePicker(t *testing.T) {
	h := newRestartTUI(t, nil, false)
	if h.model().Mode() != tui.ModeDiscover {
		t.Fatalf("mode %s", h.model().Mode())
	}
	e := tuiEntries()[0]
	h.send(tui.GoMsg{Entries: []browser.Entry{e}})
	if h.model().Mode() != tui.ModeDashboard || len(h.started) != 1 || h.started[0].Entries[0].ID != e.ID {
		t.Fatalf("mode %s, started %+v", h.model().Mode(), h.started)
	}
	h.send(tui.GoMsg{Entries: []browser.Entry{e}})
	if len(h.started) != 1 {
		t.Fatalf("a second GoMsg started again: %+v", h.started)
	}
}
