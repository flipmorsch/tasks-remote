package tui

import (
	"os"

	"github.com/charmbracelet/lipgloss"
)

type theme struct {
	header       lipgloss.Style
	title        lipgloss.Style
	section      lipgloss.Style
	panel        lipgloss.Style
	selected     lipgloss.Style
	muted        lipgloss.Style
	error        lipgloss.Style
	notice       lipgloss.Style
	badge        lipgloss.Style
	warningBadge lipgloss.Style
	key          lipgloss.Style
}

func currentTheme() theme {
	if noColor() {
		return theme{
			header:       lipgloss.NewStyle().Bold(true),
			title:        lipgloss.NewStyle().Bold(true),
			section:      lipgloss.NewStyle().Bold(true),
			panel:        lipgloss.NewStyle().Padding(0, 1),
			selected:     lipgloss.NewStyle().Bold(true),
			muted:        lipgloss.NewStyle(),
			error:        lipgloss.NewStyle(),
			notice:       lipgloss.NewStyle(),
			badge:        lipgloss.NewStyle().Bold(true),
			warningBadge: lipgloss.NewStyle().Bold(true),
			key:          lipgloss.NewStyle().Bold(true),
		}
	}
	return theme{
		header:       lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")),
		title:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252")),
		section:      lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")),
		panel:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1),
		selected:     lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")),
		muted:        lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
		error:        lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		notice:       lipgloss.NewStyle().Foreground(lipgloss.Color("42")),
		badge:        lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("42")).Padding(0, 1),
		warningBadge: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Padding(0, 1),
		key:          lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")),
	}
}

func noColor() bool {
	return os.Getenv("NO_COLOR") != ""
}
