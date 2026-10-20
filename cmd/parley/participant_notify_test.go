package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/model"
)

func TestParticipantConcurrentWaitsCannotStealAcknowledgement(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store := adapter.NewStore(filepath.Join(root, "meta.json"), filepath.Join(root, "events.jsonl"))
	if err := store.SaveMeta(adapter.Meta{RoomID: "room-test", Name: "self"}); err != nil {
		t.Fatal(err)
	}
	rt := &participantAdapterRuntime{cfg: participantDaemonConfig{Name: "self"}, store: store, notify: make(chan struct{}, 1)}
	const observers = 8
	started, finished := make(chan struct{}, observers), make(chan error, observers)
	for range observers {
		go func() {
			started <- struct{}{}
			_, err := rt.waitForEvent(0, time.Second, func(e model.Event) bool { return e.Seq == 1 })
			finished <- err
		}()
	}
	for range observers {
		<-started
	}
	// Let the existing empty-store reads settle before publishing one event.
	time.Sleep(30 * time.Millisecond)
	if err := store.AppendLocal(model.Event{Seq: 1, RoomID: "room-test", Type: model.EventMessage, Actor: "self"}); err != nil {
		t.Fatal(err)
	}
	rt.signal()
	for range observers {
		if err := <-finished; err != nil {
			t.Errorf("another waiter stole acknowledgement wakeup: %v", err)
		}
	}
}
