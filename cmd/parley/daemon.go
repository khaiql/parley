package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/artifact"
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
		RunE: func(_ *cobra.Command, _ []string) error {
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
		RunE: func(_ *cobra.Command, _ []string) error {
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
	roomDir, err := p.EnsureRoomDir(cfg.RoomID)
	if err != nil {
		return err
	}
	log := eventlog.New(parleyRuntime.RoomEventsPath(p, cfg.RoomID))
	if _, err := log.Append(model.Event{
		Type:   model.EventRoomStarted,
		RoomID: cfg.RoomID,
		Actor:  cfg.Name,
	}); err != nil {
		return err
	}

	artifactListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	artifactPort := 0
	if tcpAddr, ok := artifactListener.Addr().(*net.TCPAddr); ok {
		artifactPort = tcpAddr.Port
	}
	artifactPath := artifactHTTPPath(cfg.RoomID)
	artifactStore := artifact.NewStore(roomDir)
	srv, err := server.New("127.0.0.1:0", server.Config{
		RoomID:            cfg.RoomID,
		Topic:             cfg.Topic,
		Log:               log,
		ArtifactStore:     artifactStore,
		ArtifactLocalPort: artifactPort,
		ArtifactPath:      artifactPath,
		ArtifactLimits:    artifact.DefaultLimits(),
	})
	if err != nil {
		_ = artifactListener.Close()
		return err
	}
	go srv.Serve()
	artifactHTTP := &http.Server{Handler: srv.ArtifactHandler()}
	artifactErrCh := make(chan error, 1)
	go func() {
		err := artifactHTTP.Serve(artifactListener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		artifactErrCh <- err
	}()

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
				// Cleanup runs after the stop request is accepted by the room
				// daemon select loop, so this synchronous control response can
				// only report that artifact shutdown/cleanup has been requested.
				return adapter.ControlResponse{
					OK:               true,
					Status:           "stopping",
					ArtifactShutdown: "requested",
					ArtifactCleanup: &adapter.ArtifactCleanupStatus{
						Status:  "pending",
						Message: "artifact cleanup runs after stop is accepted",
					},
				}
			default:
				return adapter.ControlResponse{OK: false, Error: "unsupported server control request: " + req.Type}
			}
		})
	}()

	if err := parleyRuntime.SaveRoomRuntime(p, parleyRuntime.RoomRuntime{
		RoomID:            cfg.RoomID,
		Topic:             cfg.Topic,
		LocalHost:         desc.Host,
		LocalPort:         desc.Port,
		ArtifactLocalPort: artifactPort,
		ArtifactPath:      artifactPath,
		ArtifactLimits:    artifact.DefaultLimits(),
		ServerPID:         os.Getpid(),
	}); err != nil {
		_ = artifactHTTP.Close()
		_ = srv.Close()
		return err
	}

	hostErrCh := make(chan error, 1)
	go func() {
		hostErrCh <- runParticipantAdapter(participantDaemonConfig{
			Descriptor: desc,
			Name:       cfg.Name,
			Role:       cfg.Role,
		})
	}()

	closeRoom := func() {
		_ = artifactHTTP.Close()
		_ = srv.Close()
		_ = artifactStore.CleanupAll()
	}

	select {
	case <-stopCh:
		_ = appendRoomStopped(log, cfg, "stop requested")
		closeRoom()
		return nil
	case err := <-hostErrCh:
		closeRoom()
		if err == nil {
			_ = appendRoomStopped(log, cfg, "host left")
			return nil
		}
		return fmt.Errorf("host adapter stopped: %w", err)
	case err := <-controlErrCh:
		closeRoom()
		return fmt.Errorf("server control stopped: %w", err)
	case err := <-artifactErrCh:
		_ = srv.Close()
		if err == nil {
			return nil
		}
		return fmt.Errorf("artifact http stopped: %w", err)
	}
}

func artifactHTTPPath(roomID string) string {
	return "/rooms/" + roomID + "/artifacts"
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

func deriveArtifactEndpoint(desc descriptor.Descriptor, room parleyRuntime.RoomRuntime) string {
	if room.ArtifactLocalPort == 0 {
		return ""
	}
	path := room.ArtifactPath
	if path == "" {
		path = artifactHTTPPath(desc.RoomID)
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "http://" + net.JoinHostPort(desc.Host, fmt.Sprintf("%d", room.ArtifactLocalPort)) + path
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

func (rt *participantAdapterRuntime) uploadArtifacts(files []string) ([]string, []adapter.ArtifactFileResult, error) {
	if len(files) == 0 {
		return nil, nil, nil
	}
	infos, preflightResults, err := preflightArtifactFiles(files)
	if err != nil {
		return nil, preflightResults, err
	}
	type uploadResult struct {
		index int
		meta  artifact.Metadata
		err   error
	}
	concurrency := 4
	if len(files) < concurrency {
		concurrency = len(files)
	}
	sem := make(chan struct{}, concurrency)
	results := make(chan uploadResult, len(files))
	var wg sync.WaitGroup
	for i, info := range infos {
		wg.Add(1)
		go func(index int, filePath string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			meta, err := rt.uploadArtifact(filePath)
			results <- uploadResult{index: index, meta: meta, err: err}
		}(i, info.Path)
	}
	wg.Wait()
	close(results)

	metas := make([]artifact.Metadata, len(infos))
	fileResults := make([]adapter.ArtifactFileResult, len(infos))
	var firstErr error
	for result := range results {
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
		metas[result.index] = result.meta
		fileResults[result.index] = adapter.ArtifactFileResult{
			Path:   infos[result.index].Path,
			Status: "uploaded",
		}
		if result.err != nil {
			fileResults[result.index].Status = "error"
			fileResults[result.index].Error = controlErrorFromError(result.err, "artifact_upload_failed")
		}
	}
	if firstErr != nil {
		_ = rt.cleanupStagedArtifacts()
		return nil, fileResults, firstErr
	}
	ids := make([]string, 0, len(metas))
	for _, meta := range metas {
		ids = append(ids, meta.ID)
	}
	return ids, fileResults, nil
}

func preflightArtifactFiles(files []string) ([]artifact.LocalFileInfo, []adapter.ArtifactFileResult, error) {
	if len(files) > artifact.MaxFilesPerMessage {
		return nil, nil, artifact.Error{Code: "too_many_artifacts", Message: fmt.Sprintf("at most %d artifacts are allowed", artifact.MaxFilesPerMessage)}
	}
	infos := make([]artifact.LocalFileInfo, 0, len(files))
	results := make([]adapter.ArtifactFileResult, 0, len(files))
	var total int64
	var firstErr error
	for _, path := range files {
		info, err := artifact.ValidateLocalFile(path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			results = append(results, adapter.ArtifactFileResult{
				Path:   path,
				Status: "error",
				Error:  controlErrorFromError(err, "invalid_artifacts"),
			})
			continue
		}
		total += info.Size
		if total > artifact.MaxTotalBytesPerMessage {
			return nil, nil, artifact.Error{Code: "artifact_batch_too_large", Message: fmt.Sprintf("artifact batch exceeds %d bytes", artifact.MaxTotalBytesPerMessage)}
		}
		infos = append(infos, info)
		results = append(results, adapter.ArtifactFileResult{Path: path, Status: "validated"})
	}
	if firstErr != nil {
		return nil, results, artifact.Error{Code: "invalid_artifacts", Message: "one or more artifacts failed validation; no uploads started"}
	}
	return infos, results, nil
}

func (rt *participantAdapterRuntime) uploadArtifact(path string) (artifact.Metadata, error) {
	info, err := artifact.ValidateLocalFile(path)
	if err != nil {
		return artifact.Metadata{}, err
	}
	meta, err := rt.store.LoadMeta()
	if err != nil {
		return artifact.Metadata{}, err
	}
	if meta.ArtifactEndpoint == "" {
		return artifact.Metadata{}, adapter.ControlError{Code: "artifact_endpoint_unreachable", Message: "artifact endpoint is not available"}
	}
	before, err := os.Stat(path)
	if err != nil {
		return artifact.Metadata{}, err
	}

	bodyReader, bodyWriter := io.Pipe()
	writer := multipart.NewWriter(bodyWriter)
	copyErrCh := make(chan error, 1)
	go func() {
		copyErrCh <- writeArtifactMultipart(bodyWriter, writer, path, info.Name)
	}()

	req, err := http.NewRequest(http.MethodPost, meta.ArtifactEndpoint+"/staged?participant="+url.QueryEscape(rt.cfg.Name), bodyReader)
	if err != nil {
		_ = bodyReader.Close()
		return artifact.Metadata{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = bodyReader.CloseWithError(err)
		<-copyErrCh
		return artifact.Metadata{}, err
	}
	copyErr := <-copyErrCh
	defer resp.Body.Close()
	if copyErr != nil {
		return artifact.Metadata{}, copyErr
	}
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return artifact.Metadata{}, controlErrorFromHTTPBody(data, "artifact_upload_failed", "artifact upload failed")
	}
	after, err := os.Stat(path)
	if err != nil {
		return artifact.Metadata{}, err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return artifact.Metadata{}, adapter.ControlError{Code: "artifact_upload_failed", Message: fmt.Sprintf("artifact changed while uploading: %s", path)}
	}
	var uploaded artifact.Metadata
	if err := json.NewDecoder(resp.Body).Decode(&uploaded); err != nil {
		return artifact.Metadata{}, err
	}
	return uploaded, nil
}

func (rt *participantAdapterRuntime) cleanupStagedArtifacts() error {
	meta, err := rt.store.LoadMeta()
	if err != nil {
		return err
	}
	if meta.ArtifactEndpoint == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodDelete, meta.ArtifactEndpoint+"/staged?participant="+url.QueryEscape(rt.cfg.Name), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("artifact cleanup failed: %s", strings.TrimSpace(string(data)))
	}
	return nil
}

func writeArtifactMultipart(pipe *io.PipeWriter, writer *multipart.Writer, path, name string) error {
	file, err := os.Open(path)
	if err != nil {
		_ = pipe.CloseWithError(err)
		return err
	}
	defer file.Close()

	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		_ = pipe.CloseWithError(err)
		return err
	}
	_, copyErr := io.Copy(part, file)
	closeWriterErr := writer.Close()
	if copyErr != nil {
		_ = pipe.CloseWithError(copyErr)
		return copyErr
	}
	if closeWriterErr != nil {
		_ = pipe.CloseWithError(closeWriterErr)
		return closeWriterErr
	}
	return pipe.Close()
}

func (rt *participantAdapterRuntime) fetchArtifacts(ids []string, out string) []adapter.ArtifactFetchResult {
	results := make([]adapter.ArtifactFetchResult, 0, len(ids))
	for _, id := range ids {
		path, err := rt.fetchArtifact(id, out, len(ids) > 1)
		if err != nil {
			results = append(results, adapter.ArtifactFetchResult{ID: id, Status: "error", Error: controlErrorFromError(err, "artifact_unavailable")})
			continue
		}
		results = append(results, adapter.ArtifactFetchResult{ID: id, Status: "downloaded", Path: path})
	}
	return results
}

func (rt *participantAdapterRuntime) fetchArtifact(id, out string, multiple bool) (string, error) {
	meta, err := rt.store.LoadMeta()
	if err != nil {
		return "", err
	}
	if meta.ArtifactEndpoint == "" {
		return "", adapter.ControlError{Code: "artifact_endpoint_unreachable", Message: "artifact endpoint is not available"}
	}
	resp, err := http.Get(meta.ArtifactEndpoint + "/" + id)
	if err != nil {
		return "", adapter.ControlError{Code: "artifact_endpoint_unreachable", Message: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return "", controlErrorFromHTTPBody(data, "artifact_unavailable", "artifact fetch failed")
	}
	name := artifactNameFromResponse(resp, id)
	target, err := rt.artifactOutputPath(out, name, multiple)
	if err != nil {
		return "", err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		_ = file.Close()
		_ = os.Remove(target)
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return target, nil
}

func (rt *participantAdapterRuntime) artifactOutputPath(out, name string, multiple bool) (string, error) {
	if out == "" {
		dir := filepath.Join(filepath.Dir(rt.store.MetaPath), "downloads")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", err
		}
		return collisionSafePath(dir, artifact.SanitizeName(name)), nil
	}
	if info, err := os.Stat(out); err == nil {
		if info.IsDir() {
			return collisionSafePath(out, artifact.SanitizeName(name)), nil
		}
		return "", adapter.ControlError{Code: "output_exists", Message: fmt.Sprintf("output path already exists: %s", out)}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if multiple {
		if err := os.MkdirAll(out, 0o700); err != nil {
			return "", err
		}
		return collisionSafePath(out, artifact.SanitizeName(name)), nil
	}
	parent := filepath.Dir(out)
	if _, err := os.Stat(parent); err != nil {
		return "", adapter.ControlError{Code: "invalid_output_path", Message: fmt.Sprintf("output parent is not available: %s", parent)}
	}
	return out, nil
}

func controlErrorFromError(err error, fallbackCode string) *adapter.ControlError {
	if err == nil {
		return nil
	}
	var controlErr adapter.ControlError
	if errors.As(err, &controlErr) {
		return &adapter.ControlError{Code: controlErr.Code, Message: controlErr.Message}
	}
	var artifactErr artifact.Error
	if errors.As(err, &artifactErr) {
		return &adapter.ControlError{Code: artifactErr.Code, Message: artifactErr.Message}
	}
	return &adapter.ControlError{Code: fallbackCode, Message: err.Error()}
}

func controlErrorFromHTTPBody(data []byte, fallbackCode, fallbackMessage string) adapter.ControlError {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err == nil && payload.Error.Code != "" {
		return adapter.ControlError{Code: payload.Error.Code, Message: payload.Error.Message}
	}
	message := strings.TrimSpace(string(data))
	if message == "" {
		message = fallbackMessage
	}
	return adapter.ControlError{Code: fallbackCode, Message: message}
}

func boolRef(v bool) *bool {
	return &v
}

func artifactNameFromResponse(resp *http.Response, fallback string) string {
	disposition := resp.Header.Get("Content-Disposition")
	if disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil && params["filename"] != "" {
			return params["filename"]
		}
	}
	return fallback
}

func collisionSafePath(dir, name string) string {
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
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
