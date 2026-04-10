package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
)

const sidebarWidth = 30
const terminalMinForSidebar = 100

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

// AgentReadyMsg signals that the agent subprocess started successfully.
// The App transitions from the loading screen to the full chat layout.
type AgentReadyMsg struct{}

// AgentStartFailedMsg signals that the agent subprocess failed to start.
// The App quits so the caller can return the error.
type AgentStartFailedMsg struct {
	Err error
}

// App is the root Bubble Tea model that composes all TUI components.
type App struct {
	topbar          TopBar
	chat            Chat
	sidebar         Sidebar
	input           Input
	statusbar       StatusBar
	modal           *Modal                 // non-nil when a modal overlay is active
	sendFn          func(string, []string) // callback to send messages over network
	registry        *command.Registry      // slash command registry (nil = no commands)
	cmdCtx          command.Context        // context passed to slash commands
	lastInputHeight int                    // cached to avoid redundant re-layouts
	suggestions     Suggestions
	inputFSM        *InputFSM
	completionStart int // cursor position where trigger character was typed
	roomState       *room.State

	// TUI-owned state, built from room events.
	localMessages     []protocol.MessageParams
	localParticipants []protocol.Participant
	localActivities   map[string]room.Activity

	spinner spinner.Model

	initializing  bool
	agentTypeName string
	mouseEnabled  bool // true = mouse capture on (scroll wheel works); false = text selection mode

	width  int
	height int
}

// SetRoomState sets the room.State that the App will use for event-sourced
// state management. The TUI does not use this yet (wired in Task 7).
func (a *App) SetRoomState(s *room.State) {
	a.roomState = s
}

// SetInitializing puts the App into loading-screen mode.
// agentType is shown in the loading text (e.g. "claude", "gemini").
func (a *App) SetInitializing(v bool, agentType string) {
	a.initializing = v
	a.agentTypeName = agentType
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
	a.mouseEnabled = true
	a.input.SetMode(mode)
	if len(participants) > 0 {
		a.sidebar.SetParticipants(participants)
	}
	// Callbacks are no-ops because Bubble Tea uses value semantics — the App
	// is copied on every Update call, so closures captured here would mutate
	// a stale copy. Suggestion population and hiding happen inline at the
	// call sites in Update instead.
	a.inputFSM = NewInputFSM(func(InputTrigger) {}, func() {})
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	a.spinner = sp
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
// Mouse capture is enabled by default so scroll wheel works. Press Ctrl+M to
// toggle into text-selection mode (mouse capture off) and back.
func (a App) Init() tea.Cmd {
	if a.initializing {
		return tea.Batch(textarea.Blink, a.spinner.Tick, tea.EnableMouseCellMotion)
	}
	return tea.Batch(textarea.Blink, tea.EnableMouseCellMotion)
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

	case tea.MouseMsg:
		// Route scroll wheel events to the chat viewport (mouse capture must be on).
		if m.Action == tea.MouseActionPress {
			switch m.Button {
			case tea.MouseButtonWheelUp:
				a.chat.vp.ScrollUp(3)
			case tea.MouseButtonWheelDown:
				a.chat.vp.ScrollDown(3)
			default:
			}
		}
		if a.chat.vp.AtBottom() {
			a.chat.unreadCount = 0
		}
		return a, nil

	case tea.KeyMsg:
		if updated, cmd, handled := a.handleKeyMsg(m); handled {
			return updated, cmd
		}

	// ---- Room events (from room.State via channel bridge) ----

	case room.HistoryLoaded:
		a.localMessages = m.Messages
		a.localParticipants = m.Participants
		a.localActivities = m.Activities
		// Forward to existing components for rendering.
		a.sidebar.SetParticipants(m.Participants)
		a.chat.SetParticipantColors(m.Participants)
		a.chat.SetLoading(false)
		a.chat.LoadMessages(m.Messages)
		a.statusbar.SetYolo(a.roomState != nil && a.roomState.AutoApprove())
		// Populate sidebar statuses from current activities (agents already active on join).
		for name, act := range m.Activities {
			a.sidebar.SetParticipantStatus(name, activityToStatus(act))
		}
		return a, a.maybeStartSpinner()

	case room.MessageReceived:
		a.localMessages = append(a.localMessages, m.Message)
		a.chat.AddMessage(m.Message)
		return a, nil

	case room.ParticipantsChanged:
		a.localParticipants = m.Participants
		a.sidebar.SetParticipants(m.Participants)
		a.chat.SetParticipantColors(m.Participants)
		return a, a.maybeStartSpinner()

	case room.ParticipantActivityChanged:
		a.localActivities[m.Name] = m.Activity
		a.sidebar.SetParticipantStatus(m.Name, activityToStatus(m.Activity))
		return a, a.maybeStartSpinner()

	case room.ErrorOccurred:
		a.chat.AddMessage(systemMessage(m.Error.Error()))
		return a, nil

	case ServerDisconnectedMsg:
		return a, tea.Quit

	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(m)
		a.sidebar.SetSpinnerView(a.spinner.View())
		if a.initializing || isAnyActive(a.localActivities) {
			return a, cmd
		}
		return a, nil // self-terminate

	case AgentTypingMsg:
		a.input.SetAgentText(m.Text)
		a.relayoutIfInputChanged()
		return a, nil

	case LocalSystemMsg:
		a.chat.AddMessage(systemMessage(m.Text))
		return a, nil

	case AgentReadyMsg:
		a.initializing = false
		a.layout()
		return a, nil

	case AgentStartFailedMsg:
		return a, tea.Quit
	}

	// Forward key events only to input, not chat (prevents scroll jumping).
	// Scroll keys (PageUp/Down, arrows) are explicitly forwarded to the viewport.
	cmds = append(cmds, a.input.Update(msg))
	cmds = append(cmds, a.forwardScrollKey(msg)...)

	// Check for suggestion triggers and update filter after input changes.
	if _, ok := msg.(tea.KeyMsg); ok && a.input.mode == InputModeHuman {
		wasVisible := a.suggestions.Visible()
		if a.inputFSM.Current() == StateCompleting {
			// Update filter based on current input.
			val := a.input.Value()
			runes := []rune(val)
			if len(runes) <= a.completionStart {
				_ = a.inputFSM.Fire(TriggerDismiss)
				a.suggestions.Hide()
			} else {
				query := string(runes[a.completionStart+1:])
				a.suggestions.Filter(query)
			}
		} else {
			// Check for new triggers.
			val := a.input.Value()
			if val != "" {
				runes := []rune(val)
				last := runes[len(runes)-1]
				switch last {
				case '/':
					if len(runes) == 1 && a.registry != nil {
						a.completionStart = 0
						_ = a.inputFSM.Fire(TriggerSlash)
						a.populateSlashSuggestions()
					}
				case '@':
					pos := len(runes) - 1
					if pos == 0 || runes[pos-1] == ' ' || runes[pos-1] == '\n' {
						a.completionStart = pos
						_ = a.inputFSM.Fire(TriggerMention)
						a.populateMentionSuggestions()
					}
				}
			}
		}
		if wasVisible != a.suggestions.Visible() {
			a.layout()
		}
	}

	// Re-layout only if input height actually changed.
	if a.width > 0 && a.height > 0 {
		a.relayoutIfInputChanged()
	}

	return a, tea.Batch(cmds...)
}

// View satisfies tea.Model. When a modal is active it renders full-screen;
// otherwise renders topbar, chat+sidebar, and input stacked vertically.
func (a App) View() string {
	if a.initializing {
		return a.loadingView()
	}
	if a.modal != nil {
		return a.modal.View()
	}

	// Update status bar with current scroll position, sidebar visibility, and mouse mode.
	// Done in View() (value receiver) so it reflects the latest viewport state without
	// needing an extra Update pass.
	a.statusbar.SetScrollPosition(a.chat.ScrollPercent(), a.chat.AtBottom())
	a.statusbar.SetSidebarVisible(a.width >= terminalMinForSidebar)
	a.statusbar.SetMouseEnabled(a.mouseEnabled)

	chatView := a.chat.View()
	if badge := a.chat.UnreadBadge(); badge != "" {
		// Append badge as a right-aligned line below the viewport.
		// lipgloss.Place would blank the viewport content, so we append instead.
		badgeLine := lipgloss.NewStyle().
			Width(a.chat.width).
			Align(lipgloss.Right).
			Render(badge)
		chatView = chatView + "\n" + badgeLine
	}
	var middle string
	if a.width >= terminalMinForSidebar {
		middle = lipgloss.JoinHorizontal(lipgloss.Top, chatView, a.sidebar.View())
	} else {
		middle = chatView
	}
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
	topbarHeight := 2 // 1 content row + 1 bottom border
	inputHeight := a.input.Height()
	statusbarHeight := 1
	suggestionsHeight := a.suggestions.Height()
	chatHeight := a.height - topbarHeight - inputHeight - statusbarHeight - suggestionsHeight
	if chatHeight < 0 {
		chatHeight = 0
	}
	sw := sidebarWidth
	if a.width < terminalMinForSidebar {
		sw = 0
	}
	chatWidth := a.width - sw
	if chatWidth < 0 {
		chatWidth = 0
	}

	showSidebar := a.width >= terminalMinForSidebar
	a.lastInputHeight = inputHeight
	a.topbar.SetWidth(a.width)
	a.chat.SetSize(chatWidth, chatHeight)
	a.sidebar.SetSize(sw, chatHeight)
	a.input.SetWidth(a.width)
	a.statusbar.SetWidth(a.width)
	a.suggestions.SetWidth(a.width)
	if showSidebar {
		a.statusbar.SetCompactParticipants(nil, nil)
	} else {
		statusStrs := make(map[string]string, len(a.localActivities))
		for name, act := range a.localActivities {
			statusStrs[name] = activityToStatus(act)
		}
		a.statusbar.SetCompactParticipants(a.localParticipants, statusStrs)
	}
}

// handleEnterKey processes a human Enter keypress: trims input, dispatches slash
// commands or sends a chat message.
func (a *App) handleEnterKey() {
	text := strings.TrimSpace(a.input.Value())
	if text == "" {
		return
	}
	a.input.Reset()
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
		return
	}
	mentions := protocol.ParseMentions(text)
	if a.sendFn != nil {
		a.sendFn(text, mentions)
	}
}

// populateSlashSuggestions fills the suggestion list with available commands.
// handleCompletingKeys handles key events when the input FSM is in StateCompleting.
// handleKeyMsg processes tea.KeyMsg events through the three-layer routing hierarchy.
// Returns (app, cmd, true) when the key was fully consumed, (zero, nil, false) to fall through.
// The caller must use the returned App value — pointer mutations are not sufficient because
// Update has a value receiver and Bubble Tea requires the model to be returned.
func (a App) handleKeyMsg(m tea.KeyMsg) (App, tea.Cmd, bool) {
	// Layer 1: Overlay — modal intercepts ALL keys.
	if a.modal != nil {
		cmd, dismiss := a.modal.HandleKey(m)
		if dismiss {
			a.modal = nil
		}
		return a, cmd, true
	}

	// Layer 2: Permission — placeholder for #50.
	// When implemented: if pending permissions, y/n consumed, else pass through.

	// Global keys (always available).
	if m.Type == tea.KeyCtrlC {
		return a, tea.Quit, true
	}

	// Ctrl+\ toggles mouse capture: ON = scroll wheel, OFF = text selection.
	// (Ctrl+M cannot be used — it maps to Enter at the terminal protocol level.)
	if m.Type == tea.KeyCtrlBackslash {
		if a.mouseEnabled {
			a.mouseEnabled = false
			return a, tea.DisableMouse, true
		}
		a.mouseEnabled = true
		return a, tea.EnableMouseCellMotion, true
	}

	// Double-Esc clears the input (300ms window).
	if m.Type == tea.KeyEsc && a.input.mode == InputModeHuman && a.inputFSM.Current() == StateNormal {
		now := time.Now()
		if !a.input.lastEscTime.IsZero() && now.Sub(a.input.lastEscTime) < 300*time.Millisecond {
			a.input.Reset()
			a.input.lastEscTime = time.Time{}
		} else {
			a.input.lastEscTime = now
		}
		return a, nil, true
	}

	// Layer 3: Input FSM routing.
	if cmd, handled := a.handleCompletingKeys(m); handled {
		return a, cmd, true
	}

	// Normal input handling (StateNormal, or Enter fell through from StateCompleting).
	if m.Type == tea.KeyEnter && a.input.mode == InputModeHuman {
		a.handleEnterKey()
		return a, nil, true
	}

	return a, nil, false
}

// forwardScrollKey forwards PgUp/PgDown/Up/Down to the chat viewport.
// Arrow keys are only forwarded when not in StateCompleting (where they navigate suggestions).
// Non-key messages are forwarded to the chat unchanged (e.g. spinner ticks, viewport sync).
func (a *App) forwardScrollKey(msg tea.Msg) []tea.Cmd {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		return []tea.Cmd{a.chat.Update(msg)}
	}
	switch key.Type {
	case tea.KeyPgUp, tea.KeyPgDown:
		return []tea.Cmd{a.chat.Update(msg)}
	case tea.KeyUp, tea.KeyDown:
		if a.inputFSM.Current() != StateCompleting {
			return []tea.Cmd{a.chat.Update(msg)}
		}
	default:
	}
	return nil
}

// Returns (cmd, handled). If handled is true, the key was consumed.
func (a *App) handleCompletingKeys(m tea.KeyMsg) (tea.Cmd, bool) {
	if a.inputFSM.Current() != StateCompleting {
		return nil, false
	}
	switch m.Type {
	case tea.KeyUp:
		a.suggestions.MoveUp()
		return nil, true
	case tea.KeyDown:
		a.suggestions.MoveDown()
		return nil, true
	case tea.KeyTab:
		sel := a.suggestions.Selected()
		if sel.Label != "" {
			end := len([]rune(a.input.Value()))
			a.input.ReplaceRange(a.completionStart, end, sel.Label+" ")
		}
		_ = a.inputFSM.Fire(TriggerAccept)
		a.suggestions.Hide()
		a.layout()
		return nil, true
	case tea.KeyEsc:
		_ = a.inputFSM.Fire(TriggerDismiss)
		a.suggestions.Hide()
		a.layout()
		return nil, true
	case tea.KeyEnter:
		_ = a.inputFSM.Fire(TriggerSubmit)
		a.suggestions.Hide()
		a.layout()
		// Not handled — fall through to normal Enter handling in Update.
		return nil, false
	default:
		return nil, false
	}
}

func (a *App) populateSlashSuggestions() {
	if a.registry == nil {
		return
	}
	items := make([]SuggestionItem, 0)
	for _, cmd := range a.registry.Commands() {
		items = append(items, SuggestionItem{
			Label:       "/" + cmd.Name,
			Description: cmd.Description,
		})
	}
	a.suggestions.SetItems(items)
}

// populateMentionSuggestions fills the suggestion list with online participants.
func (a *App) populateMentionSuggestions() {
	items := make([]SuggestionItem, 0)
	for _, p := range a.localParticipants {
		if p.Online {
			items = append(items, SuggestionItem{
				Label:       "@" + p.Name,
				Description: p.Role,
			})
		}
	}
	a.suggestions.SetItems(items)
}

// relayoutIfInputChanged calls layout() when the input height has changed since
// the last layout pass. This avoids redundant relayouts and keeps Update's
// cyclomatic complexity within the linter limit.
func (a *App) relayoutIfInputChanged() {
	if a.input.Height() != a.lastInputHeight {
		a.layout()
	}
}

// loadingView renders a centered loading screen shown while the agent starts.
func (a App) loadingView() string {
	msg := lipgloss.NewStyle().
		Foreground(colorDimText).
		Render(a.spinner.View() + " Starting " + a.agentTypeName + "…")
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, msg)
}

// maybeStartSpinner returns a.spinner.Tick if the spinner should be running.
func (a *App) maybeStartSpinner() tea.Cmd {
	if a.initializing || isAnyActive(a.localActivities) {
		return a.spinner.Tick
	}
	return nil
}

// isAnyActive returns true if any participant is doing something (not idle).
func isAnyActive(activities map[string]room.Activity) bool {
	for _, a := range activities {
		if a != room.ActivityIdle {
			return true
		}
	}
	return false
}

// activityToStatus converts a room.Activity to a protocol status string for the sidebar.
func activityToStatus(a room.Activity) string {
	switch a {
	case room.ActivityGenerating:
		return protocol.StatusGenerating
	case room.ActivityThinking:
		return protocol.StatusThinking
	case room.ActivityUsingTool:
		return protocol.StatusUsingTool("")
	default:
		return protocol.StatusIdle
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
