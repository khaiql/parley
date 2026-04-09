package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/protocol"
)

// StatusBar renders the bottom status line.
type StatusBar struct {
	connected      bool
	yolo           bool
	width          int
	participants   []participantIcon // compact strip shown when sidebar is hidden
	sidebarVisible bool
	scrollPercent  float64
	atBottom       bool
	mouseEnabled   bool // true = scroll wheel active, false = text selection mode
}

type participantIcon struct {
	icon   string
	name   string
	status string // non-empty when agent is active (e.g. "thinking", "using tool")
}

// NewStatusBar creates a StatusBar with default values.
func NewStatusBar() StatusBar {
	return StatusBar{connected: true, atBottom: true, mouseEnabled: true}
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

// SetSidebarVisible tells the status bar whether the sidebar is currently shown.
// When visible, the left slot shows key hints instead of the compact participant strip.
func (s *StatusBar) SetSidebarVisible(v bool) {
	s.sidebarVisible = v
}

// SetMouseEnabled sets whether mouse capture is active. The key hints adapt:
// when enabled the hint offers "Ctrl+M select", when disabled "Ctrl+M scroll".
func (s *StatusBar) SetMouseEnabled(v bool) {
	s.mouseEnabled = v
}

// SetScrollPosition updates the scroll indicator.
func (s *StatusBar) SetScrollPosition(percent float64, atBottom bool) {
	s.scrollPercent = percent
	s.atBottom = atBottom
}

// SetCompactParticipants sets the participant icons shown in the left slot
// when the sidebar is hidden (narrow terminal). Pass nil or empty to clear.
// statuses maps participant name to their current activity status string.
func (s *StatusBar) SetCompactParticipants(participants []protocol.Participant, statuses map[string]string) {
	s.participants = nil
	for _, p := range participants {
		if !p.Online {
			continue
		}
		icon := "◆"
		if p.IsHuman() {
			icon = "◇"
		}
		status := ""
		if statuses != nil && statuses[p.Name] != "" && statuses[p.Name] != protocol.StatusListening {
			status = statuses[p.Name]
		}
		s.participants = append(s.participants, participantIcon{icon: icon, name: p.Name, status: status})
	}
}

// View renders the status bar as a single line.
func (s StatusBar) View() string {
	barBg := lipgloss.NewStyle().Background(colorSidebarBg)

	// Left slot: YOLO badge + either key hints (sidebar visible) or compact participant strip.
	var left string
	if s.yolo {
		yoloStyle := lipgloss.NewStyle().
			Bold(true).
			Background(colorStatusBarBg).
			Foreground(lipgloss.Color("#d29922")).
			Padding(0, 1)
		left += yoloStyle.Render("⚡ YOLO")
	}

	if s.sidebarVisible {
		// Show key hints when sidebar is already showing participant info.
		// Ctrl+M hint adapts to current mouse mode.
		mouseHint := `Ctrl+\ select`
		if !s.mouseEnabled {
			mouseHint = `Ctrl+\ scroll`
		}
		hints := []string{"/ commands", "@ mention", mouseHint}
		var hintParts []string
		for _, h := range hints {
			c := colorDimText
			if !s.mouseEnabled && h == mouseHint {
				c = colorSystem // highlight when in text-selection mode
			}
			hintParts = append(hintParts, barBg.Foreground(c).Render(h))
		}
		sep := barBg.Foreground(colorSeparator).Render(" · ")
		left += barBg.Padding(0, 1).Render(strings.Join(hintParts, sep))
	} else if len(s.participants) > 0 {
		// Compact participant strip when sidebar is hidden.
		parts := make([]string, len(s.participants))
		for i, p := range s.participants {
			nameStr := barBg.Foreground(colorDimText).Render(p.icon + " " + p.name)
			if p.status != "" {
				sepStr := barBg.Foreground(colorSeparator).Render(" ·")
				act := barBg.Foreground(colorSystem).Italic(true).Render(" " + p.status)
				parts[i] = nameStr + sepStr + act
			} else {
				parts[i] = nameStr
			}
		}
		strip := strings.Join(parts, barBg.Foreground(colorSeparator).Render("  "))
		left += barBg.Padding(0, 1).Render(strip)
	}

	// Right slot: scroll position when not at bottom; disconnected indicator when offline.
	// Connected dot removed — a permanently green dot is visual noise.
	var right string
	if !s.connected {
		disconnStyle := barBg.Foreground(lipgloss.Color("#f85149")).Padding(0, 1)
		right = disconnStyle.Render("● disconnected")
	} else if !s.atBottom {
		pct := int(s.scrollPercent * 100)
		scrollStyle := barBg.Foreground(colorDimText).Padding(0, 1)
		right = scrollStyle.Render(fmt.Sprintf("↑ %d%%", pct))
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
