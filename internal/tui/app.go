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
	"github.com/khaiql/parley/internal/room"
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
	topbar            TopBar
	chat              Chat
	sidebar           Sidebar
	input             Input
	statusbar         StatusBar
	modal             *Modal                   // non-nil when a modal overlay is active
	sendFn            func(string, []string)   // callback to send messages over network
	registry          *command.Registry        // slash command registry (nil = no commands)
	cmdCtx            command.Context          // context passed to slash commands
	lastInputHeight   int                      // cached to avoid redundant re-layouts
	pendingHistory    []protocol.MessageParams // set during room.state, loaded async
	spinnerActive     bool
	suggestions       Suggestions
	completionTrigger rune // '/' or '@', or 0 if inactive
	completionStart   int  // cursor position where trigger character was typed
	roomState         *room.State

	// TUI-owned state, built from room events.
	localMessages     []protocol.MessageParams
	localParticipants []protocol.Participant
	localActivities   map[string]room.Activity

	width  int
	height int
}

// SetRoomState sets the room.State that the App will use for event-sourced
// state management. The TUI does not use this yet (wired in Task 7).
func (a *App) SetRoomState(s *room.State) {
	a.roomState = s
}

// NewApp creates an App with the given topic, port, input mode, display name,
// send callback, and optional initial participants (may be nil).
func NewApp(topic string, port int, mode InputMode, _ string, sendFn func(string, []string), participants ...protocol.Participant) App {
	sb := NewSidebar()
	sb.SetPort(port)
	a := App{
		topbar:          NewTopBar(topic, port),
		chat:            NewChat(0, 0),
		sidebar:         sb,
		input:           NewInput(),
		statusbar:       NewStatusBar(),
		sendFn:          sendFn,
		localActivities: make(map[string]room.Activity),
	}
	a.input.SetMode(mode)
	if len(participants) > 0 {
		a.sidebar.SetParticipants(participants)
	}
	return a
}

// SetYolo sets whether the room is in yolo/auto-approve mode on the status bar.
func (a *App) SetYolo(y bool) {
	a.statusbar.SetYolo(y)
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

		case tea.KeyUp:
			if a.suggestions.Visible() {
				a.suggestions.MoveUp()
				return a, nil
			}
		case tea.KeyDown:
			if a.suggestions.Visible() {
				a.suggestions.MoveDown()
				return a, nil
			}
		case tea.KeyTab:
			if a.suggestions.Visible() {
				a.acceptSuggestion()
				a.layout()
				return a, nil
			}
		case tea.KeyEsc:
			if a.suggestions.Visible() {
				a.dismissSuggestions()
				a.layout()
				return a, nil
			}

		case tea.KeyEnter:
			if a.suggestions.Visible() {
				a.dismissSuggestions()
				a.layout()
			}
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

	// ---- Room events (from room.State via channel bridge) ----

	case room.HistoryLoaded:
		a.localMessages = m.Messages
		a.localParticipants = m.Participants
		a.localActivities = m.Activities
		// Forward to existing components for rendering.
		a.sidebar.SetParticipants(m.Participants)
		a.chat.SetLoading(false)
		a.chat.LoadMessages(m.Messages)
		a.statusbar.SetYolo(a.roomState != nil && a.roomState.AutoApprove())
		if isAnyGenerating(a.localActivities) {
			return a, spinnerTick()
		}
		return a, nil

	case room.MessageReceived:
		a.localMessages = append(a.localMessages, m.Message)
		a.chat.AddMessage(m.Message)
		return a, nil

	case room.ParticipantsChanged:
		a.localParticipants = m.Participants
		a.sidebar.SetParticipants(m.Participants)
		if isAnyGenerating(a.localActivities) {
			return a, spinnerTick()
		}
		return a, nil

	case room.ParticipantActivityChanged:
		a.localActivities[m.Name] = m.Activity
		a.sidebar.SetParticipantStatus(m.Name, activityToString(m.Activity))
		if m.Activity == room.ActivityGenerating {
			if !a.spinnerActive {
				a.spinnerActive = true
				return a, spinnerTick()
			}
		}
		return a, nil

	case room.ErrorOccurred:
		a.chat.AddMessage(systemMessage(m.Error.Error()))
		return a, nil

	case ServerDisconnectedMsg:
		return a, tea.Quit

	case SpinnerTickMsg:
		if a.sidebar.TickSpinner() {
			return a, spinnerTick()
		}
		a.spinnerActive = false
		return a, nil

	case ServerMsg:
		if a.roomState != nil {
			// room.State handles this via HandleServerMessage → emits events
			// which arrive through the event bridge goroutine. Nothing to do here.
			return a, nil
		}
		// Fallback for tests/contexts without room.State.
		a.handleServerMsg(m.Raw)
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

	// Check for suggestion triggers and update filter after input changes.
	if _, ok := msg.(tea.KeyMsg); ok && a.input.mode == InputModeHuman {
		wasVisible := a.suggestions.Visible()
		if wasVisible {
			a.updateSuggestionFilter()
		} else {
			a.checkSuggestionTrigger()
		}
		if wasVisible != a.suggestions.Visible() {
			a.layout()
		}
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
	parts := []string{a.topbar.View(), middle}
	if a.suggestions.Visible() {
		parts = append(parts, a.suggestions.View())
	}
	parts = append(parts, a.input.View(), a.statusbar.View())
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// layout recalculates and applies component sizes based on the current
// terminal dimensions.
func (a *App) layout() {
	topbarHeight := 1
	inputHeight := a.input.Height()
	statusbarHeight := 1
	suggestionsHeight := a.suggestions.Height()
	chatHeight := a.height - topbarHeight - inputHeight - statusbarHeight - suggestionsHeight
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
	a.suggestions.SetWidth(a.width)
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

// checkSuggestionTrigger scans the current input value for a trigger character
// and activates suggestions if found.
func (a *App) checkSuggestionTrigger() {
	if a.suggestions.Visible() {
		return // already active
	}
	val := a.input.Value()
	if val == "" {
		return
	}
	runes := []rune(val)
	last := runes[len(runes)-1]

	switch last {
	case '/':
		// Only trigger at the very start of input.
		if len(runes) == 1 && a.registry != nil {
			a.completionTrigger = '/'
			a.completionStart = 0
			items := make([]SuggestionItem, 0)
			for _, cmd := range a.registry.Commands() {
				items = append(items, SuggestionItem{
					Label:       "/" + cmd.Name,
					Description: cmd.Description,
				})
			}
			a.suggestions.SetItems(items)
		}
	case '@':
		// Trigger at start of input or after whitespace.
		pos := len(runes) - 1
		if pos == 0 || runes[pos-1] == ' ' || runes[pos-1] == '\n' {
			a.completionTrigger = '@'
			a.completionStart = pos
			items := make([]SuggestionItem, 0)
			for _, p := range a.sidebar.participants {
				if p.Online {
					items = append(items, SuggestionItem{
						Label:       "@" + p.Name,
						Description: p.Role,
					})
				}
			}
			a.suggestions.SetItems(items)
		}
	}
}

// updateSuggestionFilter extracts the query from the current input and filters.
func (a *App) updateSuggestionFilter() {
	if !a.suggestions.Visible() {
		return
	}
	val := a.input.Value()
	runes := []rune(val)

	// If user deleted back past the trigger, dismiss.
	if len(runes) <= a.completionStart {
		a.dismissSuggestions()
		return
	}

	query := string(runes[a.completionStart+1:])
	a.suggestions.Filter(query)
}

// acceptSuggestion inserts the selected suggestion into the input.
func (a *App) acceptSuggestion() {
	sel := a.suggestions.Selected()
	if sel.Label == "" {
		a.dismissSuggestions()
		return
	}
	end := len([]rune(a.input.Value()))
	a.input.ReplaceRange(a.completionStart, end, sel.Label+" ")
	a.dismissSuggestions()
}

// dismissSuggestions hides the suggestion list and resets trigger state.
func (a *App) dismissSuggestions() {
	a.suggestions.Hide()
	a.completionTrigger = 0
	a.completionStart = 0
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
				Online:    true,
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

// isAnyGenerating returns true if any participant has ActivityGenerating.
func isAnyGenerating(activities map[string]room.Activity) bool {
	for _, a := range activities {
		if a == room.ActivityGenerating {
			return true
		}
	}
	return false
}

// activityToString converts a room.Activity to a display string for the sidebar.
func activityToString(a room.Activity) string {
	switch a {
	case room.ActivityGenerating:
		return "generating"
	case room.ActivityThinking:
		return "thinking"
	case room.ActivityUsingTool:
		return "using tool"
	default:
		return ""
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
