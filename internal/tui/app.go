package tui

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
)

const sidebarWidth = 30

// ServerMsg wraps an incoming raw protocol message from the network.
type ServerMsg struct {
	Raw *protocol.RawMessage
}

// SpinnerTickMsg triggers a sidebar spinner frame advance.
type SpinnerTickMsg struct{}

// spinnerTick returns a tea.Cmd that sends a SpinnerTickMsg after 100ms.
func spinnerTick() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return SpinnerTickMsg{}
	})
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
	statusbar       StatusBar
	modal           *Modal                    // non-nil when a modal overlay is active
	sendFn          func(string, []string)    // callback to send messages over network
	registry        *command.Registry         // slash command registry (nil = no commands)
	cmdCtx          command.Context           // context passed to slash commands
	lastInputHeight int                       // cached to avoid redundant re-layouts
	pendingHistory  []protocol.MessageParams  // set during room.state, loaded async
	spinnerActive   bool
	width           int
	height          int
}

// NewApp creates an App with the given topic, port, input mode, display name,
// send callback, and optional initial participants (may be nil).
func NewApp(topic string, port int, mode InputMode, _ string, sendFn func(string, []string), participants ...protocol.Participant) App {
	sb := NewSidebar()
	sb.SetPort(port)
	a := App{
		topbar:    NewTopBar(topic, port),
		chat:      NewChat(0, 0),
		sidebar:   sb,
		input:     NewInput(),
		statusbar: NewStatusBar(),
		sendFn:    sendFn,
	}
	a.input.SetMode(mode)
	if len(participants) > 0 {
		a.sidebar.SetParticipants(participants)
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
		if a.modal != nil {
			a.modal.Resize(m.Width, m.Height)
		}

	case tea.KeyMsg:
		// Modal intercepts all keyboard input while visible.
		if a.modal != nil {
			switch {
			case m.Type == tea.KeyEsc, m.String() == "q":
				a.modal = nil
				return a, nil
			default:
				cmd := a.modal.Update(msg)
				return a, cmd
			}
		}
		switch m.Type {
		case tea.KeyCtrlC:
			return a, tea.Quit

		case tea.KeyEnter:
			if a.input.mode == InputModeHuman {
				text := a.input.Value()
				// Check for backslash-newline
				if newText, consumed := handleBackslashNewline(text); consumed {
					a.input.ta.SetValue(newText)
					return a, nil
				}
				text = strings.TrimSpace(text)
				if text != "" {
					a.input.Reset()
					// Slash command dispatch.
					if a.registry != nil && command.IsCommand(text) {
						result := a.registry.Execute(a.cmdCtx, text)
						if result.Error != nil {
							a.chat.AddMessage(systemMessage(result.Error.Error()))
						} else if result.Modal != nil {
							modal := NewModal(result.Modal, a.width, a.height)
							a.modal = &modal
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

	case SpinnerTickMsg:
		if a.sidebar.TickSpinner() {
			return a, spinnerTick()
		}
		a.spinnerActive = false
		return a, nil

	case ServerMsg:
		a.handleServerMsg(m.Raw)
		// If history is pending, dispatch async load.
		if len(a.pendingHistory) > 0 {
			msgs := a.pendingHistory
			a.pendingHistory = nil
			return a, tea.Batch(
				func() tea.Msg {
					return HistoryLoadedMsg{Messages: msgs}
				},
				a.maybeStartSpinner(),
			)
		}
		return a, a.maybeStartSpinner()

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

	// Forward key events only to input, not chat (prevents scroll jumping).
	cmds = append(cmds, a.input.Update(msg))
	if _, isKey := msg.(tea.KeyMsg); !isKey {
		cmds = append(cmds, a.chat.Update(msg))
	}

	// Re-layout only if input height actually changed.
	if a.width > 0 && a.height > 0 {
		if a.input.Height() != a.lastInputHeight {
			a.layout()
		}
	}

	return a, tea.Batch(cmds...)
}

// View satisfies tea.Model. When a modal is active it renders full-screen;
// otherwise renders topbar, chat+sidebar, and input stacked vertically.
func (a App) View() string {
	if a.modal != nil {
		return a.modal.View()
	}
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
		a.statusbar.View(),
	)
}

// layout recalculates and applies component sizes based on the current
// terminal dimensions.
func (a *App) layout() {
	topbarHeight := 1
	inputHeight := a.input.Height()
	statusbarHeight := 1
	chatHeight := a.height - topbarHeight - inputHeight - statusbarHeight
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
	a.statusbar.SetWidth(a.width)
}

// maybeStartSpinner checks if any participant is generating and starts
// the spinner tick if not already running.
func (a *App) maybeStartSpinner() tea.Cmd {
	if a.spinnerActive {
		return nil
	}
	for _, status := range a.sidebar.statuses {
		if status == "generating" {
			a.spinnerActive = true
			return spinnerTick()
		}
	}
	return nil
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
			a.statusbar.SetYolo(params.AutoApprove)
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
			a.sidebar.SetParticipantOffline(params.Name)
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
