package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/protocol"
)

// Sidebar renders the participant list panel.
type Sidebar struct {
	participants []protocol.Participant
	statuses     map[string]string // per-participant status text
	width        int
	height       int
	port         int
	spinnerView  string
}

// NewSidebar creates an empty Sidebar.
func NewSidebar() Sidebar {
	return Sidebar{statuses: make(map[string]string)}
}

// SetPort sets the port number displayed in the branding section.
func (s *Sidebar) SetPort(port int) {
	s.port = port
}

// SetSpinnerView sets the spinner character to display next to active statuses.
func (s *Sidebar) SetSpinnerView(v string) {
	s.spinnerView = v
}

// SetSize updates the sidebar dimensions.
func (s *Sidebar) SetSize(width, height int) {
	s.width = width
	s.height = height
}

// SetParticipants replaces the full participant list.
func (s *Sidebar) SetParticipants(participants []protocol.Participant) {
	s.participants = participants
}

// AddParticipant appends a participant, replacing any existing entry with the
// same name.
func (s *Sidebar) AddParticipant(p protocol.Participant) {
	for i, existing := range s.participants {
		if existing.Name == p.Name {
			s.participants[i] = p
			return
		}
	}
	s.participants = append(s.participants, p)
}

// SetParticipantStatus updates the activity status for a named participant.
// An empty status string means the participant is idle (nothing is shown).
func (s *Sidebar) SetParticipantStatus(name, status string) {
	if s.statuses == nil {
		s.statuses = make(map[string]string)
	}
	s.statuses[name] = status
}

// RemoveParticipant removes a participant by name.
func (s *Sidebar) RemoveParticipant(name string) {
	filtered := s.participants[:0]
	for _, p := range s.participants {
		if p.Name != name {
			filtered = append(filtered, p)
		}
	}
	s.participants = filtered
}

// SetParticipantOffline marks a participant as offline rather than removing them.
func (s *Sidebar) SetParticipantOffline(name string) {
	for i, p := range s.participants {
		if p.Name == name {
			s.participants[i].Online = false
			break
		}
	}
}

// View renders the sidebar as a string.
// Returns an empty string when width is 0 (sidebar hidden on narrow terminals).
func (s Sidebar) View() string {
	if s.width == 0 {
		return ""
	}
	// innerWidth: subtract sidebarStyle border (1) + padding (2) = 3 chars.
	// Each participant card subtracts another 3 (thick border 1 + padding 1 + gap 1).
	innerWidth := s.width - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	cardWidth := innerWidth - 3
	if cardWidth < 1 {
		cardWidth = 1
	}

	var lines []string

	// Room count — elevated to colorText so it reads clearly.
	count := fmt.Sprintf("%d in room", len(s.participants))
	countBadge := lipgloss.NewStyle().
		Foreground(colorText).
		Bold(true).
		Align(lipgloss.Center).
		Width(innerWidth).
		Render(count)
	lines = append(lines, countBadge)

	// Sort: online participants first, then offline.
	online := make([]protocol.Participant, 0, len(s.participants))
	offline := make([]protocol.Participant, 0)
	for _, p := range s.participants {
		if p.Online {
			online = append(online, p)
		} else {
			offline = append(offline, p)
		}
	}
	sorted := append(online, offline...)

	for _, p := range sorted {
		// Visible separator between participant cards (colorBorder not colorSeparator).
		lines = append(lines, lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("─", innerWidth)))

		if !p.Online {
			card := offlineNameStyle.Render(p.Name + " (offline)")
			lines = append(lines, card)
			continue
		}

		senderColor := ColorForSender(p.Name, p.IsHuman(), p.Color)

		// Build card content: name, status, directory.
		icon := "◆"
		if p.IsHuman() {
			icon = "◇"
		}

		// Name line: white text so it reads on any sidebar bg regardless of
		// participant color — color identity comes from the accent strip.
		nameLine := lipgloss.NewStyle().Bold(true).Foreground(colorText).Render(icon + " " + p.Name)
		if p.AgentType != "" {
			badge := agentBadgeStyleFor(senderColor).Render(p.AgentType)
			nameLine = lipgloss.JoinHorizontal(lipgloss.Top, nameLine, " ", badge)
		}

		var cardLines []string
		cardLines = append(cardLines, nameLine)

		// Status with spinner prefix for all active states.
		status := s.statuses[p.Name]
		if status != "" && status != protocol.StatusListening {
			statusLine := lipgloss.NewStyle().
				Foreground(senderColor).
				Italic(true).
				Render(s.spinnerView + " " + status)
			cardLines = append(cardLines, statusLine)
		}

		// Directory — truncated and dimmed.
		if p.Directory != "" {
			dir := p.Directory
			if len(dir) > cardWidth && cardWidth > 4 {
				dir = dir[:cardWidth-1] + "…"
			}
			cardLines = append(cardLines, timestampStyle.Render(dir))
		}

		// Wrap all card lines in a colored thick left border — same visual
		// language as chat messages, ties the color to the participant card.
		card := lipgloss.NewStyle().
			BorderStyle(lipgloss.ThickBorder()).
			BorderLeft(true).
			BorderTop(false).BorderRight(false).BorderBottom(false).
			BorderForeground(senderColor).
			PaddingLeft(1).
			Width(cardWidth).
			Render(lipgloss.JoinVertical(lipgloss.Left, cardLines...))

		lines = append(lines, card)
	}

	content := strings.Join(lines, "\n")
	return sidebarStyle.Width(s.width).Height(s.height).Render(content)
}
