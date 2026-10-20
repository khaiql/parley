package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoordinateCleanupFailureKeepsLocalSubscription(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "hc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	socket := filepath.Join(root, "native.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	events := make(chan net.Conn, 1)
	closed := make(chan struct{})
	snapshots := make(chan struct{}, 8)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var request struct {
					Method string
					Params struct {
						Subscriptions []struct{ Type string }
					}
				}
				if json.NewDecoder(conn).Decode(&request) != nil {
					return
				}
				switch request.Method {
				case "events.subscribe":
					io.WriteString(conn, testEventAck)
					for _, subscription := range request.Params.Subscriptions {
						if subscription.Type == "pane.updated" {
							io.Copy(io.Discard, conn)
							return
						}
					}
					events <- conn
					io.Copy(io.Discard, conn)
					close(closed)
				case "session.snapshot":
					io.WriteString(conn, `{"result":{"type":"session_snapshot","snapshot":{"panes":[]}}}`+"\n")
					snapshots <- struct{}{}
				}
			}()
		}
	}()
	host := HostSession{Config: SessionConfig{HerdrSocket: socket}, ConfigDir: filepath.Join(root, "config"), Store: NewStore(filepath.Join(root, "state"))}
	host.Config.SessionKey, _ = SessionKey(socket)
	client := &Client{HerdrBinary: "herdr", RuntimeRoot: filepath.Join(root, "runtime"), Run: func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "machine list --json":
			return []byte("[]"), nil
		case "plugin list --json":
			return []byte(`{"result":{"plugins":[{"plugin_id":"parley","enabled":true,"plugin_root":"/opt/parley"}]}}`), nil
		default:
			return nil, fmt.Errorf("unexpected command: %v", args)
		}
	}}
	if _, err = host.Setup(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	path, err := host.CoordinatorSocket()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(path))
	target := AgentTarget{MachineID: "self", HerdrSocket: socket, TerminalID: "term_gone", Kind: "codex"}
	if err = host.Store.saveBinding(Binding{RoomID: "room-a", Target: target, JoinPhase: "uncertain", JoinRequestID: "pending"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Coordinate(ctx, client) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("coordinator did not stop")
		}
	}()
	var stream net.Conn
	select {
	case stream = <-events:
	case <-time.After(time.Second):
		t.Fatal("missing subscription")
	}
	select {
	case <-snapshots:
	case <-time.After(time.Second):
		t.Fatal("missing initial cleanup")
	}
	io.WriteString(stream, `{"event":"pane_closed","data":{"type":"pane_closed"}}`+"\n")
	select {
	case <-snapshots:
	case <-closed:
		t.Fatal("cleanup failure terminated local subscription")
	case <-time.After(time.Second):
		t.Fatal("next event did not retry cleanup")
	}
}
