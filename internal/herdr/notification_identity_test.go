package herdr

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/khaiql/parley/internal/model"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

type notificationNativeFixture struct {
	mu      sync.Mutex
	agents  []notificationAgent
	prompts []string
	mode    string
	streams []net.Conn
	done    chan struct{}
}

func notificationFixture(t *testing.T) (*HostOperations, Binding, *notificationNativeFixture) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "pn-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := filepath.Join(root, "h")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	f := &notificationNativeFixture{done: make(chan struct{}), agents: []notificationAgent{{PaneID: "w:p1", TerminalID: "term_a", WorkspaceID: "w", Kind: "codex", Status: "idle", StateChangeSeq: 1, Conversation: &NativeConversation{Source: "herdr:codex", Agent: "codex", Kind: "id", Value: "original"}}}}
	t.Cleanup(func() { close(f.done) })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var req struct {
					Method string `json:"method"`
					Params struct {
						Target string `json:"target"`
						Text   string `json:"text"`
					} `json:"params"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				if req.Method == "events.subscribe" {
					f.mu.Lock()
					f.streams = append(f.streams, conn)
					f.mu.Unlock()
					_, _ = conn.Write([]byte(testEventAck))
					<-f.done
					return
				}
				f.mu.Lock()
				defer f.mu.Unlock()
				var result any
				switch req.Method {
				case "agent.list":
					result = map[string]any{"agents": f.agents}
				case "agent.get":
					for _, a := range f.agents {
						if a.PaneID == req.Params.Target {
							result = map[string]any{"agent": a}
						}
					}
				case "agent.prompt":
					f.prompts = append(f.prompts, req.Params.Text)
					if f.mode == "eof" {
						return
					}
					if f.mode == "blocked" {
						_ = json.NewEncoder(conn).Encode(map[string]any{"error": map[string]string{"code": "agent_blocked"}})
						return
					}
					for _, a := range f.agents {
						if a.PaneID == req.Params.Target {
							result = map[string]any{"type": "agent_prompted", "agent": a}
						}
					}
				default:
					result = map[string]any{}
				}
				_ = json.NewEncoder(conn).Encode(map[string]any{"result": result})
			}()
		}
	}()
	h := &HostOperations{Store: NewStore(filepath.Join(root, "plugin")), Client: &Client{LocalMachineID: "local", HerdrSocket: socket, RuntimeRoot: root}}
	b := Binding{RoomID: "room-a", SessionID: "psn_original", ParticipantName: "owned", StateDir: root, Target: AgentTarget{MachineID: "self", HerdrSocket: socket, PaneID: "w:p1", WorkspaceID: "w", TerminalID: "term_a", Kind: "codex"}, Status: "online", JoinPhase: "complete", JoinRequestID: "join-a"}
	originalFixture(t, root, b, true)
	if err := h.Store.PinRuntimeRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := h.saveBinding(b); err != nil {
		t.Fatal(err)
	}
	return h, b, f
}

func TestNotificationEnrollmentPinsOriginalConversation(t *testing.T) {
	h, b, f := notificationFixture(t)
	if err := h.EnrollNotifications(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.agents[0].Conversation.Value = "replacement"
	f.mu.Unlock()
	if err := h.EnrollNotifications(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	bindings, err := h.NotificationBindings(context.Background())
	if err != nil || len(bindings) != 1 || bindings[0].Conversation.Value != "original" {
		t.Fatalf("enrollment overwritten: %+v %v", bindings, err)
	}
	if _, err := h.ObserveNotification(context.Background(), bindings[0].Key); err == nil {
		t.Fatal("replacement conversation accepted")
	}
}

func TestNotificationEnrollmentAfterRoomHandoff(t *testing.T) {
	h, b, _ := notificationFixture(t)
	result, err := h.withHandoff(context.Background(), Response{Status: "joined", Binding: &b}, nil)
	if err != nil || result.NotificationStatus != "enrolled" {
		t.Fatalf("room enrollment: %+v %v", result, err)
	}
	bindings, err := h.NotificationBindings(context.Background())
	if err != nil || len(bindings) != 1 {
		t.Fatalf("handoff did not enroll: %v %v", bindings, err)
	}
}

func TestNotificationIdentityRequiresUniqueNativeConversation(t *testing.T) {
	for _, mode := range []string{"missing", "duplicate", "moved", "shell"} {
		t.Run(mode, func(t *testing.T) {
			h, b, f := notificationFixture(t)
			if err := h.EnrollNotifications(context.Background(), b); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			switch mode {
			case "missing":
				f.agents[0].Conversation = nil
			case "duplicate":
				f.agents = append(f.agents, f.agents[0])
			case "moved":
				f.agents[0].PaneID = "w2:p9"
				f.agents[0].TerminalID = "term_new"
			case "shell":
				f.agents = nil
			}
			f.mu.Unlock()
			o, err := h.ObserveNotification(context.Background(), HostBindingKey(b.RoomID, b.Target))
			if mode == "moved" {
				if err != nil || o.Agent.PaneID != "w2:p9" || o.Binding.Target.TerminalID != "term_a" {
					t.Fatalf("moved recipient: %+v %v", o, err)
				}
			} else if err == nil {
				t.Fatal("unverified recipient accepted")
			}
		})
	}
}

func TestNotificationObservationDoesNotConsumeInbox(t *testing.T) {
	h, b, _ := notificationFixture(t)
	if got, err := h.NotificationBindings(context.Background()); err != nil || len(got) != 0 {
		t.Fatalf("legacy binding automatically enrolled: %v %v", got, err)
	}
	if err := h.EnrollNotifications(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	p, err := h.Store.pinnedPaths()
	if err != nil {
		t.Fatal(err)
	}
	s, err := parleyRuntime.ParticipantStore(p, b.RoomID, b.ParticipantName)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []model.Event{{Seq: 18, RoomID: b.RoomID, Type: model.EventParticipantJoined, Actor: "other"}, {Seq: 19, RoomID: b.RoomID, Type: model.EventMessage, Actor: b.ParticipantName}, {Seq: 20, RoomID: b.RoomID, Type: model.EventMessage, Actor: "other", Payload: model.MessagePayload{Artifacts: []model.ArtifactMetadata{{ID: "art_file"}}}}} {
		if err := s.AppendLocal(e); err != nil {
			t.Fatal(err)
		}
	}
	o, err := h.ObserveNotification(context.Background(), HostBindingKey(b.RoomID, b.Target))
	if err != nil || o.ThroughSeq != 20 || o.SeenSeq != 17 {
		t.Fatalf("observation: %+v %v", o, err)
	}
	meta, err := s.LoadMeta()
	if err != nil || meta.LastSeenSeq != 17 {
		t.Fatalf("inbox consumed: %+v %v", meta, err)
	}
}
