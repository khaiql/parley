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
