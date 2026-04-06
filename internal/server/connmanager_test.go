package server

import (
	"testing"
	"time"
)

func TestConnectionManager_AddAndBroadcast(t *testing.T) {
	cm := NewConnectionManager()

	cc1 := &ClientConn{Name: "alice", Send: make(chan []byte, 10), Done: make(chan struct{})}
	cc2 := &ClientConn{Name: "bob", Send: make(chan []byte, 10), Done: make(chan struct{})}

	cm.Add("alice", cc1)
	cm.Add("bob", cc2)

	msg := []byte(`{"hello":"world"}`)
	cm.Broadcast(msg)

	select {
	case got := <-cc1.Send:
		if string(got) != string(msg) {
			t.Errorf("alice got %q, want %q", got, msg)
		}
	case <-time.After(time.Second):
		t.Fatal("alice did not receive broadcast")
	}

	select {
	case got := <-cc2.Send:
		if string(got) != string(msg) {
			t.Errorf("bob got %q, want %q", got, msg)
		}
	case <-time.After(time.Second):
		t.Fatal("bob did not receive broadcast")
	}
}

func TestConnectionManager_BroadcastExcept(t *testing.T) {
	cm := NewConnectionManager()

	cc1 := &ClientConn{Name: "alice", Send: make(chan []byte, 10), Done: make(chan struct{})}
	cc2 := &ClientConn{Name: "bob", Send: make(chan []byte, 10), Done: make(chan struct{})}

	cm.Add("alice", cc1)
	cm.Add("bob", cc2)

	msg := []byte(`{"from":"alice"}`)
	cm.BroadcastExcept("alice", msg)

	// bob should receive
	select {
	case got := <-cc2.Send:
		if string(got) != string(msg) {
			t.Errorf("bob got %q, want %q", got, msg)
		}
	case <-time.After(time.Second):
		t.Fatal("bob did not receive broadcast")
	}

	// alice should NOT receive
	select {
	case got := <-cc1.Send:
		t.Fatalf("alice should not have received, got %q", got)
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestConnectionManager_Remove(t *testing.T) {
	cm := NewConnectionManager()

	cc := &ClientConn{Name: "alice", Send: make(chan []byte, 10), Done: make(chan struct{})}
	cm.Add("alice", cc)
	cm.Remove("alice")

	// Done channel should be closed
	select {
	case <-cc.Done:
		// expected
	case <-time.After(time.Second):
		t.Fatal("Done channel was not closed after Remove")
	}

	// Broadcast after remove should not panic
	cm.Broadcast([]byte(`{"test":"msg"}`))
}

func TestConnectionManager_BroadcastDropsFullBuffer(t *testing.T) {
	cm := NewConnectionManager()

	// Unbuffered channel — will be full immediately
	cc := &ClientConn{Name: "alice", Send: make(chan []byte), Done: make(chan struct{})}
	cm.Add("alice", cc)

	done := make(chan struct{})
	go func() {
		cm.Broadcast([]byte(`{"big":"msg"}`))
		close(done)
	}()

	select {
	case <-done:
		// Broadcast returned without blocking — success
	case <-time.After(time.Second):
		t.Fatal("Broadcast blocked on full send channel")
	}
}
