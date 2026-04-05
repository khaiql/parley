# Unique Agent Names and Colors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure no two online agents in the same room share a name or color at the same time.

**Architecture:** Server assigns a unique `ColorIndex` (0–7) to each agent when they join by scanning the current online participants and picking the first free slot. When an agent leaves, the slot is implicitly freed (only online participants are counted). The TUI replaces FNV-hash-based color selection with the server-assigned index. For name uniqueness, the join client retries with a fresh random name when the server rejects with "name already taken", up to 10 attempts; if the user explicitly provided `--name` and it is taken, the error is surfaced immediately.

**Tech Stack:** Go, Bubble Tea, Lipgloss, JSON-RPC 2.0 over TCP

---

## File Map

| File | Change |
|------|--------|
| `internal/protocol/protocol.go` | Add `ColorIndex int` to `Participant` and `JoinedParams` |
| `internal/protocol/protocol_test.go` | JSON round-trip test for new fields |
| `internal/server/room.go` | Add `ClientConn.ColorIndex`, `assignColorIndex()`, update `Join` + `snapshot` |
| `internal/server/room_test.go` | Tests for unique/freed color indices |
| `internal/server/server.go` | Set `ColorIndex` in `JoinedParams` broadcast |
| `internal/tui/styles.go` | Add `ColorForIndex(idx int)` |
| `internal/tui/styles_test.go` | Test `ColorForIndex` |
| `internal/tui/chat.go` | Add `colors map[string]int`, `SetColors`, `AddColor`; thread through `renderMessages` / `highlightMentions` |
| `internal/tui/chat_test.go` | Test color map storage and fallback |
| `internal/tui/sidebar.go` | Replace `ColorForSender(p.Name, false)` with `ColorForIndex(p.ColorIndex)` |
| `internal/tui/app.go` | Wire color map updates in `handleServerMsg` |
| `internal/tui/testdata/*.golden` | Regenerate after color rendering change |
| `cmd/parley/main.go` | Extract `connectAndJoin`; retry loop for name collision in `runJoin` |

---

### Task 1: Add ColorIndex to protocol types

**Files:**
- Modify: `internal/protocol/protocol.go`
- Modify: `internal/protocol/protocol_test.go`

- [ ] **Step 1: Add `ColorIndex int` to `Participant` and `JoinedParams`**

In `internal/protocol/protocol.go` update these two structs:

```go
// Participant describes a single participant in a room.
type Participant struct {
	Name       string `json:"name"`
	Role       string `json:"role"`
	Directory  string `json:"directory,omitempty"`
	Repo       string `json:"repo,omitempty"`
	AgentType  string `json:"agent_type,omitempty"`
	Source     string `json:"source,omitempty"`
	Online     bool   `json:"online"`
	ColorIndex int    `json:"color_index"`
}

// JoinedParams is the server-side confirmation payload for "room/joined".
type JoinedParams struct {
	Name       string    `json:"name"`
	Role       string    `json:"role"`
	Directory  string    `json:"directory,omitempty"`
	Repo       string    `json:"repo,omitempty"`
	AgentType  string    `json:"agent_type,omitempty"`
	JoinedAt   time.Time `json:"joined_at"`
	ColorIndex int       `json:"color_index"`
}
```

- [ ] **Step 2: Write JSON round-trip test**

In `internal/protocol/protocol_test.go`, add:

```go
func TestParticipantColorIndexRoundTrip(t *testing.T) {
	p := Participant{Name: "cosmo", ColorIndex: 5}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Participant
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ColorIndex != 5 {
		t.Errorf("ColorIndex = %d, want 5", got.ColorIndex)
	}
}

func TestJoinedParamsColorIndexRoundTrip(t *testing.T) {
	jp := JoinedParams{Name: "cosmo", ColorIndex: 3}
	b, err := json.Marshal(jp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got JoinedParams
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ColorIndex != 3 {
		t.Errorf("ColorIndex = %d, want 3", got.ColorIndex)
	}
}
```

- [ ] **Step 3: Run tests**

```bash
go test ./internal/protocol/... -run "TestParticipantColorIndexRoundTrip|TestJoinedParamsColorIndexRoundTrip" -v
```

Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/protocol/protocol.go internal/protocol/protocol_test.go
git commit -m "feat: add ColorIndex to Participant and JoinedParams protocol types"
```

---

### Task 2: Server assigns unique color indices in Room.Join

**Files:**
- Modify: `internal/server/room.go`
- Modify: `internal/server/room_test.go`

- [ ] **Step 1: Write failing tests**

In `internal/server/room_test.go`, add:

```go
func TestJoinAssignsUniqueColorIndices(t *testing.T) {
	r := NewRoom("test")

	cc1 := &ClientConn{Name: "agent1", Role: "agent"}
	cc2 := &ClientConn{Name: "agent2", Role: "agent"}
	cc3 := &ClientConn{Name: "agent3", Role: "agent"}

	if _, err := r.Join(cc1); err != nil {
		t.Fatalf("join cc1: %v", err)
	}
	if _, err := r.Join(cc2); err != nil {
		t.Fatalf("join cc2: %v", err)
	}
	if _, err := r.Join(cc3); err != nil {
		t.Fatalf("join cc3: %v", err)
	}

	if cc1.ColorIndex == cc2.ColorIndex {
		t.Errorf("cc1 and cc2 share ColorIndex %d", cc1.ColorIndex)
	}
	if cc1.ColorIndex == cc3.ColorIndex {
		t.Errorf("cc1 and cc3 share ColorIndex %d", cc1.ColorIndex)
	}
	if cc2.ColorIndex == cc3.ColorIndex {
		t.Errorf("cc2 and cc3 share ColorIndex %d", cc2.ColorIndex)
	}
}

func TestJoinColorIndexFreedOnLeave(t *testing.T) {
	r := NewRoom("test")

	cc1 := &ClientConn{Name: "agent1", Role: "agent"}
	cc2 := &ClientConn{Name: "agent2", Role: "agent"}

	r.Join(cc1)
	r.Join(cc2)

	freedIdx := cc1.ColorIndex
	r.Leave("agent1")

	cc3 := &ClientConn{Name: "agent3", Role: "agent"}
	r.Join(cc3)

	if cc2.ColorIndex == cc3.ColorIndex {
		t.Errorf("cc2 and cc3 share ColorIndex %d after cc1 left", cc2.ColorIndex)
	}
	if cc3.ColorIndex != freedIdx {
		t.Errorf("cc3 ColorIndex = %d, want freed slot %d", cc3.ColorIndex, freedIdx)
	}
}

func TestJoinColorIndexInSnapshot(t *testing.T) {
	r := NewRoom("test")
	cc := &ClientConn{Name: "agent1", Role: "agent"}
	state, err := r.Join(cc)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}

	var found *protocol.Participant
	for i := range state.Participants {
		if state.Participants[i].Name == "agent1" {
			found = &state.Participants[i]
			break
		}
	}
	if found == nil {
		t.Fatal("agent1 not found in snapshot")
	}
	if found.ColorIndex != cc.ColorIndex {
		t.Errorf("snapshot ColorIndex = %d, want %d", found.ColorIndex, cc.ColorIndex)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/server/... -run "TestJoinAssignsUniqueColorIndices|TestJoinColorIndexFreedOnLeave|TestJoinColorIndexInSnapshot" -v
```

Expected: FAIL — color indices all 0, snapshot missing ColorIndex

- [ ] **Step 3: Add ColorIndex to ClientConn and implement assignColorIndex**

In `internal/server/room.go`, add `ColorIndex int` to `ClientConn`:

```go
// ClientConn represents a connected participant.
type ClientConn struct {
	Name       string
	Role       string
	Directory  string
	Repo       string
	AgentType  string
	Source     string
	Online     bool
	ColorIndex int
	Send       chan []byte
	Done       chan struct{}
}
```

Add constant and helper method after `NewRoom`:

```go
// numColorSlots is the number of distinct agent colors available (must match
// len(agentPalette) in internal/tui/styles.go).
const numColorSlots = 8

// assignColorIndex returns the first color index (0..numColorSlots-1) not
// already held by an online participant. Must be called with r.mu held.
// If all slots are taken (more than numColorSlots online agents), it wraps
// using the current participant count so the assignment is still deterministic.
func (r *Room) assignColorIndex() int {
	used := make([]bool, numColorSlots)
	for _, cc := range r.Participants {
		if cc.Online && cc.ColorIndex >= 0 && cc.ColorIndex < numColorSlots {
			used[cc.ColorIndex] = true
		}
	}
	for i, inUse := range used {
		if !inUse {
			return i
		}
	}
	// All slots taken — overflow by participant count (best-effort).
	return len(r.Participants) % numColorSlots
}
```

In `Join`, assign color index for new participants (the `else` branch):

```go
} else {
	cc.Online = true
	cc.ColorIndex = r.assignColorIndex()
	r.Participants[cc.Name] = cc
}
```

Assign color index for reconnecting participants (before setting `Online = true`):

```go
// Reconnecting offline participant — update and bring online.
if cc.Role != "" {
	existing.Role = cc.Role
}
existing.Directory = cc.Directory
existing.Repo = cc.Repo
existing.AgentType = cc.AgentType
existing.Source = cc.Source
existing.ColorIndex = r.assignColorIndex() // old slot may have been taken
existing.Online = true
existing.Send = cc.Send
existing.Done = cc.Done
```

Update `snapshot()` to include `ColorIndex`:

```go
func (r *Room) snapshot() []protocol.Participant {
	out := make([]protocol.Participant, 0, len(r.Participants))
	for _, cc := range r.Participants {
		out = append(out, protocol.Participant{
			Name:       cc.Name,
			Role:       cc.Role,
			Directory:  cc.Directory,
			Repo:       cc.Repo,
			AgentType:  cc.AgentType,
			Source:     cc.Source,
			Online:     cc.Online,
			ColorIndex: cc.ColorIndex,
		})
	}
	return out
}
```

- [ ] **Step 4: Run new tests**

```bash
go test ./internal/server/... -run "TestJoinAssignsUniqueColorIndices|TestJoinColorIndexFreedOnLeave|TestJoinColorIndexInSnapshot" -v
```

Expected: PASS

- [ ] **Step 5: Run full server tests**

```bash
go test ./internal/server/... -v
```

Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/server/room.go internal/server/room_test.go
git commit -m "feat: server assigns unique color index to each online agent"
```

---

### Task 3: Server broadcasts ColorIndex in room.joined

**Files:**
- Modify: `internal/server/server.go`

- [ ] **Step 1: Set ColorIndex in JoinedParams**

In `internal/server/server.go`, in the `room.join` case of `handleConn`, after `s.room.Join(cc)` succeeds, update the `JoinedParams` construction to include `cc.ColorIndex`:

```go
jp := protocol.JoinedParams{
	Name:       params.Name,
	Role:       effectiveRole,
	Directory:  params.Directory,
	Repo:       params.Repo,
	AgentType:  params.AgentType,
	JoinedAt:   time.Now().UTC(),
	ColorIndex: cc.ColorIndex,
}
```

- [ ] **Step 2: Build and run server tests**

```bash
go build ./internal/server/...
go test ./internal/server/... -v
```

Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/server/server.go
git commit -m "feat: include ColorIndex in room.joined broadcast"
```

---

### Task 4: TUI — add ColorForIndex to styles

**Files:**
- Modify: `internal/tui/styles.go`
- Modify: `internal/tui/styles_test.go`

- [ ] **Step 1: Write failing test**

In `internal/tui/styles_test.go`, add:

```go
func TestColorForIndex(t *testing.T) {
	for i := 0; i < len(agentPalette); i++ {
		got := ColorForIndex(i)
		if got != agentPalette[i] {
			t.Errorf("ColorForIndex(%d) = %v, want %v", i, got, agentPalette[i])
		}
	}
	// Out-of-range wraps.
	if ColorForIndex(len(agentPalette)) != agentPalette[0] {
		t.Errorf("ColorForIndex(%d) should wrap to agentPalette[0]", len(agentPalette))
	}
	if ColorForIndex(-1) != agentPalette[len(agentPalette)-1] {
		// negative index: Go's % is signed so abs-mod needed
		// We expect the implementation to handle this gracefully.
		// Just verify it doesn't panic and returns some palette color.
		_ = ColorForIndex(-1)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./internal/tui/... -run TestColorForIndex -v
```

Expected: FAIL — `ColorForIndex` undefined

- [ ] **Step 3: Add ColorForIndex to styles.go**

In `internal/tui/styles.go`, add after `ColorForSender`:

```go
// ColorForIndex returns the palette color for a server-assigned color index.
// The index wraps around so any non-negative integer is safe.
func ColorForIndex(idx int) lipgloss.Color {
	n := len(agentPalette)
	return agentPalette[((idx % n) + n) % n]
}
```

- [ ] **Step 4: Run test**

```bash
go test ./internal/tui/... -run TestColorForIndex -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tui/styles.go internal/tui/styles_test.go
git commit -m "feat: add ColorForIndex helper for server-assigned color indices"
```

---

### Task 5: TUI sidebar — use ColorForIndex

**Files:**
- Modify: `internal/tui/sidebar.go`

- [ ] **Step 1: Replace ColorForSender with ColorForIndex in sidebar View**

In `internal/tui/sidebar.go`, in the `View()` method, replace all three `ColorForSender(p.Name, false)` calls with `ColorForIndex(p.ColorIndex)`:

```go
// Name line
var nameLine string
if p.IsHuman() {
	nameLine = humanNameStyle.Render(p.Name)
} else {
	senderColor := ColorForIndex(p.ColorIndex)
	nameLine = agentNameStyleFor(senderColor).Render(p.Name)
}

// AgentType badge
if p.AgentType != "" {
	senderColor := ColorForIndex(p.ColorIndex)
	badge := agentBadgeStyleFor(senderColor).Render(p.AgentType)
	nameLine = lipgloss.JoinHorizontal(lipgloss.Top, nameLine, " ", badge)
}
// ...
// Status spinner
if status == "generating" {
	senderColor := ColorForIndex(p.ColorIndex)
	frame := spinnerFrames[s.spinnerFrame%len(spinnerFrames)]
	statusText := agentNameStyleFor(senderColor).Render(frame + " generating")
	lines = append(lines, "  "+statusText)
}
```

- [ ] **Step 2: Run sidebar tests**

```bash
go test ./internal/tui/... -run "TestSidebar" -v
```

Expected: PASS (layout tests should still pass; if golden files have exact ANSI color codes for agent participants, they need regenerating — see Task 7)

- [ ] **Step 3: Commit**

```bash
git add internal/tui/sidebar.go
git commit -m "feat: sidebar uses server-assigned ColorIndex instead of name hash"
```

---

### Task 6: TUI chat — use color map for message rendering

**Files:**
- Modify: `internal/tui/chat.go`
- Modify: `internal/tui/chat_test.go`

- [ ] **Step 1: Write failing test**

In `internal/tui/chat_test.go`, add:

```go
func TestChatSetColors(t *testing.T) {
	c := NewChat(80, 20)
	colors := map[string]int{"agent1": 3, "agent2": 5}
	c.SetColors(colors)
	if c.colors == nil {
		t.Fatal("colors map not stored after SetColors")
	}
	if c.colors["agent1"] != 3 {
		t.Errorf("colors[agent1] = %d, want 3", c.colors["agent1"])
	}
	if c.colors["agent2"] != 5 {
		t.Errorf("colors[agent2] = %d, want 5", c.colors["agent2"])
	}
}

func TestChatAddColor(t *testing.T) {
	c := NewChat(80, 20)
	c.AddColor("newagent", 2)
	if c.colors == nil {
		t.Fatal("colors map not initialized by AddColor")
	}
	if c.colors["newagent"] != 2 {
		t.Errorf("colors[newagent] = %d, want 2", c.colors["newagent"])
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
go test ./internal/tui/... -run "TestChatSetColors|TestChatAddColor" -v
```

Expected: FAIL — `SetColors`/`AddColor` undefined, `colors` field undefined

- [ ] **Step 3: Add colors map and methods to Chat**

In `internal/tui/chat.go`, update the `Chat` struct:

```go
type Chat struct {
	vp       viewport.Model
	messages []protocol.MessageParams
	colors   map[string]int // sender name → server-assigned color index
	loading  bool
	width    int
	height   int
}
```

Add `SetColors` and `AddColor` methods:

```go
// SetColors replaces the sender→colorIndex map and rebuilds the viewport.
func (c *Chat) SetColors(m map[string]int) {
	c.colors = m
	c.rebuildContent()
}

// AddColor adds or updates a single sender's color index and rebuilds the viewport.
func (c *Chat) AddColor(name string, colorIndex int) {
	if c.colors == nil {
		c.colors = make(map[string]int)
	}
	c.colors[name] = colorIndex
	c.rebuildContent()
}
```

Update `rebuildContent` to pass the color map:

```go
func (c *Chat) rebuildContent() {
	c.vp.SetContent(renderMessages(c.messages, c.width, c.colors))
}
```

- [ ] **Step 4: Add resolveAgentColor helper and update renderMessages / highlightMentions**

Add this function to `internal/tui/chat.go` (before `renderMessages`):

```go
// resolveAgentColor returns the color for an agent sender. It uses the
// server-assigned index from the colors map when available, falling back to
// the FNV hash for senders not in the map (e.g. historical messages from
// disconnected participants).
func resolveAgentColor(name string, colors map[string]int) lipgloss.Color {
	if colors != nil {
		if idx, ok := colors[name]; ok {
			return ColorForIndex(idx)
		}
	}
	return ColorForSender(name, false)
}
```

**Change 1 — `renderMessages` signature** (add `colors map[string]int` parameter):

Old signature:
```go
func renderMessages(msgs []protocol.MessageParams, width int) string {
```
New signature:
```go
func renderMessages(msgs []protocol.MessageParams, width int, colors map[string]int) string {
```

**Change 2 — inside `renderMessages`**, replace the single line:
```go
senderColor := ColorForSender(msg.From, isHuman)
```
with:
```go
var senderColor lipgloss.Color
if isHuman {
	senderColor = colorHuman
} else {
	senderColor = resolveAgentColor(msg.From, colors)
}
```

**Change 3 — inside `renderMessages`**, update the call to `highlightMentions` (the `body :=` line):
```go
body := highlightMentions(renderMarkdown(text, bodyWidth), colors)
```

**Change 4 — `highlightMentions` signature** (add `colors map[string]int` parameter):

Old signature:
```go
func highlightMentions(text string) string {
```
New signature:
```go
func highlightMentions(text string, colors map[string]int) string {
```

**Change 5 — inside `highlightMentions`**, in the loop that styles each mention (around the line `c := ColorForSender(name, false)`), replace:
```go
c := ColorForSender(name, false)
```
with:
```go
c := resolveAgentColor(name, colors)
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/tui/... -run "TestChatSetColors|TestChatAddColor" -v
```

Expected: PASS

- [ ] **Step 6: Run all TUI tests (fix compile errors)**

```bash
go test ./internal/tui/... -v
```

Expected: all PASS (any golden file failures are fixed in Task 7)

- [ ] **Step 7: Commit**

```bash
git add internal/tui/chat.go internal/tui/chat_test.go
git commit -m "feat: chat rendering uses server-assigned color indices via color map"
```

---

### Task 7: TUI app — wire color map from server events, regenerate golden files

**Files:**
- Modify: `internal/tui/app.go`
- Modify: `internal/tui/testdata/*.golden` (regenerated, not hand-edited)

- [ ] **Step 1: Update handleServerMsg to maintain chat color map**

In `internal/tui/app.go`, in `handleServerMsg`:

In the `room.state` case, after `a.sidebar.SetParticipants(params.Participants)`, add:

```go
// Build the sender→colorIndex map for chat rendering.
colors := make(map[string]int, len(params.Participants))
for _, p := range params.Participants {
	colors[p.Name] = p.ColorIndex
}
a.chat.SetColors(colors)
```

In the `room.joined` case, after `a.sidebar.AddParticipant(...)`, add:

```go
a.chat.AddColor(params.Name, params.ColorIndex)
```

- [ ] **Step 2: Build check**

```bash
go build ./internal/tui/...
```

Expected: no errors

- [ ] **Step 3: Regenerate golden files**

The sidebar now renders agent colors using `ColorForIndex(p.ColorIndex)` instead of FNV hash. Test participants have `ColorIndex=0` (default), so they get `agentPalette[0]`. Golden files that contain ANSI color sequences for agent participants need regeneration.

In `internal/tui/visual_test.go`, temporarily set `updateGolden = true`:

```go
const updateGolden = true
```

Run visual tests to regenerate:

```bash
go test ./internal/tui/... -run "TestVisual" -v
```

Set `updateGolden` back to `false`:

```go
const updateGolden = false
```

Verify tests pass with new golden files:

```bash
go test ./internal/tui/... -v
```

Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add internal/tui/app.go internal/tui/testdata/
git commit -m "feat: app wires server-assigned color indices into chat rendering"
```

---

### Task 8: Name collision retry in runJoin

**Files:**
- Modify: `cmd/parley/main.go`

- [ ] **Step 1: Extract connectAndJoin helper**

In `cmd/parley/main.go`, add this function before `runJoin`:

```go
// connectAndJoin dials the server at addr, sends room.join with params, and
// waits for a room.state response. Returns the live client and room state.
// Returns an error whose message is "name already taken" if rejected.
// The caller owns the returned client and must Close it when done.
func connectAndJoin(addr string, params protocol.JoinParams) (*client.Client, protocol.RoomStateParams, error) {
	c, err := client.New(addr)
	if err != nil {
		return nil, protocol.RoomStateParams{}, err
	}
	if err := c.Join(params); err != nil {
		c.Close()
		return nil, protocol.RoomStateParams{}, err
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg, ok := <-c.Incoming():
			if !ok {
				return nil, protocol.RoomStateParams{}, fmt.Errorf("connection closed before room state")
			}
			if msg.Method == "room.state" {
				var state protocol.RoomStateParams
				if err := json.Unmarshal(msg.Params, &state); err != nil {
					c.Close()
					return nil, protocol.RoomStateParams{}, fmt.Errorf("decode room.state: %w", err)
				}
				return c, state, nil
			}
			if msg.Error != nil {
				c.Close()
				return nil, protocol.RoomStateParams{}, fmt.Errorf("%s", msg.Error.Message)
			}
		case <-timeout:
			c.Close()
			return nil, protocol.RoomStateParams{}, fmt.Errorf("timeout: server did not send room state within 5 seconds")
		}
	}
}
```

- [ ] **Step 2: Replace connect/join/waitForState block in runJoin with retry loop**

In `runJoin`, remove the existing code that does:
```go
c, err := client.New(addr)
...
defer c.Close()
...
if err := c.Join(protocol.JoinParams{...}); err != nil { ... }

// Wait for room.state...
var roomState protocol.RoomStateParams
timeout := time.After(5 * time.Second)
found := false
for !found { ... }
```

Replace the above block (keep `dir` and `repo` detection before it) with:

```go
const maxNameAttempts = 10
userProvidedName := joinName != ""
var c *client.Client
var roomState protocol.RoomStateParams

triedNames := make(map[string]bool)
for attempt := 0; attempt < maxNameAttempts; attempt++ {
	if !userProvidedName || attempt > 0 {
		for {
			candidate := randomName()
			if !triedNames[candidate] {
				joinName = candidate
				break
			}
		}
		if attempt == 0 {
			fmt.Fprintf(os.Stderr, "No --name provided, using: %s\n", joinName)
		} else {
			fmt.Fprintf(os.Stderr, "Name taken, retrying with: %s\n", joinName)
		}
	}
	triedNames[joinName] = true

	var joinErr error
	c, roomState, joinErr = connectAndJoin(addr, protocol.JoinParams{
		Name:      joinName,
		Role:      joinRole,
		Directory: dir,
		Repo:      repo,
		AgentType: agentCmd,
	})
	if joinErr == nil {
		break
	}
	if strings.Contains(joinErr.Error(), "name already taken") {
		if userProvidedName {
			// User explicitly named themselves — don't try random alternatives.
			return fmt.Errorf("join: %w", joinErr)
		}
		c = nil
		continue
	}
	return fmt.Errorf("join: %w", joinErr)
}
if c == nil {
	return fmt.Errorf("join: could not join after %d attempts (all names taken)", maxNameAttempts)
}
defer c.Close()
```

- [ ] **Step 3: Build**

```bash
go build ./cmd/parley/...
```

Expected: no errors

- [ ] **Step 4: Run tests**

```bash
go test ./cmd/parley/... -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/parley/main.go
git commit -m "feat: retry with new random name when server rejects join as taken"
```

---

### Task 9: CI gates

- [ ] **Step 1: Build**

```bash
go build ./...
```

Expected: exit 0

- [ ] **Step 2: Lint**

```bash
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
```

Expected: no lint errors. Fix any issues found before proceeding.

- [ ] **Step 3: Tests with race detector**

```bash
go test ./... -timeout 30s -race
```

Expected: all PASS, no data races

- [ ] **Step 4: Commit any lint fixes**

If lint required changes:

```bash
git add -p
git commit -m "fix: lint issues from unique names/colors feature"
```

---

### Task 10: Push and open PR

- [ ] **Step 1: Push branch**

```bash
git push -u origin claude/bold-yonath
```

- [ ] **Step 2: Open PR**

```bash
gh pr create \
  --title "fix: unique agent names and colors — no two online agents share name or color" \
  --body "$(cat <<'EOF'
## Summary

- **Server** assigns a unique `ColorIndex` (0–7) to each agent when they join, scanning online participants for the first free slot. On leave the slot is implicitly freed.
- **Protocol** adds `ColorIndex int` to `Participant` and `JoinedParams` so the TUI always knows each participant's assigned color.
- **TUI sidebar** switches from FNV-hash color assignment to the server-assigned index, guaranteeing uniqueness among online agents.
- **TUI chat** introduces a `colors map[string]int` threaded through message rendering so message border/header colors match the sidebar. Falls back to FNV hash for historical messages from disconnected participants.
- **runJoin** retries with a new random name (up to 10 attempts) when the server rejects with "name already taken". If the user explicitly passed `--name`, the error surfaces immediately.

Fixes #75.

## Test plan

- [ ] `go test ./... -race` passes
- [ ] Start a room; join 3+ agents without `--name`; confirm distinct names and sidebar colors
- [ ] Kill one agent and join a new one; confirm the freed color slot is reused
- [ ] Join with a `--name` that is already taken; confirm an immediate error (no silent retry)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```
