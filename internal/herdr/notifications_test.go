package herdr

import (
	"context"
	"testing"
	"time"
)

func testNotificationObservation() NotificationObservation {
	return NotificationObservation{NotificationBinding: NotificationBinding{Key: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Binding: Binding{SessionID: "psn_a", RoomID: "room-a"}, Conversation: NativeConversation{Source: "herdr:codex", Agent: "codex", Kind: "id", Value: "original"}}, Agent: notificationAgent{PaneID: "w:p1", TerminalID: "term_a", Status: "idle", StateChangeSeq: 1}, SeenSeq: 4, ThroughSeq: 5}
}

func TestNotificationScheduleGraceAndConsumption(t *testing.T) {
	t.Parallel()
	start := time.Now()
	o := testNotificationObservation()
	s := newNotificationSchedule()
	s.Observe(start, o)
	if got := s.Due(start.Add(29*time.Second), nil); len(got) != 0 {
		t.Fatal("early notification")
	}
	if got := s.Due(start.Add(30*time.Second), nil); len(got) != 1 {
		t.Fatalf("missing notification: %v", got)
	}
	o.ThroughSeq = 0
	o.SeenSeq = 5
	s.Observe(start.Add(30*time.Second), o)
	if got := s.Due(start.Add(time.Minute), nil); len(got) != 0 {
		t.Fatal("consumed inbox notified")
	}
	o.ThroughSeq = 6
	s.Observe(start.Add(time.Minute), o)
	if got := s.Due(start.Add(89*time.Second), nil); len(got) != 0 {
		t.Fatal("new message skipped grace")
	}
}

func TestNotificationScheduleBusyAndPresentation(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"working", "blocked", "unknown", "done"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			start := time.Now()
			o := testNotificationObservation()
			s := newNotificationSchedule()
			s.Observe(start, o)
			o.Agent.Status = status
			s.Observe(start.Add(20*time.Second), o)
			got := s.Due(start.Add(30*time.Second), nil)
			if (len(got) == 1) != (status == "done") {
				t.Fatalf("status %s delivered %d", status, len(got))
			}
		})
	}
}

func TestNotificationScheduleMissedBusyTransitionResetsGrace(t *testing.T) {
	t.Parallel()
	for _, seq := range []uint64{2, 3} {
		start := time.Now()
		o := testNotificationObservation()
		s := newNotificationSchedule()
		s.Observe(start, o)
		o.Agent.Status = "done"
		o.Agent.StateChangeSeq = seq
		s.Observe(start.Add(29*time.Second), o)
		if got := s.Due(start.Add(30*time.Second), nil); (len(got) == 1) != (seq == 2) {
			t.Fatalf("sequence %d delivered %d reminders", seq, len(got))
		}
		if got := s.Due(start.Add(59*time.Second), nil); len(got) != 1 {
			t.Fatal("fresh grace never completed")
		}
	}
}

func TestNotificationScheduleOutstandingAndFreshGeneration(t *testing.T) {
	t.Parallel()
	start := time.Now()
	o := testNotificationObservation()
	s := newNotificationSchedule()
	s.Observe(start, o)
	a := NotificationAttempt{ID: "one", Outcome: "accepted", Coverage: []NotificationCoverage{{Key: o.Key, SessionID: "psn_a", SeenSeq: 4, ThroughSeq: 5}}}
	o.ThroughSeq = 7
	s.Observe(start.Add(time.Minute), o)
	if got := s.Due(start.Add(time.Minute), []NotificationAttempt{a}); len(got) != 0 {
		t.Fatal("outstanding batch renotified")
	}
	o.SeenSeq = 5
	s.Observe(start.Add(time.Minute), o)
	if got := s.Due(start.Add(time.Minute), []NotificationAttempt{a}); len(got) != 0 {
		t.Fatal("later batch skipped fresh grace")
	}
	if got := s.Due(start.Add(90*time.Second), []NotificationAttempt{a}); len(got) != 1 {
		t.Fatal("later batch never notified")
	}
	s = newNotificationSchedule()
	s.Observe(start.Add(90*time.Second), o)
	if got := s.Due(start.Add(91*time.Second), nil); len(got) != 0 {
		t.Fatal("reconnect reused old readiness")
	}
}

func TestNotificationAttemptRecoveryAndRetry(t *testing.T) {
	t.Parallel()
	s := NewStore(t.TempDir())
	o := testNotificationObservation()
	first, err := s.PrepareNotification(context.Background(), "local", "/tmp/herdr.sock", []NotificationObservation{o}, "")
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := s.NotificationAttempts()
	if err != nil || len(recovered) != 1 || recovered[0].Outcome != "prepared" {
		t.Fatalf("lost intent: %v %v", recovered, err)
	}
	if _, err = s.PrepareNotification(context.Background(), "local", "/tmp/herdr.sock", []NotificationObservation{o}, ""); err == nil {
		t.Fatal("uncertain preparation automatically repeated")
	}
	if err = s.RetryNotification(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.PrepareNotification(context.Background(), "local", "/tmp/herdr.sock", []NotificationObservation{o}, first.ID)
	if err != nil || next.Replaces != first.ID {
		t.Fatalf("explicit retry: %+v %v", next, err)
	}
	if _, err = s.PrepareNotification(context.Background(), "local", "/tmp/herdr.sock", []NotificationObservation{o}, first.ID); err == nil {
		t.Fatal("concurrent retry accepted twice")
	}
}

func TestNotificationScheduleRetryDoesNotAuthorizeBusyAgent(t *testing.T) {
	t.Parallel()
	start := time.Now()
	o := testNotificationObservation()
	o.Agent.Status = "working"
	s := newNotificationSchedule()
	s.Observe(start, o)
	a := NotificationAttempt{ID: "uncertain", Outcome: "uncertain", RetryRequested: true, Coverage: []NotificationCoverage{{Key: o.Key, SessionID: o.Binding.SessionID, SeenSeq: o.SeenSeq, ThroughSeq: o.ThroughSeq}}}
	s.Due(start, []NotificationAttempt{a})
	if due := s.Due(start.Add(time.Minute), []NotificationAttempt{a}); len(due) != 0 {
		t.Fatal("explicit retry bypassed readiness")
	}
}

func TestNotificationAttemptSharedCoverageAndCorruption(t *testing.T) {
	t.Parallel()
	s := NewStore(t.TempDir())
	one := testNotificationObservation()
	two := one
	two.Key = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	two.Binding.SessionID = "psn_b"
	two.Binding.RoomID = "room-b"
	two.ThroughSeq = 9
	a, err := s.PrepareNotification(context.Background(), "local", "/tmp/herdr.sock", []NotificationObservation{one, two}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteNotification(a.ID, "accepted", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.NotificationAttempts()
	if err != nil || len(got) != 1 || len(got[0].Coverage) != 2 || got[0].Outcome != "accepted" {
		t.Fatalf("shared journal: %+v %v", got, err)
	}
	if err = s.CompleteNotification(a.ID, "bogus", ""); err == nil {
		t.Fatal("invalid outcome accepted")
	}
	if err = s.RetryNotification(context.Background(), a.ID); err == nil {
		t.Fatal("known accepted submission retried")
	}
}

func TestNotificationAttemptHostIsolation(t *testing.T) {
	t.Parallel()
	s := NewStore(t.TempDir())
	o := testNotificationObservation()
	if _, err := s.PrepareNotification(context.Background(), "host-a", "/tmp/herdr.sock", []NotificationObservation{o}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareNotification(context.Background(), "host-b", "/tmp/herdr.sock", []NotificationObservation{o}, ""); err != nil {
		t.Fatalf("another host inherited suppression: %v", err)
	}
}

func TestNotificationAttemptPartialRetryPreservesUnavailableBinding(t *testing.T) {
	t.Parallel()
	s := NewStore(t.TempDir())
	one := testNotificationObservation()
	two := one
	two.Key = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	two.Binding.SessionID = "psn_b"
	first, err := s.PrepareNotification(context.Background(), "local", "/tmp/herdr.sock", []NotificationObservation{one, two}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RetryNotification(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	next, err := s.PrepareNotification(context.Background(), "local", "/tmp/herdr.sock", []NotificationObservation{one}, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Coverage) != 2 {
		t.Fatalf("retry discarded unavailable coverage: %+v", next)
	}
	afterCrash, err := s.NotificationAttempts()
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := notificationOutstanding(two.Key, two.Binding.SessionID, two.SeenSeq, afterCrash)
	if err != nil || blocked == nil || blocked.ID != next.ID {
		t.Fatalf("unavailable binding lost durable suppression: %+v %v", blocked, err)
	}
	if err = s.CompleteNotification(next.ID, "rejected", "room unavailable"); err != nil {
		t.Fatal(err)
	}
	afterRejection, err := s.NotificationAttempts()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range []NotificationObservation{one, two} {
		blocked, err := notificationOutstanding(o.Key, o.Binding.SessionID, o.SeenSeq, afterRejection)
		if err != nil || blocked == nil || blocked.ID != next.ID || blocked.RetryRequested {
			t.Fatalf("rejected retry released original uncertainty: %+v %v", blocked, err)
		}
	}
	if err = s.RetryNotification(context.Background(), next.ID); err != nil {
		t.Fatalf("explicit retry of inherited uncertainty: %v", err)
	}
	third, err := s.PrepareNotification(context.Background(), "local", "/tmp/herdr.sock", []NotificationObservation{one, two}, next.ID)
	if err != nil || len(third.Coverage) != 2 {
		t.Fatalf("shared retry chain lost coverage: %+v %v", third, err)
	}
}
