package herdr

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

func TestNotificationIntegrationRealAdapters(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "parley")
	if out, err := exec.Command("go", "build", "-o", binary, "../../cmd/parley").CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}
	for _, consumed := range []bool{false, true} {
		name := "unread"
		if consumed {
			name = "normal-listener"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, template, f := notificationFixture(t)
			h.Client.ParleyBinary = binary
			run := func(args ...string) []byte {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Env = appendEnvironment(os.Environ(), "PARLEY_STATE_DIR", h.Client.RuntimeRoot)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%v: %s %v", args, out, err)
				}
				return out
			}
			var started struct {
				RoomID     string `json:"room_id"`
				SessionID  string `json:"session_id"`
				Descriptor string `json:"descriptor"`
				Name       string `json:"name"`
			}
			if err := json.Unmarshal(run("start", "--topic", "notification integration", "--name", "receiver"), &started); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { run("stop", "--session", started.SessionID) })
			var sender struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(run("join", started.Descriptor, "--name", "sender"), &sender); err != nil {
				t.Fatal(err)
			}
			b := template
			b.RoomID = started.RoomID
			b.SessionID = started.SessionID
			b.ParticipantName = started.Name
			if err := h.saveBinding(b); err != nil {
				t.Fatal(err)
			}
			if err := h.EnrollNotifications(context.Background(), b); err != nil {
				t.Fatal(err)
			}
			run("inbox", "--session", b.SessionID)
			broker := &Broker{Store: h.Store, Client: h.Client}
			peer := Peer{Machine: Machine{ID: "local"}, Config: SessionConfig{HerdrSocket: h.Client.HerdrSocket}}
			r, w := io.Pipe()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			defer w.Close()
			var clock atomic.Int64
			start := time.Now()
			finished := make(chan error, 1)
			go func() {
				finished <- broker.runNotificationGeneration(ctx, peer, r, func() time.Time { return start.Add(time.Duration(clock.Load()) * time.Second) })
			}()
			_, _ = io.WriteString(w, testEventAck)
			await := func(predicate func() bool) {
				t.Helper()
				deadline := time.Now().Add(2 * time.Second)
				for !predicate() {
					if time.Now().After(deadline) {
						t.Fatal("notification integration condition not observed")
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			await(func() bool { status, _ := h.Store.NotificationStatuses(); return len(status) == 1 })
			run("send", "--session", sender.SessionID, "message for original recipient")
			await(func() bool {
				status, _ := h.Store.NotificationStatuses()
				return len(status) == 1 && status[0].ThroughSeq > status[0].SeenSeq
			})
			p, _ := h.Store.pinnedPaths()
			store, _ := parleyRuntime.ParticipantStore(p, b.RoomID, b.ParticipantName)
			before, _ := store.LoadMeta()
			if consumed {
				run("inbox", "--session", b.SessionID)
			}
			clock.Store(31)
			_, _ = io.WriteString(w, `{"event":"pane_agent_status_changed","data":{"type":"pane_agent_status_changed"}}`+"\n")
			if !consumed {
				await(func() bool {
					attempts, _ := h.Store.NotificationAttempts()
					return len(attempts) == 1 && attempts[0].Outcome == "accepted"
				})
			} else {
				await(func() bool {
					status, _ := h.Store.NotificationStatuses()
					return len(status) == 1 && status[0].ThroughSeq == 0
				})
			}
			cancel()
			_ = w.Close()
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("real adapter wait did not cancel")
			}
			f.mu.Lock()
			count := len(f.prompts)
			f.mu.Unlock()
			want := 1
			if consumed {
				want = 0
			}
			if count != want {
				t.Fatalf("prompts=%d want=%d", count, want)
			}
			after, _ := store.LoadMeta()
			if !consumed && after.LastSeenSeq != before.LastSeenSeq {
				t.Fatal("notification consumed real adapter inbox")
			}
		})
	}
}

func TestNotificationTransportRemoteOwnership(t *testing.T) {
	t.Parallel()
	h, b, _ := notificationFixture(t)
	notificationUnread(t, h, b)
	if err := h.EnrollNotifications(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	key, _ := SessionKey(h.Client.HerdrSocket)
	owner := SessionConfig{Role: "participant", ControllerID: "controller", MachineID: "remote", HerdrSocket: h.Client.HerdrSocket, SessionKey: key}
	host := HostSession{Store: h.Store, Config: owner, ConfigDir: filepath.Join(h.Client.RuntimeRoot, "config")}
	if err := privateDir(host.ConfigDir); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(host.ConfigDir, "controller.json"), owner); err != nil {
		t.Fatal(err)
	}
	client := &Client{LocalMachineID: "controller", Run: func(ctx context.Context, _ string, _ []string, in []byte) ([]byte, error) {
		var request RPCRequest
		if err := json.Unmarshal(in, &request); err != nil {
			return nil, err
		}
		value, err := host.Dispatch(ctx, request)
		if err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}}
	broker := &Broker{Store: NewStore(filepath.Join(h.Client.RuntimeRoot, "coordinator")), Client: client}
	peer := Peer{Machine: Machine{ID: "remote", Target: "fixture", Session: "agents"}, Config: owner, Binary: "/installed/parley"}
	data, err := broker.notificationCall(context.Background(), peer, RPCRequest{Operation: "notification-observe", BindingKey: HostBindingKey(b.RoomID, b.Target)})
	var observation NotificationObservation
	if err != nil || json.Unmarshal(data, &observation) != nil || observation.Binding.SessionID != b.SessionID || observation.ThroughSeq != 18 {
		t.Fatalf("remote observation: %s %v", data, err)
	}
	peer.Config.ControllerID = "other-controller"
	if _, err = broker.notificationCall(context.Background(), peer, RPCRequest{Operation: "notification-observe", BindingKey: HostBindingKey(b.RoomID, b.Target)}); err == nil {
		t.Fatal("foreign controller read notification state")
	}
}
