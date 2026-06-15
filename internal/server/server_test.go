package server_test

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/server"
)

func TestServerJoinSendHistory(t *testing.T) {
	dir := t.TempDir()
	log := eventlog.New(dir + "/events.jsonl")
	srv, err := server.New("127.0.0.1:0", server.Config{
		RoomID: "room-1",
		Topic:  "test topic",
		Log:    log,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })

	conn := dialAndJoin(t, srv.Addr(), "room-1", "alice")
	defer conn.Close()

	sendReq(t, conn, protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: "hello"}})
	resp := readResp(t, conn)
	if !resp.OK || resp.Event == nil || resp.Event.Type != model.EventMessage {
		t.Fatalf("send response = %#v", resp)
	}

	sendReq(t, conn, protocol.Request{Type: protocol.RequestHistory, History: &protocol.HistoryRequest{Limit: 10}})
	resp = readResp(t, conn)
	if !resp.OK || len(resp.Events) == 0 {
		t.Fatalf("history response = %#v", resp)
	}
}

func TestServerRejectsWrongRoomID(t *testing.T) {
	log := eventlog.New(t.TempDir() + "/events.jsonl")
	srv, err := server.New("127.0.0.1:0", server.Config{RoomID: "room-1", Topic: "test", Log: log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go srv.Serve()
	t.Cleanup(func() { _ = srv.Close() })

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	sendReq(t, conn, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "wrong", Name: "alice", Role: "participant"}})
	resp := readResp(t, conn)
	if resp.OK || resp.Error == nil || resp.Error.Code != "room_mismatch" {
		t.Fatalf("response = %#v", resp)
	}
}

func dialAndJoin(t *testing.T, addr, roomID, name string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	sendReq(t, conn, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: roomID, Name: name, Role: "participant"}})
	resp := readResp(t, conn)
	if !resp.OK {
		t.Fatalf("join response = %#v", resp)
	}
	return conn
}

func sendReq(t *testing.T, conn net.Conn, req protocol.Request) {
	t.Helper()
	data, err := protocol.EncodeLine(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func readResp(t *testing.T, conn net.Conn) protocol.Response {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp protocol.Response
	if err := protocol.DecodeLine(line, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}
