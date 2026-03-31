package tui

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sle/parley/internal/protocol"
)

const sidebarWidth = 28

// ServerMsg wraps an incoming raw protocol message from the network.
type ServerMsg struct {
	Raw *protocol.RawMessage
}

// AgentTypingMsg carries text to display in agent-typing mode.
type AgentTypingMsg struct {
	Text string
}

// AgentStatusMsg carries a status string (e.g. "thinking...", "using tool: ls")
// to display in the agent input area when no text is being streamed.
type AgentStatusMsg struct {
	Status string
}

// App is the root Bubble Tea model that composes all TUI components.
type App struct {
	topbar  TopBar
	chat    Chat
	sidebar Sidebar
	input   Input
	sendFn  func(string, []string) // callback to send messages over network
	width   int
	height  int
}

// NewApp creates an App with the given topic, port, input mode, display name,
// send callback, and optional initial participants (may be nil).
func NewApp(topic string, port int, mode InputMode, name string, sendFn func(string, []string), participants ...protocol.Participant) App {
	a := App{
		topbar:  NewTopBar(topic, port),
		chat:    NewChat(0, 0),
		sidebar: NewSidebar(),
		input:   NewInput(),
		sendFn:  sendFn,
	}
	a.input.SetMode(mode)
	if len(participants) > 0 {
		a.sidebar.SetParticipants(participants)
	}
	return a
}

// Init satisfies tea.Model. Returns textarea.Blink to animate the cursor.
func (a App) Init() tea.Cmd {
	return textarea.Blink
}

// Update satisfies tea.Model. Handles window sizing, key events, server
// messages, agent typing updates, and forwards remaining events to children.
func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = m.Width
		a.height = m.Height
		a.layout()

	case tea.KeyMsg:
		switch m.Type {
		case tea.KeyCtrlC:
			return a, tea.Quit

		case tea.KeyEnter:
			if a.input.mode == InputModeHuman {
				text := strings.TrimSpace(a.input.Value())
				if text != "" {
					a.input.Reset()
					mentions := parseMentions(text)
					if a.sendFn != nil {
						a.sendFn(text, mentions)
					}
				}
				return a, nil
			}
		}

	case ServerMsg:
		a.handleServerMsg(m.Raw)
		return a, nil

	case AgentTypingMsg:
		a.input.SetAgentText(m.Text)
		return a, nil

	case AgentStatusMsg:
		a.input.SetAgentStatus(m.Status)
		return a, nil
	}

	// Forward to child components.
	cmds = append(cmds, a.input.Update(msg))
	cmds = append(cmds, a.chat.Update(msg))

	return a, tea.Batch(cmds...)
}

// View satisfies tea.Model. Renders topbar, chat+sidebar, and input stacked
// vertically.
func (a App) View() string {
	middle := lipgloss.JoinHorizontal(
		lipgloss.Top,
		a.chat.View(),
		a.sidebar.View(),
	)
	return lipgloss.JoinVertical(
		lipgloss.Left,
		a.topbar.View(),
		middle,
		a.input.View(),
	)
}

// layout recalculates and applies component sizes based on the current
// terminal dimensions.
func (a *App) layout() {
	topbarHeight := 1
	inputHeight := 2 // 1 line text + 1 line border-top
	chatHeight := a.height - topbarHeight - inputHeight
	if chatHeight < 0 {
		chatHeight = 0
	}
	chatWidth := a.width - sidebarWidth
	if chatWidth < 0 {
		chatWidth = 0
	}

	a.topbar.SetWidth(a.width)
	a.chat.SetSize(chatWidth, chatHeight)
	a.sidebar.SetSize(sidebarWidth, chatHeight)
	a.input.SetWidth(a.width)
}

// handleServerMsg dispatches an incoming RawMessage to the appropriate handler
// based on its method.
func (a *App) handleServerMsg(raw *protocol.RawMessage) {
	if raw == nil {
		return
	}
	switch raw.Method {
	case "room.state":
		var params protocol.RoomStateParams
		if err := json.Unmarshal(raw.Params, &params); err == nil {
			a.sidebar.SetParticipants(params.Participants)
		}

	case "room.message":
		var params protocol.MessageParams
		if err := json.Unmarshal(raw.Params, &params); err == nil {
			a.chat.AddMessage(params)
		}

	case "room.joined":
		var params protocol.JoinedParams
		if err := json.Unmarshal(raw.Params, &params); err == nil {
			a.sidebar.AddParticipant(protocol.Participant{
				Name:      params.Name,
				Role:      params.Role,
				Directory: params.Directory,
				Repo:      params.Repo,
				AgentType: params.AgentType,
			})
		}

	case "room.left":
		var params protocol.LeftParams
		if err := json.Unmarshal(raw.Params, &params); err == nil {
			a.sidebar.RemoveParticipant(params.Name)
		}
	}
}

// parseMentions extracts @mention tokens from a message string.
func parseMentions(text string) []string {
	var mentions []string
	for _, word := range strings.Fields(text) {
		if strings.HasPrefix(word, "@") && len(word) > 1 {
			mentions = append(mentions, word[1:])
		}
	}
	return mentions
}
