# Server-Side Colour Assignment & Improved Name Generation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Server assigns unique colours to participants on join; client uses adjective-noun name generator to avoid collisions.

**Architecture:** Add `Color` field to `protocol.Participant`. Move colour palette to `internal/room/colours.go` with assignment logic. `room.State.Join()` picks an unassigned colour. TUI reads `Participant.Color` instead of computing from name hash. Client-side `randomName()` becomes adjective-noun combinator.

**Tech Stack:** Go, Bubble Tea, Lipgloss

---

### Task 1: Add `Color` field to `protocol.Participant`

**Files:**
- Modify: `internal/protocol/protocol.go:154-162`

- [ ] **Step 1: Add Color field to Participant struct**

```go
// Participant describes a single participant in a room.
type Participant struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	Color     string `json:"color,omitempty"`
	Directory string `json:"directory,omitempty"`
	Repo      string `json:"repo,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	Source    string `json:"source,omitempty"`
	Online    bool   `json:"online"`
}
```

- [ ] **Step 2: Run tests to verify no regressions**

Run: `go test ./internal/protocol/... -v`
Expected: PASS (no behaviour changes, just a new optional field)

- [ ] **Step 3: Commit**

```bash
git add internal/protocol/protocol.go
git commit -m "feat: add Color field to protocol.Participant (#75)"
```

---

### Task 2: Create colour palette and assignment logic in `internal/room/`

**Files:**
- Create: `internal/room/colours.go`
- Create: `internal/room/colours_test.go`

- [ ] **Step 1: Write failing tests for colour assignment**

Create `internal/room/colours_test.go`:

```go
package room

import "testing"

func TestAssignColour_ReturnsFromPalette(t *testing.T) {
	colour := AssignColour(nil)
	if colour == "" {
		t.Fatal("expected a colour, got empty string")
	}
	found := false
	for _, c := range AgentPalette {
		if c == colour {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("colour %q not in AgentPalette", colour)
	}
}

func TestAssignColour_AvoidsUsed(t *testing.T) {
	// Use all but one colour
	used := make([]string, len(AgentPalette)-1)
	copy(used, AgentPalette[:len(AgentPalette)-1])

	colour := AssignColour(used)
	if colour != AgentPalette[len(AgentPalette)-1] {
		t.Errorf("expected last palette colour %q, got %q", AgentPalette[len(AgentPalette)-1], colour)
	}
}

func TestAssignColour_FallbackWhenAllUsed(t *testing.T) {
	used := make([]string, len(AgentPalette))
	copy(used, AgentPalette)

	colour := AssignColour(used)
	if colour == "" {
		t.Fatal("expected a fallback colour, got empty string")
	}
	// Should still return something from the palette (wraps around)
	found := false
	for _, c := range AgentPalette {
		if c == colour {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("fallback colour %q not in AgentPalette", colour)
	}
}

func TestAssignColour_UniqueFor8Participants(t *testing.T) {
	var used []string
	seen := make(map[string]bool)
	for i := 0; i < len(AgentPalette); i++ {
		colour := AssignColour(used)
		if seen[colour] {
			t.Fatalf("duplicate colour %q on participant %d", colour, i)
		}
		seen[colour] = true
		used = append(used, colour)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/room/... -run TestAssignColour -v`
Expected: FAIL — `AssignColour` and `AgentPalette` undefined

- [ ] **Step 3: Implement `colours.go`**

Create `internal/room/colours.go`:

```go
package room

import (
	"crypto/rand"
	"math/big"
)

// AgentPalette is the set of colours available for agent participants.
var AgentPalette = []string{
	"#a78bfa", // purple
	"#7dd3fc", // cyan
	"#34d399", // emerald
	"#fbbf24", // amber
	"#f472b6", // pink
	"#60a5fa", // blue
	"#a3e635", // lime
	"#fb923c", // orange
}

// AssignColour picks a random colour from AgentPalette that is not in the used
// set. If all colours are taken, it picks a random one from the full palette
// (graceful degradation for 9+ participants).
func AssignColour(used []string) string {
	usedSet := make(map[string]bool, len(used))
	for _, c := range used {
		usedSet[c] = true
	}

	var available []string
	for _, c := range AgentPalette {
		if !usedSet[c] {
			available = append(available, c)
		}
	}

	if len(available) == 0 {
		// Fallback: all colours taken, pick randomly from full palette.
		available = AgentPalette
	}

	n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(available))))
	return available[n.Int64()]
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/room/... -run TestAssignColour -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/room/colours.go internal/room/colours_test.go
git commit -m "feat: add colour palette and assignment logic to room package (#75)"
```

---

### Task 3: Integrate colour assignment into `room.State.Join()`

**Files:**
- Modify: `internal/room/state.go` (Join method, ~lines 172-203)
- Modify: `internal/room/state_test.go`
- Modify: `internal/protocol/protocol.go` (add Color to JoinedParams)
- Modify: `internal/server/server.go` (populate JoinedParams.Color from assigned participant)
- Modify: `internal/room/dispatch.go` (forward Color from JoinedParams to Participant)

- [ ] **Step 1: Write failing tests for colour assignment on join**

Add to `internal/room/state_test.go`:

```go
func TestState_Join_AssignsColour(t *testing.T) {
	s := New(nil, command.Context{})

	snap, err := s.Join("bot1", "agent", "", "", "claude", "agent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	p := snap.Participants[0]
	if p.Color == "" {
		t.Error("expected a colour to be assigned")
	}

	// Verify colour is from the palette
	found := false
	for _, c := range AgentPalette {
		if c == p.Color {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("assigned colour %q not in AgentPalette", p.Color)
	}
}

func TestState_Join_HumanGetsNoColour(t *testing.T) {
	s := New(nil, command.Context{})

	snap, err := s.Join("alice", "human", "", "", "", "human")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if snap.Participants[0].Color != "" {
		t.Errorf("expected human to have no server-assigned colour, got %q", snap.Participants[0].Color)
	}
}

func TestState_Join_UniqueColoursForAgents(t *testing.T) {
	s := New(nil, command.Context{})

	colours := make(map[string]bool)
	for i := 0; i < len(AgentPalette); i++ {
		name := fmt.Sprintf("agent%d", i)
		snap, err := s.Join(name, "agent", "", "", "claude", "agent")
		if err != nil {
			t.Fatalf("join %d failed: %v", i, err)
		}
		c := snap.Participants[len(snap.Participants)-1].Color
		if colours[c] {
			t.Errorf("duplicate colour %q for %s", c, name)
		}
		colours[c] = true
	}
}

func TestState_Join_ReconnectKeepsColour(t *testing.T) {
	s := New(nil, command.Context{})

	snap1, _ := s.Join("bot1", "agent", "", "", "claude", "agent")
	originalColour := snap1.Participants[0].Color

	s.Leave("bot1")

	snap2, err := s.Join("bot1", "", "", "", "claude", "agent")
	if err != nil {
		t.Fatalf("rejoin failed: %v", err)
	}

	if snap2.Participants[0].Color != originalColour {
		t.Errorf("expected colour %q preserved on reconnect, got %q", originalColour, snap2.Participants[0].Color)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/room/... -run "TestState_Join_(AssignsColour|HumanGetsNoColour|UniqueColours|ReconnectKeepsColour)" -v`
Expected: FAIL — Color field not populated

- [ ] **Step 3: Update `Join()` to assign colours**

In `internal/room/state.go`, modify the `Join` method. Replace the new-participant block (lines 192-203):

```go
// Join adds a participant to the room. If a participant with the same name
// exists and is online, an error is returned. If they exist but are offline,
// they are reconnected. Returns the current room state snapshot.
func (s *State) Join(name, role, dir, repo, agentType, source string) (protocol.RoomStateParams, error) {
	for i, p := range s.participants {
		if p.Name == name {
			if p.Online {
				return protocol.RoomStateParams{}, fmt.Errorf("name already taken: %q", name)
			}
			// Reconnect offline participant — keep existing colour.
			if role != "" {
				s.participants[i].Role = role
			}
			s.participants[i].Directory = dir
			s.participants[i].Repo = repo
			s.participants[i].AgentType = agentType
			s.participants[i].Source = source
			s.participants[i].Online = true
			s.emitParticipantsChanged()
			return s.stateSnapshot(), nil
		}
	}

	// Assign colour for non-human participants.
	var colour string
	if source != "human" {
		colour = AssignColour(s.usedColours())
	}

	// New participant.
	s.participants = append(s.participants, protocol.Participant{
		Name:      name,
		Role:      role,
		Color:     colour,
		Directory: dir,
		Repo:      repo,
		AgentType: agentType,
		Source:    source,
		Online:    true,
	})
	s.emitParticipantsChanged()
	return s.stateSnapshot(), nil
}

// usedColours returns all colours currently assigned to participants.
func (s *State) usedColours() []string {
	var colours []string
	for _, p := range s.participants {
		if p.Color != "" {
			colours = append(colours, p.Color)
		}
	}
	return colours
}
```

- [ ] **Step 4: Run room tests to verify they pass**

Run: `go test ./internal/room/... -v`
Expected: ALL PASS (new tests + existing tests)

- [ ] **Step 5: Add `Color` to `JoinedParams`**

In `internal/protocol/protocol.go`, add `Color` to `JoinedParams`:

```go
// JoinedParams is the server-side confirmation payload for "room/joined".
type JoinedParams struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Color     string    `json:"color,omitempty"`
	Directory string    `json:"directory,omitempty"`
	Repo      string    `json:"repo,omitempty"`
	AgentType string    `json:"agent_type,omitempty"`
	JoinedAt  time.Time `json:"joined_at"`
}
```

- [ ] **Step 6: Populate `JoinedParams.Color` in server.go**

In `internal/server/server.go`, after `s.state.Join()` succeeds, the assigned colour can be found in the returned snapshot's participant list. Update the `jp` construction (around line 162) to look up the colour:

```go
// Find the newly joined participant's assigned colour from the state snapshot.
var assignedColor string
for _, p := range stateParams.Participants {
	if p.Name == params.Name {
		assignedColor = p.Color
		break
	}
}

// Notify other participants.
jp := protocol.JoinedParams{
	Name:      params.Name,
	Role:      effectiveRole,
	Color:     assignedColor,
	Directory: params.Directory,
	Repo:      params.Repo,
	AgentType: params.AgentType,
	JoinedAt:  time.Now().UTC(),
}
```

Note: `stateParams` is the return value of `s.state.Join()` — verify this variable name by reading the existing handler code.

- [ ] **Step 7: Forward `Color` in dispatch.go**

In `internal/room/dispatch.go`, update the `MethodJoined` handler (around line 78) to include `Color` when constructing the `Participant`:

```go
p := protocol.Participant{
	Name:      params.Name,
	Role:      params.Role,
	Color:     params.Color,
	Directory: params.Directory,
	Repo:      params.Repo,
	AgentType: params.AgentType,
	Online:    true,
}
```

- [ ] **Step 8: Write test for dispatch forwarding the colour**

Add to `internal/room/dispatch_test.go` (find the existing `MethodJoined` test around line 92):

```go
func TestDispatch_Joined_ForwardsColor(t *testing.T) {
	s := New(nil, command.Context{})

	joined := protocol.JoinedParams{
		Name:      "bot1",
		Role:      "agent",
		Color:     "#a78bfa",
		AgentType: "claude",
	}
	s.HandleServerMessage(rawMsg(t, protocol.MethodJoined, joined))

	ps := s.Participants()
	if len(ps) != 1 {
		t.Fatalf("expected 1 participant, got %d", len(ps))
	}
	if ps[0].Color != "#a78bfa" {
		t.Errorf("expected Color %q, got %q", "#a78bfa", ps[0].Color)
	}
}
```

- [ ] **Step 9: Run all tests**

Run: `go test ./internal/... -timeout 30s`
Expected: ALL PASS

- [ ] **Step 10: Commit**

```bash
git add internal/room/state.go internal/room/state_test.go internal/protocol/protocol.go internal/server/server.go internal/room/dispatch.go internal/room/dispatch_test.go
git commit -m "feat: assign unique colour to agents on join (#75)"
```

---

### Task 4: Update TUI to use `Participant.Color`

**Files:**
- Modify: `internal/tui/styles.go`
- Modify: `internal/tui/styles_test.go`
- Modify: `internal/tui/sidebar.go`
- Modify: `internal/tui/chat.go`

- [ ] **Step 1: Write tests for updated `ColorForSender`**

Replace `internal/tui/styles_test.go`:

```go
package tui

import "testing"

func TestColorForSenderHumanAlwaysOrange(t *testing.T) {
	c := ColorForSender("Alice", true, "")
	if c != colorHuman {
		t.Errorf("expected human color %v, got %v", colorHuman, c)
	}
	// Different name, still human.
	c2 := ColorForSender("Bob", true, "")
	if c2 != colorHuman {
		t.Errorf("expected human color %v for Bob, got %v", colorHuman, c2)
	}
}

func TestColorForSender_UsesAssignedColour(t *testing.T) {
	c := ColorForSender("bot1", false, "#a78bfa")
	if string(c) != "#a78bfa" {
		t.Errorf("expected assigned colour #a78bfa, got %v", c)
	}
}

func TestColorForSender_FallsBackToHash(t *testing.T) {
	// No assigned colour — should fall back to hash-based
	c1 := ColorForSender("claude-code", false, "")
	c2 := ColorForSender("claude-code", false, "")
	if c1 != c2 {
		t.Errorf("same name should return same fallback color: got %v and %v", c1, c2)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/... -run TestColorForSender -v`
Expected: FAIL — signature mismatch

- [ ] **Step 3: Update `ColorForSender` signature and implementation**

In `internal/tui/styles.go`, replace lines 22-38:

```go
// ColorForSender returns the display colour for a participant.
// If assignedColor is non-empty, it is used directly (server-assigned).
// Humans always get colorHuman. Agents with no assigned colour fall back
// to FNV hash of name (for 9+ participants or legacy data).
func ColorForSender(name string, isHuman bool, assignedColor string) lipgloss.Color {
	if isHuman {
		return colorHuman
	}
	if assignedColor != "" {
		return lipgloss.Color(assignedColor)
	}
	h := fnv.New32a()
	h.Write([]byte(name))
	idx := int(h.Sum32()) % len(room.AgentPalette)
	return lipgloss.Color(room.AgentPalette[idx])
}
```

Add import for `"github.com/khaiql/parley/internal/room"`.

Remove the `agentPalette` variable from `styles.go` (lines 22-25) since it's now in `room.AgentPalette`.

- [ ] **Step 4: Run styles tests to verify they pass**

Run: `go test ./internal/tui/... -run TestColorForSender -v`
Expected: PASS

- [ ] **Step 5: Update sidebar.go to pass participant colour**

In `internal/tui/sidebar.go`, update all `ColorForSender` calls to pass `p.Color`:

Line 141: `senderColor := ColorForSender(p.Name, false)` → `senderColor := ColorForSender(p.Name, false, p.Color)`
Line 147: `senderColor := ColorForSender(p.Name, false)` → (already computed above, remove duplicate)
Line 156: `senderColor := ColorForSender(p.Name, false)` → `senderColor := ColorForSender(p.Name, false, p.Color)`

The sidebar already has `p` (a `protocol.Participant`) in scope, so `p.Color` is directly available.

- [ ] **Step 6: Update chat.go to pass participant colour**

The chat renders messages, which don't carry a `Color` field. The chat needs to look up the participant's colour. Two call sites need updating:

**chat.go line 180** — `renderMessages`: This function only has `msg.From` and `msg.IsHuman()`. It doesn't have access to participants. We need to pass a colour lookup function or a participant map.

Update `Chat` struct to hold a participant colour map:

In `internal/tui/chat.go`, add a field to `Chat`:

```go
type Chat struct {
	vp          viewport.Model
	messages    []protocol.MessageParams
	colorMap    map[string]string // name → assigned hex colour
	loading     bool
	width       int
	height      int
}
```

Add a setter:

```go
// SetParticipantColors updates the name→color mapping used for message rendering.
func (c *Chat) SetParticipantColors(participants []protocol.Participant) {
	if c.colorMap == nil {
		c.colorMap = make(map[string]string)
	}
	for _, p := range participants {
		if p.Color != "" {
			c.colorMap[p.Name] = p.Color
		}
	}
}
```

Update `rebuildContent` to pass the colour map:

```go
func (c *Chat) rebuildContent() {
	c.vp.SetContent(renderMessages(c.messages, c.width, c.colorMap))
}
```

Update `renderMessages` signature:

```go
func renderMessages(msgs []protocol.MessageParams, width int, colorMap map[string]string) string {
```

And update the `ColorForSender` call at line 180:

```go
assignedColor := ""
if colorMap != nil {
	assignedColor = colorMap[msg.From]
}
senderColor := ColorForSender(msg.From, isHuman, assignedColor)
```

Update `highlightMentions` to accept and use the colour map — change line 266:

```go
func highlightMentions(text string, colorMap map[string]string) string {
```

And the `ColorForSender` call at line 303:

```go
assignedColor := ""
if colorMap != nil {
	assignedColor = colorMap[name]
}
c := ColorForSender(name, false, assignedColor)
```

Update the call site in `renderMessages` (line 201):

```go
body := highlightMentions(renderMarkdown(text, bodyWidth), colorMap)
```

- [ ] **Step 7: Update app.go to wire participant colours to chat**

In `internal/tui/app.go`, update the `ParticipantsChanged` handler (line 208) to also update chat colours:

```go
case room.ParticipantsChanged:
	a.localParticipants = m.Participants
	a.sidebar.SetParticipants(m.Participants)
	a.chat.SetParticipantColors(m.Participants)
	return a, a.maybeStartSpinnerFromActivities()
```

Also update the `HistoryLoaded` handler (line 192):

```go
case room.HistoryLoaded:
	a.localMessages = m.Messages
	a.localParticipants = m.Participants
	a.localActivities = m.Activities
	a.sidebar.SetParticipants(m.Participants)
	a.chat.SetParticipantColors(m.Participants)
	a.chat.SetLoading(false)
	a.chat.LoadMessages(m.Messages)
	a.statusbar.SetYolo(a.roomState != nil && a.roomState.AutoApprove())
	return a, a.maybeStartSpinnerFromActivities()
```

- [ ] **Step 8: Run all TUI tests**

Run: `go test ./internal/tui/... -v`
Expected: PASS. Golden file tests may need regeneration if colour values changed.

- [ ] **Step 9: Commit**

```bash
git add internal/tui/styles.go internal/tui/styles_test.go internal/tui/sidebar.go internal/tui/chat.go internal/tui/app.go
git commit -m "feat: TUI reads participant colour from server instead of computing (#75)"
```

---

### Task 5: Replace client-side name generator with adjective-noun combinator

**Files:**
- Modify: `cmd/parley/join.go:51-61`

- [ ] **Step 1: Write test for new name generator**

Create `cmd/parley/name_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestRandomName_AdjNounFormat(t *testing.T) {
	name := randomName()
	parts := strings.SplitN(name, "-", 2)
	if len(parts) != 2 {
		t.Fatalf("expected adjective-noun format, got %q", name)
	}
	if parts[0] == "" || parts[1] == "" {
		t.Fatalf("expected non-empty adjective and noun, got %q", name)
	}
}

func TestRandomName_Uniqueness(t *testing.T) {
	// Generate 50 names, expect no duplicates (500 combinations, so very unlikely)
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		name := randomName()
		if seen[name] {
			t.Errorf("duplicate name %q after %d iterations", name, i)
		}
		seen[name] = true
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/parley/... -run TestRandomName -v`
Expected: FAIL — `randomName()` returns single word, not adjective-noun

- [ ] **Step 3: Replace `randomName()` with adjective-noun combinator**

In `cmd/parley/join.go`, replace the `randomName` function (lines 51-61):

```go
func randomName() string {
	adjectives := []string{
		"swift", "quiet", "bold", "bright", "fuzzy",
		"clever", "gentle", "keen", "lucky", "nimble",
		"plucky", "rusty", "snowy", "spry", "steady",
		"tidy", "vivid", "warm", "witty", "zesty",
	}
	nouns := []string{
		"babbage", "bramble", "cosmo", "dingo", "ember",
		"ferris", "goblin", "hickory", "ibex", "junco",
		"kitsune", "loki", "moss", "noodle", "orca",
		"pascal", "pickle", "quokka", "ruckus", "sprocket",
		"turing", "umbra", "vortex", "wombat", "yeti",
	}
	ai, _ := rand.Int(rand.Reader, big.NewInt(int64(len(adjectives))))
	ni, _ := rand.Int(rand.Reader, big.NewInt(int64(len(nouns))))
	return adjectives[ai.Int64()] + "-" + nouns[ni.Int64()]
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/parley/... -run TestRandomName -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/parley/join.go cmd/parley/name_test.go
git commit -m "feat: replace fixed name pool with adjective-noun generator (#75)"
```

---

### Task 6: Full integration verification

**Files:** None (test-only)

- [ ] **Step 1: Run full test suite**

Run: `go test ./... -timeout 30s -race`
Expected: ALL PASS

- [ ] **Step 2: Run linter**

Run: `go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m`
Expected: PASS

- [ ] **Step 3: Build binary**

Run: `go build -o parley ./cmd/parley`
Expected: Compiles successfully

- [ ] **Step 4: Regenerate golden files if needed**

If visual tests failed in Step 1, regenerate:
- Set `updateGolden = true` in `internal/tui/visual_test.go`
- Run: `go test ./internal/tui/... -run TestVisual`
- Set `updateGolden = false`
- Review diffs in `internal/tui/testdata/`

- [ ] **Step 5: Commit golden files if updated**

```bash
git add internal/tui/testdata/
git commit -m "test: update golden files for colour assignment changes (#75)"
```
