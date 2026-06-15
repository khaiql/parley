package eventlog

import (
	"path/filepath"
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
