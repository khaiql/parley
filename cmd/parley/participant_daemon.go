package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/paths"
	"github.com/khaiql/parley/internal/protocol"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

const daemonAckTimeout = 5 * time.Second

type participantAdapterRuntime struct {
	cfg    participantDaemonConfig
	store  *adapter.Store
	conn   net.Conn
	reader *bufio.Reader

	writeMu sync.Mutex
	notify  chan struct{}
}

func runParticipantAdapter(cfg participantDaemonConfig) error {
	p := paths.New(paths.DefaultRoot())
	store, err := parleyRuntime.ParticipantStore(p, cfg.Descriptor.RoomID, cfg.Name)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("tcp", cfg.Descriptor.Addr(), daemonStartupTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	rt := &participantAdapterRuntime{
		cfg:    cfg,
		store:  store,
		conn:   conn,
		reader: bufio.NewReader(conn),
		notify: make(chan struct{}, 1),
	}
	if err := rt.join(); err != nil {
		return err
	}

	socketPath := parleyRuntime.ParticipantSocketPath(p, cfg.Descriptor.RoomID, cfg.Name)
	defer os.Remove(socketPath)

	readErrCh := make(chan error, 1)
	go func() {
		readErrCh <- rt.readLoop()
	}()

	controlErrCh := make(chan error, 1)
	go func() {
		controlErrCh <- adapter.ServeControl(socketPath, rt.handleControl)
	}()

	select {
	case err := <-readErrCh:
		_ = rt.markDisconnectedUnlessLeft()
		time.Sleep(100 * time.Millisecond)
		return err
	case err := <-controlErrCh:
		_ = rt.markDisconnectedUnlessLeft()
		return err
	}
}

func (rt *participantAdapterRuntime) join() error {
	if err := rt.write(protocol.Request{
		Type: protocol.RequestJoin,
		Join: &protocol.JoinRequest{
			RoomID:    rt.cfg.Descriptor.RoomID,
			Name:      rt.cfg.Name,
			Role:      rt.cfg.Role,
			Directory: rt.cfg.Directory,
			Repo:      rt.cfg.Repo,
		},
	}); err != nil {
		return err
	}

	resp, err := rt.readResponse()
	if err != nil {
		return err
	}
	if !resp.OK {
		return responseError(resp)
	}
	artifactEndpoint := ""
	if resp.Room != nil {
		artifactEndpoint = deriveArtifactEndpoint(rt.cfg.Descriptor, parleyRuntime.RoomRuntime{
			RoomID:            resp.Room.RoomID,
			ArtifactLocalPort: resp.Room.ArtifactLocalPort,
			ArtifactPath:      resp.Room.ArtifactPath,
		})
	}
	if err := rt.store.SaveMeta(adapter.Meta{
		RoomID:           rt.cfg.Descriptor.RoomID,
		Name:             rt.cfg.Name,
		Role:             rt.cfg.Role,
		Descriptor:       rt.cfg.Descriptor.String(),
		ArtifactEndpoint: artifactEndpoint,
		Status:           "online",
	}); err != nil {
		return err
	}
	if err := rt.appendResponse(resp); err != nil {
		return err
	}
	return rt.store.MarkReceivedSeen()
}

func (rt *participantAdapterRuntime) readLoop() error {
	for {
		resp, err := rt.readResponse()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if resp.OK {
			if err := rt.appendResponse(resp); err != nil {
				return err
			}
			rt.signal()
			continue
		}
		rt.signal()
	}
}

func (rt *participantAdapterRuntime) readResponse() (protocol.Response, error) {
	line, err := rt.reader.ReadBytes('\n')
	if err != nil {
		return protocol.Response{}, err
	}
	var resp protocol.Response
	if err := protocol.DecodeLine(line, &resp); err != nil {
		return protocol.Response{}, err
	}
	return resp, nil
}

func (rt *participantAdapterRuntime) write(req protocol.Request) error {
	data, err := protocol.EncodeLine(req)
	if err != nil {
		return err
	}
	rt.writeMu.Lock()
	defer rt.writeMu.Unlock()
	_, err = rt.conn.Write(data)
	return err
}

func (rt *participantAdapterRuntime) appendResponse(resp protocol.Response) error {
	for _, ev := range resp.Events {
		if err := rt.store.AppendLocal(ev); err != nil {
			return err
		}
	}
	if resp.Event != nil {
		return rt.store.AppendLocal(*resp.Event)
	}
	return nil
}

func (rt *participantAdapterRuntime) handleControl(req adapter.ControlRequest) adapter.ControlResponse {
	switch req.Type {
	case "status":
		return adapter.ControlResponse{OK: true}
	case "send":
		events, files, err := rt.send(req.Text, req.Files)
		if err != nil {
			return adapter.ControlResponse{OK: false, Error: err.Error(), Files: files, MessageCommitted: boolRef(false)}
		}
		return adapter.ControlResponse{OK: true, Status: "sent", Events: events}
	case "artifact_fetch":
		results := rt.fetchArtifacts(req.ArtifactIDs, req.Out)
		status := artifactFetchStatus(results)
		return adapter.ControlResponse{OK: true, Status: status, Results: results}
	case "wait":
		events, timedOut, err := rt.wait(req.Timeout)
		if err != nil {
			return adapter.ControlResponse{OK: false, Error: err.Error()}
		}
		if timedOut {
			return adapter.ControlResponse{OK: true, Status: "timeout"}
		}
		return adapter.ControlResponse{OK: true, Events: events}
	case "leave":
		events, err := rt.leave()
		if err != nil {
			return adapter.ControlResponse{OK: false, Error: err.Error()}
		}
		return adapter.ControlResponse{OK: true, Status: "left", Events: events}
	default:
		return adapter.ControlResponse{OK: false, Error: "unsupported participant control request: " + req.Type}
	}
}

func artifactFetchStatus(results []adapter.ArtifactFetchResult) string {
	if len(results) == 0 {
		return "error"
	}
	downloaded := 0
	failed := 0
	for _, result := range results {
		if result.Status == "downloaded" {
			downloaded++
			continue
		}
		failed++
	}
	switch {
	case downloaded == len(results):
		return "downloaded"
	case failed == len(results):
		return "error"
	default:
		return "partial"
	}
}

func (rt *participantAdapterRuntime) send(text string, files []string) ([]model.Event, []adapter.ArtifactFileResult, error) {
	afterSeq, err := rt.lastReceivedSeq()
	if err != nil {
		return nil, nil, err
	}
	artifactIDs, fileResults, err := rt.uploadArtifacts(files)
	if err != nil {
		return nil, fileResults, err
	}
	committed := false
	defer func() {
		if !committed && len(artifactIDs) > 0 {
			_ = rt.cleanupStagedArtifacts()
		}
	}()
	if err := rt.write(protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: text, ArtifactIDs: artifactIDs}}); err != nil {
		return nil, fileResults, err
	}
	ev, err := rt.waitForEvent(afterSeq, daemonAckTimeout, func(ev model.Event) bool {
		return ev.Type == model.EventMessage && ev.Actor == rt.cfg.Name && messageText(ev) == text && messageArtifactCount(ev) == len(artifactIDs)
	})
	if err != nil {
		return nil, fileResults, err
	}
	events, err := rt.takeUnseenThrough(ev.Seq, daemonAckTimeout)
	if err != nil {
		return nil, fileResults, err
	}
	committed = true
	return events, fileResults, nil
}

func (rt *participantAdapterRuntime) wait(rawTimeout string) ([]model.Event, bool, error) {
	timeout, err := time.ParseDuration(rawTimeout)
	if err != nil {
		return nil, false, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		events, err := rt.store.WaitReadyBatch(rt.cfg.Name)
		if err != nil {
			return nil, false, err
		}
		if len(events) > 0 {
			return events, false, nil
		}
		select {
		case <-rt.notify:
		case <-timer.C:
			return nil, true, nil
		}
	}
}

func (rt *participantAdapterRuntime) leave() ([]model.Event, error) {
	afterSeq, err := rt.lastReceivedSeq()
	if err != nil {
		return nil, err
	}
	if err := rt.write(protocol.Request{Type: protocol.RequestLeave, Leave: &protocol.LeaveRequest{Name: rt.cfg.Name}}); err != nil {
		return nil, err
	}
	ev, err := rt.waitForEvent(afterSeq, daemonAckTimeout, func(ev model.Event) bool {
		return ev.Type == model.EventParticipantLeft && ev.Actor == rt.cfg.Name
	})
	if err != nil {
		return nil, err
	}
	if err := rt.markStatus("left"); err != nil {
		return nil, err
	}
	return []model.Event{ev}, nil
}

func (rt *participantAdapterRuntime) waitForEvent(afterSeq int64, timeout time.Duration, match func(model.Event) bool) (model.Event, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		events, err := rt.store.EventsAfterSeq(afterSeq, 0)
		if err != nil {
			return model.Event{}, err
		}
		for _, ev := range events {
			if match(ev) {
				return ev, nil
			}
		}
		select {
		case <-rt.notify:
		case <-timer.C:
			return model.Event{}, fmt.Errorf("timed out waiting for server acknowledgement")
		}
	}
}

func (rt *participantAdapterRuntime) takeUnseenThrough(seq int64, timeout time.Duration) ([]model.Event, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var out []model.Event
	for {
		events, err := rt.store.TakeUnseenThrough(seq)
		if err != nil {
			return nil, err
		}
		if len(events) > 0 {
			out = append(out, events...)
			if out[len(out)-1].Seq >= seq {
				return out, nil
			}
		}
		meta, err := rt.store.LoadMeta()
		if err != nil {
			return nil, err
		}
		if meta.LastSeenSeq >= seq {
			return out, nil
		}
		select {
		case <-rt.notify:
		case <-timer.C:
			return nil, fmt.Errorf("timed out waiting for contiguous events through seq %d", seq)
		}
	}
}

func (rt *participantAdapterRuntime) lastReceivedSeq() (int64, error) {
	meta, err := rt.store.LoadMeta()
	if err != nil {
		return 0, err
	}
	return meta.LastReceivedSeq, nil
}

func (rt *participantAdapterRuntime) markStatus(status string) error {
	meta, err := rt.store.LoadMeta()
	if err != nil {
		return err
	}
	meta.Status = status
	return rt.store.SaveMeta(meta)
}

func (rt *participantAdapterRuntime) markDisconnectedUnlessLeft() error {
	meta, err := rt.store.LoadMeta()
	if err != nil {
		return err
	}
	if meta.Status == "left" {
		return nil
	}
	meta.Status = "disconnected"
	return rt.store.SaveMeta(meta)
}

func (rt *participantAdapterRuntime) signal() {
	select {
	case rt.notify <- struct{}{}:
	default:
	}
}

func boolRef(v bool) *bool {
	return &v
}

func responseError(resp protocol.Response) error {
	if resp.Error == nil {
		return fmt.Errorf("server returned an unsuccessful response")
	}
	return fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
}

func messageText(ev model.Event) string {
	if payload, ok := ev.Payload.(model.MessagePayload); ok {
		return payload.Text
	}
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return ""
	}
	text, _ := payload["text"].(string)
	return text
}

func messageArtifactCount(ev model.Event) int {
	if payload, ok := ev.Payload.(model.MessagePayload); ok {
		return len(payload.Artifacts)
	}
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return 0
	}
	artifacts, _ := payload["artifacts"].([]interface{})
	return len(artifacts)
}
