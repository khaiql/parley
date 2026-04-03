package web

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExport_InjectsDataIntoTemplate(t *testing.T) {
	dir := t.TempDir()

	room := map[string]string{"topic": "Test Topic", "id": "room-1"}
	agents := []map[string]string{
		{"name": "Alice", "role": "human", "source": "human"},
		{"name": "Bot", "role": "helper", "source": "agent", "agent_type": "claude-code"},
	}
	messages := []map[string]any{
		{
			"id": "msg-1", "seq": 1, "from": "Alice", "source": "human",
			"role": "user", "content": []map[string]string{{"type": "text", "text": "Hello"}},
		},
	}

	writeTestJSON(t, filepath.Join(dir, "room.json"), room)
	writeTestJSON(t, filepath.Join(dir, "agents.json"), agents)
	writeTestJSON(t, filepath.Join(dir, "messages.json"), messages)

	var buf bytes.Buffer
	err := Export(dir, &buf)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	html := buf.String()

	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("output missing DOCTYPE")
	}

	// The placeholder in the data script tag should be replaced.
	// (The template JS has a guard check string that legitimately contains the placeholder.)
	dataTag := `<script id="parley-data" type="application/json">{{PARLEY_DATA}}</script>`
	if strings.Contains(html, dataTag) {
		t.Error("output still contains unreplaced placeholder in data script tag")
	}

	// Extract and validate the embedded JSON.
	start := strings.Index(html, `<script id="parley-data" type="application/json">`)
	end := strings.Index(html, `</script>`)
	if start == -1 || end == -1 {
		t.Fatal("could not find parley-data script tag")
	}
	jsonStart := start + len(`<script id="parley-data" type="application/json">`)
	jsonStr := html[jsonStart:end]

	var data struct {
		Room     json.RawMessage `json:"room"`
		Agents   json.RawMessage `json:"agents"`
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		t.Fatalf("embedded JSON is invalid: %v\nJSON: %s", err, jsonStr)
	}

	if !strings.Contains(string(data.Room), "Test Topic") {
		t.Error("embedded room data missing topic")
	}
	if !strings.Contains(string(data.Agents), "claude-code") {
		t.Error("embedded agents data missing agent_type")
	}
	if !strings.Contains(string(data.Messages), "Hello") {
		t.Error("embedded messages data missing message text")
	}
}

func TestExport_MissingRoomJSON(t *testing.T) {
	dir := t.TempDir()
	writeTestJSON(t, filepath.Join(dir, "messages.json"), []string{})

	var buf bytes.Buffer
	err := Export(dir, &buf)
	if err == nil {
		t.Fatal("expected error for missing room.json")
	}
}

func TestExport_ProducesValidSelfContainedHTML(t *testing.T) {
	dir := t.TempDir()

	room := map[string]string{"topic": "Integration Test", "id": "int-1"}
	agents := []map[string]string{
		{"name": "Human", "role": "human", "source": "human", "directory": "/tmp"},
		{"name": "Agent", "role": "coder", "source": "agent", "agent_type": "claude-code", "directory": "/tmp"},
	}
	messages := []map[string]any{
		{
			"id": "msg-1", "seq": 1, "from": "Human", "source": "human",
			"role": "user", "timestamp": "2026-04-01T14:00:00Z",
			"content":  []map[string]string{{"type": "text", "text": "Hello @Agent"}},
			"mentions": []string{"Agent"},
		},
		{
			"id": "msg-2", "seq": 2, "from": "Agent", "source": "agent",
			"role": "assistant", "timestamp": "2026-04-01T14:00:30Z",
			"content": []map[string]string{{"type": "text", "text": "Hi! Here's some `inline code` and:\n```go\nfmt.Println(\"hello\")\n```"}},
		},
	}

	writeTestJSON(t, filepath.Join(dir, "room.json"), room)
	writeTestJSON(t, filepath.Join(dir, "agents.json"), agents)
	writeTestJSON(t, filepath.Join(dir, "messages.json"), messages)

	var buf bytes.Buffer
	if err := Export(dir, &buf); err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	html := buf.String()

	// Must be a complete HTML document.
	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("missing DOCTYPE")
	}
	if !strings.Contains(html, "<style>") {
		t.Error("missing inlined CSS")
	}
	if !strings.Contains(html, "renderApp") {
		t.Error("missing inlined JS")
	}

	// Must not contain the raw placeholder.
	dataTag := `<script id="parley-data" type="application/json">{{PARLEY_DATA}}</script>`
	if strings.Contains(html, dataTag) {
		t.Error("placeholder not replaced")
	}

	// Embedded data must be parseable.
	start := strings.Index(html, `<script id="parley-data" type="application/json">`)
	tagLen := len(`<script id="parley-data" type="application/json">`)
	end := strings.Index(html[start+tagLen:], `</script>`)
	jsonStr := html[start+tagLen : start+tagLen+end]

	var data map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		t.Fatalf("invalid embedded JSON: %v", err)
	}

	if _, ok := data["room"]; !ok {
		t.Error("missing room in embedded data")
	}
	if _, ok := data["agents"]; !ok {
		t.Error("missing agents in embedded data")
	}
	if _, ok := data["messages"]; !ok {
		t.Error("missing messages in embedded data")
	}
}

func TestExport_OptionalAgentsJSON(t *testing.T) {
	dir := t.TempDir()

	writeTestJSON(t, filepath.Join(dir, "room.json"), map[string]string{"topic": "No Agents", "id": "r-1"})
	writeTestJSON(t, filepath.Join(dir, "messages.json"), []any{})

	var buf bytes.Buffer
	err := Export(dir, &buf)
	if err != nil {
		t.Fatalf("Export() should succeed without agents.json: %v", err)
	}

	if !strings.Contains(buf.String(), `"agents":[]`) {
		t.Error("expected empty agents array in output")
	}
}

func writeTestJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
