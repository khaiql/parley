package herdr

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotificationWorkerWaitsForAckAndInvalidatesDisconnect(t *testing.T) {
	t.Parallel()
	h, b, _ := notificationFixture(t)
	notificationUnread(t, h, b)
	if err := h.EnrollNotifications(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	broker := &Broker{Store: h.Store, Client: h.Client}
	peer := Peer{Machine: Machine{ID: "local"}, Config: SessionConfig{HerdrSocket: h.Client.HerdrSocket}}
	r, w := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var clock atomic.Int64
	start := time.Now()
	finished := make(chan error, 1)
	go func() {
		finished <- broker.runNotificationGeneration(ctx, peer, r, func() time.Time { return start.Add(time.Duration(clock.Load()) * time.Second) })
	}()
	clock.Store(60)
	time.Sleep(20 * time.Millisecond)
	if got, _ := h.Store.NotificationAttempts(); len(got) != 0 {
		t.Fatal("delivery before subscription acknowledgement")
	}
	_, _ = io.WriteString(w, testEventAck)
	time.Sleep(40 * time.Millisecond)
	_ = w.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("disconnect did not cancel worker")
	}
	if got, _ := h.Store.NotificationAttempts(); len(got) != 0 {
		t.Fatal("disconnect reused old readiness")
	}
}

func TestNotificationWorkerDeliversOneFixedReminder(t *testing.T) {
	t.Parallel()
	h, b, f := notificationFixture(t)
	notificationUnread(t, h, b)
	if err := h.EnrollNotifications(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	broker := &Broker{Store: h.Store, Client: h.Client}
	peer := Peer{Machine: Machine{ID: "local"}, Config: SessionConfig{HerdrSocket: h.Client.HerdrSocket}}
	r, w := io.Pipe()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var clock atomic.Int64
	start := time.Now()
	finished := make(chan error, 1)
	go func() {
		finished <- broker.runNotificationGeneration(ctx, peer, r, func() time.Time { return start.Add(time.Duration(clock.Load()) * time.Second) })
	}()
	_, _ = io.WriteString(w, testEventAck)
	deadline := time.Now().Add(2 * time.Second)
	for {
		status, err := h.Store.NotificationStatuses()
		if err != nil {
			t.Fatal(err)
		}
		if len(status) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not observe binding")
		}
		time.Sleep(10 * time.Millisecond)
	}
	clock.Store(31)
	_, _ = io.WriteString(w, `{"event":"pane_agent_status_changed","data":{"type":"pane_agent_status_changed"}}`+"\n")
	for {
		f.mu.Lock()
		count := len(f.prompts)
		f.mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("eligible reminder not submitted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	clock.Store(100)
	_, _ = io.WriteString(w, `{"event":"pane_agent_status_changed","data":{"type":"pane_agent_status_changed"}}`+"\n")
	cancel()
	_ = w.Close()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) != 1 {
		t.Fatalf("duplicate reminder: %v", f.prompts)
	}
	a, err := h.Store.NotificationAttempts()
	if err != nil || len(a) != 1 || a[0].Outcome != "accepted" {
		t.Fatalf("lost receipt: %+v %v", a, err)
	}
}
