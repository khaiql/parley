# Server-Side Colour Assignment & Improved Name Generation

**Issue**: [#75](https://github.com/khaiql/parley/issues/75) — Agent colour and name collisions on join
**Date**: 2026-04-06

## Problem

1. **Duplicate colours**: The TUI computes agent colours via `FNV-1a hash(name) % 8`. With 25 possible names and 8 colours, different names frequently map to the same colour, causing visual confusion.
2. **Name collisions**: Agents pick a random name from a fixed 25-name pool. Collisions cause join rejections, requiring retry or manual `--name` flag.

## Design

### 1. Protocol: Add `Color` to `Participant`

Add a `Color` field to `protocol.Participant`:

```go
type Participant struct {
    Name      string `json:"name"`
    Role      string `json:"role"`
    Color     string `json:"color,omitempty"` // hex, e.g. "#a78bfa"
    Directory string `json:"directory,omitempty"`
    Repo      string `json:"repo,omitempty"`
    AgentType string `json:"agent_type,omitempty"`
    Source    string `json:"source,omitempty"`
    Online    bool   `json:"online"`
}
```

Clients do not send a colour preference. The server assigns it on join and returns it in the `room.state` response and `room.joined` broadcast.

### 2. Server-Side Colour Assignment (room.State)

Move the 8-colour agent palette from `internal/tui/styles.go` to `internal/room/` (e.g., a `colours.go` file). The TUI imports it from there.

**Palette** (unchanged values):
```
#a78bfa  purple
#7dd3fc  cyan
#34d399  emerald
#fbbf24  amber
#f472b6  pink
#60a5fa  blue
#a3e635  lime
#fb923c  orange
```

**Assignment logic in `room.State.Join()`**:
1. Collect the set of colours already assigned to existing participants (all, including offline).
2. Compute the available set: palette minus assigned.
3. If available set is non-empty: pick a random colour from it.
4. If all 8 are taken (9+ participants): fall back to `FNV-1a hash(name) % len(palette)`.
5. Store the chosen colour in `Participant.Color`.

**Reconnection**: When an offline participant rejoins with the same name, reuse their existing `Color`. No new assignment.

**No recycling**: Colours are never freed on disconnect/leave. They stay claimed for the room's lifetime. This ensures reconnecting participants always get their original colour.

### 3. Persistence

The `Color` field is part of `Participant`, so it is automatically serialized/deserialized by `persistence.JSONStore` when saving/loading room state. On room restore, assigned colours are recovered from the participant list — no separate tracking needed.

### 4. Client-Side Name Generation

Replace the fixed 25-name pool in `cmd/parley/join.go` with an adjective-noun combinator:

- **Adjectives** (~20): "swift", "quiet", "bold", "bright", "fuzzy", "clever", "gentle", "keen", "lucky", "nimble", "plucky", "rusty", "snowy", "spry", "steady", "tidy", "vivid", "warm", "witty", "zesty"
- **Nouns** (~25): keep existing names — "babbage", "bramble", "cosmo", "dingo", "ember", "ferris", "goblin", "hickory", "ibex", "junco", "kitsune", "loki", "moss", "noodle", "orca", "pascal", "pickle", "quokka", "ruckus", "sprocket", "turing", "umbra", "vortex", "wombat", "yeti"

Format: `adjective-noun` (e.g., "swift-orca", "bold-ferris").

This gives ~500 combinations, making collisions extremely unlikely. The server's existing "name already taken" rejection remains as a safety net.

The host name stays as `$USER` env var or "host" default — unchanged.

### 5. TUI Changes

- `ColorForSender()` in `styles.go`: update to accept the participant's `Color` string directly (from `Participant.Color`), instead of computing from name.
- All rendering call sites (sidebar, chat borders, mentions) pass the participant's assigned colour.
- **Host**: keeps fixed orange (`#f0883e`) — not server-assigned.
- **System messages**: keep fixed gray (`#8b949e`).
- The hash-based `ColorForSender(name)` function remains as a fallback for edge cases (e.g., rendering a message from an unknown sender).

### 6. Scope Boundaries

**In scope**:
- `Color` field on `Participant` in protocol
- Server-side colour assignment in `room.State.Join()`
- Colour persistence via existing participant serialization
- Adjective-noun name generator on client
- TUI reads `Participant.Color` instead of computing

**Out of scope**:
- Colour recycling on disconnect
- Client-side colour preference/negotiation
- Expanding the palette beyond 8
- Changing host or system message colours
- Server-side name assignment
