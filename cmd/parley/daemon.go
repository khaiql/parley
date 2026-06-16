package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/descriptor"
	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/paths"
	"github.com/khaiql/parley/internal/protocol"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
	"github.com/khaiql/parley/internal/server"
)

const (
	daemonStartupTimeout = 5 * time.Second
	daemonPollInterval   = 25 * time.Millisecond
	daemonAckTimeout     = 5 * time.Second
)

type roomDaemonConfig struct {
	RoomID string
	Topic  string
	Name   string
	Role   string
}

type participantDaemonConfig struct {
	Descriptor descriptor.Descriptor
	Name       string
	Role       string
	Directory  string
	Repo       string
}

var (
	launchRoomDaemon        = startRoomDaemonProcess
	launchParticipantDaemon = startParticipantDaemonProcess
)

func roomDaemonCmd() *cobra.Command {
	var cfg roomDaemonConfig

	cmd := &cobra.Command{
		Use:    "__room-daemon",
		Hidden: true,
		Args:   noArgsJSON,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfg.RoomID == "" || cfg.Topic == "" || cfg.Name == "" {
				return fmt.Errorf("room daemon requires --room, --topic, and --name")
			}
			if cfg.Role == "" {
				cfg.Role = "host"
			}
			return runRoomDaemon(cfg)
		},
	}
	cmd.Flags().StringVar(&cfg.RoomID, "room", "", "Room ID")
	cmd.Flags().StringVar(&cfg.Topic, "topic", "", "Room topic")
	cmd.Flags().StringVar(&cfg.Name, "name", "", "Host participant name")
	cmd.Flags().StringVar(&cfg.Role, "role", "host", "Host participant role")
	return cmd
}

func participantDaemonCmd() *cobra.Command {
	var rawDescriptor string
	var cfg participantDaemonConfig

	cmd := &cobra.Command{
		Use:    "__participant-daemon",
		Hidden: true,
		Args:   noArgsJSON,
		RunE: func(cmd *cobra.Command, args []string) error {
			desc, err := descriptor.Parse(rawDescriptor)
			if err != nil {
				return fmt.Errorf("invalid descriptor: %w", err)
			}
			cfg.Descriptor = desc
			if cfg.Name == "" {
				return fmt.Errorf("participant daemon requires --name")
			}
			if cfg.Role == "" {
				cfg.Role = "participant"
			}
			return runParticipantAdapter(cfg)
		},
	}
	cmd.Flags().StringVar(&rawDescriptor, "descriptor", "", "Room descriptor")
	cmd.Flags().StringVar(&cfg.Name, "name", "", "Participant name")
	cmd.Flags().StringVar(&cfg.Role, "role", "participant", "Participant role")
	cmd.Flags().StringVar(&cfg.Directory, "dir", "", "Participant working directory")
	cmd.Flags().StringVar(&cfg.Repo, "repo", "", "Participant repository URL")
	return cmd
}

func newRoomID() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "room-" + hex.EncodeToString(b[:]), nil
}

func startRoomDaemonProcess(cfg roomDaemonConfig) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer devNull.Close()

	cmd := exec.Command(
		exe,
		"__room-daemon",
		"--room", cfg.RoomID,
		"--topic", cfg.Topic,
		"--name", cfg.Name,
		"--role", cfg.Role,
	)
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	if err := waitForRoomDaemonReady(paths.New(paths.DefaultRoot()), cfg, cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return pid, err
	}
	if err := cmd.Process.Release(); err != nil {
		return pid, err
	}
	return pid, nil
}

func startParticipantDaemonProcess(cfg participantDaemonConfig) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	defer devNull.Close()

	args := []string{
		"__participant-daemon",
		"--descriptor", cfg.Descriptor.String(),
		"--name", cfg.Name,
		"--role", cfg.Role,
	}
	if cfg.Directory != "" {
		args = append(args, "--dir", cfg.Directory)
	}
	if cfg.Repo != "" {
		args = append(args, "--repo", cfg.Repo)
	}

	cmd := exec.Command(exe, args...)
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	if err := waitForParticipantDaemonReady(paths.New(paths.DefaultRoot()), cfg, cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		return pid, err
	}
	if err := cmd.Process.Release(); err != nil {
		return pid, err
	}
	return pid, nil
}

func waitForRoomDaemonReady(p paths.Paths, cfg roomDaemonConfig, proc *os.Process) error {
	deadline := time.Now().Add(daemonStartupTimeout)
	for time.Now().Before(deadline) {
		if err := processAlive(proc); err != nil {
			return fmt.Errorf("room daemon exited before becoming ready: %w", err)
		}
		meta, err := parleyRuntime.LoadRoomRuntime(p, cfg.RoomID)
		if err == nil && meta.LocalPort > 0 && controlReady(parleyRuntime.ServerSocketPath(p, cfg.RoomID)) && controlReady(parleyRuntime.ParticipantSocketPath(p, cfg.RoomID, cfg.Name)) {
			return nil
		}
		time.Sleep(daemonPollInterval)
	}
	return fmt.Errorf("room daemon did not become ready within %s", daemonStartupTimeout)
}

func waitForParticipantDaemonReady(p paths.Paths, cfg participantDaemonConfig, proc *os.Process) error {
	deadline := time.Now().Add(daemonStartupTimeout)
	for time.Now().Before(deadline) {
		if err := processAlive(proc); err != nil {
			return fmt.Errorf("participant daemon exited before becoming ready: %w", err)
		}
		store, err := parleyRuntime.ParticipantStore(p, cfg.Descriptor.RoomID, cfg.Name)
		if err != nil {
			return err
		}
		meta, err := store.LoadMeta()
		if err == nil && meta.Status == "online" && controlReady(parleyRuntime.ParticipantSocketPath(p, cfg.Descriptor.RoomID, cfg.Name)) {
			return nil
		}
		time.Sleep(daemonPollInterval)
	}
	return fmt.Errorf("participant daemon did not become ready within %s", daemonStartupTimeout)
}

func processAlive(proc *os.Process) error {
	if proc == nil {
		return nil
	}
	return proc.Signal(syscall.Signal(0))
}

func controlReady(socketPath string) bool {
	resp, err := adapter.CallControl(socketPath, adapter.ControlRequest{Type: "status"})
	return err == nil && resp.OK
}

func runRoomDaemon(cfg roomDaemonConfig) error {
	p := paths.New(paths.DefaultRoot())
	log := eventlog.New(parleyRuntime.RoomEventsPath(p, cfg.RoomID))
	if _, err := log.Append(model.Event{
		Type:   model.EventRoomStarted,
		RoomID: cfg.RoomID,
		Actor:  cfg.Name,
	}); err != nil {
		return err
	}

	srv, err := server.New("127.0.0.1:0", server.Config{
		RoomID: cfg.RoomID,
		Topic:  cfg.Topic,
		Log:    log,
	})
	if err != nil {
		return err
	}
	go srv.Serve()

	desc := descriptor.Descriptor{Host: "127.0.0.1", Port: srv.Port(), RoomID: cfg.RoomID}
	stopCh := make(chan struct{})
	var stopOnce sync.Once
	serverSocket := parleyRuntime.ServerSocketPath(p, cfg.RoomID)
	defer os.Remove(serverSocket)

	controlErrCh := make(chan error, 1)
	go func() {
		controlErrCh <- adapter.ServeControl(serverSocket, func(req adapter.ControlRequest) adapter.ControlResponse {
			switch req.Type {
			case "status":
				return adapter.ControlResponse{OK: true}
			case "stop":
				stopOnce.Do(func() { close(stopCh) })
				return adapter.ControlResponse{OK: true, Status: "stopping"}
			default:
				return adapter.ControlResponse{OK: false, Error: "unsupported server control request: " + req.Type}
			}
		})
	}()

	hostErrCh := make(chan error, 1)
	go func() {
		hostErrCh <- runParticipantAdapter(participantDaemonConfig{
			Descriptor: desc,
			Name:       cfg.Name,
			Role:       cfg.Role,
		})
	}()

	if err := parleyRuntime.SaveRoomRuntime(p, parleyRuntime.RoomRuntime{
		RoomID:    cfg.RoomID,
		Topic:     cfg.Topic,
		LocalHost: desc.Host,
		LocalPort: desc.Port,
		ServerPID: os.Getpid(),
	}); err != nil {
		_ = srv.Close()
		return err
	}

	select {
	case <-stopCh:
		_ = appendRoomStopped(log, cfg, "stop requested")
		return srv.Close()
	case err := <-hostErrCh:
		_ = srv.Close()
		if err == nil {
			_ = appendRoomStopped(log, cfg, "host left")
			return nil
		}
		return fmt.Errorf("host adapter stopped: %w", err)
	case err := <-controlErrCh:
		_ = srv.Close()
		return fmt.Errorf("server control stopped: %w", err)
	}
}

func appendRoomStopped(log *eventlog.Log, cfg roomDaemonConfig, reason string) error {
	_, err := log.Append(model.Event{
		Type:    model.EventRoomStopped,
		RoomID:  cfg.RoomID,
		Actor:   cfg.Name,
		Payload: model.RoomStoppedPayload{Reason: reason},
	})
	return err
}

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
	if err := rt.store.SaveMeta(adapter.Meta{
		RoomID:     rt.cfg.Descriptor.RoomID,
		Name:       rt.cfg.Name,
		Role:       rt.cfg.Role,
		Descriptor: rt.cfg.Descriptor.String(),
		Status:     "online",
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
		events, err := rt.send(req.Text)
		if err != nil {
			return adapter.ControlResponse{OK: false, Error: err.Error()}
		}
		return adapter.ControlResponse{OK: true, Status: "sent", Events: events}
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

func (rt *participantAdapterRuntime) send(text string) ([]model.Event, error) {
	afterSeq, err := rt.lastReceivedSeq()
	if err != nil {
		return nil, err
	}
	if err := rt.write(protocol.Request{Type: protocol.RequestSend, Send: &protocol.SendRequest{Text: text}}); err != nil {
		return nil, err
	}
	ev, err := rt.waitForEvent(afterSeq, daemonAckTimeout, func(ev model.Event) bool {
		return ev.Type == model.EventMessage && ev.Actor == rt.cfg.Name && messageText(ev) == text
	})
	if err != nil {
		return nil, err
	}
	events, err := rt.takeUnseenThrough(ev.Seq, daemonAckTimeout)
	if err != nil {
		return nil, err
	}
	return events, nil
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
			if err := rt.store.MarkSeenThrough(events[len(events)-1].Seq); err != nil {
				return nil, false, err
			}
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
		events, err := eventlog.New(rt.store.EventsPath).AfterSeq(afterSeq, 0)
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

func responseError(resp protocol.Response) error {
	if resp.Error == nil {
		return fmt.Errorf("server returned an unsuccessful response")
	}
	return fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
}

func messageText(ev model.Event) string {
	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		return ""
	}
	text, _ := payload["text"].(string)
	return text
}
