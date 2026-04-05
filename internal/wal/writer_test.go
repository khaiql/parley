package wal_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khaiql/parley/internal/wal"
)

func TestWriter_LogWritesNDJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "debug", "test.wal")

	w, err := wal.New(path, "babbage")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	raw := []byte(`{"type":"stream_event","event":{"type":"content_block_delta"}}`)
	if err := w.Log("out", raw); err != nil {
		t.Fatalf("Log: %v", err)
	}

	rawIn := []byte(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`)
	if err := w.Log("in", rawIn); err != nil {
		t.Fatalf("Log: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	var entry wal.Entry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal line 0: %v", err)
	}
	if entry.Agent != "babbage" {
		t.Errorf("agent = %q, want %q", entry.Agent, "babbage")
	}
	if entry.Direction != "out" {
		t.Errorf("dir = %q, want %q", entry.Direction, "out")
	}
	if entry.Timestamp == "" {
		t.Error("timestamp is empty")
	}
	if entry.Raw == nil {
		t.Error("raw is nil")
	}
}

func TestWriter_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "test.wal")

	w, err := wal.New(path, "agent")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = w.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}
