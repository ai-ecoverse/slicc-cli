package tui

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/ai-ecoverse/slicc-cli/internal/browser"
)

const newProfileRow = "new profile…"

type profilePicker struct {
	entry  browser.Entry
	rows   []string
	def    string
	cursor int
	input  textinput.Model
}

func (m *Model) loadProfiles() {
	m.known = map[string][]string{}
	for _, e := range m.entries {
		if e.Kind == browser.KindInstalled && e.Installed.Engine == browser.EngineChromium {
			m.known[e.ID] = m.cfg.Profiles(e)
		}
	}
}

func (m *Model) openProfiles() {
	if len(m.entries) == 0 {
		return
	}
	e := m.entries[m.cursor]
	if e.Kind == browser.KindSix {
		m.message = "SLICC six's profile is used as it is; it has no SLICC profiles to choose"
		return
	}
	if e.Kind != browser.KindInstalled || e.Installed.Engine != browser.EngineChromium {
		m.message = "profiles are for browsers slicc launches; " + e.Name + " is not one"
		return
	}
	def := browser.DefaultProfileName(*e.Installed)
	if m.cfg.Profile != "" {
		def = m.cfg.Profile
	}
	rows := []string{def}
	for _, p := range m.profileList(e) {
		if p != def {
			rows = append(rows, p)
		}
	}
	rows = append(rows, newProfileRow)
	cursor := 0
	if cur := m.profiles[e.ID]; cur != "" {
		if i := slices.Index(rows, cur); i >= 0 {
			cursor = i
		}
	}
	m.prof = profilePicker{entry: e, rows: rows, def: def, cursor: cursor, input: newInput()}
	m.mode = ModeProfile
}

func (m Model) profileKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.message = ""
	switch k.String() {
	case "up", "k":
		if m.prof.cursor > 0 {
			m.prof.cursor--
		}
	case "down", "j":
		if m.prof.cursor < len(m.prof.rows)-1 {
			m.prof.cursor++
		}
	case "esc", "q":
		m.mode = ModePick
	case "enter":
		row := m.prof.rows[m.prof.cursor]
		if row == newProfileRow {
			m.mode = ModeNewProfile
			return m, m.prof.input.Focus()
		}
		m.setProfile(row)
		m.mode = ModePick
	}
	return m, nil
}

func (m *Model) setProfile(name string) {
	id := m.prof.entry.ID
	if name == m.prof.def && m.cfg.Profile == "" {
		delete(m.profiles, id)
		return
	}
	m.profiles[id] = name
}

func (m Model) newProfileKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = ModeProfile
		m.message = ""
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.prof.input.Value())
		if !browser.ValidProfileName(name) {
			m.message = fmt.Sprintf("%q is not a profile name: use letters, digits, '.', '_' and '-'", name)
			return m, nil
		}
		m.setProfile(name)
		m.message = ""
		m.mode = ModePick
		return m, nil
	}
	var cmd tea.Cmd
	m.prof.input, cmd = m.prof.input.Update(k)
	return m, cmd
}

func (m Model) profileView() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · profile for %s    %s\n", m.st.title.Render("slicc"), m.prof.entry.Name, m.st.faint.Render("↑↓ move · enter choose · esc back"))
	current := m.profiles[m.prof.entry.ID]
	if current == "" {
		current = m.prof.def
	}
	for i, r := range m.prof.rows {
		pointer := "  "
		if i == m.prof.cursor {
			pointer = m.st.cursor.Render("›") + " "
		}
		mark := "○"
		if r == current {
			mark = m.st.selected.Render("●")
		}
		label := r
		if r == m.prof.def {
			label += m.st.faint.Render(" (default)")
		}
		fmt.Fprintf(&b, "%s%s %s\n", pointer, mark, label)
	}
	if m.mode == ModeNewProfile {
		b.WriteString("\n  " + m.prof.input.View() + "\n")
	}
	if m.message != "" {
		b.WriteString("\n" + m.st.bad.Render(m.message) + "\n")
	}
	return b.String()
}
