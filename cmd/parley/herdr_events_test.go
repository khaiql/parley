package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/herdr"
)

type eventHelperFixture struct {
	root, socket, inventory, fail string
	peer                          herdr.Peer
	acks                          atomic.Int32
	relay                         chan net.Conn
	listener                      net.Listener
	handshake                     []byte
}

func eventFixture(t *testing.T) *eventHelperFixture {
	t.Helper()
	root, e := os.MkdirTemp("/tmp", "parley-event-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	root, _ = filepath.EvalSymlinks(root)
	f := &eventHelperFixture{root: root, socket: filepath.Join(root, "h.sock"), inventory: filepath.Join(root, "inventory"), fail: filepath.Join(root, "fail"), relay: make(chan net.Conn, 4)}
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	t.Setenv("PARLEY_RPC_REJECTION_CHILD", "1")
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	write := func(name, body string) {
		if e := os.WriteFile(filepath.Join(root, name), []byte(body), 0700); e != nil {
			t.Fatal(e)
		}
	}
	write("ssh", "#!/bin/sh\nprintf x >> '"+root+"/calls'\nexec '"+os.Args[0]+"' -test.run=^TestHerdrRPCRejectedHelperChild$\n")
	registry := `{"result":{"plugins":[{"plugin_id":"parley","enabled":true,"plugin_root":"` + root + `"}]}}`
	write("herdr", "#!/bin/sh\nprintf x > '"+root+"/attempted'\nif [ -f '"+f.fail+"' ]; then printf 'temporary process outage\\n' >&2; exit 255; fi\ncase \"$3 $4\" in\n'session list') cat '"+f.inventory+"';;\n'plugin list') printf '%s\\n' '"+registry+"';;\n'plugin config-dir') printf '%s\\n' '"+root+"/config';;\nesac\n")
	f.running(t)
	key, _ := herdr.SessionKey(f.socket)
	config := herdr.SessionConfig{Role: "participant", ControllerID: "local", MachineID: "peer", HerdrSocket: f.socket, SessionKey: key}
	dir := filepath.Join(root, "config", "sessions", key)
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	data, _ := json.Marshal(config)
	if e = os.WriteFile(filepath.Join(dir, "controller.json"), data, 0600); e != nil {
		t.Fatal(e)
	}
	f.peer = herdr.Peer{Machine: herdr.Machine{ID: "peer", Target: "host", Session: "agents"}, Binary: "/opt/parley", Config: config}
	return f
}
func (f *eventHelperFixture) running(t *testing.T) {
	t.Helper()
	data := `{"sessions":[{"name":"agents","running":true,"socket_path":"` + f.socket + `"}]}`
	if e := os.WriteFile(f.inventory, []byte(data), 0600); e != nil {
		t.Fatal(e)
	}
}
func (f *eventHelperFixture) start(t *testing.T, capabilityError bool) {
	t.Helper()
	listener, e := net.Listen("unix", f.socket)
	if e != nil {
		t.Fatal(e)
	}
	f.listener = listener
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			go func() {
				var request struct {
					ID string `json:"id"`
				}
				if e := json.NewDecoder(conn).Decode(&request); e != nil {
					conn.Close()
					return
				}
				if request.ID == "parley" && f.handshake != nil {
					conn.Write(append(append([]byte(nil), f.handshake...), '\n'))
					conn.Close()
					return
				}
				if capabilityError {
					io.WriteString(conn, `{"id":"parley","error":{"code":"invalid_request","message":"unsupported subscription"}}`+"\n")
					conn.Close()
					return
				}
				if request.ID == "parley" {
					io.WriteString(conn, `{"id":"parley","result":{"type":"subscription_started"}}`+"\n")
					conn.Close()
					return
				}
				f.acks.Add(1)
				io.WriteString(conn, `{"id":"parley-requests","result":{"type":"subscription_started"}}`+"\n")
				f.relay <- conn
			}()
		}
	}()
}
func TestHerdrRPCEventRestart(t *testing.T) {
	f := eventFixture(t)
	f.start(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	reads := 0
	c := herdr.Client{}
	e := c.WatchRequests(ctx, f.peer, func(context.Context, herdr.Peer) error {
		reads++
		if f.acks.Load() != int32(reads) {
			t.Fatal("reconcile before fresh ack", reads, f.acks.Load())
		}
		conn := <-f.relay
		if reads == 1 {
			conn.Close()
			f.listener.Close()
			f.start(t, false)
			time.Sleep(100 * time.Millisecond)
			return context.Canceled
		} else {
			conn.Close()
			cancel()
		}
		return nil
	})
	if e != context.Canceled || reads != 2 {
		t.Fatal(e, reads)
	}
}
func TestHerdrRPCEventTemporaryStartup(t *testing.T) {
	for _, kind := range []string{"session", "socket", "process"} {
		t.Run(kind, func(t *testing.T) {
			f := eventFixture(t)
			switch kind {
			case "session":
				f.start(t, false)
				os.WriteFile(f.inventory, []byte(`{"sessions":[]}`), 0600)
			case "socket":
			case "process":
				f.start(t, false)
				os.WriteFile(f.fail, []byte("x"), 0600)
			}
			restored := make(chan struct{})
			defer func() { <-restored }()
			go func() {
				deadline := time.Now().Add(3 * time.Second)
				for {
					if _, e := os.Stat(filepath.Join(f.root, "attempted")); e == nil {
						break
					}
					if time.Now().After(deadline) {
						close(restored)
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				time.Sleep(200 * time.Millisecond)
				switch kind {
				case "session":
					f.running(t)
				case "socket":
					f.start(t, false)
				case "process":
					os.Remove(f.fail)
				}
				close(restored)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			reads := 0
			c := herdr.Client{}
			e := c.WatchRequests(ctx, f.peer, func(context.Context, herdr.Peer) error {
				reads++
				select {
				case <-restored:
				default:
					t.Fatal("reconciled during outage")
				}
				if f.acks.Load() != 1 {
					t.Fatal("fresh ack missing")
				}
				(<-f.relay).Close()
				cancel()
				return nil
			})
			if e != context.Canceled || reads != 1 {
				t.Fatal(e, reads)
			}
			calls, _ := os.ReadFile(filepath.Join(f.root, "calls"))
			if len(calls) < 2 {
				t.Fatal("did not retry", string(calls))
			}
		})
	}
}
func TestHerdrRPCEventStartupRejection(t *testing.T) {
	for _, kind := range []string{"capability", "controller"} {
		t.Run(kind, func(t *testing.T) {
			f := eventFixture(t)
			if kind == "controller" {
				f.peer.Config.ControllerID = "different"
			}
			f.start(t, kind == "capability")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			reads := 0
			c := herdr.Client{}
			e := c.WatchRequests(ctx, f.peer, func(context.Context, herdr.Peer) error { reads++; return nil })
			calls, _ := os.ReadFile(filepath.Join(f.root, "calls"))
			if e == nil || reads != 0 || string(calls) != "x" || !strings.Contains(e.Error(), map[string]string{"capability": "unsupported subscription", "controller": "controller_conflict"}[kind]) {
				t.Fatal(e, reads, string(calls))
			}
		})
	}
}

func TestHerdrRPCEventMalformedHandshake(t *testing.T) {
	for _, kind := range []string{"JSON", "UTF8", "frame-limit"} {
		t.Run(kind, func(t *testing.T) {
			f := eventFixture(t)
			diagnostic := ""
			switch kind {
			case "JSON":
				f.handshake = []byte(`{bad}`)
				diagnostic = "invalid character"
			case "UTF8":
				f.handshake = []byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}
				diagnostic = "invalid UTF-8"
			case "frame-limit":
				f.handshake = []byte(strings.Repeat("x", 1024*1024+1))
				diagnostic = "token too long"
			}
			f.start(t, false)
			ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
			defer cancel()
			reads := 0
			c := herdr.Client{}
			e := c.WatchRequests(ctx, f.peer, func(context.Context, herdr.Peer) error { reads++; return nil })
			calls, _ := os.ReadFile(filepath.Join(f.root, "calls"))
			if e == nil || !strings.Contains(e.Error(), diagnostic) || !strings.Contains(e.Error(), "helper_startup_failed") || reads != 0 || f.acks.Load() != 0 || string(calls) != "x" {
				t.Fatal(e, reads, f.acks.Load(), string(calls))
			}
		})
	}
}
