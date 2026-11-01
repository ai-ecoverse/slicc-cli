package tui

import (
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
	"github.com/ai-ecoverse/slicc-cli/internal/flow"
)

type Config struct {
	Entries  []browser.Entry
	Profiles func(browser.Entry) []string
	Profile  string
	Last     browser.Last
	Dir      func(browser.Entry, string) string
	Start    func(Choice)
	Stop     func()
	Color    bool
	Notice   string
	Now      func() time.Time

	Settings    Settings
	JerkVersion string
	Runner      []string
	CheckRunner func([]string) error
	LiveMounts  func() bool
	Apply       func(s Settings, relink bool)
	NewPairing  func()
}

type Choice struct {
	Entries  []browser.Entry
	Profiles map[string]string
}

type EntriesMsg []browser.Entry

type EventMsg flow.Event

type DoneMsg struct{ Err error }

type NoticeMsg string

type tickMsg time.Time

type Mode string

const (
	ModeDiscover   Mode = "discover"
	ModePick       Mode = "pick"
	ModeProfile    Mode = "profile"
	ModeNewProfile Mode = "new-profile"
	ModeDashboard  Mode = "dashboard"
	ModeCaps       Mode = "caps"
	ModeClosing    Mode = "closing"
	ModeDone       Mode = "done"
)

type Model struct {
	cfg      Config
	st       styles
	mode     Mode
	entries  []browser.Entry
	cursor   int
	moved    bool
	selected []string
	profiles map[string]string
	known    map[string][]string
	prof     profilePicker
	message  string
	dash     dashboard
	settings Settings
	caps     capsEditor
	err      error
	height   int
	width    int
	dark     bool
	order    []group
	open     map[group]bool
	went     bool
	last     string
	used     map[string]time.Time
	note     string
}

func New(cfg Config) Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Profiles == nil {
		cfg.Profiles = func(browser.Entry) []string { return nil }
	}
	m := Model{cfg: cfg, st: newStyles(cfg.Color, true), dark: true, mode: ModeDiscover, profiles: map[string]string{}, known: map[string][]string{}, open: map[group]bool{}}
	m.dash = newDashboard(cfg.Now())
	m.dash.notice = cfg.Notice
	m.settings = cfg.Settings.clone()
	if cfg.Entries != nil {
		m.setEntries(cfg.Entries)
		m.mode = ModePick
	}
	return m
}

func (m *Model) applyLast() {
	l := m.cfg.Last
	if l.ID == "" || l.Profile == "" || m.cfg.Profile != "" || m.profiles[l.ID] != "" {
		return
	}
	for _, e := range m.entries {
		if e.ID == l.ID && e.Kind == browser.KindInstalled && e.Installed.Engine == browser.EngineChromium &&
			l.Profile != browser.DefaultProfileName(*e.Installed) && slices.Contains(m.profileList(e), l.Profile) {
			m.profiles[l.ID] = l.Profile
		}
	}
}

func (m Model) Mode() Mode { return m.mode }

func (m Model) Cursor() int { return m.cursor }

func (m Model) Selected() []string { return slices.Clone(m.selected) }

func (m Model) ProfileFor(id string) string { return m.profiles[id] }

func (m Model) Message() string { return m.message }

func (m Model) Err() error { return m.err }

func (m Model) Init() tea.Cmd {
	if !m.cfg.Color {
		return nil
	}
	return tea.RequestBackgroundColor
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case EntriesMsg:
		m.moved = false
		m.setEntries([]browser.Entry(msg))
		if m.mode == ModeDiscover {
			m.mode = ModePick
		}
		return m, nil
	case NoticeMsg:
		m.dash.notice = string(msg)
		return m, nil
	case EventMsg:
		m.dash.apply(flow.Event(msg), m.cfg.Now())
		return m, nil
	case DoneMsg:
		m.err = msg.Err
		m.mode = ModeDone
		return m, tea.Quit
	case tickMsg:
		if m.mode == ModeDashboard || m.mode == ModeCaps || m.mode == ModeClosing {
			return m, tick()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.height, m.width = msg.Height, msg.Width
		return m, nil
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		m.st = newStyles(m.cfg.Color, m.dark)
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	if m.mode == ModeNewProfile {
		var cmd tea.Cmd
		m.prof.input, cmd = m.prof.input.Update(msg)
		return m, cmd
	}
	if m.mode == ModeCaps && m.caps.editing != inputNone {
		var cmd tea.Cmd
		m.caps.input, cmd = m.caps.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m.quit()
	}
	switch m.mode {
	case ModeDiscover:
		if k.String() == "q" {
			return m.quit()
		}
	case ModePick:
		return m.pickKey(k)
	case ModeProfile:
		return m.profileKey(k)
	case ModeNewProfile:
		return m.newProfileKey(k)
	case ModeDashboard:
		if m.dash.pair {
			return m.pairKey(k)
		}
		switch k.String() {
		case "q":
			return m.quit()
		case "c":
			m.openCaps()
		case "m", "a":
			return m.startInput(inputFolder, -1, "")
		}
	case ModeCaps:
		return m.capsKey(k)
	}
	return m, nil
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeDashboard, ModeCaps:
		m.mode = ModeClosing
		if m.cfg.Stop != nil {
			m.cfg.Stop()
		}
		return m, nil
	case ModeClosing, ModeDone:
		return m, nil
	}
	m.mode = ModeDone
	return m, tea.Quit
}

const sixUnpicked = "SLICC six's profile is never picked for you; move to it, or select it with space"

func (m Model) View() tea.View {
	var s string
	switch m.mode {
	case ModeDiscover:
		s = m.st.title.Render("slicc") + " · looking for browsers…\n\n" + m.st.faint.Render("q quit") + "\n"
	case ModePick:
		s = m.pickView()
	case ModeProfile, ModeNewProfile:
		s = m.profileView()
	case ModeCaps:
		s = m.capsView()
	case ModeDone:
		if m.went {
			s = m.dashView()
		}
	default:
		s = m.dashView()
	}
	return tea.NewView(s)
}

func shortVersion(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
}

func (m Model) profileList(e browser.Entry) []string {
	if l, ok := m.known[e.ID]; ok {
		return l
	}
	return m.cfg.Profiles(e)
}

func hostPort(cdp string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(cdp, "http://"), "https://")
	s, _, _ = strings.Cut(s, "/")
	if h, p, ok := strings.Cut(s, ":"); ok && (h == "127.0.0.1" || h == "localhost") {
		return ":" + p
	}
	return s
}

func newInput() textinput.Model {
	in := textinput.New()
	in.Placeholder = "profile name"
	in.CharLimit = 64
	in.SetWidth(32)
	return in
}
