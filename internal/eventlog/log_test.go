package eventlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/model"
)

func TestLogAppendAssignsSequence(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "events.jsonl"))
	ev, err := log.Append(model.Event{
		Type:      model.EventMessage,
		Timestamp: time.Now().UTC(),
		RoomID:    "room-1",
		Actor:     "alice",
		Payload:   model.MessagePayload{Text: "hello"},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if ev.Seq != 1 {
		t.Fatalf("seq = %d, want 1", ev.Seq)
	}
	events, err := log.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 1 {
		t.Fatalf("events = %#v", events)
	}
}

func TestLogAfterSeq(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "events.jsonl"))
	for i := 0; i < 3; i++ {
		if _, err := log.Append(model.Event{Type: model.EventMessage, RoomID: "r", Actor: "a"}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	events, err := log.AfterSeq(1, 10)
	if err != nil {
		t.Fatalf("AfterSeq: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 2 || events[1].Seq != 3 {
		t.Fatalf("events = %#v", events)
	}
}

func TestLogReadAllCorruptJSONIncludesPathAndLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(`{"seq":1,"type":"message"}`+"\n"+"not-json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := New(path).ReadAll()
	if err == nil {
		t.Fatal("ReadAll error = nil, want corrupt JSON error")
	}
	if !strings.Contains(err.Error(), path+":2:") {
		t.Fatalf("error = %q, want path and line number", err)
	}
}

func TestLogAfterSeqHonorsLimit(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "events.jsonl"))
	for i := 0; i < 3; i++ {
		if _, err := log.Append(model.Event{Type: model.EventMessage, RoomID: "r", Actor: "a"}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	events, err := log.AfterSeq(1, 1)
	if err != nil {
		t.Fatalf("AfterSeq: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 2 {
		t.Fatalf("events = %#v, want only seq 2", events)
	}
}

func TestLogAppendDefaultsZeroTimestamp(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "events.jsonl"))
	ev, err := log.Append(model.Event{Type: model.EventMessage, RoomID: "r", Actor: "a"})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if ev.Timestamp.IsZero() {
		t.Fatal("Timestamp is zero, want default timestamp")
	}

	events, err := log.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 1 || events[0].Timestamp.IsZero() {
		t.Fatalf("events = %#v, want persisted default timestamp", events)
	}
}

func TestLogReadAllMissingFileReturnsEmpty(t *testing.T) {
	events, err := New(filepath.Join(t.TempDir(), "missing.jsonl")).ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want empty", events)
	}
}

func TestLogFileModeIs0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if _, err := New(path).Append(model.Event{Type: model.EventMessage, RoomID: "r", Actor: "a"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %v, want 0600", got)
	}
}

func TestLogAppendAssignsIncreasingSequences(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "events.jsonl"))
	first, err := log.Append(model.Event{Type: model.EventMessage, RoomID: "r", Actor: "a"})
	if err != nil {
		t.Fatalf("Append first: %v", err)
	}
	second, err := log.Append(model.Event{Type: model.EventMessage, RoomID: "r", Actor: "a"})
	if err != nil {
		t.Fatalf("Append second: %v", err)
	}
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("seqs = %d, %d; want 1, 2", first.Seq, second.Seq)
	}
}

func TestLogReadsLargeEventLine(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "events.jsonl"))
	text := strings.Repeat("x", 70*1024)
	if _, err := log.Append(model.Event{
		Type:    model.EventMessage,
		RoomID:  "r",
		Actor:   "a",
		Payload: model.MessagePayload{Text: text},
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	events, err := log.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	payload, ok := events[0].Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("payload = %#v, want object", events[0].Payload)
	}
	if got, ok := payload["text"].(string); !ok || got != text {
		t.Fatalf("payload text length = %d, ok = %v; want %d", len(got), ok, len(text))
	}
}

func TestLogAppendAssignedPreservesSequenceAndTimestamp(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "events.jsonl"))
	ts := time.Date(2026, 6, 15, 12, 30, 0, 0, time.UTC)

	err := log.AppendAssigned(model.Event{
		Seq:       42,
		Type:      model.EventMessage,
		Timestamp: ts,
		RoomID:    "r",
		Actor:     "a",
		Payload:   model.MessagePayload{Text: "mirrored"},
	})
	if err != nil {
		t.Fatalf("AppendAssigned: %v", err)
	}

	events, err := log.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].Seq != 42 {
		t.Fatalf("Seq = %d, want 42", events[0].Seq)
	}
	if !events[0].Timestamp.Equal(ts) {
		t.Fatalf("Timestamp = %s, want %s", events[0].Timestamp, ts)
	}
}

func TestLogAppendAssignedRejectsMissingSequence(t *testing.T) {
	log := New(filepath.Join(t.TempDir(), "events.jsonl"))

	err := log.AppendAssigned(model.Event{Type: model.EventMessage, RoomID: "r", Actor: "a"})
	if err == nil {
		t.Fatal("AppendAssigned error = nil, want missing sequence error")
	}
}
