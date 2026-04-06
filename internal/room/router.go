package room

import (
	"fmt"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/protocol"
)

// MessageRouter consumes room events and routes chat messages to a destination.
type MessageRouter interface {
	Start(events <-chan Event)
	Close()
}

// DebounceRouter routes incoming chat messages to an agent driver.
// @mentioned messages are delivered immediately; non-mentioned messages are
// batched with a debounce delay to avoid flooding the agent.
type DebounceRouter struct {
	agentName string
	delay     time.Duration
	send      func(string)

	mu           sync.Mutex
	pendingMsg   string
	pendingTimer *time.Timer
	done         chan struct{}
}

// NewDebounceRouter creates a router that delivers messages via send.
// Messages mentioning agentName are sent immediately; others are batched
// with the given debounce delay.
func NewDebounceRouter(agentName string, delay time.Duration, send func(string)) *DebounceRouter {
	return &DebounceRouter{
		agentName: agentName,
		delay:     delay,
		send:      send,
	}
}

// Start begins consuming events. It runs a goroutine that processes events
// until the channel is closed.
func (r *DebounceRouter) Start(events <-chan Event) {
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		for evt := range events {
			if msg, ok := evt.(MessageReceived); ok {
				r.route(msg.Message)
			}
		}
		r.flush()
	}()
}

// Close waits for the event loop goroutine to finish.
func (r *DebounceRouter) Close() {
	if r.done != nil {
		<-r.done
	}
}

func (r *DebounceRouter) route(msg protocol.MessageParams) {
	if msg.From == r.agentName {
		return
	}

	text := ""
	if len(msg.Content) > 0 {
		text = msg.Content[0].Text
	}
	formatted := fmt.Sprintf("%s: %s", msg.From, text)
	mentioned := isMentioned(msg.Mentions, r.agentName)

	r.mu.Lock()
	defer r.mu.Unlock()

	if mentioned {
		if r.pendingTimer != nil {
			r.pendingTimer.Stop()
			r.pendingTimer = nil
		}
		if r.pendingMsg != "" {
			r.send(r.pendingMsg)
			r.pendingMsg = ""
		}
		r.send(formatted)
		return
	}

	if r.pendingMsg != "" {
		r.pendingMsg += "\n" + formatted
	} else {
		r.pendingMsg = formatted
	}

	if r.pendingTimer == nil {
		r.pendingTimer = time.AfterFunc(r.delay, func() {
			r.flush()
		})
	} else {
		r.pendingTimer.Reset(r.delay)
	}
}

func (r *DebounceRouter) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pendingTimer != nil {
		r.pendingTimer.Stop()
		r.pendingTimer = nil
	}
	if r.pendingMsg != "" {
		r.send(r.pendingMsg)
		r.pendingMsg = ""
	}
}

func isMentioned(mentions []string, name string) bool {
	for _, m := range mentions {
		if m == name {
			return true
		}
	}
	return false
}
