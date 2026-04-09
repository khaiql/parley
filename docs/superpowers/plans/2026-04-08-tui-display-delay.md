# TUI Display Delay Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the TUI immediately on `parley join` with a centered loading screen while the agent subprocess starts, then transition to the full chat UI once the agent is ready.

**Architecture:** Reorder `runJoin()` so the TUI starts first and the agent starts in a background goroutine. `App` gains an `initializing bool` field — when true, `View()` renders a centered loading screen using `bubbles/spinner` instead of the chat layout. The custom sidebar spinner (`spinnerFrames`, `TickSpinner`) is replaced with the same `bubbles/spinner` model owned by `App`.

**Tech Stack:** Go, Bubble Tea, `github.com/charmbracelet/bubbles/spinner` (already in go.mod at v1.0.0), Lipgloss

---

## File Map

| File | Change |
|------|--------|
| `internal/tui/sidebar.go` | Remove `spinnerFrames`, `spinnerFrame`, `TickSpinner()`. Add `spinnerView string` field + `SetSpinnerView(string)` method. Update `View()` to use `s.spinnerView`. |
| `internal/tui/sidebar_test.go` | Delete `TestSidebarTickSpinner`. Add `TestSidebarViewGeneratingSpinner_UsesSpinnerView`. |
| `internal/tui/app.go` | Remove `SpinnerTickMsg`, `spinnerTick()`, `maybeStartSpinnerFromActivities()`. Add `spinner spinner.Model`, `initializing bool`, `agentTypeName string`. Add `SetInitializing(bool, string)`, `AgentReadyMsg`, `AgentStartFailedMsg`. Update `Init()`, `Update()`, `View()`. |
| `internal/tui/app_test.go` | Replace `TestApp_SpinnerTickChains*` with `spinner.TickMsg`-based tests. Add loading screen tests. |
| `internal/tui/testdata/` | Regenerate golden files after spinner migration. |
| `cmd/parley/join.go` | Reorder: create TUI before `startAgent`. Launch agent goroutine. Handle `AgentReadyMsg` / `AgentStartFailedMsg`. Mutex-protected cleanup. |

---

## Task 1: Migrate sidebar to `bubbles/spinner`

**Files:**
- Modify: `internal/tui/sidebar.go`
- Modify: `internal/tui/sidebar_test.go`

- [ ] **Step 1: Write failing test for `SetSpinnerView`**

Add to `internal/tui/sidebar_test.go`:

```go
func TestSidebarViewGeneratingSpinner_UsesSpinnerView(t *testing.T) {
	s := NewSidebar()
	s.SetSize(30, 20)
	s.AddParticipant(protocol.Participant{Name: "bot1", Role: "coder", Source: "agent", Online: true})
	s.SetParticipantStatus("bot1", "generating")
	s.SetSpinnerView("⠋")

	view := stripANSI(s.View())
	if !contains(view, "⠋") {
		t.Errorf("sidebar view should contain spinner frame '⠋', got: %q", view)
	}
	if !contains(view, "generating") {
		t.Errorf("sidebar view should contain 'generating', got: %q", view)
	}
}
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
cd /Users/sle/group_chat/.claude/worktrees/snug-painting-turing
go test ./internal/tui/ -run TestSidebarViewGeneratingSpinner_UsesSpinnerView -v
```

Expected: compile error — `SetSpinnerView undefined`

- [ ] **Step 3: Replace sidebar custom spinner with `spinnerView string`**

In `internal/tui/sidebar.go`:

Remove the `var spinnerFrames` line and the `spinnerFrame int` field. Replace with `spinnerView string`. Remove the entire `TickSpinner` method. Add `SetSpinnerView`:

```go
// SetSpinnerView updates the spinner frame string used in generating status rows.
// Called by App whenever the spinner ticks.
func (s *Sidebar) SetSpinnerView(v string) {
	s.spinnerView = v
}
```

In `View()`, replace the line:
```go
frame := spinnerFrames[s.spinnerFrame%len(spinnerFrames)]
statusText := agentNameStyleFor(senderColor).Render(frame + " generating")
```
with:
```go
statusText := agentNameStyleFor(senderColor).Render(s.spinnerView + " generating")
```

The `Sidebar` struct becomes:
```go
type Sidebar struct {
	participants []protocol.Participant
	statuses     map[string]string
	width        int
	height       int
	port         int
	spinnerView  string
}
```

- [ ] **Step 4: Delete `TestSidebarTickSpinner` from `sidebar_test.go`**

Remove the entire `TestSidebarTickSpinner` test function (lines 239–257).

- [ ] **Step 5: Run sidebar tests**

```bash
go test ./internal/tui/ -run TestSidebar -v
```

Expected: all sidebar tests PASS (including the new `TestSidebarViewGeneratingSpinner_UsesSpinnerView`).

- [ ] **Step 6: Commit**

```bash
git add internal/tui/sidebar.go internal/tui/sidebar_test.go
git commit -m "refactor(tui): replace sidebar custom spinner with spinnerView field

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 2: Migrate App from `SpinnerTickMsg` to `bubbles/spinner`

**Files:**
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/app_test.go`

- [ ] **Step 1: Write failing tests for new spinner behavior**

Replace `TestApp_SpinnerTickChainsWhenGenerating` and `TestApp_SpinnerTickStopsWhenNobodyGenerating` in `internal/tui/app_test.go` with:

```go
func TestApp_SpinnerTick_ContinuesWhenGenerating(t *testing.T) {
	a := makeApp()
	a.localActivities["agent-1"] = room.ActivityGenerating

	model, cmd := a.Update(spinner.TickMsg{})
	_ = model

	if cmd == nil {
		t.Fatal("expected spinner tick command to continue when someone is generating")
	}
}

func TestApp_SpinnerTick_StopsWhenNobodyGenerating(t *testing.T) {
	a := makeApp()
	a.localActivities["agent-1"] = room.ActivityIdle

	model, cmd := a.Update(spinner.TickMsg{})
	_ = model

	if cmd != nil {
		t.Fatal("expected nil command when nobody is generating and not initializing")
	}
}
```

Add import `"github.com/charmbracelet/bubbles/spinner"` to the test file imports.

- [ ] **Step 2: Run failing tests**

```bash
go test ./internal/tui/ -run "TestApp_SpinnerTick" -v
```

Expected: compile errors — `SpinnerTickMsg` used in old tests doesn't exist yet (those tests still use the old type), and `a.spinner` doesn't exist on `App`.

- [ ] **Step 3: Update `app.go` — remove old spinner, add `bubbles/spinner`**

Add import to `internal/tui/app.go`:
```go
"github.com/charmbracelet/bubbles/spinner"
```

Remove from `app.go`:
- `SpinnerTickMsg` type definition
- `spinnerTick()` function
- `maybeStartSpinnerFromActivities()` method
- The `case SpinnerTickMsg:` block in `Update()`

Add `spinner spinner.Model` field to `App` struct:
```go
type App struct {
    // ... existing fields ...
    spinner spinner.Model
}
```

Initialize in `NewApp()` (add after creating `a`):
```go
sp := spinner.New()
sp.Spinner = spinner.Line
a.spinner = sp
```

Add `spinner.TickMsg` handler in `Update()`, before the `// Forward key events` section:

```go
case spinner.TickMsg:
    a.spinner, cmd = a.spinner.Update(m)
    a.sidebar.SetSpinnerView(a.spinner.View())
    if isAnyGenerating(a.localActivities) {
        return a, cmd
    }
    return a, nil // self-terminate
```

(Task 3 will extend this to also continue when `a.initializing` is true.)

Replace the return statements in the event cases that called `maybeStartSpinnerFromActivities()`:

`case room.HistoryLoaded:` — replace:
```go
return a, a.maybeStartSpinnerFromActivities()
```
with:
```go
return a, a.maybeStartSpinner()
```

`case room.ParticipantsChanged:` — same replacement.

`case room.ParticipantActivityChanged:` — same replacement.

Add the new helper method:
```go
// maybeStartSpinner returns a.spinner.Tick if the spinner should be running.
// Callers use this to (re)start the self-terminating spinner loop.
func (a *App) maybeStartSpinner() tea.Cmd {
	if isAnyGenerating(a.localActivities) {
		return a.spinner.Tick
	}
	return nil
}
```

- [ ] **Step 4: Run new spinner tests**

```bash
go test ./internal/tui/ -run "TestApp_SpinnerTick" -v
```

Expected: both PASS.

- [ ] **Step 5: Run full TUI test suite**

```bash
go test ./internal/tui/ -v 2>&1 | tail -20
```

Expected: all PASS. If golden files differ (spinner character changed), proceed to Step 6.

- [ ] **Step 6: Regenerate golden files if needed**

If any visual golden test fails, open `internal/tui/visual_test.go` and temporarily set `updateGolden = true`, run the tests, then set it back to `false`:

```bash
# Edit visual_test.go: set updateGolden = true
go test ./internal/tui/ -run "TestVisual" -v
# Edit visual_test.go: set updateGolden = false
go test ./internal/tui/ -run "TestVisual" -v
```

Expected: all PASS with `updateGolden = false`.

- [ ] **Step 7: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go internal/tui/testdata/
git commit -m "refactor(tui): replace SpinnerTickMsg with bubbles/spinner

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 3: Add loading screen to `App`

**Files:**
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/app_test.go`
- Modify: `internal/tui/visual_test.go` (new golden test)

- [ ] **Step 1: Write failing tests for loading screen**

Add to `internal/tui/app_test.go`:

```go
func TestApp_LoadingScreen_ViewShowsCenteredText(t *testing.T) {
	a := makeApp()
	a.SetInitializing(true, "claude")

	model, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = model.(App)

	view := stripANSI(a.View())
	if !strings.Contains(view, "Starting claude") {
		t.Errorf("loading screen should contain 'Starting claude', got:\n%s", view)
	}
	// Full chat components should NOT appear
	if strings.Contains(view, "PARTICIPANTS") {
		t.Errorf("loading screen should not show sidebar, got:\n%s", view)
	}
}

func TestApp_AgentReadyMsg_ExitsLoadingScreen(t *testing.T) {
	a := makeApp()
	a.SetInitializing(true, "claude")

	model, _ := a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	a = model.(App)

	model, _ = a.Update(AgentReadyMsg{})
	a = model.(App)

	if a.initializing {
		t.Error("expected initializing=false after AgentReadyMsg")
	}
	view := stripANSI(a.View())
	if strings.Contains(view, "Starting claude") {
		t.Errorf("after AgentReadyMsg, should show chat not loading screen")
	}
}

func TestApp_AgentStartFailedMsg_ReturnsQuit(t *testing.T) {
	a := makeApp()
	a.SetInitializing(true, "claude")

	_, cmd := a.Update(AgentStartFailedMsg{Err: fmt.Errorf("process failed")})

	// cmd should be tea.Quit — we can verify by checking it's non-nil
	// (tea.Quit is a non-nil Cmd; exact equality isn't testable but non-nil is sufficient)
	if cmd == nil {
		t.Error("expected tea.Quit command from AgentStartFailedMsg")
	}
}
```

- [ ] **Step 2: Run failing tests**

```bash
go test ./internal/tui/ -run "TestApp_Loading|TestApp_AgentReady|TestApp_AgentStartFailed" -v
```

Expected: compile errors — `SetInitializing`, `AgentReadyMsg`, `AgentStartFailedMsg` undefined.

- [ ] **Step 3: Add loading screen fields and message types to `app.go`**

Add new message types near the top of `app.go` (after `AgentTypingMsg`):

```go
// AgentReadyMsg signals that the agent subprocess started successfully.
// The App transitions from the loading screen to the full chat layout.
type AgentReadyMsg struct{}

// AgentStartFailedMsg signals that the agent subprocess failed to start.
// The App quits so the caller can return the error.
type AgentStartFailedMsg struct {
	Err error
}
```

Add fields to `App` struct:
```go
type App struct {
    // ... existing fields ...
    initializing  bool
    agentTypeName string
}
```

Add `SetInitializing` method:
```go
// SetInitializing puts the App into loading-screen mode.
// agentType is shown in the loading text (e.g. "claude", "gemini").
func (a *App) SetInitializing(v bool, agentType string) {
	a.initializing = v
	a.agentTypeName = agentType
}
```

Update `Init()` to start the spinner when initializing:
```go
func (a App) Init() tea.Cmd {
	if a.initializing {
		return tea.Batch(textarea.Blink, a.spinner.Tick)
	}
	return textarea.Blink
}
```

Add `spinner.TickMsg` continuation for `initializing` in the existing spinner handler — update the self-terminate check from:
```go
if !isAnyGenerating(a.localActivities) {
    cmds = cmds[:len(cmds)-1]
}
```
to:
```go
if !a.initializing && !isAnyGenerating(a.localActivities) {
    cmds = cmds[:len(cmds)-1]
}
```

Add handlers in `Update()` for the new message types (add before the `// Forward key events` comment):

```go
case AgentReadyMsg:
    a.initializing = false
    a.layout()
    return a, nil

case AgentStartFailedMsg:
    return a, tea.Quit
```

Update `View()` to branch on `initializing`:

```go
func (a App) View() string {
	if a.initializing {
		return a.loadingView()
	}
	if a.modal != nil {
		return a.modal.View()
	}
	// ... rest of existing View() unchanged ...
}
```

Add `loadingView()` method:

```go
// loadingView renders a centered loading screen shown while the agent starts.
func (a App) loadingView() string {
	msg := lipgloss.NewStyle().
		Foreground(colorDimText).
		Render(a.spinner.View() + " Starting " + a.agentTypeName + "…")
	return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, msg)
}
```

Also update `maybeStartSpinner()` to consider `initializing`:
```go
func (a *App) maybeStartSpinner() tea.Cmd {
	if a.initializing || isAnyGenerating(a.localActivities) {
		return a.spinner.Tick
	}
	return nil
}
```

- [ ] **Step 4: Run new loading screen tests**

```bash
go test ./internal/tui/ -run "TestApp_Loading|TestApp_AgentReady|TestApp_AgentStartFailed" -v
```

Expected: all PASS.

- [ ] **Step 5: Add golden test for loading screen**

Add to `internal/tui/visual_test.go`:

```go
func TestVisualLoadingScreen80x24(t *testing.T) {
	app := NewApp("test topic", 1234, InputModeHuman, "sle", nil)
	app.SetInitializing(true, "claude")
	model, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	app = model.(App)
	output := app.View()
	assertGolden(t, "loading_screen_80x24", output)
}
```

- [ ] **Step 6: Run golden test (creates file on first run)**

```bash
go test ./internal/tui/ -run TestVisualLoadingScreen80x24 -v
```

Expected: PASS — creates `internal/tui/testdata/loading_screen_80x24.golden`. Inspect the file to confirm "Starting claude" appears centered.

- [ ] **Step 7: Run full TUI test suite**

```bash
go test ./internal/tui/ -timeout 30s
```

Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/tui/app.go internal/tui/app_test.go internal/tui/visual_test.go internal/tui/testdata/
git commit -m "feat(tui): add loading screen for agent initialization

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 4: Reorder `runJoin()` startup sequence

**Files:**
- Modify: `cmd/parley/join.go`

- [ ] **Step 1: Rewrite `runJoin()` with TUI-first ordering**

Replace the body of `runJoin()` in `cmd/parley/join.go` with:

```go
func runJoin(cmd *cobra.Command, args []string) error {
	if joinName == "" {
		joinName = randomName()
		fmt.Fprintf(os.Stderr, "No --name provided, using: %s\n", joinName)
	}

	agentType := protocol.NormalizeAgentType(joinAgentType)
	extraArgs := parseExtraArgs(cmd, args)

	c, err := client.New(fmt.Sprintf("localhost:%d", joinPort))
	if err != nil {
		return fmt.Errorf("join: connect: %w", err)
	}
	defer c.Close()

	roomState, err := joinRoom(c, agentType)
	if err != nil {
		return err
	}

	store := persistence.NewJSONStore(defaultParleyDir())
	resumeSessionID := lookupResumeSession(store, roomState.RoomID)

	rs := room.New(nil, command.Context{})
	app := tui.NewApp(roomState.Topic, joinPort, tui.InputModeAgent, joinName, nil, roomState.Participants...)
	app.SetAgent(joinName, joinRole)
	app.SetYolo(roomState.AutoApprove)
	app.SetRoomState(rs)
	app.SetInitializing(true, agentType)

	p := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())

	bridgeEvents(p, rs)
	replayRoomState(rs, roomState)
	startJoinNetworkLoop(c, rs, p)

	// Agent starts in background; TUI shows loading screen immediately.
	var (
		mu        sync.Mutex
		theDriver driver.AgentDriver
		theDisp   *dispatcher.Debounce
		agentErr  error
	)
	go func() {
		d, err := startAgent(agentType, extraArgs, roomState, resumeSessionID)
		if err != nil {
			mu.Lock()
			agentErr = err
			mu.Unlock()
			p.Send(tui.AgentStartFailedMsg{Err: err})
			return
		}
		disp := startDispatcher(rs, d)
		mu.Lock()
		theDriver = d
		theDisp = disp
		mu.Unlock()
		p.Send(tui.AgentReadyMsg{})
		startAgentBridge(c, d, p)
	}()

	_, err = p.Run()

	rs.Close()

	mu.Lock()
	d := theDriver
	disp := theDisp
	startErr := agentErr
	mu.Unlock()

	if disp != nil {
		disp.Close()
	}
	if d != nil {
		_ = d.Stop()
		saveAgentSession(store, roomState.RoomID, d)
	}
	if startErr != nil {
		return fmt.Errorf("join: start agent driver: %w", startErr)
	}
	return err
}
```

Add `"sync"` to imports in `join.go`.

- [ ] **Step 2: Build to verify it compiles**

```bash
go build ./cmd/parley/
```

Expected: builds with no errors.

- [ ] **Step 3: Run all tests**

```bash
go test ./... -timeout 30s
```

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/parley/join.go
git commit -m "feat(join): show TUI immediately with loading screen while agent starts

Fixes #105

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 5: CI quality gates

- [ ] **Step 1: Build check**

```bash
go build ./...
```

Expected: exits 0.

- [ ] **Step 2: Lint**

```bash
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
```

Expected: no errors. Fix any flagged issues before proceeding.

- [ ] **Step 3: Tests with race detector**

```bash
go test ./... -timeout 30s -race
```

Expected: all PASS with no race conditions detected.

- [ ] **Step 4: Push**

```bash
git pull --rebase
git push
```
