package herdr

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNativeStatusEventsPreserveNotificationSubscription(t *testing.T) {
	t.Parallel()
	h, _, native := notificationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	relayDone, readerDone := make(chan error, 1), make(chan error, 1)
	go func() {
		err := relayEvents(ctx, h.Client.HerdrSocket, w, map[string]any{}, true)
		_ = w.Close()
		relayDone <- err
	}()
	started, woke := make(chan struct{}), make(chan struct{}, 1)
	go func() {
		readerDone <- ReadEvents(ctx, r, func() error { close(started); return nil }, func() error { woke <- struct{}{}; return nil })
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("subscription did not start")
	}
	// Captured Herdr 0.9.3 frames: dotted event name, untagged data. Focus
	// changes done to idle without replacing the agent or subscription.
	frames := []string{`{"data":{"pane":{"agent":"claude","agent_status":"idle","pane_id":"w2:p1","terminal_id":"term_a","terminal_title":"Working","workspace_id":"w2"},"type":"pane_updated"},"event":"pane_updated"}`}
	for _, status := range []string{"working", "done", "idle"} {
		frames = append(frames, `{"data":{"agent":"claude","agent_status":"`+status+`","pane_id":"w2:p1","workspace_id":"w2"},"event":"pane.agent_status_changed"}`)
	}
	for _, frame := range frames {
		native.mu.Lock()
		conn := native.streams[0]
		native.mu.Unlock()
		_, err := io.WriteString(conn, frame+"\n")
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-woke:
		case err := <-readerDone:
			t.Fatalf("native status invalidated observation: %v", err)
		case <-ctx.Done():
			t.Fatal("native status did not wake observation")
		}
	}
	cancel()
	if err := <-readerDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := <-relayDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNotificationPromptSerializesControllerRelease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	h, b, native := notificationFixture(t)
	notificationUnread(t, h, b)
	if err := h.EnrollNotifications(ctx, b); err != nil {
		t.Fatal(err)
	}
	o, err := h.ObserveNotification(ctx, HostBindingKey(b.RoomID, b.Target))
	if err != nil {
		t.Fatal(err)
	}
	a, err := h.Store.PrepareNotification(ctx, "local", h.Client.HerdrSocket, []NotificationObservation{o}, "")
	if err != nil {
		t.Fatal(err)
	}
	key, _ := SessionKey(h.Client.HerdrSocket)
	host := HostSession{Config: SessionConfig{HerdrSocket: h.Client.HerdrSocket, SessionKey: key}, ConfigDir: filepath.Join(t.TempDir(), "config"), Store: h.Store}
	claim := RPCRequest{Operation: "claim-controller", Session: "agents", SessionKey: key, ControllerID: "old", MachineID: "local"}
	if _, err = host.Dispatch(ctx, claim); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once, unblock sync.Once
	defer unblock.Do(func() { close(release) })
	native.mu.Lock()
	native.beforeList = func() { once.Do(func() { close(entered); <-release }) }
	native.mu.Unlock()
	prompt := RPCRequest{Operation: "notification-prompt", Session: "agents", SessionKey: key, ControllerID: "old", Notification: &a}
	done := make(chan error, 1)
	go func() { _, err := host.Dispatch(ctx, prompt); done <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt never reached identity lookup")
	}
	revoke := RPCRequest{Operation: "release-controller", Session: "agents", SessionKey: key, ControllerID: "old"}
	deadline, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err = host.Dispatch(deadline, revoke); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("controller release passed an in-flight terminal mutation: %v", err)
	}
	unblock.Do(func() { close(release) })
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = host.Dispatch(ctx, revoke); err != nil {
		t.Fatal(err)
	}
	claim.ControllerID = "new"
	if _, err = host.Dispatch(ctx, claim); err != nil {
		t.Fatal(err)
	}
	if _, err = host.Dispatch(ctx, prompt); err == nil {
		t.Fatal("revoked controller submitted another input")
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	if len(native.prompts) != 1 {
		t.Fatalf("prompts=%d, want only the input before revocation", len(native.prompts))
	}
}

func TestNotificationQuietTickSkipsArchiveButObservesRetry(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, b, native := notificationFixture(t)
	notificationUnread(t, h, b)
	if err := h.EnrollNotifications(ctx, b); err != nil {
		t.Fatal(err)
	}
	o, err := h.ObserveNotification(ctx, HostBindingKey(b.RoomID, b.Target))
	if err != nil {
		t.Fatal(err)
	}
	old := o
	old.SeenSeq, old.ThroughSeq = 16, 17
	archive, err := h.Store.PrepareNotification(ctx, "local", h.Client.HerdrSocket, []NotificationObservation{old}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Store.CompleteNotification(archive.ID, "accepted", ""); err != nil {
		t.Fatal(err)
	}
	active, err := h.Store.PrepareNotification(ctx, "local", h.Client.HerdrSocket, []NotificationObservation{o}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = h.Store.CompleteNotification(active.ID, "uncertain", "lost reply"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	g := &notificationGeneration{broker: &Broker{Store: h.Store, Client: h.Client}, peer: Peer{Machine: Machine{ID: "local"}, Config: SessionConfig{HerdrSocket: h.Client.HerdrSocket}}, ctx: ctx, now: func() time.Time { return now }, schedule: newNotificationSchedule(), bindings: map[string]NotificationBinding{}, pending: map[string]bool{}, waits: map[string]context.CancelFunc{}, waited: make(chan notificationWaitResult, 64)}
	if err = g.refresh(true); err != nil {
		t.Fatal(err)
	}
	dir, err := h.Store.notificationDir("attempts")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, archive.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("archive must not be reopened on quiet ticks"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.refresh(false); err != nil {
		t.Fatalf("quiet tick revisited historical archive: %v", err)
	}
	if g.diagnostics[0].LastNotifiedSeq != 17 {
		t.Fatal("lost historical accepted high-water mark")
	}
	if err = g.refreshJournal(true); err == nil {
		t.Fatal("recovery failed to validate the complete archive")
	}
	activePath := filepath.Join(dir, active.ID+".json")
	activeData, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(activePath, []byte("invalid active intent"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = g.refresh(false); err == nil {
		t.Fatal("quiet tick failed to validate an outstanding mutable intent")
	}
	if err = os.WriteFile(activePath, activeData, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = h.Store.RetryNotification(ctx, active.ID); err != nil {
		t.Fatal(err)
	}
	if err = g.refresh(false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Second)
	if err = g.refresh(false); err != nil {
		t.Fatal(err)
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	if len(native.prompts) != 1 {
		t.Fatal("cross-process retry request was not delivered after its fresh grace")
	}
}

func TestNotificationTickObservesPriorGenerationCompletion(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h, b, _ := notificationFixture(t)
	notificationUnread(t, h, b)
	if err := h.EnrollNotifications(ctx, b); err != nil {
		t.Fatal(err)
	}
	o, err := h.ObserveNotification(ctx, HostBindingKey(b.RoomID, b.Target))
	if err != nil {
		t.Fatal(err)
	}
	a, err := h.Store.PrepareNotification(ctx, "local", h.Client.HerdrSocket, []NotificationObservation{o}, "")
	if err != nil {
		t.Fatal(err)
	}
	g := &notificationGeneration{broker: &Broker{Store: h.Store, Client: h.Client}, peer: Peer{Machine: Machine{ID: "local"}, Config: SessionConfig{HerdrSocket: h.Client.HerdrSocket}}, ctx: ctx, now: time.Now, schedule: newNotificationSchedule(), bindings: map[string]NotificationBinding{}, pending: map[string]bool{}, waits: map[string]context.CancelFunc{}, waited: make(chan notificationWaitResult, 64)}
	if err = g.refresh(true); err != nil {
		t.Fatal(err)
	}
	// Consumption can be observed before the prior generation writes its result.
	g.schedule.bindings[o.Key].observation.SeenSeq = o.ThroughSeq
	// A canceled generation can finish its durable acknowledgement after this
	// generation loads the prepared intent during peer profile replacement.
	if err = h.Store.CompleteNotification(a.ID, "accepted", ""); err != nil {
		t.Fatal(err)
	}
	if err = g.refresh(false); err != nil {
		t.Fatal(err)
	}
	if g.diagnostics[0].LastNotifiedSeq != 18 {
		t.Fatal("late acceptance left the diagnostic high-water mark stale")
	}
}
