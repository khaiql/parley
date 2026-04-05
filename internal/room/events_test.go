package room

import (
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

func TestSubscribe_ReceivesEmittedEvents(t *testing.T) {
	s := &State{}
	ch := s.Subscribe()

	evt := ParticipantsChanged{
		Participants: []protocol.Participant{
			{Name: "alice", Role: "human", Online: true},
		},
	}
	s.emit(evt)

	select {
	case got := <-ch:
		pc, ok := got.(ParticipantsChanged)
		if !ok {
			t.Fatalf("expected ParticipantsChanged, got %T", got)
		}
		if len(pc.Participants) != 1 || pc.Participants[0].Name != "alice" {
			t.Fatalf("unexpected participants: %+v", pc.Participants)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestSubscribe_MultipleSubscribers(t *testing.T) {
	s := &State{}
	ch1 := s.Subscribe()
	ch2 := s.Subscribe()

	evt := MessageReceived{
		Message: protocol.MessageParams{
			ID:   "msg-1",
			From: "bob",
		},
	}
	s.emit(evt)

	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case got := <-ch:
			mr, ok := got.(MessageReceived)
			if !ok {
				t.Fatalf("subscriber %d: expected MessageReceived, got %T", i, got)
			}
			if mr.Message.ID != "msg-1" {
				t.Fatalf("subscriber %d: unexpected message ID: %s", i, mr.Message.ID)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out waiting for event", i)
		}
	}
}

func TestEmit_DropsWhenChannelFull(t *testing.T) {
	s := &State{}
	ch := s.Subscribe()

	// Fill the channel to capacity (64).
	for i := 0; i < 64; i++ {
		s.emit(ErrorOccurred{Error: nil})
	}

	// This 65th emit must not block.
	done := make(chan struct{})
	go func() {
		s.emit(ErrorOccurred{Error: nil})
		close(done)
	}()

	select {
	case <-done:
		// good — emit returned without blocking
	case <-time.After(time.Second):
		t.Fatal("emit blocked when channel was full")
	}

	// Verify exactly 64 events are in the channel.
	if len(ch) != 64 {
		t.Fatalf("expected 64 events in channel, got %d", len(ch))
	}
}

func TestActivity_Constants(t *testing.T) {
	if ActivityListening != 0 {
		t.Fatalf("expected ActivityListening=0, got %d", ActivityListening)
	}
	if ActivityThinking != 1 {
		t.Fatalf("expected ActivityThinking=1, got %d", ActivityThinking)
	}
	if ActivityGenerating != 2 {
		t.Fatalf("expected ActivityGenerating=2, got %d", ActivityGenerating)
	}
	if ActivityUsingTool != 3 {
		t.Fatalf("expected ActivityUsingTool=3, got %d", ActivityUsingTool)
	}
}
