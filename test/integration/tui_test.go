package integration

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
	"github.com/ai-ecoverse/slicc-cli/internal/tui"
)

func tuiEntries() []browser.Entry {
	eps := []browser.Endpoint{
		{CDP: "http://127.0.0.1:9333", Name: "Brave", Version: "1.84.0", Source: browser.SourceProbe, SevenTabs: []browser.Tab{}},
		{CDP: "http://127.0.0.1:9222", Name: "Google Chrome", Version: "141.0.7390.1", Source: browser.SourceProbe, SevenOpen: true, SevenTabs: []browser.Tab{{ID: "t", URL: "https://lars.sliccy.ai/"}}},
	}
	ins := []browser.Installed{
		{ID: "electron:slack", Name: "Slack", Engine: browser.EngineElectron, Path: "/Applications/Slack.app", Executable: "slack", DebugSupport: browser.DebugUnknown},
		{ID: "chrome-canary", Name: "Google Chrome Canary", Engine: browser.EngineChromium, Version: "143.0.1.0", Path: "/c", Executable: "c", DebugSupport: browser.DebugSupported},
		{ID: "chrome", Name: "Google Chrome", Engine: browser.EngineChromium, Version: "141.0.7390.1", Path: "/g", Executable: "g", DebugSupport: browser.DebugSupported},
		{ID: "electron:claude", Name: "Claude", Engine: browser.EngineElectron, Path: "/Applications/Claude.app", Executable: "claude", DebugSupport: browser.DebugDisabled},
	}
	return browser.Rank(eps, ins)
}

type tuiHarness struct {
	t       *testing.T
	m       tea.Model
	started []tui.Choice
	stopped int
	now     time.Time
}

func newTUI(t *testing.T, profile string) *tuiHarness {
	h := &tuiHarness{t: t, now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	h.m = tui.New(tui.Config{
		Entries: tuiEntries(),
		Profile: profile,
		Profiles: func(e browser.Entry) []string {
			if e.ID == "chrome" {
				return []string{"chrome", "work"}
			}
			return nil
		},
		Start: func(c tui.Choice) { h.started = append(h.started, c) },
		Stop:  func() { h.stopped++ },
		Now:   func() time.Time { return h.now },
	})
	return h
}

func (h *tuiHarness) model() tui.Model { return h.m.(tui.Model) }

func (h *tuiHarness) send(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	h.m, cmd = h.m.Update(msg)
	return cmd
}

func (h *tuiHarness) keys(keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "up":
			msg = tea.KeyPressMsg{Code: tea.KeyUp}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "space":
			msg = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
		case "ctrl+c":
			msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
		default:
			for _, r := range k {
				cmd = h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			continue
		}
		cmd = h.send(msg)
	}
	return cmd
}

func (h *tuiHarness) view() string { return h.m.View().Content }

func (h *tuiHarness) rows() []string {
	var rows []string
	for _, l := range strings.Split(h.view(), "\n")[1:] {
		if strings.Contains(l, "○") || strings.Contains(l, "●") {
			rows = append(rows, l)
		}
	}
	return rows
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestTUIShowsTheRankingBestFirst(t *testing.T) {
	h := newTUI(t, "")
	rows := h.rows()
	want := []string{"Google Chrome 141.0", "Brave 1.84", "Google Chrome 141.0", "Google Chrome Canary 143.0", "Claude", "Slack"}
	if len(rows) != len(want) {
		t.Fatalf("rows:\n%s", h.view())
	}
	for i, w := range want {
		if !strings.Contains(rows[i], w) {
			t.Fatalf("row %d %q lacks %q:\n%s", i, rows[i], w, h.view())
		}
	}
	checks := map[int][]string{
		0: {"›", "CDP :9222", "seven open", "best match"},
		1: {"CDP :9333"},
		2: {"installed · profile chrome"},
		3: {"installed · new SLICC profile chrome-canary"},
		4: {"Electron", "debugging disabled"},
		5: {"installed · Electron"},
	}
	for i, subs := range checks {
		for _, s := range subs {
			if !strings.Contains(rows[i], s) {
				t.Errorf("row %d %q lacks %q", i, rows[i], s)
			}
		}
	}
	if strings.Contains(rows[1], "seven open") || strings.Contains(rows[1], "best match") {
		t.Errorf("row 1 %q", rows[1])
	}
	if strings.Contains(h.view(), "\x1b[") {
		t.Errorf("ANSI codes with color off: %q", h.view())
	}
	if !strings.Contains(h.view(), "space select") || !strings.Contains(h.view(), "p profile") {
		t.Errorf("no key help:\n%s", h.view())
	}
}

func TestTUIEnterGoesWithTheHighlightedRow(t *testing.T) {
	h := newTUI(t, "")
	h.keys("down", "up", "down")
	if cmd := h.keys("enter"); isQuit(cmd) {
		t.Fatal("enter quit")
	}
	if len(h.started) != 1 || len(h.started[0].Entries) != 1 || h.started[0].Entries[0].ID != "cdp:127.0.0.1:9333" {
		t.Fatalf("started %+v", h.started)
	}
	if h.model().Mode() != tui.ModeDashboard {
		t.Fatalf("mode %s", h.model().Mode())
	}
}

func TestTUIMultiSelectHostsSevenInTheFirstSelected(t *testing.T) {
	h := newTUI(t, "")
	h.keys("down", "space", "down", "space", "down", "down", "down", "space")
	if got := h.model().Selected(); strings.Join(got, ",") != "cdp:127.0.0.1:9333,chrome,electron:slack" {
		t.Fatalf("selected %v", got)
	}
	rows := h.rows()
	if !strings.Contains(rows[1], "● ") || !strings.Contains(rows[1], "hosts seven") || !strings.Contains(rows[2], "extra cdp #1") || !strings.Contains(rows[5], "extra cdp #2") {
		t.Fatalf("rows:\n%s", h.view())
	}
	if strings.Contains(rows[0], "best match") {
		t.Errorf("best match still shown with a selection: %q", rows[0])
	}
	h.keys("up", "space")
	if h.model().Message() == "" || len(h.model().Selected()) != 3 {
		t.Fatalf("selecting Claude: message %q, selected %v", h.model().Message(), h.model().Selected())
	}
	h.keys("up", "up", "space")
	if got := h.model().Selected(); strings.Join(got, ",") != "cdp:127.0.0.1:9333,electron:slack" {
		t.Fatalf("after unselecting chrome: %v", got)
	}
	h.keys("enter")
	if len(h.started) != 1 {
		t.Fatalf("started %+v", h.started)
	}
	ids := []string{}
	for _, e := range h.started[0].Entries {
		ids = append(ids, e.ID)
	}
	if strings.Join(ids, ",") != "cdp:127.0.0.1:9333,electron:slack" {
		t.Fatalf("choice %v", ids)
	}
}

func TestTUIElectronCannotHostSeven(t *testing.T) {
	h := newTUI(t, "")
	h.keys("down", "down", "down", "down", "down", "space", "up", "up", "up", "up", "space", "enter")
	if len(h.started) != 0 || h.model().Mode() != tui.ModePick || !strings.Contains(h.view(), "Electron app and can't host seven") {
		t.Fatalf("started %v, mode %s:\n%s", h.started, h.model().Mode(), h.view())
	}
}

func TestTUIProfileChoice(t *testing.T) {
	h := newTUI(t, "")
	h.keys("p")
	if h.model().Mode() != tui.ModePick || !strings.Contains(h.model().Message(), "profiles are for browsers slicc launches") {
		t.Fatalf("p on an endpoint: mode %s, message %q", h.model().Mode(), h.model().Message())
	}
	h.keys("down", "down", "p")
	if h.model().Mode() != tui.ModeProfile {
		t.Fatalf("mode %s", h.model().Mode())
	}
	v := h.view()
	for _, s := range []string{"profile for Google Chrome", "chrome (default)", "work", "new profile…"} {
		if !strings.Contains(v, s) {
			t.Fatalf("profile view lacks %q:\n%s", s, v)
		}
	}
	h.keys("down", "enter")
	if h.model().ProfileFor("chrome") != "work" || h.model().Mode() != tui.ModePick {
		t.Fatalf("profile %q, mode %s", h.model().ProfileFor("chrome"), h.model().Mode())
	}
	if !strings.Contains(h.rows()[2], "installed · profile work") {
		t.Fatalf("row %q", h.rows()[2])
	}
	h.keys("down", "p", "down", "enter")
	if h.model().Mode() != tui.ModeNewProfile {
		t.Fatalf("mode %s:\n%s", h.model().Mode(), h.view())
	}
	h.keys("bad/name", "enter")
	if h.model().Mode() != tui.ModeNewProfile || !strings.Contains(h.view(), "not a profile name") {
		t.Fatalf("bad name accepted:\n%s", h.view())
	}
	h.keys("esc", "esc")
	if h.model().Mode() != tui.ModePick || h.model().ProfileFor("chrome-canary") != "" {
		t.Fatalf("esc: mode %s, profile %q", h.model().Mode(), h.model().ProfileFor("chrome-canary"))
	}
	h.keys("p", "down", "enter", "canary-test", "enter")
	if h.model().ProfileFor("chrome-canary") != "canary-test" {
		t.Fatalf("new profile %q:\n%s", h.model().ProfileFor("chrome-canary"), h.view())
	}
	if !strings.Contains(h.rows()[3], "new SLICC profile canary-test") {
		t.Fatalf("row %q", h.rows()[3])
	}
	h.keys("up", "space", "down", "space", "enter")
	if len(h.started) != 1 {
		t.Fatalf("started %v", h.started)
	}
	c := h.started[0]
	if c.Profiles["chrome"] != "work" || c.Profiles["chrome-canary"] != "canary-test" || len(c.Entries) != 2 {
		t.Fatalf("choice %+v", c)
	}
}

func TestTUIProfileFlagIsTheDefault(t *testing.T) {
	h := newTUI(t, "work")
	if !strings.Contains(h.rows()[2], "installed · profile work") || !strings.Contains(h.rows()[3], "new SLICC profile work") {
		t.Fatalf("rows:\n%s", h.view())
	}
}

func TestTUIDashboard(t *testing.T) {
	h := newTUI(t, "")
	h.send(tui.NoticeMsg("slicc 9.9.9 is available (you have 1.0.0); run `slicc update`"))
	h.keys("down", "down", "enter")
	if h.model().Mode() != tui.ModeDashboard {
		t.Fatalf("mode %s", h.model().Mode())
	}
	ev := func(e flow.Event) { h.send(tui.EventMsg(e)) }
	ev(flow.Event{Event: flow.Launched, Browser: "chrome", Name: "Google Chrome", Version: "141.0.7390.1", Role: flow.RoleSeven, CDP: "http://127.0.0.1:50000"})
	ev(flow.Event{Event: flow.Attached, Browser: "chrome", Name: "Google Chrome", Version: "141.0.7390.1", Role: flow.RoleSeven, Launched: true, CDP: "http://127.0.0.1:50000"})
	ev(flow.Event{Event: flow.Attached, Browser: "cdp:127.0.0.1:9333", Name: "Brave", Role: flow.RoleExtra, CDP: "http://127.0.0.1:9333"})
	ev(flow.Event{Event: flow.Serving, Caps: []string{"net", "http", "cdp", "ssh"}, Policy: "every public address, this machine, and the local network", Compute: "ssh via bash -c"})
	if !strings.Contains(h.view(), "waiting for seven") {
		t.Fatalf("before an offer:\n%s", h.view())
	}
	ev(flow.Event{Event: flow.Connecting})
	ev(flow.Event{Event: flow.Linked, Peer: "seven", Tab: "https://seven.sliccy.ai/", Caps: []string{"net", "http", "cdp"}, Policy: "every public address, this machine, and the local network"})
	h.now = h.now.Add(3*time.Minute + 12*time.Second)
	for range 3 {
		ev(flow.Event{Event: flow.StreamOpen, Stream: "tcp"})
	}
	ev(flow.Event{Event: flow.StreamDone, Stream: "tcp"})
	ev(flow.Event{Event: flow.StreamOpen, Stream: "cdp"})
	ev(flow.Event{Event: flow.Kernel, Kernel: "http://<port>.kernel.localhost/"})
	v := h.view()
	for _, s := range []string{
		"Google Chrome   141.0", "launched · CDP :50000  hosts seven",
		"Brave", "attached · CDP :9333  extra cdp",
		"https://seven.sliccy.ai/", `linked with "seven" · up 3m12s`,
		"network   net · http · cdp", "policy    every public address",
		"tcp 2 open, 3 total", "cdp 1 open, 1 total", "● kernel  http://<port>.kernel.localhost/",
		"slicc 9.9.9 is available", "● shell   ssh via bash -c", "c capabilities",
	} {
		if !strings.Contains(v, s) {
			t.Fatalf("dashboard lacks %q:\n%s", s, v)
		}
	}
	ev(flow.Event{Event: flow.BrowserGone, Browser: "cdp:127.0.0.1:9333", Name: "Brave", Role: flow.RoleExtra})
	if !strings.Contains(h.view(), "extra cdp  gone") {
		t.Fatalf("after browser-gone:\n%s", h.view())
	}
	ev(flow.Event{Event: flow.BrowserBack, Browser: "cdp:127.0.0.1:9333", Name: "Brave", Role: flow.RoleExtra})
	if strings.Contains(h.view(), "extra cdp  gone") || !strings.Contains(h.view(), "Brave is back") {
		t.Fatalf("after browser-back:\n%s", h.view())
	}
	ev(flow.Event{Event: flow.LinkLost, Reason: "page navigated"})
	if !strings.Contains(h.view(), "reconnecting") {
		t.Fatalf("after link-lost:\n%s", h.view())
	}
	if isQuit(h.keys("q")) || h.stopped != 1 || h.model().Mode() != tui.ModeClosing || !strings.Contains(h.view(), "closing…") {
		t.Fatalf("q: stopped %d, mode %s", h.stopped, h.model().Mode())
	}
	h.keys("ctrl+c")
	if h.stopped != 1 {
		t.Fatalf("stop called %d times", h.stopped)
	}
	ev(flow.Event{Event: flow.Closed, Browsers: []string{"chrome"}})
	if cmd := h.send(tui.DoneMsg{}); !isQuit(cmd) {
		t.Fatal("DoneMsg did not quit")
	}
}

func TestTUIQuitBeforeGoing(t *testing.T) {
	h := newTUI(t, "")
	if !isQuit(h.keys("q")) || len(h.started) != 0 {
		t.Fatal("q in the picker did not quit")
	}
	h = newTUI(t, "")
	if !isQuit(h.keys("ctrl+c")) {
		t.Fatal("ctrl+c in the picker did not quit")
	}
	d := tui.New(tui.Config{})
	if d.Mode() != tui.ModeDiscover || !strings.Contains(d.View().Content, "looking for browsers") {
		t.Fatalf("discover mode %s", d.Mode())
	}
	m, _ := d.Update(tui.EntriesMsg(nil))
	if !strings.Contains(m.View().Content, "no browsers found") {
		t.Fatalf("empty list:\n%s", m.View().Content)
	}
}

func TestTUIScrollsALongList(t *testing.T) {
	h := newTUI(t, "")
	h.send(tea.WindowSizeMsg{Width: 100, Height: 9})
	if rows := h.rows(); len(rows) != 3 || !strings.Contains(h.view(), "↓ 3 more") || strings.Contains(h.view(), "↑ 3 more") {
		t.Fatalf("top:\n%s", h.view())
	}
	h.keys("down", "down", "down", "down", "down")
	rows := h.rows()
	if len(rows) != 3 || !strings.Contains(rows[2], "Slack") || !strings.Contains(rows[2], "›") || !strings.Contains(h.view(), "↑ 3 more") {
		t.Fatalf("bottom:\n%s", h.view())
	}
}

type appliedCaps struct {
	s      tui.Settings
	relink bool
}

func TestTUICapabilities(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := &tuiHarness{t: t, now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	var applied []appliedCaps
	live := false
	h.m = tui.New(tui.Config{
		Entries:     tuiEntries(),
		Start:       func(c tui.Choice) { h.started = append(h.started, c) },
		Stop:        func() { h.stopped++ },
		Now:         func() time.Time { return h.now },
		Settings:    tui.Settings{Jerk: true, Kernel: 80},
		JerkVersion: "0.6.2",
		Runner:      []string{"sh", "-c"},
		CheckRunner: func([]string) error { return nil },
		LiveMounts:  func() bool { return live },
		Apply:       func(s tui.Settings, relink bool) { applied = append(applied, appliedCaps{s, relink}) },
	})
	h.keys("down", "down", "enter")
	h.send(tui.EventMsg(flow.Event{Event: flow.Serving, Caps: []string{"net", "http", "jerk", "jerk-seal", "cdp"}, Policy: "p"}))
	h.keys("c")
	if h.model().Mode() != tui.ModeCaps || !strings.Contains(h.view(), "ALWAYS OFFERED  net · http · cdp") {
		t.Fatalf("c: mode %s\n%s", h.model().Mode(), h.view())
	}
	h.keys("down", "space", "a", dir+"/project:proj:ro", "enter")
	v := h.view()
	for _, s := range []string{"+ shell sh -c", "+ folder proj", "applying relinks seven", "read-only"} {
		if !strings.Contains(v, s) {
			t.Fatalf("pending lacks %q:\n%s", s, v)
		}
	}
	h.keys("s")
	if len(applied) != 1 || !applied[0].relink || strings.Join(applied[0].s.Shell, " ") != "sh -c" ||
		len(applied[0].s.Folders) != 1 || applied[0].s.Folders[0].Name != "proj" || !applied[0].s.Folders[0].ReadOnly ||
		applied[0].s.Folders[0].Path != filepath.Join(dir, "project") || !applied[0].s.Jerk || applied[0].s.Kernel != 80 {
		t.Fatalf("first apply %+v", applied)
	}
	if h.model().Mode() != tui.ModeDashboard || !strings.Contains(h.view(), "relinking") {
		t.Fatalf("after s: mode %s\n%s", h.model().Mode(), h.view())
	}
	live = true
	h.keys("c", "down", "down", "down", "r")
	if !strings.Contains(h.view(), "applies live") {
		t.Fatalf("read-only toggle on a shared folder should apply live:\n%s", h.view())
	}
	h.keys("s")
	if len(applied) != 2 || applied[1].relink || applied[1].s.Folders[0].ReadOnly {
		t.Fatalf("live apply %+v", applied)
	}
	h.keys("c", "down", "down", "down", "x", "esc")
	if !strings.Contains(h.view(), "apply these changes?") {
		t.Fatalf("esc with pending changes:\n%s", h.view())
	}
	h.keys("n")
	if h.model().Mode() != tui.ModeDashboard || len(applied) != 2 {
		t.Fatalf("n should discard: mode %s, %d applies", h.model().Mode(), len(applied))
	}
	h.keys("c", "down", "down", "enter")
	h.send(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	h.keys("abc", "enter")
	if !strings.Contains(h.view(), `"abc" is not a port`) {
		t.Fatalf("bad port:\n%s", h.view())
	}
	h.keys("esc", "esc")
	if h.model().Mode() != tui.ModeDashboard {
		t.Fatalf("esc twice: mode %s", h.model().Mode())
	}
	h.keys("m", dir+"/nope", "enter")
	if !strings.Contains(h.view(), "is not an existing folder") || h.model().Mode() != tui.ModeCaps {
		t.Fatalf("m with a missing folder:\n%s", h.view())
	}
}

func TestTUIDashboardKeepsRelinkingOnItsOwnBye(t *testing.T) {
	h := newTUI(t, "")
	h.keys("down", "down", "enter")
	h.send(tui.EventMsg(flow.Event{Event: flow.Linked, Peer: "seven", Caps: []string{"net"}}))
	h.send(tui.EventMsg(flow.Event{Event: flow.LinkLost, Reason: flow.RelinkReason}))
	if v := h.view(); !strings.Contains(v, "relinking") || strings.Contains(v, "reconnecting") {
		t.Fatalf("after its own relink bye:\n%s", v)
	}
}

func TestTUIRunnerKeepsArgumentBoundaries(t *testing.T) {
	h := &tuiHarness{t: t, now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	var applied []tui.Settings
	h.m = tui.New(tui.Config{
		Entries:     tuiEntries(),
		Start:       func(c tui.Choice) { h.started = append(h.started, c) },
		Now:         func() time.Time { return h.now },
		Settings:    tui.Settings{Shell: []string{"bash", "-c", "echo foo"}},
		CheckRunner: func([]string) error { return nil },
		Apply:       func(s tui.Settings, _ bool) { applied = append(applied, s) },
	})
	h.keys("down", "down", "enter", "c", "down", "enter")
	if !strings.Contains(h.view(), `bash -c 'echo foo'`) {
		t.Fatalf("the runner editor:\n%s", h.view())
	}
	h.keys("enter")
	if strings.Contains(h.view(), "PENDING") {
		t.Fatalf("an unchanged runner shows as a change:\n%s", h.view())
	}
	h.keys("down", "space", "s")
	if len(applied) != 1 || !slices.Equal(applied[0].Shell, []string{"bash", "-c", "echo foo"}) {
		t.Fatalf("applied %+v", applied)
	}
}
