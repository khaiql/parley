package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/artifact"
	"github.com/khaiql/parley/internal/descriptor"
	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/paths"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
	"github.com/khaiql/parley/internal/server"
)

const artifactHeaderTimeout = 5 * time.Second

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
	artifactHTTP := &http.Server{
		Handler:           srv.ArtifactHandler(),
		ReadHeaderTimeout: artifactHeaderTimeout,
	}
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
