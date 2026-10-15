package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestHandoffDeliveryAndReplay(t *testing.T) {
	for _, scenario := range []string{"submitted", "blocked", "uncertain", "invalid-ack", "replaced", "claude", "gemini", "opencode", "skill-directory", "corrupt-receipt", "wrong-target", "wrong-type", "empty-ack"} {
		t.Run(scenario, func(t *testing.T) {
			mode, kind := scenario, "codex"
			if scenario == "claude" || scenario == "gemini" || scenario == "opencode" {
				mode, kind = "submitted", scenario
			}
			root, _ := os.MkdirTemp("/tmp", "hh-")
			t.Cleanup(func() { os.RemoveAll(root) })
			if err := os.MkdirAll(filepath.Join(root, "skills", "parley"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "skills", "parley", "SKILL.md"), []byte("Parley skill"), 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "skill-directory" {
				os.Remove(filepath.Join(root, "skills", "parley", "SKILL.md"))
				os.Mkdir(filepath.Join(root, "skills", "parley", "SKILL.md"), 0700)
			}
			socket := filepath.Join(root, "h")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { listener.Close() })
			var prompts atomic.Int32
			serveHandoffFixture(t, listener, mode, kind, &prompts)
			client := &Client{LocalMachineID: "local", HerdrSocket: socket, ParleyBinary: filepath.Join(root, "bin", "parley")}
			operations := HostOperations{Store: NewStore(filepath.Join(root, "state")), Client: client}
			binding := Binding{RoomID: "room-a", SessionID: "psn_here", ParticipantName: "agent-a", StateDir: root, Target: AgentTarget{MachineID: "local", HerdrSocket: socket, TerminalID: "term_a", Kind: kind}}
			if mode == "corrupt-receipt" {
				dir, _ := operations.Store.recordDir("handoffs")
				key := hashBytes([]byte(binding.RoomID + ":" + binding.SessionID + ":" + socket + ":term_a:" + kind))
				if err := writeJSON(filepath.Join(dir, key+".json"), HandoffResult{Status: "bogus"}); err != nil {
					t.Fatal(err)
				}
			}
			first := operations.Handoff(context.Background(), binding, false)
			second := operations.Handoff(context.Background(), binding, false)
			want := "submitted"
			if mode == "blocked" || mode == "replaced" || mode == "skill-directory" {
				want = "failed"
			}
			if mode == "uncertain" || mode == "invalid-ack" || mode == "corrupt-receipt" || mode == "wrong-target" || mode == "wrong-type" || mode == "empty-ack" {
				want = "uncertain"
			}
			if first.Status != want || second.Status != want {
				t.Fatal(first, second)
			}
			expected := int32(1)
			if mode == "replaced" || mode == "skill-directory" || mode == "corrupt-receipt" {
				expected = 0
			}
			if prompts.Load() != expected {
				t.Fatal("handoff repeated", prompts.Load())
			}
			if err := operations.saveBinding(binding); err != nil {
				t.Fatal(err)
			}
			statuses, err := operations.Store.HandoffStatuses()
			if err != nil || statuses[binding.SessionID].Status != want {
				t.Fatal(statuses, err)
			}
			if mode == "submitted" {
				var group sync.WaitGroup
				for range 4 {
					group.Add(1)
					go func() {
						defer group.Done()
						if result := operations.Handoff(context.Background(), binding, false); result.Status != "submitted" {
							t.Error(result)
						}
					}()
				}
				group.Wait()
				if prompts.Load() != 1 {
					t.Fatal("concurrent replay duplicated prompt")
				}
			}
			if mode == "blocked" {
				operations.Handoff(context.Background(), binding, true)
				if prompts.Load() != 2 {
					t.Fatal("explicit retry not delivered")
				}
			}
			if mode == "uncertain" {
				operations.Handoff(context.Background(), binding, true)
				if prompts.Load() != 1 {
					t.Fatal("uncertain delivery retried")
				}
			}
		})
	}
}

func TestCompletedFormReadsCurrentHandoffReceipt(t *testing.T) {
	operations, req, root := brokerFixture(t)
	binding := Binding{RoomID: req.RoomID, SessionID: "psn_existing", Target: req.Target, JoinPhase: "complete"}
	owner := SessionConfig{MachineID: "local", ControllerID: "controller"}
	if err := operations.Store.Enqueue(req); err != nil {
		t.Fatal(err)
	}
	outcome, err := operations.Store.ClaimRequest(req, owner.ControllerID)
	if err != nil {
		t.Fatal(err)
	}
	outcome.Phase = "complete"
	outcome.Response = Response{Status: "joined", Binding: &binding, Handoff: &HandoffResult{Status: "failed"}}
	if err := operations.Store.Complete(req.ID, outcome); err != nil {
		t.Fatal(err)
	}
	dir, _ := operations.Store.recordDir("handoffs")
	if err := writeJSON(filepath.Join(dir, handoffKey(binding)+".json"), HandoffResult{Status: "submitted"}); err != nil {
		t.Fatal(err)
	}
	host := HostSession{Store: operations.Store, ConfigDir: root}
	result, err := host.directForm(context.Background(), *operations, owner, req, Room{})
	if err != nil || result.Handoff == nil || result.Handoff.Status != "submitted" {
		t.Fatal(result, err)
	}
}

func serveHandoffFixture(t *testing.T, listener net.Listener, mode, kind string, prompts *atomic.Int32) {
	t.Helper()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				var frame struct {
					Method string
					Params map[string]any
				}
				if json.NewDecoder(conn).Decode(&frame) != nil {
					return
				}
				reply := `{"result":{}}`
				switch frame.Method {
				case "agent.list":
					reply = `{"result":{"agents":[{"terminal_id":"term_a","pane_id":"w2:p9"}]}}`
				case "agent.get":
					reply = fmt.Sprintf(`{"result":{"agent":{"terminal_id":"term_a","pane_id":"w2:p9","workspace_id":"w2","agent":%q,"agent_status":"working"}}}`, kind)
					if mode == "replaced" {
						reply = `{"result":{"agent":{"terminal_id":"different","pane_id":"w2:p9","agent":"codex"}}}`
					}
				case "agent.prompt":
					reply = fmt.Sprintf(`{"result":{"type":"agent_prompted","agent":{"terminal_id":"term_a","agent":%q}}}`, kind)
					prompts.Add(1)
					text, _ := frame.Params["text"].(string)
					if frame.Params["target"] != "w2:p9" || !strings.Contains(text, "room-a") || !strings.Contains(text, "psn_here") || !strings.Contains(text, "skills/parley/SKILL.md") || !strings.Contains(text, "PARLEY_STATE_DIR") || !strings.Contains(text, "PARLEY_BIN") {
						t.Errorf("wrong handoff %+v", frame.Params)
					}
					if strings.Contains(text, "presence message") || strings.Contains(text, "every participant command") || strings.Contains(text, "Do not start or join") {
						t.Error("handoff repeats skill instructions or requests automatic chatter")
					}
					if frame.Params["wait"] != nil {
						t.Error("waits for agent completion")
					}
					if mode == "blocked" {
						reply = `{"error":{"code":"agent_blocked","message":"approval dialog"}}`
					}
					if mode == "uncertain" {
						return
					}
					if mode == "wrong-target" {
						reply = `{"result":{"type":"agent_prompted","agent":{"terminal_id":"another","agent":"codex"}}}`
					}
					if mode == "wrong-type" {
						reply = `{"result":{"type":"agent_get","agent":{"terminal_id":"term_a","agent":"codex"}}}`
					}
					if mode == "empty-ack" {
						reply = `{"result":{}}`
					}
					if mode == "invalid-ack" {
						reply = `{"result":null}`
					}
				}
				fmt.Fprintln(conn, reply)
			}()
		}
	}()
}
