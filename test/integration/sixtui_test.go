package integration

import (
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

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
	if !strings.Contains(last, "six ") || strings.Contains(last, "might lose data") || strings.Contains(last, "141 ") || !strings.Contains(last, "opens in Google Chrome 141") || strings.Contains(last, "best match") ||
		strings.Count(h.view(), "might lose data") != 1 || !strings.Contains(h.view(), "SLICC six profiles  re-use existing SLICC profile (might lose data)") {
		t.Fatalf("six row %q:\n%s", last, h.view())
	}
	h.keys("enter")
	if len(h.started) != 1 || h.started[0].Entries[0].Kind == browser.KindSix {
		t.Fatalf("enter went with %+v", h.started)
	}

	h = newSixTUI(t, append(tuiEntries(), sixRow()))
	h.keys("down", "down", "down", "down", "p")
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

func TestTUISixRowSaysWhenItWasLastUsed(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{0: "used today", 26 * time.Hour: "used yesterday", 50 * time.Hour: "used 2 days ago", 21 * 24 * time.Hour: "used 3 weeks ago", 95 * 24 * time.Hour: "used 3 months ago"}
	for ago, want := range cases {
		dir := t.TempDir()
		state := filepath.Join(dir, "Local State")
		if err := os.WriteFile(state, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(state, now.Add(-ago), now.Add(-ago)); err != nil {
			t.Fatal(err)
		}
		row := sixRow()
		row.Six.Dir = dir
		h := newSixTUI(t, []browser.Entry{row})
		h.now = now
		if r := h.rows()[0]; !strings.Contains(r, want) {
			t.Errorf("%v ago: row %q lacks %q", ago, r, want)
		}
	}
}

func TestTUISixWarningIsBoldOnLightTerminals(t *testing.T) {
	bold := regexp.MustCompile(`\x1b\[(?:[0-9]+;)*1;(?:[0-9]+;)*3[0-9][;m][^\x1b]*might lose data`)
	for _, light := range []bool{false, true} {
		var m tea.Model = tui.New(tui.Config{Entries: []browser.Entry{sixRow()}, Color: true})
		if light {
			m, _ = m.Update(tea.BackgroundColorMsg{Color: color.White})
		}
		if got := bold.MatchString(m.View().Content); got != light {
			t.Errorf("light %v: bold warning %v:\n%q", light, got, m.View().Content)
		}
	}
}
