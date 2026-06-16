package server_test

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/server"
)

type testServer struct {
	srv     *server.Server
	logPath string
}

type testClient struct {
	conn   net.Conn
	reader *bufio.Reader
}

func TestServerJoinSendHistory(t *testing.T) {
	ts := newTestServer(t)
	conn := dialAndJoin(t, ts.srv.Addr(), "room-1", "alice")
	defer conn.Close()

	conn.Send(t, protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: "hello"}})
	resp := conn.Read(t)
	if !resp.OK || resp.Event == nil || resp.Event.Type != model.EventMessage {
		t.Fatalf("send response = %#v", resp)
	}

	conn.Send(t, protocol.Request{Type: protocol.RequestHistory, History: &protocol.HistoryRequest{Limit: 10}})
	resp = conn.Read(t)
	if !resp.OK || len(resp.Events) == 0 {
		t.Fatalf("history response = %#v", resp)
	}
}

func TestServerRejectsWrongRoomID(t *testing.T) {
	ts := newTestServer(t)
	conn := dialClient(t, ts.srv.Addr())
	defer conn.Close()

	conn.Send(t, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "wrong", Name: "alice", Role: "participant"}})
	assertErrorCode(t, conn.Read(t), "room_mismatch")
}

func TestServerRejectsHistoryBeforeJoin(t *testing.T) {
	ts := newTestServer(t)
	conn := dialClient(t, ts.srv.Addr())
	defer conn.Close()

	conn.Send(t, protocol.Request{Type: protocol.RequestHistory, History: &protocol.HistoryRequest{Limit: 10}})
	assertErrorCode(t, conn.Read(t), "not_joined")
}

func TestServerMalformedRequestsBeforeJoinReturnErrors(t *testing.T) {
	tests := []struct {
		name string
		req  protocol.Request
		code string
	}{
		{
			name: "send nil payload",
			req:  protocol.Request{Type: protocol.RequestSend},
			code: "bad_request",
		},
		{
			name: "history nil payload",
			req:  protocol.Request{Type: protocol.RequestHistory},
			code: "bad_request",
		},
		{
			name: "send before join",
			req:  protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: "hello"}},
			code: "not_joined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			conn := dialClient(t, ts.srv.Addr())
			defer conn.Close()

			conn.Send(t, tt.req)
			assertErrorCode(t, conn.Read(t), tt.code)
		})
	}
}

func TestServerMalformedRequestsAfterJoinReturnErrors(t *testing.T) {
	tests := []struct {
		name string
		req  protocol.Request
		code string
	}{
		{
			name: "send nil payload",
			req:  protocol.Request{Type: protocol.RequestSend},
			code: "bad_request",
		},
		{
			name: "history nil payload",
			req:  protocol.Request{Type: protocol.RequestHistory},
			code: "bad_request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			conn := dialAndJoin(t, ts.srv.Addr(), "room-1", "alice")
			defer conn.Close()

			conn.Send(t, tt.req)
			assertErrorCode(t, conn.Read(t), tt.code)
		})
	}
}

func TestServerJoinResponseDoesNotDuplicateEventSeq(t *testing.T) {
	ts := newTestServer(t)
	conn := dialClient(t, ts.srv.Addr())
	defer conn.Close()

	conn.Send(t, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "room-1", Name: "alice", Role: "participant"}})
	resp := conn.Read(t)
	if !resp.OK || resp.Event == nil {
		t.Fatalf("join response = %#v", resp)
	}
	for _, ev := range resp.Events {
		if ev.Seq == resp.Event.Seq {
			t.Fatalf("join response duplicated seq %d in Events and Event: %#v", ev.Seq, resp)
		}
	}
}

func TestServerJoinSnapshotIsBoundedToLatestTranscriptEvents(t *testing.T) {
	ts := newTestServer(t)
	alice := dialAndJoin(t, ts.srv.Addr(), "room-1", "alice")
	defer alice.Close()

	for i := 1; i <= 55; i++ {
		alice.Send(t, protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: "message-" + twoDigit(i)}})
		resp := alice.Read(t)
		if !resp.OK {
			t.Fatalf("send %d response = %#v", i, resp)
		}
	}

	bob := dialClient(t, ts.srv.Addr())
	defer bob.Close()
	bob.Send(t, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "room-1", Name: "bob", Role: "participant"}})
	resp := bob.Read(t)
	if !resp.OK {
		t.Fatalf("bob join response = %#v", resp)
	}
	if len(resp.Events) > 50 {
		t.Fatalf("join response returned %d events, want at most 50", len(resp.Events))
	}
	if len(resp.Events) == 0 || eventText(t, resp.Events[len(resp.Events)-1]) != "message-55" {
		t.Fatalf("join response latest event = %#v, want message-55", resp.Events)
	}
	if resp.Event != nil {
		for _, ev := range resp.Events {
			if ev.Seq == resp.Event.Seq {
				t.Fatalf("join response duplicated current join event seq %d", ev.Seq)
			}
		}
	}
}

func TestServerHistoryLimitReturnsLatestEvents(t *testing.T) {
	ts := newTestServer(t)
	conn := dialAndJoin(t, ts.srv.Addr(), "room-1", "alice")
	defer conn.Close()

	for _, text := range []string{"one", "two", "three"} {
		conn.Send(t, protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: text}})
		if resp := conn.Read(t); !resp.OK {
			t.Fatalf("send response = %#v", resp)
		}
	}

	conn.Send(t, protocol.Request{Type: protocol.RequestHistory, History: &protocol.HistoryRequest{Limit: 1}})
	resp := conn.Read(t)
	if !resp.OK || len(resp.Events) != 1 {
		t.Fatalf("history response = %#v", resp)
	}
	if got := eventText(t, resp.Events[0]); got != "three" {
		t.Fatalf("history latest text = %q, want three", got)
	}
}

func TestServerRejectsDuplicateOnlineName(t *testing.T) {
	ts := newTestServer(t)
	alice := dialAndJoin(t, ts.srv.Addr(), "room-1", "alice")
	defer alice.Close()

	duplicate := dialClient(t, ts.srv.Addr())
	defer duplicate.Close()
	duplicate.Send(t, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "room-1", Name: "alice", Role: "participant"}})
	assertErrorCode(t, duplicate.Read(t), "name_taken")
}

func TestServerNormalizesParticipantNamesForDuplicates(t *testing.T) {
	ts := newTestServer(t)
	alice := dialClient(t, ts.srv.Addr())
	defer alice.Close()
	alice.Send(t, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "room-1", Name: " alice ", Role: "participant"}})
	resp := alice.Read(t)
	if !resp.OK || resp.Event == nil || resp.Event.Actor != "alice" {
		t.Fatalf("trimmed join response = %#v", resp)
	}
	if len(resp.Participants) != 1 || resp.Participants[0].Name != "alice" {
		t.Fatalf("participants = %#v, want trimmed alice", resp.Participants)
	}

	duplicate := dialClient(t, ts.srv.Addr())
	defer duplicate.Close()
	duplicate.Send(t, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "room-1", Name: "alice", Role: "participant"}})
	assertErrorCode(t, duplicate.Read(t), "name_taken")
}

func TestServerRejectsInvalidParticipantName(t *testing.T) {
	ts := newTestServer(t)
	conn := dialClient(t, ts.srv.Addr())
	defer conn.Close()

	conn.Send(t, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "room-1", Name: "bad name", Role: "participant"}})
	assertErrorCode(t, conn.Read(t), "bad_request")
}

func TestServerRejectsCaseInsensitiveDuplicateOnlineName(t *testing.T) {
	ts := newTestServer(t)
	bob := dialAndJoin(t, ts.srv.Addr(), "room-1", "Bob")
	defer bob.Close()

	duplicate := dialClient(t, ts.srv.Addr())
	defer duplicate.Close()
	duplicate.Send(t, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "room-1", Name: "bob", Role: "participant"}})
	assertErrorCode(t, duplicate.Read(t), "name_taken")
}

func TestServerBroadcastsSendToOtherClientsAndRespondsToSender(t *testing.T) {
	ts := newTestServer(t)
	alice := dialAndJoin(t, ts.srv.Addr(), "room-1", "alice")
	defer alice.Close()
	bob := dialAndJoin(t, ts.srv.Addr(), "room-1", "bob")
	defer bob.Close()
	readEvent(t, alice, model.EventParticipantJoined, "bob")

	alice.Send(t, protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: "hello bob"}})
	aliceResp := alice.Read(t)
	if !aliceResp.OK || aliceResp.Event == nil || aliceResp.Event.Type != model.EventMessage || aliceResp.Event.Actor != "alice" {
		t.Fatalf("alice command response = %#v", aliceResp)
	}
	if got := eventText(t, *aliceResp.Event); got != "hello bob" {
		t.Fatalf("alice command response text = %q, want hello bob", got)
	}

	bobPush := readEvent(t, bob, model.EventMessage, "alice")
	if got := eventText(t, *bobPush.Event); got != "hello bob" {
		t.Fatalf("bob pushed text = %q, want hello bob", got)
	}
}

func TestServerParsesMentionsForOnlineParticipants(t *testing.T) {
	ts := newTestServer(t)
	alice := dialAndJoin(t, ts.srv.Addr(), "room-1", "alice")
	defer alice.Close()
	bob := dialAndJoin(t, ts.srv.Addr(), "room-1", "bob")
	defer bob.Close()
	readEvent(t, alice, model.EventParticipantJoined, "bob")

	alice.Send(t, protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: "@bob @missing @bob please check"}})
	resp := alice.Read(t)
	if !resp.OK || resp.Event == nil {
		t.Fatalf("send response = %#v", resp)
	}
	if got := eventMentions(t, *resp.Event); !reflect.DeepEqual(got, []string{"bob"}) {
		t.Fatalf("mentions = %#v, want [bob]", got)
	}
}

func TestServerDisconnectEmitsParticipantLeftToOtherClients(t *testing.T) {
	ts := newTestServer(t)
	alice := dialAndJoin(t, ts.srv.Addr(), "room-1", "alice")
	defer alice.Close()
	bob := dialAndJoin(t, ts.srv.Addr(), "room-1", "bob")
	readEvent(t, alice, model.EventParticipantJoined, "bob")

	bob.Close()
	readEvent(t, alice, model.EventParticipantLeft, "bob")
}

func TestServerCloseReturnsWithIdlePreJoinConnection(t *testing.T) {
	ts := newTestServer(t)
	conn := dialClient(t, ts.srv.Addr())
	defer conn.Close()
	if _, err := conn.conn.Write([]byte("{")); err != nil {
		t.Fatalf("write partial pre-join request: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		done <- ts.srv.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(300 * time.Millisecond):
		conn.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Close after unblocking conn: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Close did not return even after idle connection was closed")
		}
		t.Fatal("Close did not return while idle pre-join connection was open")
	}
}

func TestServerScannerErrorReturnsBadRequest(t *testing.T) {
	ts := newTestServer(t)
	conn := dialClient(t, ts.srv.Addr())
	defer conn.Close()

	tooLarge := bytes.Repeat([]byte("x"), 1024*1024+1)
	tooLarge = append(tooLarge, '\n')
	if _, err := conn.conn.Write(tooLarge); err != nil {
		t.Fatalf("write oversized line: %v", err)
	}

	assertErrorCode(t, conn.Read(t), "bad_request")
}

func TestServerLeaveAppendFailureKeepsParticipantOnline(t *testing.T) {
	ts := newTestServer(t)
	alice := dialAndJoin(t, ts.srv.Addr(), "room-1", "alice")
	defer alice.Close()

	originalLog, err := os.ReadFile(ts.logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if err := os.Remove(ts.logPath); err != nil {
		t.Fatalf("remove log: %v", err)
	}
	if err := os.Mkdir(ts.logPath, 0o700); err != nil {
		t.Fatalf("mkdir log path: %v", err)
	}

	alice.Send(t, protocol.Request{Type: protocol.RequestLeave, Leave: &protocol.LeaveRequest{}})
	assertErrorCode(t, alice.Read(t), "log_append_failed")

	if err := os.Remove(ts.logPath); err != nil {
		t.Fatalf("remove log directory: %v", err)
	}
	if err := os.WriteFile(ts.logPath, originalLog, 0o600); err != nil {
		t.Fatalf("restore log: %v", err)
	}

	duplicate := dialClient(t, ts.srv.Addr())
	defer duplicate.Close()
	duplicate.Send(t, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: "room-1", Name: "alice", Role: "participant"}})
	assertErrorCode(t, duplicate.Read(t), "name_taken")
}

func newTestServer(t *testing.T) testServer {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	log := eventlog.New(logPath)
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
	return testServer{srv: srv, logPath: logPath}
}

func dialAndJoin(t *testing.T, addr, roomID, name string) *testClient {
	t.Helper()
	conn := dialClient(t, addr)
	conn.Send(t, protocol.Request{Type: protocol.RequestJoin, Join: &protocol.JoinRequest{RoomID: roomID, Name: name, Role: "participant"}})
	resp := conn.Read(t)
	if !resp.OK {
		t.Fatalf("join response = %#v", resp)
	}
	return conn
}

func dialClient(t *testing.T, addr string) *testClient {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return &testClient{conn: conn, reader: bufio.NewReader(conn)}
}

func (c *testClient) Close() {
	_ = c.conn.Close()
}

func (c *testClient) Send(t *testing.T, req protocol.Request) {
	t.Helper()
	data, err := protocol.EncodeLine(req)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := c.conn.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func (c *testClient) Read(t *testing.T) protocol.Response {
	t.Helper()
	return c.ReadWithin(t, 2*time.Second)
}

func (c *testClient) ReadWithin(t *testing.T, timeout time.Duration) protocol.Response {
	t.Helper()
	_ = c.conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := c.reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	_ = c.conn.SetReadDeadline(time.Time{})
	var resp protocol.Response
	if err := protocol.DecodeLine(line, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func readEvent(t *testing.T, conn *testClient, typ model.EventType, actor string) protocol.Response {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp := conn.ReadWithin(t, time.Until(deadline))
		if resp.Event == nil {
			continue
		}
		if resp.Event.Type == typ && resp.Event.Actor == actor {
			return resp
		}
	}
	t.Fatalf("did not receive event type=%s actor=%s", typ, actor)
	return protocol.Response{}
}

func assertErrorCode(t *testing.T, resp protocol.Response, code string) {
	t.Helper()
	if resp.OK || resp.Error == nil || resp.Error.Code != code {
		t.Fatalf("response = %#v, want error code %q", resp, code)
	}
}

func eventText(t *testing.T, ev model.Event) string {
	t.Helper()
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("payload = %#v, want object", ev.Payload)
	}
	text, ok := payload["text"].(string)
	if !ok {
		t.Fatalf("payload text = %#v, want string", payload["text"])
	}
	return text
}

func eventMentions(t *testing.T, ev model.Event) []string {
	t.Helper()
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("payload = %#v, want object", ev.Payload)
	}
	raw, ok := payload["mentions"].([]interface{})
	if !ok {
		t.Fatalf("payload mentions = %#v, want array", payload["mentions"])
	}
	mentions := make([]string, 0, len(raw))
	for _, item := range raw {
		mention, ok := item.(string)
		if !ok {
			t.Fatalf("mention = %#v, want string", item)
		}
		mentions = append(mentions, mention)
	}
	return mentions
}

func twoDigit(n int) string {
	return fmt.Sprintf("%02d", n)
}
