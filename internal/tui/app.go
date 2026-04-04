package tui

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
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

// ServerDisconnectedMsg signals that the server connection was lost.
type ServerDisconnectedMsg struct{}

// LocalSystemMsg injects a local-only system message into the chat view.
// These messages are not broadcast to other participants.
type LocalSystemMsg struct {
	Text string
}

// HistoryLoadedMsg signals that message history has been loaded.
type HistoryLoadedMsg struct {
	Messages []protocol.MessageParams
}

// App is the root Bubble Tea model that composes all TUI components.
type App struct {
	topbar          TopBar
	chat            Chat
	sidebar         Sidebar
	input           Input
	sendFn          func(string, []string)    // callback to send messages over network
	registry        *command.Registry         // slash command registry (nil = no commands)
	cmdCtx          command.Context           // context passed to slash commands
	nameColors      map[string]lipgloss.Color // stable color per participant
	colorIdx        int                       // next color index to assign
	lastInputHeight int                       // cached to avoid redundant re-layouts
	pendingHistory  []protocol.MessageParams  // set during room.state, loaded async
	width           int
	height          int
}

// NewApp creates an App with the given topic, port, input mode, display name,
// send callback, and optional initial participants (may be nil).
func NewApp(topic string, port int, mode InputMode, _ string, sendFn func(string, []string), participants ...protocol.Participant) App {
	a := App{
		topbar:     NewTopBar(topic, port),
		chat:       NewChat(0, 0),
		sidebar:    NewSidebar(),
		input:      NewInput(),
		sendFn:     sendFn,
		nameColors: make(map[string]lipgloss.Color),
	}
	a.input.SetMode(mode)
	if len(participants) > 0 {
		for _, p := range participants {
			a.assignColor(p.Name, p.Role)
		}
		a.sidebar.SetParticipants(participants)
		a.sidebar.SetNameColors(a.nameColors)
	}
	return a
}

// SetAgent configures the agent name and role shown in the topbar.
func (a *App) SetAgent(name, role string) {
	a.topbar.SetAgent(name, role)
}

// SetCommandRegistry configures the slash command registry and context.
// This is only used by the host TUI.
func (a *App) SetCommandRegistry(reg *command.Registry, ctx command.Context) {
	a.registry = reg
	a.cmdCtx = ctx
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
					// Slash command dispatch.
					if a.registry != nil && command.IsCommand(text) {
						result := a.registry.Execute(a.cmdCtx, text)
						if result.Error != nil {
							a.chat.AddMessage(systemMessage(result.Error.Error()))
						} else if result.LocalMessage != "" {
							a.chat.AddMessage(systemMessage(result.LocalMessage))
						}
						return a, nil
					}
					mentions := protocol.ParseMentions(text)
					if a.sendFn != nil {
						a.sendFn(text, mentions)
					}
				}
				return a, nil
			}
		default:
			// ignore other keys
		}

	case ServerDisconnectedMsg:
		return a, tea.Quit

	case ServerMsg:
		a.handleServerMsg(m.Raw)
		// If history is pending, dispatch async load.
		if len(a.pendingHistory) > 0 {
			msgs := a.pendingHistory
			a.pendingHistory = nil
			return a, func() tea.Msg {
				return HistoryLoadedMsg{Messages: msgs}
			}
		}
		return a, nil

	case HistoryLoadedMsg:
		a.chat.SetLoading(false)
		a.chat.LoadMessages(m.Messages)
		return a, nil

	case AgentTypingMsg:
		a.input.SetAgentText(m.Text)
		return a, nil

	case LocalSystemMsg:
		a.chat.AddMessage(systemMessage(m.Text))
		return a, nil
	}

	// Forward to child components.
	cmds = append(cmds, a.input.Update(msg))
	cmds = append(cmds, a.chat.Update(msg))

	// Re-layout only if input height actually changed.
	if a.width > 0 && a.height > 0 {
		if a.input.Height() != a.lastInputHeight {
			a.layout()
		}
	}

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
	inputHeight := a.input.Height()
	chatHeight := a.height - topbarHeight - inputHeight
	if chatHeight < 0 {
		chatHeight = 0
	}
	chatWidth := a.width - sidebarWidth
	if chatWidth < 0 {
		chatWidth = 0
	}

	a.lastInputHeight = inputHeight
	a.topbar.SetWidth(a.width)
	a.chat.SetSize(chatWidth, chatHeight)
	a.sidebar.SetSize(sidebarWidth, chatHeight)
	a.input.SetWidth(a.width)
}

// assignColor gives a participant a stable color. Humans get colorHuman;
// agents cycle through participantColors.
func (a *App) assignColor(name, role string) {
	if _, ok := a.nameColors[name]; ok {
		return
	}
	if role == "human" {
		a.nameColors[name] = colorHuman
	} else {
		a.nameColors[name] = participantColors[a.colorIdx%len(participantColors)]
		a.colorIdx++
	}
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
			for _, p := range params.Participants {
				a.assignColor(p.Name, p.Role)
			}
			a.sidebar.SetParticipants(params.Participants)
			a.sidebar.SetNameColors(a.nameColors)
			a.chat.SetNameColors(a.nameColors)
			if len(params.Messages) > 0 {
				a.chat.SetLoading(true)
				a.pendingHistory = params.Messages
			}
		}

	case "room.message":
		var params protocol.MessageParams
		if err := json.Unmarshal(raw.Params, &params); err == nil {
			a.chat.AddMessage(params)
		}

	case "room.joined":
		var params protocol.JoinedParams
		if err := json.Unmarshal(raw.Params, &params); err == nil {
			a.assignColor(params.Name, params.Role)
			a.sidebar.AddParticipant(protocol.Participant{
				Name:      params.Name,
				Role:      params.Role,
				Directory: params.Directory,
				Repo:      params.Repo,
				AgentType: params.AgentType,
			})
			a.sidebar.SetNameColors(a.nameColors)
			a.chat.SetNameColors(a.nameColors)
		}

	case "room.left":
		var params protocol.LeftParams
		if err := json.Unmarshal(raw.Params, &params); err == nil {
			a.sidebar.RemoveParticipant(params.Name)
		}

	case "room.status":
		var params protocol.StatusParams
		if err := json.Unmarshal(raw.Params, &params); err == nil {
			a.sidebar.SetParticipantStatus(params.Name, params.Status)
		}
	}
}

// systemMessage creates a local-only system MessageParams for display in the
// chat view. These are not broadcast to other participants.
func systemMessage(text string) protocol.MessageParams {
	return protocol.MessageParams{
		From:   "system",
		Source: "system",
		Role:   "system",
		Content: []protocol.Content{
			{Type: "text", Text: text},
		},
	}
}
