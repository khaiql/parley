package server

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
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

func TestServerHandleSendPublishesCommittedEventWhenSenderAckFails(t *testing.T) {
	srv := newInternalTestServer(t)
	aliceConn := &recordingConn{writeErr: errors.New("sender write failed")}
	bobConn := newRecordingConn()
	srv.registerOnlineTestClient("alice", aliceConn)
	srv.registerOnlineTestClient("bob", bobConn)

	resp := srv.handleSend("alice", protocol.SendRequest{Text: "committed"})
	if !resp.OK || resp.Event == nil {
		t.Fatalf("handleSend response = %#v", resp)
	}
	if err := srv.conns["alice"].write(resp); err == nil {
		t.Fatal("sender ack write succeeded, want forced failure")
	}

	pushed := bobConn.readResponse(t)
	if !pushed.OK || pushed.Event == nil || pushed.Event.Seq != resp.Event.Seq || pushed.Event.Type != model.EventMessage {
		t.Fatalf("bob pushed response = %#v, want committed message seq %d", pushed, resp.Event.Seq)
	}
}

func TestServerHandleLeavePublishesCommittedEventWhenAckFails(t *testing.T) {
	srv := newInternalTestServer(t)
	aliceConn := &recordingConn{writeErr: errors.New("leave ack failed")}
	bobConn := newRecordingConn()
	srv.registerOnlineTestClient("alice", aliceConn)
	srv.registerOnlineTestClient("bob", bobConn)

	resp := srv.handleLeave("alice")
	if !resp.OK || resp.Event == nil {
		t.Fatalf("handleLeave response = %#v", resp)
	}
	if err := srv.conns["alice"].write(resp); err == nil {
		t.Fatal("leave ack write succeeded, want forced failure")
	}

	pushed := bobConn.readResponse(t)
	if !pushed.OK || pushed.Event == nil || pushed.Event.Seq != resp.Event.Seq || pushed.Event.Type != model.EventParticipantLeft {
		t.Fatalf("bob pushed response = %#v, want participant.left seq %d", pushed, resp.Event.Seq)
	}
}

func TestServerRollbackJoinPublishesCompensatingLeave(t *testing.T) {
	srv := newInternalTestServer(t)
	bobConn := newRecordingConn()
	srv.registerOnlineTestClient("bob", bobConn)
	aliceConn := &recordingConn{writeErr: errors.New("join ack failed")}

	resp := srv.handleJoin(aliceConn, protocol.JoinRequest{RoomID: "room-1", Name: "alice", Role: "participant"})
	if !resp.OK || resp.Event == nil {
		t.Fatalf("handleJoin response = %#v", resp)
	}
	cc := srv.connectionForName("alice")
	if cc == nil {
		t.Fatal("alice connection was not registered")
	}
	if err := cc.writeLocked(resp); err == nil {
		t.Fatal("join ack write succeeded, want forced failure")
	}
	if err := srv.rollbackJoin("alice", cc); err != nil {
		t.Fatalf("rollbackJoin: %v", err)
	}

	joined := bobConn.readResponse(t)
	if joined.Event == nil || joined.Event.Type != model.EventParticipantJoined || joined.Event.Actor != "alice" {
		t.Fatalf("first pushed response = %#v, want alice joined", joined)
	}
	left := bobConn.readResponse(t)
	if left.Event == nil || left.Event.Type != model.EventParticipantLeft || left.Event.Actor != "alice" {
		t.Fatalf("second pushed response = %#v, want alice left", left)
	}

	events, err := srv.log.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 2 || events[0].Type != model.EventParticipantJoined || events[1].Type != model.EventParticipantLeft {
		t.Fatalf("events = %#v, want joined then compensating left", events)
	}
}

func TestServerRollbackJoinAppendFailurePreservesOnlineState(t *testing.T) {
	srv, logPath := newInternalTestServerWithLogPath(t)
	aliceConn := newRecordingConn()

	resp := srv.handleJoin(aliceConn, protocol.JoinRequest{RoomID: "room-1", Name: "alice", Role: "participant"})
	if !resp.OK || resp.Event == nil {
		t.Fatalf("handleJoin response = %#v", resp)
	}
	cc := srv.connectionForName("alice")
	if cc == nil {
		t.Fatal("alice connection was not registered")
	}

	originalLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if err := os.Remove(logPath); err != nil {
		t.Fatalf("remove log: %v", err)
	}
	if err := os.Mkdir(logPath, 0o700); err != nil {
		t.Fatalf("mkdir log path: %v", err)
	}

	if err := srv.rollbackJoin("alice", cc); err == nil {
		t.Fatal("rollbackJoin succeeded, want compensating leave append failure")
	}

	srv.mu.Lock()
	participant := srv.participants["alice"]
	currentConn := srv.conns["alice"]
	srv.mu.Unlock()
	if !participant.Online {
		t.Fatal("participant was marked offline without durable compensating leave")
	}
	if currentConn != cc {
		t.Fatal("connection was removed without durable compensating leave")
	}

	if err := os.Remove(logPath); err != nil {
		t.Fatalf("remove log directory: %v", err)
	}
	if err := os.WriteFile(logPath, originalLog, 0o600); err != nil {
		t.Fatalf("restore log: %v", err)
	}
	events, err := srv.log.ReadAll()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(events) != 1 || events[0].Type != model.EventParticipantJoined {
		t.Fatalf("events = %#v, want only durable join", events)
	}
}

func TestServerPublicationRecipientsAreCapturedAtCommit(t *testing.T) {
	srv := newInternalTestServerWithoutPublisher(t)
	aliceConn := newRecordingConn()
	bobConn := newRecordingConn()
	carolConn := newRecordingConn()
	srv.registerOnlineTestClient("alice", aliceConn)
	srv.registerOnlineTestClient("bob", bobConn)

	sendResp := srv.handleSend("alice", protocol.SendRequest{Text: "queued before carol joins"})
	if !sendResp.OK || sendResp.Event == nil {
		t.Fatalf("handleSend response = %#v", sendResp)
	}
	pub := srv.takePublication(t)

	joinResp := srv.handleJoin(carolConn, protocol.JoinRequest{RoomID: "room-1", Name: "carol", Role: "participant"})
	if !joinResp.OK || joinResp.Event == nil {
		t.Fatalf("handleJoin response = %#v", joinResp)
	}
	carolCC := srv.connectionForName("carol")
	if carolCC == nil {
		t.Fatal("carol connection was not registered")
	}
	if err := carolCC.writeLocked(joinResp); err != nil {
		t.Fatalf("write carol join response: %v", err)
	}
	carolJoinResp := carolConn.readResponse(t)
	if len(carolJoinResp.Events) != 1 || carolJoinResp.Events[0].Seq != sendResp.Event.Seq {
		t.Fatalf("carol join snapshot = %#v, want queued message seq %d", carolJoinResp.Events, sendResp.Event.Seq)
	}

	srv.publishDirect(pub)

	bobPush := bobConn.readResponse(t)
	if bobPush.Event == nil || bobPush.Event.Seq != sendResp.Event.Seq {
		t.Fatalf("bob push = %#v, want queued message seq %d", bobPush, sendResp.Event.Seq)
	}
	carolConn.assertNoResponse(t)
}

func TestServerPublicationSkipsOfflineConnectionEntries(t *testing.T) {
	srv := newInternalTestServerWithoutPublisher(t)
	aliceConn := newRecordingConn()
	bobConn := newRecordingConn()
	srv.registerOnlineTestClient("alice", aliceConn)
	srv.registerOnlineTestClient("bob", bobConn)

	leaveResp := srv.handleLeave("alice")
	if !leaveResp.OK || leaveResp.Event == nil {
		t.Fatalf("alice leave response = %#v", leaveResp)
	}
	_ = srv.takePublication(t)

	srv.mu.Lock()
	aliceParticipant := srv.participants["alice"]
	_, aliceStillRegistered := srv.conns["alice"]
	srv.mu.Unlock()
	if aliceParticipant.Online {
		t.Fatal("alice is still online after committed leave")
	}
	if !aliceStillRegistered {
		t.Fatal("alice connection entry was removed before leave ack")
	}

	sendResp := srv.handleSend("bob", protocol.SendRequest{Text: "after alice left"})
	if !sendResp.OK || sendResp.Event == nil {
		t.Fatalf("bob send response = %#v", sendResp)
	}
	srv.publishDirect(srv.takePublication(t))

	aliceConn.assertNoResponse(t)
}

func TestServerDisconnectLeaveAppendFailureKeepsConnectionRegistered(t *testing.T) {
	srv, logPath := newInternalTestServerWithLogPath(t)
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	if !srv.trackAcceptedConn(serverConn) {
		t.Fatal("trackAcceptedConn returned false")
	}

	done := make(chan struct{})
	go func() {
		defer srv.wg.Done()
		srv.handleConn(serverConn)
		close(done)
	}()

	reader := bufio.NewReader(clientConn)
	writeConnRequest(t, clientConn, protocol.Request{
		Type: protocol.RequestJoin,
		Join: &protocol.JoinRequest{RoomID: "room-1", Name: "alice", Role: "participant"},
	})
	resp := readConnResponse(t, clientConn, reader)
	if !resp.OK || resp.Event == nil {
		t.Fatalf("join response = %#v", resp)
	}
	cc := srv.connectionForName("alice")
	if cc == nil {
		t.Fatal("alice connection was not registered")
	}

	restoreLog := forceLogAppendFailure(t, logPath)
	defer restoreLog()
	if err := clientConn.Close(); err != nil {
		t.Fatalf("close client conn: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler to exit")
	}

	srv.mu.Lock()
	participant := srv.participants["alice"]
	currentConn := srv.conns["alice"]
	srv.mu.Unlock()
	if !participant.Online {
		t.Fatal("participant was marked offline without durable disconnect leave")
	}
	if currentConn != cc {
		t.Fatal("connection was removed without durable disconnect leave")
	}
}

func TestServerBroadcastDropLeaveAppendFailureKeepsConnectionRegistered(t *testing.T) {
	srv, logPath := newInternalTestServerWithLogPath(t)
	srv.registerOnlineTestClient("alice", &recordingConn{})
	bobConn := &recordingConn{writeErr: errors.New("broadcast write failed")}
	srv.registerOnlineTestClient("bob", bobConn)
	cc := srv.connectionForName("bob")
	if cc == nil {
		t.Fatal("bob connection was not registered")
	}

	restoreLog := forceLogAppendFailure(t, logPath)
	defer restoreLog()
	srv.broadcastEventExcept("alice", model.Event{Seq: 100, Type: model.EventMessage, RoomID: "room-1", Actor: "alice"})

	srv.mu.Lock()
	participant := srv.participants["bob"]
	currentConn := srv.conns["bob"]
	srv.mu.Unlock()
	if !participant.Online {
		t.Fatal("participant was marked offline without durable drop leave")
	}
	if currentConn != cc {
		t.Fatal("connection was removed without durable drop leave")
	}
	if bobConn.closed {
		t.Fatal("connection was closed without durable drop leave")
	}
}

func TestServerPublishesCommittedEventsInSequenceOrder(t *testing.T) {
	srv := newInternalTestServer(t)
	bobConn := newRecordingConn()
	srv.registerOnlineTestClient("alice", &recordingConn{})
	srv.registerOnlineTestClient("carol", &recordingConn{})
	srv.registerOnlineTestClient("bob", bobConn)

	first := srv.handleSend("alice", protocol.SendRequest{Text: "first"})
	if !first.OK || first.Event == nil {
		t.Fatalf("first send = %#v", first)
	}
	second := srv.handleSend("carol", protocol.SendRequest{Text: "second"})
	if !second.OK || second.Event == nil {
		t.Fatalf("second send = %#v", second)
	}

	firstPush := bobConn.readResponse(t)
	secondPush := bobConn.readResponse(t)
	if firstPush.Event == nil || secondPush.Event == nil {
		t.Fatalf("pushes = %#v %#v, want events", firstPush, secondPush)
	}
	if firstPush.Event.Seq != first.Event.Seq || secondPush.Event.Seq != second.Event.Seq {
		t.Fatalf("push seqs = %d, %d; want %d, %d", firstPush.Event.Seq, secondPush.Event.Seq, first.Event.Seq, second.Event.Seq)
	}
	if firstPush.Event.Seq >= secondPush.Event.Seq {
		t.Fatalf("pushes out of order: %d then %d", firstPush.Event.Seq, secondPush.Event.Seq)
	}
}

func TestServerPublishesDropLeaveAfterAlreadyQueuedEvents(t *testing.T) {
	srv := newInternalTestServer(t)
	blockWrite := make(chan struct{})
	writeStarted := make(chan struct{})
	bobConn := newRecordingConn()
	failingConn := &recordingConn{
		writeErr:     errors.New("peer write failed"),
		writeBlock:   blockWrite,
		writeStarted: writeStarted,
	}
	srv.registerOnlineTestClient("alice", &recordingConn{})
	srv.registerOnlineTestClient("carol", &recordingConn{})
	srv.registerOnlineTestClient("bob", bobConn)
	srv.registerOnlineTestClient("dana", failingConn)

	first := srv.handleSend("alice", protocol.SendRequest{Text: "first"})
	if !first.OK || first.Event == nil {
		t.Fatalf("first send = %#v", first)
	}
	select {
	case <-writeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for failing peer write to start")
	}

	second := srv.handleSend("carol", protocol.SendRequest{Text: "second"})
	if !second.OK || second.Event == nil {
		t.Fatalf("second send = %#v", second)
	}
	close(blockWrite)

	firstPush := bobConn.readResponse(t)
	secondPush := bobConn.readResponse(t)
	leavePush := bobConn.readResponse(t)
	if firstPush.Event == nil || firstPush.Event.Seq != first.Event.Seq {
		t.Fatalf("first pushed response = %#v, want seq %d", firstPush, first.Event.Seq)
	}
	if secondPush.Event == nil || secondPush.Event.Seq != second.Event.Seq {
		t.Fatalf("second pushed response = %#v, want seq %d", secondPush, second.Event.Seq)
	}
	if leavePush.Event == nil || leavePush.Event.Type != model.EventParticipantLeft || leavePush.Event.Actor != "dana" {
		t.Fatalf("third pushed response = %#v, want dana participant.left", leavePush)
	}
	if !(firstPush.Event.Seq < secondPush.Event.Seq && secondPush.Event.Seq < leavePush.Event.Seq) {
		t.Fatalf("pushes out of order: %d, %d, %d", firstPush.Event.Seq, secondPush.Event.Seq, leavePush.Event.Seq)
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
	srv, _ := newInternalTestServerWithLogPath(t)
	return srv
}

func newInternalTestServerWithLogPath(t *testing.T) (*Server, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	srv, err := New("127.0.0.1:0", Config{
		RoomID: "room-1",
		Topic:  "test topic",
		Log:    eventlog.New(logPath),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv, logPath
}

func newInternalTestServerWithoutPublisher(t *testing.T) *Server {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	log := eventlog.New(logPath)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv := &Server{
		listener:      ln,
		cfg:           Config{RoomID: "room-1", Topic: "test topic", Log: log},
		log:           log,
		participants:  make(map[string]model.Participant),
		conns:         make(map[string]*clientConn),
		activeConns:   make(map[net.Conn]struct{}),
		serveStarted:  make(chan struct{}),
		closed:        make(chan struct{}),
		publisherDone: make(chan struct{}),
	}
	srv.publishCond = sync.NewCond(&srv.publishMu)
	t.Cleanup(func() { _ = ln.Close() })
	return srv
}

func (s *Server) registerOnlineTestClient(name string, conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.participants[name] = model.Participant{Name: name, Role: "participant", Online: true}
	s.conns[name] = &clientConn{name: name, conn: conn}
}

func (s *Server) takePublication(t *testing.T) publication {
	t.Helper()
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if len(s.publishQueue) == 0 {
		t.Fatal("publish queue is empty")
	}
	pub := s.publishQueue[0]
	copy(s.publishQueue, s.publishQueue[1:])
	s.publishQueue[len(s.publishQueue)-1] = publication{}
	s.publishQueue = s.publishQueue[:len(s.publishQueue)-1]
	return pub
}

func forceLogAppendFailure(t *testing.T, logPath string) func() {
	t.Helper()
	originalLog, err := os.ReadFile(logPath)
	originalExists := true
	if os.IsNotExist(err) {
		originalExists = false
	} else if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove log: %v", err)
	}
	if err := os.Mkdir(logPath, 0o700); err != nil {
		t.Fatalf("mkdir log path: %v", err)
	}
	return func() {
		t.Helper()
		if err := os.Remove(logPath); err != nil {
			t.Fatalf("remove log directory: %v", err)
		}
		if !originalExists {
			return
		}
		if err := os.WriteFile(logPath, originalLog, 0o600); err != nil {
			t.Fatalf("restore log: %v", err)
		}
	}
}

func writeConnRequest(t *testing.T, conn net.Conn, req protocol.Request) {
	t.Helper()
	data, err := protocol.EncodeLine(req)
	if err != nil {
		t.Fatalf("EncodeLine: %v", err)
	}
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write request: %v", err)
	}
}

func readConnResponse(t *testing.T, conn net.Conn, reader *bufio.Reader) protocol.Response {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp protocol.Response
	if err := protocol.DecodeLine(line, &resp); err != nil {
		t.Fatalf("DecodeLine: %v", err)
	}
	return resp
}

type recordingConn struct {
	writeErr       error
	writeDeadlines []time.Time
	closed         bool
	writes         chan []byte
	writeBlock     <-chan struct{}
	writeStarted   chan struct{}
	writeStartOnce sync.Once
}

func (c *recordingConn) Read([]byte) (int, error) {
	return 0, errors.New("read not implemented")
}

func (c *recordingConn) Write(p []byte) (int, error) {
	if c.writeStarted != nil {
		c.writeStartOnce.Do(func() {
			close(c.writeStarted)
		})
	}
	if c.writeBlock != nil {
		<-c.writeBlock
	}
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	if c.writes != nil {
		data := make([]byte, len(p))
		copy(data, p)
		c.writes <- data
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

func newRecordingConn() *recordingConn {
	return &recordingConn{writes: make(chan []byte, 16)}
}

func (c *recordingConn) readResponse(t *testing.T) protocol.Response {
	t.Helper()
	select {
	case data := <-c.writes:
		var resp protocol.Response
		if err := protocol.DecodeLine(data, &resp); err != nil {
			t.Fatalf("DecodeLine: %v", err)
		}
		return resp
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for pushed response")
		return protocol.Response{}
	}
}

func (c *recordingConn) assertNoResponse(t *testing.T) {
	t.Helper()
	select {
	case data := <-c.writes:
		var resp protocol.Response
		if err := protocol.DecodeLine(data, &resp); err != nil {
			t.Fatalf("DecodeLine unexpected response: %v", err)
		}
		t.Fatalf("unexpected pushed response = %#v", resp)
	default:
	}
}

type dummyAddr string

func (a dummyAddr) Network() string {
	return string(a)
}

func (a dummyAddr) String() string {
	return string(a)
}
