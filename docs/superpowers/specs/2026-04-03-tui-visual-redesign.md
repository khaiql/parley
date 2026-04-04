# TUI Visual Redesign Spec

## Problem

The current Parley TUI has poor readability and visual separation:
- All agents share the same color (`#a5d6ff`), making it impossible to distinguish senders in a busy multi-agent chat
- No visual boundary between messages — only a single `\n` separates them
- Low contrast: body text (`#c9d1d9`) and timestamps (`#484f58`) are hard to read on dark backgrounds
- Sidebar shows all agents in the same blue with no hierarchy
- No status bar for room state awareness
- No markdown rendering — message bodies are plain text
- "Listening" status indicator is unreliable — only generation status is trustworthy

## Design Decisions

### 1. Message Design System

Each sender gets a unique colored thick left border. Messages from the same sender are grouped — only the first message in a consecutive run shows the name and timestamp. A thin horizontal separator line appears between messages from different senders.

**Color assignment:**
- Human participants always get orange (`#f0883e`)
- Agent participants are assigned from an 8-color palette based on a deterministic hash of their name:
  - Violet `#a78bfa`, Sky Blue `#7dd3fc`, Emerald `#34d399`, Amber `#fbbf24`, Pink `#f472b6`, Blue `#60a5fa`, Lime `#a3e635`, Tangerine `#fb923c`
- Same name always produces the same color across sessions

**Message rendering:**
```
┃ sender_name  05:49          ← thick left border in sender's color
┃ Message body text here.        name + timestamp on first message only
┃
┃ Second consecutive message     ← no name/timestamp, same border
┃ from the same sender.
─────────────────────────────── ← thin separator when sender changes
┃ different_sender  05:50
┃ Their message body.
```

**Timestamp rules:**
- Show on the first message in a consecutive group from the same sender
- Also show when 5+ minutes have elapsed since the last displayed timestamp, regardless of sender
- Format: `HH:MM` (unchanged)

### 2. System Messages

System messages (join, leave, etc.) render as centered, dimmed, italic text with no border:

```
                    — sle joined —
```

No blank line padding above or below. They sit between the separator lines of adjacent chat messages.

### 3. Color Palette & Contrast

Updated colors for better readability:

| Token | Current | New | Purpose |
|-------|---------|-----|---------|
| colorText | `#c9d1d9` | `#e1e4e8` | Body text |
| colorDimText | `#484f58` | `#6e7681` | Timestamps, metadata |
| colorBorder | `#30363d` | `#3b3f47` | Borders, separators |
| colorSeparator | (none) | `#21262d` | Thin lines between messages |
| colorSidebarBg | (none) | `#161b22` | Sidebar background |

Human color remains `#f0883e`. The single `colorAgent (#a5d6ff)` is replaced by the per-sender palette above.

### 4. Glamour Markdown Rendering

Message bodies are rendered through `charmbracelet/glamour` with a dark theme. This provides:
- Syntax-highlighted code blocks
- Bold, italic, strikethrough
- Bullet and numbered lists
- Inline code
- Headings
- Block quotes

The Glamour renderer is initialized once with a fixed width matching the chat viewport. Width is recalculated on terminal resize.

### 5. Sidebar Redesign

Layout from top to bottom:

```
┌──────────────────────┐
│       parley         │  ← app name, bold, primary color, centered
│       :55568         │  ← port, dim text, centered
├──────────────────────┤  ← separator line
│ PARTICIPANTS         │  ← uppercase, dim, letter-spaced
├──────────────────────┤
│ sle                  │  ← human name in orange
│   ~/group_chat       │  ← directory, dim
├──────────────────────┤
│ growth  [gemini]     │  ← agent name in violet, type badge
│   ⠹ generating       │  ← spinner + label in agent's color (only while active)
├──────────────────────┤
│ love                 │  ← agent name in sky blue
│   ~/Projects/vocab…  │  ← directory, truncated
├──────────────────────┤
│ engineer  [claude]   │  ← agent name in emerald, type badge
│   ~/Projects/ai-d…   │  ← directory, truncated
└──────────────────────┘
```

Key changes from current:
- App branding at top (name + port)
- Participant names use their assigned chat border color (not all the same blue)
- Agent type shown as inline badge in the participant's color on a dim background
- Separator lines between each participant
- Background: `#161b22` (distinct from chat area)
- Section header: uppercase, dim, letter-spaced

**Agent status indicator:**
- Only "generating" status is shown — the previous "listening" indicator is removed as unreliable
- Shown as a braille spinner animation (`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`) followed by "generating" text
- Rendered in the agent's assigned color
- Displayed below the agent name in the sidebar
- Animated via `tea.Tick` (e.g., 100ms frame interval)
- Disappears when the agent stops streaming

### 6. Status Bar (New Component)

A single-line bar at the very bottom of the layout, below the input area.

```
[? help] 4 participants  room: 3a7f…                    ● connected
```

| Segment | Position | Color | Condition |
|---------|----------|-------|-----------|
| `? help` | Left | Bold, dim bg (`#30363d`) | Always |
| Participant count | Left | Dim text | Always |
| Room ID (truncated) | Left | Dim text | Always |
| Connection status | Right | Green `#3fb950` or red | Always |

YOLO mode indicator deferred to #57.

### 7. Input Area

Keep `bubbles/textarea`. Changes:

- Add `❯` prompt indicator in primary color before the textarea
- Add backslash-newline support: when the user presses Enter and the text ends with `\`, replace the `\` with a newline instead of sending
- Placeholder: `"Type a message…"`
- Agent streaming mode: unchanged (last line of agent output with `▊` cursor)
- Tab completion deferred to #54 — will be implemented as a separate dialog component (per OpenCode research)

## Layout

```
┌──────────────────────────────────────────────┐
│  topbar (1 line)                             │
├──────────────────────────────┬───────────────┤
│                              │               │
│  chat viewport               │  sidebar      │
│  (width - sidebarWidth)      │  (30 chars)   │
│                              │               │
├──────────────────────────────┴───────────────┤
│  input area (1-line textarea + prompt)       │
├──────────────────────────────────────────────┤
│  status bar (1 line)                         │
└──────────────────────────────────────────────┘
```

Sidebar width increases from 28 to 30 characters to accommodate agent type badges.

## Files to Modify

| File | Changes |
|------|---------|
| `internal/tui/styles.go` | New color palette, per-sender color assignment function, message border styles, sidebar background, status bar styles |
| `internal/tui/chat.go` | Glamour rendering, bordered message blocks, sender grouping, separator lines, timestamp logic |
| `internal/tui/sidebar.go` | Branding section, color-matched names, agent badges, separator lines, background, spinner animation |
| `internal/tui/topbar.go` | Contrast fixes (minor) |
| `internal/tui/input.go` | Prompt indicator, backslash-newline support |
| `internal/tui/statusbar.go` | New component: status bar with segments |
| `internal/tui/app.go` | Add status bar to layout, adjust heights, spinner tick |
| `go.mod` | Add `glamour` dependency |

## Dependencies

- `github.com/charmbracelet/glamour` — markdown rendering (new dependency)

## Related Issues

- #54 — tab completion (input stays as textarea; completion will be a separate dialog)
- #55 — info overlay (room info, join/resume commands)
- #57 — YOLO mode visual indicator in status bar

## Out of Scope

- Tab completion implementation (#54)
- Info overlay (#55)
- YOLO mode visual indicator (#57)
- Adding `description` field to JoinParams for agent role descriptions
- Light mode / adaptive colors
