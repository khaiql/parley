# Remote Join Support Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow `parley join` to connect to a remote host by accepting an optional `--host/-H` flag (defaulting to `localhost`), and update the topbar to display the actual host rather than the hardcoded string "localhost".

**Architecture:** Two focused changes: (1) add a `joinHost` flag to `cmd/parley/join.go` and thread it into `client.New`, (2) add a `host` field + `SetHost` method to `internal/tui/topbar.go` and expose `App.SetHost` in `internal/tui/app.go` so the join command can push the remote host into the UI. The host command is not changed — it already binds to all interfaces (`":PORT"`) and uses localhost for its own client connection, which is correct.

**Tech Stack:** Go, Cobra flags, Bubble Tea TUI, Lipgloss rendering

---

### Task 1: Add `host` field and `SetHost` to TopBar

**Files:**
- Modify: `internal/tui/topbar.go`
- Create: `internal/tui/topbar_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/tui/topbar_test.go`:

```go
package tui

import (
	"strings"
	"testing"
)

func TestTopBar_DefaultHostIsLocalhost(t *testing.T) {
	tb := NewTopBar("my topic", 8080)
	tb.SetWidth(80)
	view := tb.View()
	if !strings.Contains(view, "localhost:8080") {
		t.Errorf("expected topbar to contain %q, got:\n%s", "localhost:8080", view)
	}
}

func TestTopBar_SetHostChangesDisplay(t *testing.T) {
	tb := NewTopBar("my topic", 9000)
	tb.SetHost("192.168.1.50")
	tb.SetWidth(80)
	view := tb.View()
	if !strings.Contains(view, "192.168.1.50:9000") {
		t.Errorf("expected topbar to contain %q, got:\n%s", "192.168.1.50:9000", view)
	}
	if strings.Contains(view, "localhost") {
		t.Errorf("expected topbar NOT to contain %q after SetHost, got:\n%s", "localhost", view)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go test ./internal/tui/... -run TestTopBar_ -v`

Expected: FAIL — `SetHost` undefined

- [ ] **Step 3: Add `host` field and `SetHost` to TopBar**

In `internal/tui/topbar.go`, make these changes:

Change the `TopBar` struct to add a `host` field:
```go
type TopBar struct {
	topic string
	port  int
	host  string // remote host; defaults to "localhost"
	name  string // agent name (empty for host)
	role  string // agent role (empty for host)
	width int
}
```

Change `NewTopBar` to initialise `host`:
```go
func NewTopBar(topic string, port int) TopBar {
	return TopBar{topic: topic, port: port, host: "localhost"}
}
```

Add `SetHost` after `NewTopBar`:
```go
// SetHost overrides the host label shown in the topbar (default: "localhost").
func (t *TopBar) SetHost(h string) {
	t.host = h
}
```

Change the `View()` rendering of the right segment from:
```go
right = lipgloss.NewStyle().Foreground(colorDimText).Render(fmt.Sprintf("localhost:%d", t.port))
```
to:
```go
right = lipgloss.NewStyle().Foreground(colorDimText).Render(fmt.Sprintf("%s:%d", t.host, t.port))
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go test ./internal/tui/... -run TestTopBar_ -v`

Expected: PASS

- [ ] **Step 5: Run full TUI test suite**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go test ./internal/tui/... -timeout 30s`

Expected: all tests pass, golden files unchanged (default host is still "localhost")

- [ ] **Step 6: Commit**

```bash
cd /Users/sle/group_chat/.worktrees/issue-107-remote-join
git add internal/tui/topbar.go internal/tui/topbar_test.go
git commit -m "feat(tui): add SetHost to TopBar for remote host display

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 2: Expose `App.SetHost` in the TUI app

**Files:**
- Modify: `internal/tui/app.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/tui/topbar_test.go` (append after the existing tests — no import changes needed, `strings` and `testing` are already imported):

```go
func TestApp_SetHostPropagatesToTopBar(t *testing.T) {
	app := NewApp("topic", 7777, InputModeHuman, "tester", nil)
	app.SetHost("10.0.0.1")
	// Access topbar directly (same package) to verify the host was propagated.
	app.topbar.SetWidth(80)
	view := app.topbar.View()
	if !strings.Contains(view, "10.0.0.1:7777") {
		t.Errorf("expected topbar to contain %q after App.SetHost, got:\n%s", "10.0.0.1:7777", view)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go test ./internal/tui/... -run TestApp_SetHost -v`

Expected: FAIL — `SetHost` undefined on App

- [ ] **Step 3: Add `SetHost` method to App**

In `internal/tui/app.go`, add this method near the other `Set*` methods (e.g. after `SetAgent`):

```go
// SetHost sets the remote host label shown in the topbar (default: "localhost").
// Call this after NewApp when joining a remote session.
func (a *App) SetHost(h string) {
	a.topbar.SetHost(h)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go test ./internal/tui/... -run TestApp_SetHost -v`

Expected: PASS

- [ ] **Step 5: Run full TUI test suite**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go test ./internal/tui/... -timeout 30s`

Expected: all tests pass

- [ ] **Step 6: Commit**

```bash
cd /Users/sle/group_chat/.worktrees/issue-107-remote-join
git add internal/tui/app.go internal/tui/topbar_test.go
git commit -m "feat(tui): expose App.SetHost for remote join display

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 3: Add `--host/-H` flag to `join` command

**Files:**
- Modify: `cmd/parley/join.go`
- Create: `cmd/parley/join_host_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/parley/join_host_test.go`:

```go
package main

import (
	"fmt"
	"testing"
)

func TestJoinHostFlagDefault(t *testing.T) {
	// joinHost is a package-level var; its zero-value before flag parsing is "".
	// After cobra init() runs (package init), the default is "localhost".
	// Reinitialise the command to pick up the registered default.
	joinCmd.ResetFlags()
	initJoinFlags()

	got, err := joinCmd.Flags().GetString("host")
	if err != nil {
		t.Fatalf("host flag not registered: %v", err)
	}
	if got != "localhost" {
		t.Errorf("expected default host %q, got %q", "localhost", got)
	}
}

func TestJoinAddrFormat(t *testing.T) {
	tests := []struct {
		host string
		port int
		want string
	}{
		{"localhost", 8080, "localhost:8080"},
		{"192.168.1.10", 9000, "192.168.1.10:9000"},
		{"my-server.local", 1234, "my-server.local:1234"},
	}
	for _, tc := range tests {
		got := fmt.Sprintf("%s:%d", tc.host, tc.port)
		if got != tc.want {
			t.Errorf("addr(%q,%d) = %q, want %q", tc.host, tc.port, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go test ./cmd/parley/... -run TestJoinHost -v`

Expected: FAIL — `initJoinFlags` undefined

- [ ] **Step 3: Add flag and extract `initJoinFlags` helper**

In `cmd/parley/join.go`:

1. Add the package-level var:
```go
var joinHost string
```
(add alongside the existing `joinPort`, `joinName`, etc. block)

2. Extract the flag-registration calls from `init()` into a new helper function, and call it from `init()`. Replace the current `init()` body with:

```go
func initJoinFlags() {
	joinCmd.Flags().IntVar(&joinPort, "port", 0, "Port of the session to join (required)")
	joinCmd.Flags().StringVarP(&joinHost, "host", "H", "localhost", "Hostname or IP of the session to join")
	joinCmd.Flags().StringVar(&joinName, "name", "", "Your name in the session (random if not set)")
	joinCmd.Flags().StringVar(&joinRole, "role", "agent", "Your role in the session")
	joinCmd.Flags().BoolVar(&joinResume, "resume", false, "Resume prior agent session (looks up session ID from saved agents.json)")
	joinCmd.Flags().StringVarP(&joinAgentType, "agent-type", "t", protocol.AgentTypeClaude, fmt.Sprintf("Agent type (%s)", strings.Join(protocol.SupportedAgentTypes(), ", ")))
	joinCmd.Flags().SetInterspersed(false)
	_ = joinCmd.MarkFlagRequired("port")
}

func init() {
	initJoinFlags()
	rootCmd.AddCommand(joinCmd)
}
```

3. In `runJoin`, change the `client.New` call from:
```go
c, err := client.New(fmt.Sprintf("localhost:%d", joinPort))
```
to:
```go
c, err := client.New(fmt.Sprintf("%s:%d", joinHost, joinPort))
```

4. After `app.SetAgent(joinName, joinRole)` in `runJoin`, add:
```go
app.SetHost(joinHost)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go test ./cmd/parley/... -run TestJoinHost -v`

Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go test ./... -timeout 30s`

Expected: all packages pass

- [ ] **Step 6: Build to confirm compilation**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go build ./...`

Expected: exits 0, no output

- [ ] **Step 7: Verify flag appears in help**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go run ./cmd/parley join --help`

Expected output contains:
```
--host string   Hostname or IP of the session to join (default "localhost")
```

- [ ] **Step 8: Commit**

```bash
cd /Users/sle/group_chat/.worktrees/issue-107-remote-join
git add cmd/parley/join.go cmd/parley/join_host_test.go
git commit -m "feat(join): add --host/-H flag for remote session support

Fixes #107. The join command previously hardcoded localhost.
--host accepts any hostname or IP; defaults to localhost so
existing workflows are unchanged. The topbar now displays the
actual host:port for remote connections.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 4: CI quality gates

**Files:** none (verification only)

- [ ] **Step 1: Run lint**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m`

Expected: exits 0. Fix any reported issues before proceeding.

- [ ] **Step 2: Run tests with race detector**

Run: `cd /Users/sle/group_chat/.worktrees/issue-107-remote-join && go test ./... -timeout 30s -race`

Expected: all packages pass, no races detected

- [ ] **Step 3: Push branch and open PR**

```bash
cd /Users/sle/group_chat/.worktrees/issue-107-remote-join
git push -u origin issue-107-remote-join
gh pr create \
  --title "feat(join): add --host flag to support remote sessions (#107)" \
  --body "$(cat <<'EOF'
## Summary

- Adds `--host` flag to `parley join` (default: `localhost`) so agents and humans can connect to a session hosted on a different machine
- The server already binds to all interfaces (`":PORT"`), so no host-side changes are needed
- The topbar now shows the actual `host:port` instead of the hardcoded string `localhost:port`, so remote clients display the correct address

## Test plan

- [ ] `go test ./... -race` passes
- [ ] `parley join --help` shows `--host string` flag with default `localhost`
- [ ] Existing local workflow: `parley join --port 8080` still works (default host = localhost)
- [ ] Remote workflow: `parley join --host 192.168.1.5 --port 8080` connects to the given address
- [ ] Topbar displays `192.168.1.5:8080` when joining remotely

Closes #107

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```
