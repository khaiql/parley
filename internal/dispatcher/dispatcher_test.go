package dispatcher

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
)

type collector struct {
	mu   sync.Mutex
	msgs []string
}

func (c *collector) send(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, text)
}

func (c *collector) get() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.msgs))
	copy(out, c.msgs)
	return out
}

func makeMsg(from, text string, mentions []string) protocol.MessageParams {
	return protocol.MessageParams{
		From:     from,
		Source:   "human",
		Role:     "human",
		Content:  []protocol.Content{{Type: "text", Text: text}},
		Mentions: mentions,
	}
}

func TestDebounce_IgnoresOwnMessages(t *testing.T) {
	col := &collector{}
	d := NewDebounce("bot", 50*time.Millisecond, col.send)

	events := make(chan room.Event, 8)
	d.Start(events)

	events <- room.MessageReceived{Message: makeMsg("bot", "my own message", nil)}
	close(events)
	d.Close()

	if msgs := col.get(); len(msgs) != 0 {
		t.Errorf("expected no messages, got %v", msgs)
	}
}

func TestDebounce_MentionDeliversImmediately(t *testing.T) {
	col := &collector{}
	d := NewDebounce("bot", 50*time.Millisecond, col.send)

	events := make(chan room.Event, 8)
	d.Start(events)

	events <- room.MessageReceived{Message: makeMsg("alice", "@bot help me", []string{"bot"})}
	time.Sleep(10 * time.Millisecond)

	msgs := col.get()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "alice:") {
		t.Errorf("expected formatted as 'alice: ...', got %q", msgs[0])
	}

	close(events)
	d.Close()
}

func TestDebounce_NonMentionBatchesWithDelay(t *testing.T) {
	col := &collector{}
	d := NewDebounce("bot", 50*time.Millisecond, col.send)

	events := make(chan room.Event, 8)
	d.Start(events)

	events <- room.MessageReceived{Message: makeMsg("alice", "hello", nil)}

	time.Sleep(10 * time.Millisecond)
	if msgs := col.get(); len(msgs) != 0 {
		t.Errorf("expected 0 messages before debounce, got %d", len(msgs))
	}

	time.Sleep(60 * time.Millisecond)
	if msgs := col.get(); len(msgs) != 1 {
		t.Errorf("expected 1 message after debounce, got %d", len(msgs))
	}

	close(events)
	d.Close()
}

func TestDebounce_MentionFlushesPendingBatch(t *testing.T) {
	col := &collector{}
	d := NewDebounce("bot", 200*time.Millisecond, col.send)

	events := make(chan room.Event, 8)
	d.Start(events)

	events <- room.MessageReceived{Message: makeMsg("alice", "first", nil)}
	time.Sleep(10 * time.Millisecond)

	events <- room.MessageReceived{Message: makeMsg("bob", "@bot second", []string{"bot"})}
	time.Sleep(10 * time.Millisecond)

	msgs := col.get()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d: %v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0], "alice:") {
		t.Errorf("first should be alice's batch, got %q", msgs[0])
	}
	if !strings.Contains(msgs[1], "bob:") {
		t.Errorf("second should be bob's mention, got %q", msgs[1])
	}

	close(events)
	d.Close()
}

func TestDebounce_CloseFlushes(t *testing.T) {
	col := &collector{}
	d := NewDebounce("bot", 5*time.Second, col.send)

	events := make(chan room.Event, 8)
	d.Start(events)

	events <- room.MessageReceived{Message: makeMsg("alice", "pending", nil)}
	time.Sleep(10 * time.Millisecond)

	close(events)
	d.Close()

	msgs := col.get()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 flushed message, got %d", len(msgs))
	}
}

func TestDebounce_FormatsNameColonText(t *testing.T) {
	col := &collector{}
	d := NewDebounce("bot", 50*time.Millisecond, col.send)

	events := make(chan room.Event, 8)
	d.Start(events)

	events <- room.MessageReceived{Message: makeMsg("alice", "@bot do this", []string{"bot"})}
	time.Sleep(10 * time.Millisecond)

	msgs := col.get()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	want := "alice: @bot do this"
	if msgs[0] != want {
		t.Errorf("got %q, want %q", msgs[0], want)
	}

	close(events)
	d.Close()
}

func TestDebounce_DebounceResetsOnNewMessage(t *testing.T) {
	col := &collector{}
	d := NewDebounce("bot", 50*time.Millisecond, col.send)

	events := make(chan room.Event, 8)
	d.Start(events)

	events <- room.MessageReceived{Message: makeMsg("alice", "msg1", nil)}
	time.Sleep(30 * time.Millisecond)

	events <- room.MessageReceived{Message: makeMsg("bob", "msg2", nil)}
	time.Sleep(30 * time.Millisecond)

	if msgs := col.get(); len(msgs) != 0 {
		t.Errorf("expected 0 messages (timer reset), got %d", len(msgs))
	}

	time.Sleep(40 * time.Millisecond)
	msgs := col.get()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 batched message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0], "alice:") || !strings.Contains(msgs[0], "bob:") {
		t.Errorf("expected both senders in batch, got %q", msgs[0])
	}

	close(events)
	d.Close()
}
