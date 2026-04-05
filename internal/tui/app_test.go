package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/khaiql/parley/internal/command"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
)

// ---- handleServerMsg ---------------------------------------------------------

func makeApp() App {
	return NewApp("test-topic", 9000, InputModeHuman, "tester", nil)
}

func rawParams(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// fakeRoom is a minimal RoomQuerier for modal integration testing.
type fakeRoom struct{}

func (f *fakeRoom) GetID() string    { return "room-1" }
func (f *fakeRoom) GetTopic() string { return "test-topic" }
func (f *fakeRoom) GetPort() int     { return 9000 }
func (f *fakeRoom) GetParticipants() []command.ParticipantInfo {
	return []command.ParticipantInfo{
		{Name: "alice", Role: "agent", Directory: "/tmp/alice", AgentType: "claude", Online: true},
	}
}
func (f *fakeRoom) GetMessageCount() int { return 0 }

func makeAppWithRegistry() App {
	app := makeApp()
	reg := command.NewRegistry()
	reg.Register(command.InfoCommand)
	ctx := command.Context{Room: &fakeRoom{}}
	app.SetCommandRegistry(reg, ctx)
	// Give the app a size so the modal can compute dimensions.
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return model.(App)
}

func TestHandleServerMsg_RoomMessage_AddsToChat(t *testing.T) {
	a := makeApp()

	msg := protocol.MessageParams{
		ID:   "msg-1",
		From: "alice",
		Role: "human",
		Content: []protocol.Content{
			{Type: "text", Text: "hello"},
		},
	}
	raw := &protocol.RawMessage{
		Method: "room.message",
		Params: rawParams(t, msg),
	}

	a.handleServerMsg(raw)

	if len(a.chat.messages) != 1 {
		t.Fatalf("expected 1 message in chat, got %d", len(a.chat.messages))
	}
	if a.chat.messages[0].From != "alice" {
		t.Errorf("unexpected From: %s", a.chat.messages[0].From)
	}
}

func TestHandleServerMsg_RoomState_SetsParticipants(t *testing.T) {
	a := makeApp()

	state := protocol.RoomStateParams{
		Topic: "test-topic",
		Participants: []protocol.Participant{
			{Name: "alice", Role: "human"},
			{Name: "bot", Role: "agent"},
		},
	}
	raw := &protocol.RawMessage{
		Method: "room.state",
		Params: rawParams(t, state),
	}

	a.handleServerMsg(raw)

	if len(a.sidebar.participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(a.sidebar.participants))
	}
	if a.sidebar.participants[0].Name != "alice" {
		t.Errorf("unexpected first participant: %s", a.sidebar.participants[0].Name)
	}
}

func TestHandleServerMsg_RoomJoined_AddsParticipant(t *testing.T) {
	a := makeApp()

	joined := protocol.JoinedParams{
		Name: "carol",
		Role: "agent",
	}
	raw := &protocol.RawMessage{
		Method: "room.joined",
		Params: rawParams(t, joined),
	}

	a.handleServerMsg(raw)

	if len(a.sidebar.participants) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(a.sidebar.participants))
	}
	if a.sidebar.participants[0].Name != "carol" {
		t.Errorf("unexpected participant name: %s", a.sidebar.participants[0].Name)
	}
	if !a.sidebar.participants[0].Online {
		t.Errorf("expected joined participant to be online")
	}
}

func TestHandleServerMsg_RoomLeft_SetsParticipantOffline(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "alice", Role: "human", Online: true},
		{Name: "bot", Role: "agent", Online: true},
	})

	left := protocol.LeftParams{Name: "bot"}
	raw := &protocol.RawMessage{
		Method: "room.left",
		Params: rawParams(t, left),
	}

	a.handleServerMsg(raw)

	if len(a.sidebar.participants) != 2 {
		t.Fatalf("expected 2 participants after leave, got %d", len(a.sidebar.participants))
	}
	if a.sidebar.participants[1].Name != "bot" || a.sidebar.participants[1].Online != false {
		t.Errorf("expected bot to be marked offline: %+v", a.sidebar.participants[1])
	}
}

func TestHandleServerMsg_RoomStatus_UpdatesSidebarStatus(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "bot1", Role: "agent"},
	})

	sp := protocol.StatusParams{Name: "bot1", Status: "thinking…"}
	raw := &protocol.RawMessage{
		Method: "room.status",
		Params: rawParams(t, sp),
	}

	a.handleServerMsg(raw)

	if a.sidebar.statuses["bot1"] != "thinking…" {
		t.Errorf("expected sidebar status 'thinking…' for bot1, got %q", a.sidebar.statuses["bot1"])
	}
}

func TestHandleServerMsg_RoomStatusClear_ClearsSidebarStatus(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "bot1", Role: "agent"},
	})
	a.sidebar.SetParticipantStatus("bot1", "thinking…")

	sp := protocol.StatusParams{Name: "bot1", Status: ""}
	raw := &protocol.RawMessage{
		Method: "room.status",
		Params: rawParams(t, sp),
	}

	a.handleServerMsg(raw)

	if a.sidebar.statuses["bot1"] != "" {
		t.Errorf("expected empty status after clear, got %q", a.sidebar.statuses["bot1"])
	}
}

func TestHandleServerMsg_RoomState_ReplayesMessageHistory(t *testing.T) {
	a := makeApp()

	state := protocol.RoomStateParams{
		Topic: "test-topic",
		Participants: []protocol.Participant{
			{Name: "alice", Role: "human"},
		},
		Messages: []protocol.MessageParams{
			{ID: "msg-1", From: "alice", Role: "human", Content: []protocol.Content{{Type: "text", Text: "hello"}}},
			{ID: "msg-2", From: "alice", Role: "human", Content: []protocol.Content{{Type: "text", Text: "world"}}},
		},
	}
	raw := &protocol.RawMessage{
		Method: "room.state",
		Params: rawParams(t, state),
	}

	a.handleServerMsg(raw)

	// History is now loaded asynchronously: handleServerMsg sets pendingHistory,
	// then Update dispatches HistoryLoadedMsg.
	if len(a.pendingHistory) != 2 {
		t.Fatalf("expected 2 pending messages after room.state, got %d", len(a.pendingHistory))
	}

	// Simulate the async load completing.
	model, _ := a.Update(ServerMsg{Raw: raw})
	a = model.(App)
	model, _ = a.Update(HistoryLoadedMsg{Messages: state.Messages})
	a = model.(App)

	if len(a.chat.messages) != 2 {
		t.Fatalf("expected 2 messages in chat after history load, got %d", len(a.chat.messages))
	}
	if a.chat.messages[0].From != "alice" {
		t.Errorf("unexpected first message From: %s", a.chat.messages[0].From)
	}
	if a.chat.messages[1].Content[0].Text != "world" {
		t.Errorf("unexpected second message text: %s", a.chat.messages[1].Content[0].Text)
	}
}

func TestTypingDoesNotScrollChat(t *testing.T) {
	a := makeApp()

	// Add enough messages to make the viewport scrollable.
	for i := 0; i < 50; i++ {
		a.chat.AddMessage(protocol.MessageParams{
			ID:   fmt.Sprintf("msg-%d", i),
			From: "alice",
			Role: "human",
			Content: []protocol.Content{
				{Type: "text", Text: fmt.Sprintf("Message number %d with some text", i)},
			},
		})
	}

	// Simulate a window size to trigger layout.
	model, _ := a.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	a = model.(App)

	// Record viewport scroll position after layout.
	scrollBefore := a.chat.vp.YOffset

	// Send key events that the viewport's default KeyMap would interpret as
	// scroll commands: 'k' maps to Up in the viewport's KeyMap.
	// When the user types these characters into the input, the chat viewport
	// should NOT scroll.
	scrollKeys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'k'}}, // viewport: scroll up
		{Type: tea.KeyRunes, Runes: []rune{'u'}}, // viewport: half page up
		{Type: tea.KeyRunes, Runes: []rune{'b'}}, // viewport: page up
	}
	for _, k := range scrollKeys {
		model, _ = a.Update(k)
		a = model.(App)
	}

	scrollAfter := a.chat.vp.YOffset

	if scrollAfter != scrollBefore {
		t.Errorf("typing changed viewport scroll position: before=%d, after=%d", scrollBefore, scrollAfter)
	}
}

func TestHandleServerMsg_NilRaw_NoOp(t *testing.T) {
	a := makeApp()
	// Should not panic.
	a.handleServerMsg(nil)
}

func TestHandleServerMsg_UnknownMethod_NoOp(t *testing.T) {
	a := makeApp()
	raw := &protocol.RawMessage{Method: "unknown.method"}
	// Should not panic.
	a.handleServerMsg(raw)
	if len(a.sidebar.participants) != 0 {
		t.Error("expected no participants after unknown method")
	}
}

// ---- Modal integration -------------------------------------------------------

func TestApp_InfoCommand_ShowsModal(t *testing.T) {
	app := makeAppWithRegistry()

	// Type "/info" and press Enter.
	for _, ch := range "/info" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)

	if app.modal == nil {
		t.Fatal("expected modal to be shown after /info command")
	}
	if len(app.chat.messages) != 0 {
		t.Errorf("expected chat history to be clean, got %d messages", len(app.chat.messages))
	}
	view := app.View()
	if !strings.Contains(view, "Room Info") {
		t.Errorf("expected modal view to contain 'Room Info', got:\n%s", view)
	}
}

func TestApp_ModalDismissedByEsc(t *testing.T) {
	app := makeAppWithRegistry()

	// Open modal via /info.
	for _, ch := range "/info" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if app.modal == nil {
		t.Fatal("precondition: modal must be open")
	}

	// Dismiss with Esc.
	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	app = model.(App)
	if app.modal != nil {
		t.Fatal("expected modal to be dismissed after Esc")
	}
}

func TestApp_ModalDismissedByQ(t *testing.T) {
	app := makeAppWithRegistry()

	// Open modal via /info.
	for _, ch := range "/info" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)
	if app.modal == nil {
		t.Fatal("precondition: modal must be open")
	}

	// Dismiss with q.
	model, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	app = model.(App)
	if app.modal != nil {
		t.Fatal("expected modal to be dismissed after q")
	}
}

func TestApp_ModalView_ShowsModalContent(t *testing.T) {
	app := makeAppWithRegistry()

	for _, ch := range "/info" {
		model, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		app = model.(App)
	}
	model, _ := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = model.(App)

	view := app.View()
	if !strings.Contains(view, "Room Info") {
		t.Errorf("expected modal view to contain 'Room Info', got:\n%s", view)
	}
}

func TestApp_SlashTrigger_ActivatesSuggestions(t *testing.T) {
	a := makeApp()
	reg := command.NewRegistry()
	reg.Register(&command.Command{Name: "info", Usage: "/info", Description: "Room info"})
	reg.Register(&command.Command{Name: "save", Usage: "/save", Description: "Save state"})
	a.SetCommandRegistry(reg, command.Context{})

	a.input.ta.SetValue("/")
	a.checkSuggestionTrigger()

	if !a.suggestions.Visible() {
		t.Error("expected suggestions visible after typing /")
	}
	if a.completionTrigger != '/' {
		t.Errorf("expected trigger '/', got %c", a.completionTrigger)
	}
}

func TestApp_AtTrigger_ActivatesSuggestions(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "claude", Role: "agent", Online: true},
		{Name: "gemini", Role: "agent", Online: true},
	})

	a.input.ta.SetValue("@")
	a.checkSuggestionTrigger()

	if !a.suggestions.Visible() {
		t.Error("expected suggestions visible after typing @")
	}
	if a.completionTrigger != '@' {
		t.Errorf("expected trigger '@', got %c", a.completionTrigger)
	}
}

func TestApp_AtTrigger_MidMessage(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "claude", Role: "agent", Online: true},
	})

	a.input.ta.SetValue("hello @")
	a.checkSuggestionTrigger()

	if !a.suggestions.Visible() {
		t.Error("expected suggestions visible after 'hello @'")
	}
	if a.completionStart != 6 {
		t.Errorf("expected completionStart 6, got %d", a.completionStart)
	}
}

func TestApp_AtTrigger_NotAfterAlpha(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "claude", Role: "agent", Online: true},
	})

	a.input.ta.SetValue("email@")
	a.checkSuggestionTrigger()

	if a.suggestions.Visible() {
		t.Error("expected suggestions NOT visible after 'email@'")
	}
}

func TestApp_SlashTrigger_NilRegistry_NoActivation(t *testing.T) {
	a := makeApp()

	a.input.ta.SetValue("/")
	a.checkSuggestionTrigger()

	if a.suggestions.Visible() {
		t.Error("expected suggestions NOT visible when registry is nil")
	}
}

func TestApp_AcceptSuggestion_InsertsText(t *testing.T) {
	a := makeApp()
	a.sidebar.SetParticipants([]protocol.Participant{
		{Name: "claude", Role: "agent", Online: true},
		{Name: "gemini", Role: "agent", Online: true},
	})

	a.input.ta.SetValue("hello @cl")
	a.completionTrigger = '@'
	a.completionStart = 6
	a.suggestions.SetItems([]SuggestionItem{
		{Label: "@claude", Description: "agent"},
		{Label: "@gemini", Description: "agent"},
	})
	a.suggestions.Filter("cl")

	a.acceptSuggestion()

	got := a.input.Value()
	if got != "hello @claude " {
		t.Errorf("expected 'hello @claude ', got %q", got)
	}
	if a.suggestions.Visible() {
		t.Error("expected suggestions hidden after accept")
	}
}

func TestApp_FilterSuggestions_NarrowsList(t *testing.T) {
	a := makeApp()
	reg := command.NewRegistry()
	reg.Register(&command.Command{Name: "info", Usage: "/info", Description: "Room info"})
	reg.Register(&command.Command{Name: "save", Usage: "/save", Description: "Save state"})
	a.SetCommandRegistry(reg, command.Context{})

	a.input.ta.SetValue("/")
	a.checkSuggestionTrigger()

	a.input.ta.SetValue("/s")
	a.updateSuggestionFilter()

	if len(a.suggestions.filtered) != 1 {
		t.Fatalf("expected 1 filtered item, got %d", len(a.suggestions.filtered))
	}
	if a.suggestions.filtered[0].Label != "/save" {
		t.Errorf("expected /save, got %s", a.suggestions.filtered[0].Label)
	}
}

// ---- Room event handlers (Task 7) ------------------------------------------

func makeAppWithRoomState() App {
	app := makeApp()
	app.localActivities = make(map[string]room.Activity)
	return app
}

func TestApp_RoomMessageReceived_AddsToChat(t *testing.T) {
	a := makeAppWithRoomState()
	msg := protocol.MessageParams{
		From:    "alice",
		Content: []protocol.Content{{Type: "text", Text: "hello"}},
	}
	model, _ := a.Update(room.MessageReceived{Message: msg})
	a = model.(App)

	if len(a.localMessages) != 1 {
		t.Fatalf("expected 1 local message, got %d", len(a.localMessages))
	}
	if a.localMessages[0].From != "alice" {
		t.Fatalf("expected from alice, got %s", a.localMessages[0].From)
	}
}

func TestApp_RoomHistoryLoaded_BulkReplacesState(t *testing.T) {
	a := makeAppWithRoomState()
	model, _ := a.Update(room.HistoryLoaded{
		Messages: []protocol.MessageParams{
			{From: "alice", Content: []protocol.Content{{Type: "text", Text: "hi"}}},
			{From: "bob", Content: []protocol.Content{{Type: "text", Text: "hey"}}},
		},
		Participants: []protocol.Participant{
			{Name: "alice", Online: true},
			{Name: "bob", Online: true},
		},
		Activities: map[string]room.Activity{
			"bob": room.ActivityGenerating,
		},
	})
	a = model.(App)

	if len(a.localMessages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(a.localMessages))
	}
	if len(a.localParticipants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(a.localParticipants))
	}
	if a.localActivities["bob"] != room.ActivityGenerating {
		t.Fatal("expected bob to be generating")
	}
}

func TestApp_RoomParticipantsChanged_UpdatesLocal(t *testing.T) {
	a := makeAppWithRoomState()
	model, _ := a.Update(room.ParticipantsChanged{
		Participants: []protocol.Participant{
			{Name: "alice", Online: true},
		},
	})
	a = model.(App)

	if len(a.localParticipants) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(a.localParticipants))
	}
}

func TestApp_RoomParticipantActivityChanged_UpdatesLocal(t *testing.T) {
	a := makeAppWithRoomState()
	model, _ := a.Update(room.ParticipantActivityChanged{
		Name:     "claude",
		Activity: room.ActivityGenerating,
	})
	a = model.(App)

	if a.localActivities["claude"] != room.ActivityGenerating {
		t.Fatal("expected claude to be generating")
	}
}

func TestApp_RoomErrorOccurred_AddsSystemMessage(t *testing.T) {
	a := makeAppWithRoomState()
	model, _ := a.Update(room.ErrorOccurred{
		Error: fmt.Errorf("test error"),
	})
	a = model.(App)

	if len(a.chat.messages) == 0 {
		t.Fatal("expected error message in chat")
	}
}
