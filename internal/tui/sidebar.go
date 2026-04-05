package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/protocol"
)

// spinnerFrames are braille characters used for the generating animation.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Sidebar renders the participant list panel.
type Sidebar struct {
	participants []protocol.Participant
	statuses     map[string]string // per-participant status text
	width        int
	height       int
	port         int
	spinnerFrame int
}

// NewSidebar creates an empty Sidebar.
func NewSidebar() Sidebar {
	return Sidebar{statuses: make(map[string]string)}
}

// SetPort sets the port number displayed in the branding section.
func (s *Sidebar) SetPort(port int) {
	s.port = port
}

// TickSpinner advances the spinner frame and returns true if any participant
// has "generating" status (caller should keep ticking).
func (s *Sidebar) TickSpinner() bool {
	s.spinnerFrame = (s.spinnerFrame + 1) % len(spinnerFrames)
	for _, p := range s.participants {
		if s.statuses[p.Name] == "generating" {
			return true
		}
	}
	return false
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
func (s Sidebar) View() string {
	innerWidth := s.width - 4 // account for border + padding
	if innerWidth < 1 {
		innerWidth = 1
	}

	var lines []string

	// Branding section
	brand := sidebarBrandStyle.Width(innerWidth).Render("parley")
	lines = append(lines, brand)

	// Section header
	lines = append(lines, sidebarTitleStyle.Render("PARTICIPANTS"))

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
		// Separator between participants
		lines = append(lines, separatorStyle.Render(strings.Repeat("─", innerWidth)))

		if !p.Online {
			nameLine := offlineNameStyle.Render(p.Name + " (offline)")
			lines = append(lines, nameLine)
			continue
		}

		// Name line
		var nameLine string
		if p.IsHuman() {
			nameLine = humanNameStyle.Render(p.Name)
		} else {
			senderColor := ColorForIndex(p.ColorIndex)
			nameLine = agentNameStyleFor(senderColor).Render(p.Name)
		}

		// AgentType badge (instead of Role badge)
		if p.AgentType != "" {
			senderColor := ColorForIndex(p.ColorIndex)
			badge := agentBadgeStyleFor(senderColor).Render(p.AgentType)
			nameLine = lipgloss.JoinHorizontal(lipgloss.Top, nameLine, " ", badge)
		}
		lines = append(lines, nameLine)

		// Status display
		status := s.statuses[p.Name]
		if status == "generating" {
			senderColor := ColorForIndex(p.ColorIndex)
			frame := spinnerFrames[s.spinnerFrame%len(spinnerFrames)]
			statusText := agentNameStyleFor(senderColor).Render(frame + " generating")
			lines = append(lines, "  "+statusText)
		} else if status != "" && status != "listening" {
			lines = append(lines, participantStatusStyle.Render("  "+status))
		}

		// Directory
		if p.Directory != "" {
			dir := p.Directory
			maxLen := innerWidth - 2
			if maxLen > 4 && len(dir) > maxLen {
				dir = dir[:maxLen-1] + "…"
			}
			lines = append(lines, timestampStyle.Render("  "+dir))
		}
	}

	content := strings.Join(lines, "\n")
	return sidebarStyle.Width(s.width).Height(s.height).Render(content)
}
