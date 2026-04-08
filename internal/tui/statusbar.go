package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StatusBar renders the bottom status line.
type StatusBar struct {
	connected bool
	yolo      bool
	width     int
}

// NewStatusBar creates a StatusBar with default values.
func NewStatusBar() StatusBar {
	return StatusBar{connected: true}
}

// SetWidth sets the available width for rendering.
func (s *StatusBar) SetWidth(w int) {
	s.width = w
}

// SetConnected sets the connection status.
func (s *StatusBar) SetConnected(c bool) {
	s.connected = c
}

// SetYolo sets whether the room is in yolo/auto-approve mode.
func (s *StatusBar) SetYolo(y bool) {
	s.yolo = y
}

// View renders the status bar as a single line.
func (s StatusBar) View() string {
	barBg := lipgloss.NewStyle().Background(colorSidebarBg)

	// Left: optional YOLO badge.
	var left string
	if s.yolo {
		yoloStyle := lipgloss.NewStyle().
			Bold(true).
			Background(colorStatusBarBg).
			Foreground(lipgloss.Color("#d29922")).
			Padding(0, 1)
		left += yoloStyle.Render("⚡ YOLO")
	}

	// Right: connection status.
	var right string
	if s.connected {
		connStyle := barBg.Foreground(colorConnected).Padding(0, 1)
		right = connStyle.Render("● connected")
	} else {
		disconnStyle := barBg.Foreground(lipgloss.Color("#f85149")).Padding(0, 1)
		right = disconnStyle.Render("● disconnected")
	}

	// Fill gap.
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	gap := s.width - leftWidth - rightWidth
	if gap < 0 {
		gap = 0
	}
	fill := barBg.Render(strings.Repeat(" ", gap))

	return left + fill + right
}
