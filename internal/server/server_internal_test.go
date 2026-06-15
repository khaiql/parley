package server

import (
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/protocol"
)

func TestServerHandleJoinRegistersConnectionBeforeResponseWrite(t *testing.T) {
	srv := newInternalTestServer(t)
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	resp := srv.handleJoin(serverConn, protocol.JoinRequest{RoomID: "room-1", Name: "alice", Role: "participant"})
	if !resp.OK {
		t.Fatalf("handleJoin response = %#v", resp)
	}

	srv.mu.Lock()
	cc := srv.conns["alice"]
	srv.mu.Unlock()
	if cc == nil {
		t.Fatal("join did not register connection before response write")
	}
	if cc.conn != serverConn {
		t.Fatalf("registered conn = %#v, want join conn", cc.conn)
	}
	if cc.writeMu.TryLock() {
		cc.writeMu.Unlock()
		t.Fatal("join connection write lock was not held for ordered response write")
	}
	cc.writeMu.Unlock()
}

func TestWriteResponseSetsFiniteWriteDeadline(t *testing.T) {
	conn := &recordingConn{}
	if err := writeResponse(conn, protocol.Response{OK: true}); err != nil {
		t.Fatalf("writeResponse: %v", err)
	}
	if len(conn.writeDeadlines) < 2 {
		t.Fatalf("write deadlines = %#v, want finite deadline and clear", conn.writeDeadlines)
	}
	if conn.writeDeadlines[0].IsZero() {
		t.Fatalf("first write deadline is zero, want finite deadline")
	}
	if !conn.writeDeadlines[len(conn.writeDeadlines)-1].IsZero() {
		t.Fatalf("last write deadline = %v, want cleared zero deadline", conn.writeDeadlines[len(conn.writeDeadlines)-1])
	}
}

func TestServerBroadcastRemovesClientOnWriteError(t *testing.T) {
	srv := newInternalTestServer(t)
	conn := &recordingConn{writeErr: errors.New("write failed")}
	cc := &clientConn{name: "bob", conn: conn}

	srv.mu.Lock()
	srv.participants["bob"] = model.Participant{Name: "bob", Role: "participant", Online: true}
	srv.conns["bob"] = cc
	srv.mu.Unlock()

	srv.broadcastEventExcept("alice", model.Event{Seq: 1, Type: model.EventMessage, RoomID: "room-1", Actor: "alice"})

	srv.mu.Lock()
	_, stillConnected := srv.conns["bob"]
	participant := srv.participants["bob"]
	srv.mu.Unlock()

	if stillConnected {
		t.Fatal("client with write error stayed registered")
	}
	if participant.Online {
		t.Fatal("client with write error stayed online")
	}
	if !conn.closed {
		t.Fatal("client with write error was not closed")
	}
}

func TestServerRejectsAcceptedConnAfterCloseBegins(t *testing.T) {
	srv := newInternalTestServer(t)
	conn := &recordingConn{}

	srv.beginClose()
	if srv.trackAcceptedConn(conn) {
		t.Fatal("accepted connection was tracked after close began")
	}
	if !conn.closed {
		t.Fatal("accepted connection after close began was not closed")
	}
}

func newInternalTestServer(t *testing.T) *Server {
	t.Helper()
	srv, err := New("127.0.0.1:0", Config{
		RoomID: "room-1",
		Topic:  "test topic",
		Log:    eventlog.New(filepath.Join(t.TempDir(), "events.jsonl")),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv
}

type recordingConn struct {
	writeErr       error
	writeDeadlines []time.Time
	closed         bool
}

func (c *recordingConn) Read([]byte) (int, error) {
	return 0, errors.New("read not implemented")
}

func (c *recordingConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return len(p), nil
}

func (c *recordingConn) Close() error {
	c.closed = true
	return nil
}

func (c *recordingConn) LocalAddr() net.Addr {
	return dummyAddr("local")
}

func (c *recordingConn) RemoteAddr() net.Addr {
	return dummyAddr("remote")
}

func (c *recordingConn) SetDeadline(time.Time) error {
	return nil
}

func (c *recordingConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *recordingConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadlines = append(c.writeDeadlines, t)
	return nil
}

type dummyAddr string

func (a dummyAddr) Network() string {
	return string(a)
}

func (a dummyAddr) String() string {
	return string(a)
}
