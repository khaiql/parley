# TUI Display Delay Fix

**Issue:** [#105](https://github.com/khaiql/parley/issues/105)  
**Date:** 2026-04-08

## Problem

When `parley join` is run, the TUI only appears after the agent subprocess is fully responsive. For Claude Code and Gemini, this can take several seconds, leaving the user staring at a blank terminal with no feedback.

## Solution

Reorder the startup sequence in `runJoin()` so the TUI appears immediately. The agent starts in a background goroutine. While the agent is initializing, a full-screen loading screen is shown. Once the agent is ready, the loading screen transitions to the normal chat UI.

## Startup Sequence (new)

**Before:**
```
connect → join room → startAgent (blocks) → create TUI → p.Run()
```

**After:**
```
connect → join room → create TUI → [goroutine: startAgent] → p.Run()
                                         ↓ AgentReadyMsg or AgentStartFailedMsg
```

## Changes

### `cmd/parley/join.go`

- Move agent initialization into a goroutine launched before `p.Run()`
- The goroutine sends `tui.AgentReadyMsg` on success or `tui.AgentStartFailedMsg{Err}` on failure
- A `sync.Mutex`-protected struct stores the driver and dispatcher references once ready, for cleanup after `p.Run()` returns
- The dispatcher (`startDispatcher`) and agent bridge (`startAgentBridge`) are wired inside the goroutine once the driver is available
- After `p.Run()` returns: stop driver, close dispatcher, save agent session; if agent failed to start, return the error (cobra prints it)

### `internal/tui/app.go`

**New message types:**
- `AgentReadyMsg{}` — agent started successfully; transition to full chat UI
- `AgentStartFailedMsg{Err error}` — agent failed to start; quit TUI, caller returns error

**New `App` field:**
- `initializing bool` — when true, `View()` renders the loading screen instead of the full layout

**New method:**
- `SetInitializing(bool)` — called from `runJoin()` before `p.Run()`

**`Init()` change:**
- Also returns `a.spinner.Tick` when `initializing == true`

**`View()` change:**
- When `initializing == true`: render centered loading screen (see below)
- Otherwise: existing full layout

**`Update()` additions:**
- `AgentReadyMsg`: set `initializing = false`, trigger layout
- `AgentStartFailedMsg`: return `tea.Quit`
- `spinner.TickMsg`: update spinner model; return `a.spinner.Tick` if `initializing == true` or any participant is generating

### `internal/tui/sidebar.go`

- Remove `spinnerFrames` var, `spinnerFrame int` field, and `TickSpinner()` method
- Embed a `spinner.Model` from `github.com/charmbracelet/bubbles/spinner`
- Sidebar's `Update(msg tea.Msg)` handles `spinner.TickMsg` internally
- Activity rendering uses `s.spinner.View()` instead of `spinnerFrames[s.spinnerFrame]`

### `internal/tui/app.go` (spinner consolidation)

- Remove `SpinnerTickMsg` type, `spinnerTick()` func, and `SpinnerTickMsg` case in `Update()`
- Remove `maybeStartSpinnerFromActivities()` helper
- Add `spinner spinner.Model` field; initialize in `NewApp()` with `spinner.New()`
- In `Update()`, handle `spinner.TickMsg`: update `a.spinner`, pass to sidebar, continue ticking if `initializing || isAnyGenerating(a.localActivities)`
- On `spinner.TickMsg`, explicitly call `a.sidebar.Update(msg)` so the sidebar's embedded spinner advances in sync

## Loading Screen

Rendered by `App.View()` when `initializing == true`:

```
[terminal full screen]

         ⣷ Starting claude…

```

- Uses `lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, content)` for centering
- Spinner frame from `a.spinner.View()`
- Agent type name from a stored `agentTypeName string` field set before `p.Run()`
- Style: `colorDimText` (consistent with existing "Loading history…" in chat)

## Error Handling

If `startAgent()` fails:
- Goroutine sends `AgentStartFailedMsg{Err}` to the program
- `App.Update()` returns `tea.Quit`
- After `p.Run()` returns, `runJoin()` returns the stored error
- Cobra prints it to stdout as usual — same behavior as today

## Testing

- Golden file snapshot test for the loading screen render in `internal/tui/testdata/`
- Existing e2e test (`/e2e-test`) covers the end-to-end join flow including TUI appearing promptly
- All existing TUI golden tests must still pass after spinner migration
