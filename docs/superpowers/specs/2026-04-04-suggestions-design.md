# Chat Input Suggestions & Tab-Completion

Implements autocomplete for slash commands (`/`) and agent mentions (`@`) in the chat input, per [#64](https://github.com/khaiql/parley/issues/64).

## Component: `Suggestions`

New file: `internal/tui/suggestions.go`

A standalone Bubble Tea model that App owns and renders between chat and input. It has no knowledge of commands or participants — it operates on a generic item type.

### Data type

```go
type SuggestionItem struct {
    Label       string // displayed and inserted text (e.g., "/save", "@claude")
    Description string // shown next to label (e.g., "Save room state", "agent")
}
```

### Model state

| Field        | Type               | Purpose                                    |
|------------- |--------------------|--------------------------------------------|
| `items`      | `[]SuggestionItem` | Full list provided by App                  |
| `filtered`   | `[]SuggestionItem` | Items matching current query               |
| `query`      | `string`           | Current filter text                        |
| `cursor`     | `int`              | Selected index in filtered list            |
| `visible`    | `bool`             | Whether the overlay is showing             |
| `maxVisible` | `int`              | Capped at 5; scroll if more                |

### Methods

- `SetItems(items []SuggestionItem)` — replaces full list, resets filter and cursor
- `Filter(query string)` — case-insensitive prefix match on Label, resets cursor to 0
- `MoveUp() / MoveDown()` — navigate filtered list with wrapping
- `Selected() SuggestionItem` — returns item at cursor
- `Visible() bool` — whether suggestions are showing
- `Show() / Hide()` — toggle visibility
- `View() string` — renders up to 5 items in a bordered box

### Rendering

- Bordered box, up to 7 lines (5 items + top/bottom border)
- Selected item highlighted with `colorPrimary`
- Label in `colorText`, description in `colorDimText` separated by ` — `
- Border uses `colorBorder` (consistent with input border)
- Width matches input width

## Trigger Detection & State in App

### New fields on `App`

| Field              | Type          | Purpose                                         |
|--------------------|---------------|-------------------------------------------------|
| `suggestions`      | `Suggestions` | The component                                   |
| `completionTrigger`| `rune`        | Which character started completion (`/` or `@`)  |
| `completionStart`  | `int`         | Cursor position where trigger was typed          |

### Activation

- **`/` commands**: Activates when input value is exactly `/` (start of empty input). Builds items from `registry.Commands()`.
- **`@` mentions**: Activates when `@` is typed at the start of input or after a whitespace character (prevents false triggers in text like `email@`). Builds items from sidebar participants.
- **Nil registry**: `/` trigger does not activate when registry is nil (non-host TUI). `@` mentions always work.

### Filtering

After each keystroke while suggestions are visible, extract text from `completionStart+1` to current cursor position as the query. Pass to `suggestions.Filter()`.

### Key routing while suggestions are visible

| Key        | Action                                              |
|------------|-----------------------------------------------------|
| Up/Down    | `suggestions.MoveUp()/MoveDown()` (not sent to textarea) |
| Tab/Enter  | Accept selection, insert text, close suggestions     |
| Esc        | Close suggestions, keep current text                 |
| Other keys | Forward to textarea, then re-filter                  |

### Dismissal

Suggestions close when:
- User presses Esc
- User deletes back past the trigger character
- Input becomes empty
- Filtered list becomes empty

## Selection & Text Insertion

When the user presses Tab or Enter with suggestions visible:

1. Get `suggestions.Selected()` (e.g., `SuggestionItem{Label: "/save"}`)
2. Replace text from `completionStart` to current cursor position with `Label + " "`
3. Close suggestions

Everything before the trigger character stays untouched. Example: `hello @clau` + Tab becomes `hello @claude `.

### Input change

`Input` gets one new method: `ReplaceRange(start, end int, text string)` — splices replacement into the textarea value and positions cursor after inserted text. Input doesn't know why the replacement is happening.

## Layout

Current: `topbar | chat | input`
With suggestions: `topbar | chat | suggestions | input`

`App.layout()` subtracts suggestion box height from chat viewport when suggestions are visible, so chat shrinks temporarily.

## Registry Extension

Add `Commands() []*Command` to `command.Registry` — returns full Command objects in registration order. Gives App access to `Name`, `Usage`, and `Description` for building `SuggestionItem` list.

`Available()` remains unchanged.

## Testing

- **`suggestions_test.go`**: Unit tests for filtering, navigation, cursor wrapping, empty list, max visible cap
- **`registry_test.go`**: Test `Commands()` returns full objects in order
- **App integration**: Verify trigger detection, key routing, and text insertion using Bubble Tea test helpers
