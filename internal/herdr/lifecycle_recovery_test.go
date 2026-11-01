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

func TestPeerFailureNotification(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "pf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	socket := filepath.Join(root, "h")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	bodies := make(chan string, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				var request struct {
					Method string
					Params struct{ Title, Body string }
				}
				defer conn.Close()
				if json.NewDecoder(conn).Decode(&request) != nil {
					return
				}
				if request.Method == "events.subscribe" {
					io.WriteString(conn, testEventAck)
					io.Copy(io.Discard, conn)
					return
				}
				if request.Method == "notification.show" {
					bodies <- request.Params.Body
				}
				io.WriteString(conn, `{"result":{"shown":true}}`+"\n")
			}()
		}
	}()
	client := &Client{HerdrSocket: socket}
	failures := map[string]string{}
	machine := Machine{ID: "peer", Label: "Work", Target: "work-alias", Session: "default"}
	report := func(cause error) {
		t.Helper()
		reportPeerFailure(context.Background(), client, failures, machine, cause)
	}
	report(fmt.Errorf("Permission denied (publickey)."))
	select {
	case body := <-bodies:
		for _, want := range []string{"Work", "work-alias", "default", "Permission denied (publickey).", "rooms"} {
			if !strings.Contains(body, want) {
				t.Fatal(body)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("peer failure hidden from Herdr")
	}
	report(fmt.Errorf("Permission denied (publickey)."))
	if len(bodies) != 0 {
		t.Fatal("duplicate notification")
	}
	report(nil)
	report(fmt.Errorf("Permission denied (publickey)."))
	if len(bodies) != 1 {
		t.Fatal("recovery did not reset notification")
	}
	<-bodies
	client.HerdrBinary, client.RuntimeRoot = "herdr", filepath.Join(root, "runtime")
	client.Run = func(_ context.Context, _ string, args []string, _ []byte) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "machine list --json":
			return []byte(`[{"id":"peer","label":"Work","target":"work-alias","session":"default","enabled":true}]`), nil
		case "plugin list --json":
			return []byte(`{"result":{"plugins":[{"plugin_id":"parley","enabled":true,"plugin_root":"/opt/parley"}]}}`), nil
		default:
			return nil, fmt.Errorf("Permission denied (publickey).")
		}
	}
	host := HostSession{Config: SessionConfig{HerdrSocket: socket}, ConfigDir: filepath.Join(root, "config"), Store: NewStore(filepath.Join(root, "state"))}
	host.Config.SessionKey, _ = SessionKey(socket)
	if _, err := host.Setup(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	path, err := host.CoordinatorSocket()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Dir(path))
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
		case <-time.After(3 * time.Second):
			t.Error("coordinator did not stop")
		}
	}()
	select {
	case body := <-bodies:
		if !strings.Contains(body, "Permission denied (publickey).") {
			t.Fatal(body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("coordinator hid activation error")
	}
}

func TestStreamFailureNotificationRetriesUntilShown(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "sf-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	listener, err := net.Listen("unix", filepath.Join(root, "h"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	attempts := make(chan string, 4)
	go func() {
		count := 0
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var request struct {
				Method string
				Params struct{ Body string }
			}
			if json.NewDecoder(conn).Decode(&request) == nil && request.Method == "notification.show" {
				count++
				attempts <- request.Params.Body
				if count != 2 {
					io.WriteString(conn, `{"result":{"shown":false,"reason":"busy"}}`+"\n")
				} else {
					io.WriteString(conn, `{"result":{"shown":true}}`+"\n")
				}
			}
			conn.Close()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		reportStreamFailure(ctx, &Client{HerdrSocket: filepath.Join(root, "h")}, Machine{ID: "peer", Target: "work-alias", Session: "default"}, fmt.Errorf("stream rejected"))
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("retry survived cancellation")
		}
	}()
	for i := 0; i < 2; i++ {
		select {
		case body := <-attempts:
			if !strings.Contains(body, "stream rejected") {
				t.Fatal(body)
			}
		case <-time.After(4 * time.Second):
			t.Fatal("undelivered stream failure not retried")
		}
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retry continued after successful delivery")
	}
	// Removal or profile replacement cancels the subscription's pending retry.
	canceled, stop := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		reportStreamFailure(canceled, &Client{HerdrSocket: filepath.Join(root, "h")}, Machine{ID: "peer"}, fmt.Errorf("stream rejected"))
		close(stopped)
	}()
	select {
	case <-attempts:
	case <-time.After(time.Second):
		stop()
		t.Fatal("notification attempt did not start")
	}
	stop()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("retry ignored cancellation")
	}
}
