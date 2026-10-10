package tui

import (
	"fmt"
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
	Start    func(Choice)
	Stop     func()
	Color    bool
	Notice   string
	Now      func() time.Time
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
	ModeClosing    Mode = "closing"
	ModeDone       Mode = "done"
)

type Model struct {
	cfg      Config
	st       styles
	mode     Mode
	entries  []browser.Entry
	cursor   int
	selected []string
	profiles map[string]string
	known    map[string][]string
	prof     profilePicker
	message  string
	dash     dashboard
	err      error
	height   int
}

func New(cfg Config) Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Profiles == nil {
		cfg.Profiles = func(browser.Entry) []string { return nil }
	}
	m := Model{cfg: cfg, st: newStyles(cfg.Color), mode: ModeDiscover, profiles: map[string]string{}, known: map[string][]string{}}
	m.dash = newDashboard(cfg.Now())
	m.dash.notice = cfg.Notice
	if cfg.Entries != nil {
		m.entries = cfg.Entries
		m.mode = ModePick
		m.loadProfiles()
	}
	return m
}

func (m Model) Mode() Mode { return m.mode }

func (m Model) Cursor() int { return m.cursor }

func (m Model) Selected() []string { return slices.Clone(m.selected) }

func (m Model) ProfileFor(id string) string { return m.profiles[id] }

func (m Model) Message() string { return m.message }

func (m Model) Err() error { return m.err }

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case EntriesMsg:
		m.entries = []browser.Entry(msg)
		m.cursor = 0
		m.loadProfiles()
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
		if m.mode == ModeDashboard || m.mode == ModeClosing {
			return m, tick()
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	if m.mode == ModeNewProfile {
		var cmd tea.Cmd
		m.prof.input, cmd = m.prof.input.Update(msg)
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
		if k.String() == "q" {
			return m.quit()
		}
	}
	return m, nil
}

func (m Model) quit() (tea.Model, tea.Cmd) {
	switch m.mode {
	case ModeDashboard:
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

func (m Model) pickKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.message = ""
	switch k.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.entries)-1 {
			m.cursor++
		}
	case "space":
		m.toggle()
	case "p":
		m.openProfiles()
	case "enter":
		return m.goChoice()
	case "q", "esc":
		return m.quit()
	}
	return m, nil
}

func (m *Model) toggle() {
	if len(m.entries) == 0 {
		return
	}
	e := m.entries[m.cursor]
	if i := slices.Index(m.selected, e.ID); i >= 0 {
		m.selected = slices.Delete(m.selected, i, i+1)
		return
	}
	if err := flow.CanExtra(e); err != nil {
		m.message = err.Error()
		return
	}
	m.selected = append(m.selected, e.ID)
}

func (m Model) choice() []browser.Entry {
	if len(m.selected) == 0 {
		if len(m.entries) == 0 {
			return nil
		}
		return []browser.Entry{m.entries[m.cursor]}
	}
	var out []browser.Entry
	for _, id := range m.selected {
		for _, e := range m.entries {
			if e.ID == id {
				out = append(out, e)
			}
		}
	}
	return out
}

func (m Model) goChoice() (tea.Model, tea.Cmd) {
	sel := m.choice()
	if err := flow.Check(sel); err != nil {
		m.message = err.Error()
		return m, nil
	}
	profiles := map[string]string{}
	for _, e := range sel {
		if p := m.profiles[e.ID]; p != "" {
			profiles[e.ID] = p
		}
	}
	notice := m.dash.notice
	m.mode = ModeDashboard
	m.dash = newDashboard(m.cfg.Now())
	m.dash.notice = notice
	if m.cfg.Start != nil {
		m.cfg.Start(Choice{Entries: sel, Profiles: profiles})
	}
	return m, tick()
}

func (m Model) View() tea.View {
	var s string
	switch m.mode {
	case ModeDiscover:
		s = m.st.title.Render("slicc") + " · looking for browsers…\n\n" + m.st.faint.Render("q quit") + "\n"
	case ModePick:
		s = m.pickView()
	case ModeProfile, ModeNewProfile:
		s = m.profileView()
	default:
		s = m.dash.view(m.st, m.mode, m.cfg.Now())
	}
	return tea.NewView(s)
}

func (m Model) pickView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · pick browsers    %s\n", m.st.title.Render("slicc"), m.st.faint.Render("↑↓ move · space select · enter go · p profile · q quit"))
	if len(m.entries) == 0 {
		b.WriteString("\n  no browsers found; install Chrome, or start one with --remote-debugging-port\n")
	}
	start, end := m.window()
	if start > 0 {
		b.WriteString(m.st.faint.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(m.row(i, m.entries[i]))
		b.WriteByte('\n')
	}
	if end < len(m.entries) {
		b.WriteString(m.st.faint.Render(fmt.Sprintf("  ↓ %d more", len(m.entries)-end)) + "\n")
	}
	if m.message != "" {
		b.WriteString("\n" + m.st.bad.Render(m.message) + "\n")
	}
	return b.String()
}

func (m Model) window() (int, int) {
	n := len(m.entries)
	visible := m.height - 6
	if m.height <= 0 || n <= visible {
		return 0, n
	}
	visible = max(visible, 3)
	start := min(max(m.cursor-visible/2, 0), n-visible)
	return start, start + visible
}

func (m Model) row(i int, e browser.Entry) string {
	pointer := "  "
	if i == m.cursor {
		pointer = m.st.cursor.Render("›") + " "
	}
	mark := "○"
	tag := ""
	if at := slices.Index(m.selected, e.ID); at >= 0 {
		mark = m.st.selected.Render("●")
		tag = "hosts seven"
		if at > 0 {
			tag = fmt.Sprintf("extra cdp #%d", at)
		}
	} else if i == 0 && len(m.selected) == 0 {
		tag = "best match"
	}
	name := e.Name
	if e.Version != "" {
		name += " " + shortVersion(e.Version)
	}
	line := fmt.Sprintf("%s%s %-28s %s", pointer, mark, name, m.state(e))
	if tag != "" {
		line += "  " + m.st.faint.Render("← "+tag)
	}
	return line
}

func shortVersion(v string) string {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
}

func (m Model) state(e browser.Entry) string {
	if e.Kind == browser.KindEndpoint {
		s := "CDP " + hostPort(e.Endpoint.CDP)
		if e.Endpoint.SevenOpen {
			s += " · " + m.st.good.Render("seven open")
		}
		if e.Endpoint.WSOnly {
			s += " · websocket only"
		}
		return s
	}
	in := e.Installed
	if in.Engine == browser.EngineElectron {
		if in.DebugSupport == browser.DebugDisabled {
			return "installed · Electron · " + m.st.bad.Render("debugging disabled")
		}
		return "installed · Electron"
	}
	name := m.profiles[e.ID]
	if name == "" {
		name = browser.DefaultProfileName(*in)
		if m.cfg.Profile != "" {
			name = m.cfg.Profile
		}
	}
	if !slices.Contains(m.profileList(e), name) {
		return "installed · new SLICC profile " + name
	}
	return "installed · profile " + name
}

func (m Model) profileList(e browser.Entry) []string {
	if l, ok := m.known[e.ID]; ok {
		return l
	}
	return m.cfg.Profiles(e)
}

func hostPort(cdp string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(cdp, "http://"), "https://")
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
