// Package dispatcher provides message delivery policies for agent drivers.
// Each agent gets its own Dispatcher instance that controls how and when
// messages are delivered: debouncing, prioritization, batching, etc.
package dispatcher

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/room"
)

// Dispatcher consumes room events and delivers chat messages to an agent.
type Dispatcher interface {
	// Start begins consuming events from a room.State subscription.
	Start(events <-chan room.Event)
	// Close waits for the event loop to finish and flushes pending messages.
	Close()
}

// Debounce delivers @mentioned messages immediately and batches non-mentioned
// messages with a delay to avoid flooding the agent.
type Debounce struct {
	agentName string
	delay     time.Duration
	send      func(string)

	mu           sync.Mutex
	pendingMsg   string
	pendingTimer *time.Timer
	done         chan struct{}
}

// NewDebounce creates a dispatcher that delivers messages via send.
// Messages mentioning agentName are sent immediately; others are batched
// with the given debounce delay.
func NewDebounce(agentName string, delay time.Duration, send func(string)) *Debounce {
	return &Debounce{
		agentName: agentName,
		delay:     delay,
		send:      send,
	}
}

// Start begins consuming events. It runs a goroutine that processes events
// until the channel is closed.
func (d *Debounce) Start(events <-chan room.Event) {
	d.done = make(chan struct{})
	go func() {
		defer close(d.done)
		for evt := range events {
			if msg, ok := evt.(room.MessageReceived); ok {
				d.dispatch(msg.Message)
			}
		}
		d.flush()
	}()
}

// Close waits for the event loop goroutine to finish.
func (d *Debounce) Close() {
	if d.done != nil {
		<-d.done
	}
}

func (d *Debounce) dispatch(msg protocol.MessageParams) {
	if msg.From == d.agentName {
		return
	}

	text := ""
	if len(msg.Content) > 0 {
		text = msg.Content[0].Text
	}
	formatted := fmt.Sprintf("%s: %s", msg.From, text)
	mentioned := isMentioned(msg.Mentions, d.agentName)

	d.mu.Lock()
	defer d.mu.Unlock()

	if mentioned {
		if d.pendingTimer != nil {
			d.pendingTimer.Stop()
			d.pendingTimer = nil
		}
		if d.pendingMsg != "" {
			d.send(d.pendingMsg)
			d.pendingMsg = ""
		}
		d.send(formatted)
		return
	}

	if d.pendingMsg != "" {
		d.pendingMsg += "\n" + formatted
	} else {
		d.pendingMsg = formatted
	}

	if d.pendingTimer == nil {
		d.pendingTimer = time.AfterFunc(d.delay, func() {
			d.flush()
		})
	} else {
		d.pendingTimer.Reset(d.delay)
	}
}

func (d *Debounce) flush() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pendingTimer != nil {
		d.pendingTimer.Stop()
		d.pendingTimer = nil
	}
	if d.pendingMsg != "" {
		d.send(d.pendingMsg)
		d.pendingMsg = ""
	}
}

func isMentioned(mentions []string, name string) bool {
	return slices.Contains(mentions, name)
}
