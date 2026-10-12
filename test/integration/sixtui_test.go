package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/tui"
)

func sixRow() browser.Entry {
	in := browser.Installed{ID: "chrome", Name: "Google Chrome", Engine: browser.EngineChromium, Version: "141.0.7390.1", Path: "/g", Executable: "g", DebugSupport: browser.DebugSupported}
	return browser.SixProfileEntry(browser.Six{Dir: "/fake/six", Label: "six"}, &in)
}

func newSixTUI(t *testing.T, entries []browser.Entry) *tuiHarness {
	h := &tuiHarness{t: t, now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	h.m = tui.New(tui.Config{
		Entries: entries,
		Start:   func(c tui.Choice) { h.started = append(h.started, c) },
		Stop:    func() { h.stopped++ },
		Now:     func() time.Time { return h.now },
	})
	return h
}

func TestTUISixRowIsLastAndNeverPreselected(t *testing.T) {
	h := newSixTUI(t, append(tuiEntries(), sixRow()))
	rows := h.rows()
	last := rows[len(rows)-1]
	if !strings.Contains(last, "Chrome · SLICC six profile") || !strings.Contains(last, "SLICC six's own profile, used in place") || strings.Contains(last, "best match") {
		t.Fatalf("six row %q:\n%s", last, h.view())
	}
	h.keys("enter")
	if len(h.started) != 1 || h.started[0].Entries[0].Kind == browser.KindSix {
		t.Fatalf("enter went with %+v", h.started)
	}

	h = newSixTUI(t, append(tuiEntries(), sixRow()))
	h.keys("down", "down", "down", "down", "down", "down", "p")
	if !strings.Contains(h.model().Message(), "SLICC six's profile is used as it is") {
		t.Fatalf("p on the six row: %q", h.model().Message())
	}
	h.keys("enter")
	if len(h.started) != 1 || len(h.started[0].Entries) != 1 || h.started[0].Entries[0].ID != "six" || h.started[0].Entries[0].Six == nil {
		t.Fatalf("picking the six row: %+v", h.started)
	}
}

func TestTUIOnlySixRowNeedsAnExplicitPick(t *testing.T) {
	h := newSixTUI(t, []browser.Entry{sixRow()})
	if strings.Contains(h.rows()[0], "best match") {
		t.Fatalf("row %q", h.rows()[0])
	}
	h.keys("enter")
	if len(h.started) != 0 || h.model().Mode() != tui.ModePick || !strings.Contains(h.view(), "never picked for you") {
		t.Fatalf("enter alone chose six's profile: %+v\n%s", h.started, h.view())
	}
	h.keys("space", "enter")
	if len(h.started) != 1 || h.started[0].Entries[0].ID != "six" {
		t.Fatalf("space and enter: %+v", h.started)
	}
}
