package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const wideLayoutMinWidth = 96

func joinPanels(left, right string, leftWidth, rightWidth int) string {
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftWidth).Render(left),
		" ",
		lipgloss.NewStyle().Width(rightWidth).Render(right),
	)
}

func renderHint(parts ...string) string {
	return strings.Join(parts, "  ")
}
