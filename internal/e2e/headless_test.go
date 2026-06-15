package e2e

import (
	"bufio"
	"errors"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/descriptor"
	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/paths"
	"github.com/khaiql/parley/internal/protocol"
	"github.com/khaiql/parley/internal/runtime"
	"github.com/khaiql/parley/internal/server"
)

func TestHeadlessRoomTwoParticipants(t *testing.T) {
	root := t.TempDir()
	host := StartServerForTest(t, root, "topic", "host", "host")
	agent := JoinForTest(t, root, host.Descriptor, "agent", "reviewer")

	host.Send(t, "@agent please respond")
	got := agent.Wait(t, 2*time.Second)
	assertMessage(t, got, "host", "@agent please respond")

	agent.Send(t, "I am here")
	got = host.Wait(t, 2*time.Second)
	assertMessage(t, got, "agent", "I am here")
}

type ServerHandle struct {
	*AdapterHandle
	Descriptor string
}

type AdapterHandle struct {
	name   string
	role   string
	roomID string
	desc   string
	conn   net.Conn
	reader *bufio.Reader
	store  *adapter.Store

	writeMu  sync.Mutex
	appendMu sync.Mutex
	lastSeq  int64
	pending  map[int64]model.Event

	notify chan struct{}
	done   chan struct{}

	errMu sync.Mutex
	err   error
}

func StartServerForTest(t testing.TB, root, topic, name, role string) ServerHandle {
	t.Helper()

	p := paths.New(root)
	roomID := "room-1"
	log := eventlog.New(runtime.RoomEventsPath(p, roomID))
	srv, err := server.New("127.0.0.1:0", server.Config{
		RoomID: roomID,
		Topic:  topic,
		Log:    log,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	go srv.Serve()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Fatalf("server.Close: %v", err)
		}
	})

	desc := descriptor.Descriptor{Host: "127.0.0.1", Port: srv.Port(), RoomID: roomID}.String()
	host := JoinForTest(t, root, desc, name, role)
	return ServerHandle{AdapterHandle: host, Descriptor: desc}
}

func JoinForTest(t testing.TB, root, rawDescriptor, name, role string) *AdapterHandle {
	t.Helper()

	desc, err := descriptor.Parse(rawDescriptor)
	if err != nil {
		t.Fatalf("descriptor.Parse: %v", err)
	}
	conn, err := net.Dial("tcp", desc.Addr())
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	p := paths.New(root)
	store, err := runtime.ParticipantStore(p, desc.RoomID, name)
	if err != nil {
		t.Fatalf("runtime.ParticipantStore: %v", err)
	}
	if err := store.SaveMeta(adapter.Meta{
		RoomID:     desc.RoomID,
		Name:       name,
		Role:       role,
		Descriptor: rawDescriptor,
		Status:     "online",
	}); err != nil {
		t.Fatalf("store.SaveMeta: %v", err)
	}

	h := &AdapterHandle{
		name:    name,
		role:    role,
		roomID:  desc.RoomID,
		desc:    rawDescriptor,
		conn:    conn,
		reader:  bufio.NewReader(conn),
		store:   store,
		pending: make(map[int64]model.Event),
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	h.write(t, protocol.Request{
		Type: protocol.RequestJoin,
		Join: &protocol.JoinRequest{
			RoomID: desc.RoomID,
			Name:   name,
			Role:   role,
		},
	})

	resp := h.read(t, 2*time.Second)
	if !resp.OK {
		t.Fatalf("join response = %#v", resp)
	}
	if err := h.appendResponse(resp); err != nil {
		t.Fatalf("append join response: %v", err)
	}
	if _, err := h.store.Inbox(false); err != nil {
		t.Fatalf("mark join events seen: %v", err)
	}

	go h.readLoop()
	return h
}

func assertMessage(t testing.TB, resp adapter.ControlResponse, actor, text string) {
	t.Helper()
	if !resp.OK || resp.Status != "ok" {
		t.Fatalf("response = %#v, want ok message response", resp)
	}
	if len(resp.Events) == 0 {
		t.Fatal("response contained no events")
	}
	ev := resp.Events[len(resp.Events)-1]
	if ev.Type != model.EventMessage || ev.Actor != actor || eventText(ev) != text {
		t.Fatalf("last event = %#v, want message from %s with text %q", ev, actor, text)
	}
}

func (h *AdapterHandle) Send(t testing.TB, text string) {
	t.Helper()

	meta, err := h.store.LoadMeta()
	if err != nil {
		t.Fatalf("load meta before send: %v", err)
	}
	afterSeq := meta.LastReceivedSeq
	h.write(t, protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: text}})
	if err := h.waitForLocalMessage(text, afterSeq, 2*time.Second); err != nil {
		t.Fatalf("wait for local send acknowledgement: %v", err)
	}
	if _, err := h.store.Inbox(false); err != nil {
		t.Fatalf("mark sent events seen: %v", err)
	}
}

func (h *AdapterHandle) Wait(t testing.TB, timeout time.Duration) adapter.ControlResponse {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		if err := h.readErr(); err != nil {
			t.Fatalf("adapter read loop: %v", err)
		}
		events, err := h.store.WaitReadyBatch(h.name)
		if err != nil {
			t.Fatalf("WaitReadyBatch: %v", err)
		}
		if len(events) > 0 {
			if _, err := h.store.Inbox(false); err != nil {
				t.Fatalf("mark waited events seen: %v", err)
			}
			return adapter.ControlResponse{OK: true, Status: "ok", Events: events}
		}

		select {
		case <-h.notify:
		case <-timer.C:
			return adapter.ControlResponse{OK: true, Status: "timeout"}
		case <-h.done:
			if err := h.readErr(); err != nil {
				t.Fatalf("adapter read loop: %v", err)
			}
			return adapter.ControlResponse{OK: true, Status: "adapter_disconnected"}
		}
	}
}

func (h *AdapterHandle) write(t testing.TB, req protocol.Request) {
	t.Helper()

	data, err := protocol.EncodeLine(req)
	if err != nil {
		t.Fatalf("protocol.EncodeLine: %v", err)
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	if _, err := h.conn.Write(data); err != nil {
		t.Fatalf("write request: %v", err)
	}
}

func (h *AdapterHandle) read(t testing.TB, timeout time.Duration) protocol.Response {
	t.Helper()

	if err := h.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	line, err := h.reader.ReadBytes('\n')
	_ = h.conn.SetReadDeadline(time.Time{})
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp protocol.Response
	if err := protocol.DecodeLine(line, &resp); err != nil {
		t.Fatalf("protocol.DecodeLine: %v", err)
	}
	return resp
}

func (h *AdapterHandle) readLoop() {
	defer close(h.done)
	for {
		line, err := h.reader.ReadBytes('\n')
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				h.setReadErr(err)
			}
			return
		}
		var resp protocol.Response
		if err := protocol.DecodeLine(line, &resp); err != nil {
			h.setReadErr(err)
			return
		}
		if err := h.appendResponse(resp); err != nil {
			h.setReadErr(err)
			return
		}
		h.signal()
	}
}

func (h *AdapterHandle) appendResponse(resp protocol.Response) error {
	if !resp.OK {
		if resp.Error == nil {
			return errors.New("server response failed without error payload")
		}
		return errors.New(resp.Error.Code + ": " + resp.Error.Message)
	}

	events := make([]model.Event, 0, len(resp.Events)+1)
	events = append(events, resp.Events...)
	if resp.Event != nil {
		events = append(events, *resp.Event)
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].Seq < events[j].Seq
	})

	h.appendMu.Lock()
	defer h.appendMu.Unlock()
	for _, ev := range events {
		if ev.Seq == 0 {
			continue
		}
		h.pending[ev.Seq] = ev
	}
	return h.flushPendingLocked()
}

func (h *AdapterHandle) flushPendingLocked() error {
	meta, err := h.store.LoadMeta()
	if err != nil {
		return err
	}
	if meta.LastReceivedSeq > h.lastSeq {
		h.lastSeq = meta.LastReceivedSeq
	}
	for {
		next := h.lastSeq + 1
		ev, ok := h.pending[next]
		if !ok {
			return nil
		}
		if err := h.store.AppendLocal(ev); err != nil {
			return err
		}
		delete(h.pending, next)
		h.lastSeq = next
	}
}

func (h *AdapterHandle) waitForLocalMessage(text string, afterSeq int64, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		found, err := h.hasLocalMessage(text, afterSeq)
		if err != nil {
			return err
		}
		if found {
			return nil
		}

		select {
		case <-h.notify:
		case <-timer.C:
			return errors.New("timed out waiting for sent message acknowledgement")
		case <-h.done:
			if err := h.readErr(); err != nil {
				return err
			}
			return errors.New("adapter disconnected before sent message acknowledgement")
		}
	}
}

func (h *AdapterHandle) hasLocalMessage(text string, afterSeq int64) (bool, error) {
	events, err := eventlog.New(h.store.EventsPath).AfterSeq(afterSeq, 0)
	if err != nil {
		return false, err
	}
	for _, ev := range events {
		if ev.Type == model.EventMessage && ev.Actor == h.name && eventText(ev) == text {
			return true, nil
		}
	}
	return false, nil
}

func eventText(ev model.Event) string {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return ""
	}
	text, _ := payload["text"].(string)
	return text
}

func (h *AdapterHandle) signal() {
	select {
	case h.notify <- struct{}{}:
	default:
	}
}

func (h *AdapterHandle) setReadErr(err error) {
	h.errMu.Lock()
	defer h.errMu.Unlock()
	if h.err == nil {
		h.err = err
	}
}

func (h *AdapterHandle) readErr() error {
	h.errMu.Lock()
	defer h.errMu.Unlock()
	return h.err
}
