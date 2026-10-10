package tui

import (
	"charm.land/lipgloss/v2"
)

type styles struct {
	title    lipgloss.Style
	faint    lipgloss.Style
	cursor   lipgloss.Style
	selected lipgloss.Style
	good     lipgloss.Style
	warn     lipgloss.Style
	bad      lipgloss.Style
}

func newStyles(color bool) styles {
	if !color {
		p := lipgloss.NewStyle()
		return styles{title: p, faint: p, cursor: p, selected: p, good: p, warn: p, bad: p}
	}
	return styles{
		title:    lipgloss.NewStyle().Bold(true),
		faint:    lipgloss.NewStyle().Faint(true),
		cursor:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		selected: lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		good:     lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		warn:     lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
		bad:      lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
	}
}
