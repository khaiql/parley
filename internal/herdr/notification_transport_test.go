package herdr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/khaiql/parley/internal/model"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

func notificationUnread(t *testing.T, h *HostOperations, b Binding) {
	t.Helper()
	p, err := h.Store.pinnedPaths()
	if err != nil {
		t.Fatal(err)
	}
	s, err := parleyRuntime.ParticipantStore(p, b.RoomID, b.ParticipantName)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AppendLocal(model.Event{Seq: 18, RoomID: b.RoomID, Actor: "other", Type: model.EventMessage, Payload: model.MessagePayload{Text: "DO NOT INJECT THIS BODY"}}); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationTransportFreshIdentityAndAcknowledgement(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"accepted", "replacement", "blocked", "eof", "consumed", "cycled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			h, b, f := notificationFixture(t)
			notificationUnread(t, h, b)
			if err := h.EnrollNotifications(context.Background(), b); err != nil {
				t.Fatal(err)
			}
			o, err := h.ObserveNotification(context.Background(), HostBindingKey(b.RoomID, b.Target))
			if err != nil {
				t.Fatal(err)
			}
			a, err := h.Store.PrepareNotification(context.Background(), "local", h.Client.HerdrSocket, []NotificationObservation{o}, "")
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			f.mode = mode
			if mode == "replacement" {
				f.agents[0].Conversation.Value = "different"
			}
			if mode == "blocked" {
				f.agents[0].Status = "blocked"
			}
			if mode == "cycled" {
				f.agents[0].StateChangeSeq += 2
			}
			f.mu.Unlock()
			if mode == "consumed" {
				p, _ := h.Store.pinnedPaths()
				s, _ := parleyRuntime.ParticipantStore(p, b.RoomID, b.ParticipantName)
				if _, err = s.Inbox(false); err != nil {
					t.Fatal(err)
				}
			}
			got := h.DeliverNotification(context.Background(), a)
			want := "rejected"
			if mode == "accepted" {
				want = "accepted"
			}
			if mode == "eof" {
				want = "uncertain"
			}
			if got.Outcome != want {
				t.Fatalf("%s: %+v", mode, got)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if mode == "accepted" || mode == "eof" {
				if len(f.prompts) != 1 || !strings.Contains(f.prompts[0], "--session psn_original") || strings.Contains(f.prompts[0], "DO NOT INJECT") {
					t.Fatalf("unsafe or missing reminder: %v", f.prompts)
				}
			} else if len(f.prompts) != 0 {
				t.Fatalf("input to ineligible target: %v", f.prompts)
			}
		})
	}
}

func TestNotificationTransportRPCFields(t *testing.T) {
	t.Parallel()
	key, _ := SessionKey("/tmp/herdr.sock")
	for _, op := range []string{"notification-bindings", "notification-observe", "notification-wait", "notification-prompt", "notification-events"} {
		r := RPCRequest{Operation: op, Session: "agents", SessionKey: key, ControllerID: "local"}
		if op == "notification-observe" || op == "notification-wait" {
			r.BindingKey = strings.Repeat("a", 64)
		}
		if op == "notification-prompt" {
			o := testNotificationObservation()
			r.Notification = &NotificationAttempt{ID: "one", MachineID: "peer", HerdrSocket: "/tmp/herdr.sock", Conversation: o.Conversation, ReadyStateSeq: 1, ReadyPaneID: "w:p1", ReadyTerminalID: "term_a", Outcome: "prepared", Coverage: []NotificationCoverage{{Key: o.Key, SessionID: "psn_a", SeenSeq: 4, ThroughSeq: 5}}}
		}
		data, _ := json.Marshal(r)
		if _, err := DecodeRPC(strings.NewReader(string(data))); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		r.Target = "w:p-unrelated"
		data, _ = json.Marshal(r)
		if _, err := DecodeRPC(strings.NewReader(string(data))); err == nil {
			t.Fatalf("%s accepted unrelated target", op)
		}
	}
}
