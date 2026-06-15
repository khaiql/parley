package adapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
)

func TestInboxPeekDoesNotAdvanceCursor(t *testing.T) {
	store := newTestStore(t)
	if err := store.AppendLocal(testEvent(1, model.EventMessage, "alice")); err != nil {
		t.Fatalf("AppendLocal: %v", err)
	}

	events, err := store.Inbox(true)
	if err != nil {
		t.Fatalf("Inbox peek: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 1 {
		t.Fatalf("events = %#v, want seq 1", events)
	}
	meta, err := store.LoadMeta()
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.LastSeenSeq != 0 {
		t.Fatalf("LastSeenSeq = %d, want 0", meta.LastSeenSeq)
	}
}

func TestInboxAdvancesCursor(t *testing.T) {
	store := newTestStore(t)
	mustAppendLocal(t, store, testEvent(1, model.EventParticipantJoined, "alice"))
	mustAppendLocal(t, store, testEvent(2, model.EventMessage, "alice"))

	events, err := store.Inbox(false)
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("events = %#v, want seqs 1, 2", events)
	}
	meta, err := store.LoadMeta()
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.LastSeenSeq != 2 {
		t.Fatalf("LastSeenSeq = %d, want 2", meta.LastSeenSeq)
	}

	events, err = store.Inbox(false)
	if err != nil {
		t.Fatalf("Inbox again: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want empty after cursor advance", events)
	}
}

func TestWaitBatchReturnsInterveningEventsThroughMessage(t *testing.T) {
	store := newTestStore(t)
	mustAppendLocal(t, store, testEvent(1, model.EventParticipantJoined, "bob"))
	mustAppendLocal(t, store, testEvent(2, model.EventMessage, "me"))
	mustAppendLocal(t, store, testEvent(3, model.EventParticipantLeft, "bob"))
	mustAppendLocal(t, store, testEvent(4, model.EventMessage, "alice"))
	mustAppendLocal(t, store, testEvent(5, model.EventMessage, "carol"))

	events, err := store.WaitReadyBatch("me")
	if err != nil {
		t.Fatalf("WaitReadyBatch: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %#v, want first four unseen events", events)
	}
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Fatalf("events[%d].Seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
	meta, err := store.LoadMeta()
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.LastSeenSeq != 0 {
		t.Fatalf("LastSeenSeq = %d, want wait to leave cursor unchanged", meta.LastSeenSeq)
	}
}

func TestWaitReadyBatchReturnsEmptyWithoutOtherMessage(t *testing.T) {
	store := newTestStore(t)
	mustAppendLocal(t, store, testEvent(1, model.EventParticipantJoined, "alice"))
	mustAppendLocal(t, store, testEvent(2, model.EventMessage, "me"))

	events, err := store.WaitReadyBatch("me")
	if err != nil {
		t.Fatalf("WaitReadyBatch: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %#v, want empty without unseen message from another actor", events)
	}
}

func TestWaitTimeoutShape(t *testing.T) {
	data, err := json.Marshal(ControlResponse{OK: true, Status: "timeout"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got := string(data); got != `{"ok":true,"status":"timeout"}` {
		t.Fatalf("timeout response JSON = %s", got)
	}
	_ = time.Second
}

func TestAppendLocalPreservesSequenceAndUpdatesLastReceivedSeq(t *testing.T) {
	store := newTestStore(t)
	if err := store.SaveMeta(Meta{LastReceivedSeq: 5}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	mustAppendLocal(t, store, testEvent(4, model.EventMessage, "alice"))
	mustAppendLocal(t, store, testEvent(10, model.EventMessage, "bob"))

	meta, err := store.LoadMeta()
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.LastReceivedSeq != 10 {
		t.Fatalf("LastReceivedSeq = %d, want 10", meta.LastReceivedSeq)
	}
	events, err := eventlog.New(store.EventsPath).ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 2 || events[0].Seq != 4 || events[1].Seq != 10 {
		t.Fatalf("persisted events = %#v, want assigned seqs 4 and 10", events)
	}
}

func TestControlSocketRoundTrip(t *testing.T) {
	socketPath := filepath.Join(shortSocketDir(t), "control.sock")
	serveControlForTest(t, socketPath, func(req ControlRequest) ControlResponse {
		if req.Type != "send" {
			return ControlResponse{OK: false, Error: "unexpected request"}
		}
		return ControlResponse{
			OK:     true,
			Status: "sent",
			Events: []model.Event{{
				Seq:    12,
				Type:   model.EventMessage,
				RoomID: "r",
				Actor:  "alice",
			}},
		}
	})

	resp, err := CallControl(socketPath, ControlRequest{Type: "send", Text: "hello"})
	if err != nil {
		t.Fatalf("CallControl: %v", err)
	}
	if !resp.OK || resp.Status != "sent" || len(resp.Events) != 1 || resp.Events[0].Seq != 12 {
		t.Fatalf("response = %#v", resp)
	}
}

func TestControlHandlerErrorResponse(t *testing.T) {
	socketPath := filepath.Join(shortSocketDir(t), "control.sock")
	serveControlForTest(t, socketPath, func(req ControlRequest) ControlResponse {
		return ControlResponse{OK: false, Error: "unsupported: " + req.Type}
	})

	resp, err := CallControl(socketPath, ControlRequest{Type: "nope"})
	if err != nil {
		t.Fatalf("CallControl: %v", err)
	}
	if resp.OK || !strings.Contains(resp.Error, "unsupported: nope") {
		t.Fatalf("response = %#v, want handler error", resp)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return NewStore(filepath.Join(dir, "participant.json"), filepath.Join(dir, "events.jsonl"))
}

func testEvent(seq int64, typ model.EventType, actor string) model.Event {
	return model.Event{
		Seq:    seq,
		Type:   typ,
		RoomID: "room-1",
		Actor:  actor,
	}
}

func mustAppendLocal(t *testing.T, store *Store, ev model.Event) {
	t.Helper()
	if err := store.AppendLocal(ev); err != nil {
		t.Fatalf("AppendLocal: %v", err)
	}
}

func serveControlForTest(t *testing.T, socketPath string, handler func(ControlRequest) ControlResponse) {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		errCh <- ServeControl(socketPath, handler)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := CallControl(socketPath, ControlRequest{Type: "probe"}); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case err := <-errCh:
		t.Fatalf("ServeControl exited before accepting connections: %v", err)
	default:
		t.Fatalf("ServeControl did not create socket at %s", socketPath)
	}
}

func shortSocketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "parley-adapter-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("RemoveAll %s: %v", dir, err)
		}
	})
	return dir
}
