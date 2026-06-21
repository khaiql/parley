package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/adapter"
	"github.com/khaiql/parley/internal/artifact"
	"github.com/khaiql/parley/internal/descriptor"
	"github.com/khaiql/parley/internal/eventlog"
	"github.com/khaiql/parley/internal/model"
	"github.com/khaiql/parley/internal/paths"
	parleyRuntime "github.com/khaiql/parley/internal/runtime"
)

func executeForTest(args ...string) ([]byte, error) {
	buf := new(bytes.Buffer)
	cmd := newRootCmd()
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := execute(cmd)
	return buf.Bytes(), err
}

func assertNoTopLevelOK(t *testing.T, out []byte) {
	t.Helper()
	var body map[string]json.RawMessage
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if _, ok := body["ok"]; ok {
		t.Fatalf("top-level ok key present: %s", out)
	}
}

func assertTopLevelStatus(t *testing.T, out []byte, want string) {
	t.Helper()
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if body.Status != want {
		t.Fatalf("status = %q, want %q\n%s", body.Status, want, out)
	}
}

func useParleyHome(t *testing.T) paths.Paths {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return paths.New(filepath.Join(home, ".parley"))
}

func useShortParleyHome(t *testing.T) paths.Paths {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "parley-cli-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(home); err != nil {
			t.Logf("RemoveAll %s: %v", home, err)
		}
	})
	t.Setenv("HOME", home)
	return paths.New(filepath.Join(home, ".parley"))
}

func serveParticipantControlForMainTest(t *testing.T, p paths.Paths, roomID, name string, handler func(adapter.ControlRequest) adapter.ControlResponse) <-chan adapter.ControlRequest {
	t.Helper()
	store := adapter.NewStore(
		parleyRuntime.ParticipantMetaPath(p, roomID, name),
		parleyRuntime.ParticipantEventsPath(p, roomID, name),
	)
	if err := store.SaveMeta(adapter.Meta{RoomID: roomID, Name: name, Status: "online"}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	socketPath := parleyRuntime.ParticipantSocketPath(p, roomID, name)
	requests := make(chan adapter.ControlRequest, 4)
	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.ServeControl(socketPath, func(req adapter.ControlRequest) adapter.ControlResponse {
			requests <- req
			return handler(req)
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("ServeControl stopped: %v", err)
		default:
		}
		conn, err := net.DialTimeout("unix", socketPath, 10*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return requests
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("control socket did not become ready")
	return requests
}

func TestStopReportsArtifactShutdownAndCleanupStatus(t *testing.T) {
	p := useShortParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	socketPath := parleyRuntime.ServerSocketPath(p, "room-1")
	errCh := make(chan error, 1)
	go func() {
		errCh <- adapter.ServeControl(socketPath, func(req adapter.ControlRequest) adapter.ControlResponse {
			if req.Type != "stop" {
				return adapter.ControlResponse{OK: false, Error: "unexpected request"}
			}
			return adapter.ControlResponse{
				OK:               true,
				Status:           "stopping",
				ArtifactShutdown: "requested",
				ArtifactCleanup: &adapter.ArtifactCleanupStatus{
					Status:  "pending",
					Message: "artifact cleanup runs after stop is accepted",
				},
			}
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("ServeControl stopped: %v", err)
		default:
		}
		conn, err := net.DialTimeout("unix", socketPath, 10*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			goto ready
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server control socket did not become ready")

ready:
	out, err := executeForTest("stop")
	if err != nil {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	var body struct {
		Status           string `json:"status"`
		ArtifactShutdown string `json:"artifact_shutdown"`
		ArtifactCleanup  struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"artifact_cleanup"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if body.Status != "stopping" || body.ArtifactShutdown != "requested" || body.ArtifactCleanup.Status == "" || body.ArtifactCleanup.Message == "" {
		t.Fatalf("stop body = %#v", body)
	}
}

func TestVersionJSON(t *testing.T) {
	out, err := executeForTest("version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	assertNoTopLevelOK(t, out)
	assertTopLevelStatus(t, out, "ok")

	var body struct {
		Status          string `json:"status"`
		Version         string `json:"version"`
		ProtocolVersion string `json:"protocol_version"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if body.Version != "dev" {
		t.Fatalf("version = %q, want dev", body.Version)
	}
	if body.ProtocolVersion != "v1" {
		t.Fatalf("protocol_version = %q, want v1", body.ProtocolVersion)
	}
}

var generatedNameRE = regexp.MustCompile(`^[a-z]+_[a-z]+_[0-9]{4}$`)

func assertGeneratedName(t *testing.T, name string) {
	t.Helper()
	if !generatedNameRE.MatchString(name) {
		t.Fatalf("generated name = %q, want adjective_noun_0000 format", name)
	}
}

func TestJoinGeneratesNameWhenOmitted(t *testing.T) {
	p := useParleyHome(t)
	original := launchParticipantDaemon
	t.Cleanup(func() { launchParticipantDaemon = original })
	launchParticipantDaemon = func(cfg participantDaemonConfig) (int, error) {
		assertGeneratedName(t, cfg.Name)
		store, err := parleyRuntime.ParticipantStore(p, cfg.Descriptor.RoomID, cfg.Name)
		if err != nil {
			t.Fatalf("ParticipantStore: %v", err)
		}
		if err := store.SaveMeta(adapter.Meta{
			RoomID:     cfg.Descriptor.RoomID,
			Name:       cfg.Name,
			Role:       cfg.Role,
			Descriptor: cfg.Descriptor.String(),
			Status:     "online",
		}); err != nil {
			t.Fatalf("SaveMeta: %v", err)
		}
		return 23456, nil
	}

	out, err := executeForTest("join", "parley://127.0.0.1:1234/room-1", "--role", "reviewer")
	if err != nil {
		t.Fatalf("join: %v\n%s", err, out)
	}
	var body struct {
		Name      string `json:"name"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	assertGeneratedName(t, body.Name)
	session, err := parleyRuntime.LoadSession(p, body.SessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if session.Name != body.Name {
		t.Fatalf("session name = %q, want %q", session.Name, body.Name)
	}
}

func TestStatusWithoutActiveParticipationReturnsJSON(t *testing.T) {
	useParleyHome(t)
	out, err := executeForTest("status")
	if err == nil {
		t.Fatal("expected status without active participation to fail")
	}
	assertTopLevelStatus(t, out, "error")
	assertJSONErrorCode(t, out, "no_active_participation")
}

func TestInviteUsesActiveRoomMetadata(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveRoomRuntime(p, parleyRuntime.RoomRuntime{
		RoomID:    "room-1",
		Topic:     "debug parser",
		LocalHost: "127.0.0.1",
		LocalPort: 49231,
	}); err != nil {
		t.Fatalf("SaveRoomRuntime: %v", err)
	}
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}

	out, err := executeForTest("invite")
	if err != nil {
		t.Fatalf("invite: %v\n%s", err, out)
	}

	var body parleyRuntime.InviteResponse
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if body.Descriptor != "parley://127.0.0.1:49231/room-1" {
		t.Fatalf("descriptor = %q", body.Descriptor)
	}
}

func TestInviteUsesSessionRoomMetadata(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveRoomRuntime(p, parleyRuntime.RoomRuntime{
		RoomID:    "room-1",
		Topic:     "debug parser",
		LocalHost: "127.0.0.1",
		LocalPort: 49231,
	}); err != nil {
		t.Fatalf("SaveRoomRuntime: %v", err)
	}
	if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: "psn_test", RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	out, err := executeForTest("invite", "--session", "psn_test")
	if err != nil {
		t.Fatalf("invite: %v\n%s", err, out)
	}

	var body parleyRuntime.InviteResponse
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if body.Descriptor != "parley://127.0.0.1:49231/room-1" {
		t.Fatalf("descriptor = %q", body.Descriptor)
	}
}

func TestSessionsListsSessionMetadata(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveRoomRuntime(p, parleyRuntime.RoomRuntime{
		RoomID:    "room-1",
		Topic:     "debug parser",
		LocalHost: "127.0.0.1",
		LocalPort: 49231,
	}); err != nil {
		t.Fatalf("SaveRoomRuntime: %v", err)
	}
	if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: "psn_test", RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	store := adapter.NewStore(
		parleyRuntime.ParticipantMetaPath(p, "room-1", "codex"),
		parleyRuntime.ParticipantEventsPath(p, "room-1", "codex"),
	)
	if err := store.SaveMeta(adapter.Meta{
		RoomID:          "room-1",
		Name:            "codex",
		Role:            "reviewer",
		Descriptor:      "parley://127.0.0.1:49231/room-1",
		Status:          "online",
		LastReceivedSeq: 8,
		LastSeenSeq:     5,
	}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	out, err := executeForTest("sessions")
	if err != nil {
		t.Fatalf("sessions: %v\n%s", err, out)
	}

	var body struct {
		Status   string `json:"status"`
		Sessions []struct {
			SessionID       string `json:"session_id"`
			RoomID          string `json:"room_id"`
			Name            string `json:"name"`
			Role            string `json:"role"`
			Descriptor      string `json:"descriptor"`
			Status          string `json:"status"`
			AdapterRunning  bool   `json:"adapter_running"`
			ServerRunning   bool   `json:"server_running"`
			LastReceivedSeq int64  `json:"last_received_seq"`
			LastSeenSeq     int64  `json:"last_seen_seq"`
			CommandArgs     string `json:"command_args"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	assertNoTopLevelOK(t, out)
	if body.Status != "sessions" || len(body.Sessions) != 1 {
		t.Fatalf("sessions body = %#v", body)
	}
	got := body.Sessions[0]
	if got.SessionID != "psn_test" || got.RoomID != "room-1" || got.Name != "codex" || got.Role != "reviewer" {
		t.Fatalf("session = %#v", got)
	}
	if got.Descriptor != "parley://127.0.0.1:49231/room-1" || got.Status != "online" {
		t.Fatalf("session descriptor/status = %#v", got)
	}
	if got.LastReceivedSeq != 8 || got.LastSeenSeq != 5 || got.CommandArgs != "--session psn_test" {
		t.Fatalf("session cursors/command args = %#v", got)
	}
}

func TestInboxPeekReadsParticipantMirrorWithoutAdvancingCursor(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	store := adapter.NewStore(
		parleyRuntime.ParticipantMetaPath(p, "room-1", "codex"),
		parleyRuntime.ParticipantEventsPath(p, "room-1", "codex"),
	)
	if err := store.SaveMeta(adapter.Meta{RoomID: "room-1", Name: "codex", LastSeenSeq: 0}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	if err := store.AppendLocal(model.Event{
		Seq:       1,
		Type:      model.EventMessage,
		Timestamp: time.Now().UTC(),
		RoomID:    "room-1",
		Actor:     "alice",
		Payload:   model.MessagePayload{Text: "hello"},
	}); err != nil {
		t.Fatalf("AppendLocal: %v", err)
	}

	out, err := executeForTest("inbox", "--peek")
	if err != nil {
		t.Fatalf("inbox --peek: %v\n%s", err, out)
	}

	var body struct {
		Status string        `json:"status"`
		Events []model.Event `json:"events"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	assertNoTopLevelOK(t, out)
	if body.Status != "unread" || len(body.Events) != 1 || body.Events[0].Seq != 1 {
		t.Fatalf("inbox body = %#v", body)
	}
	meta, err := store.LoadMeta()
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.LastSeenSeq != 0 {
		t.Fatalf("LastSeenSeq = %d, want 0 after peek", meta.LastSeenSeq)
	}
}

func TestInboxWithoutFlagsFailsWhenRoomHasMultipleLocalParticipants(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	for _, name := range []string{"codex", "sle"} {
		store := adapter.NewStore(
			parleyRuntime.ParticipantMetaPath(p, "room-1", name),
			parleyRuntime.ParticipantEventsPath(p, "room-1", name),
		)
		if err := store.SaveMeta(adapter.Meta{RoomID: "room-1", Name: name}); err != nil {
			t.Fatalf("SaveMeta %s: %v", name, err)
		}
	}

	out, err := executeForTest("inbox", "--peek")
	if err == nil {
		t.Fatalf("expected bare inbox to fail when local participants are ambiguous\n%s", out)
	}
	assertJSONErrorCode(t, out, "ambiguous_participation")
}

func TestInboxSessionSelectsParticipantWhenRoomHasMultipleLocalParticipants(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	for _, name := range []string{"codex", "sle"} {
		store := adapter.NewStore(
			parleyRuntime.ParticipantMetaPath(p, "room-1", name),
			parleyRuntime.ParticipantEventsPath(p, "room-1", name),
		)
		if err := store.SaveMeta(adapter.Meta{RoomID: "room-1", Name: name}); err != nil {
			t.Fatalf("SaveMeta %s: %v", name, err)
		}
		if name == "sle" {
			if err := store.AppendLocal(model.Event{
				Seq:     1,
				Type:    model.EventMessage,
				RoomID:  "room-1",
				Actor:   "codex",
				Payload: model.MessagePayload{Text: "hello host"},
			}); err != nil {
				t.Fatalf("AppendLocal: %v", err)
			}
		}
	}
	if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: "psn_test", RoomID: "room-1", Name: "sle"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	out, err := executeForTest("inbox", "--session", "psn_test", "--peek")
	if err != nil {
		t.Fatalf("inbox --session: %v\n%s", err, out)
	}

	var body struct {
		Status string        `json:"status"`
		Events []model.Event `json:"events"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	assertNoTopLevelOK(t, out)
	if body.Status != "unread" || len(body.Events) != 1 || body.Events[0].Actor != "codex" {
		t.Fatalf("inbox body = %#v", body)
	}
}

func TestSessionCannotBeMixedWithRoomOrName(t *testing.T) {
	useParleyHome(t)
	for _, args := range [][]string{
		{"inbox", "--session", "psn_test", "--room", "room-1", "--peek"},
		{"inbox", "--session", "psn_test", "--name", "codex", "--peek"},
	} {
		out, err := executeForTest(args...)
		if err == nil {
			t.Fatalf("expected %v to fail", args)
		}
		assertJSONErrorCode(t, out, "ambiguous_participation")
	}
}

func TestInboxEmptyReturnsStatusAndEventsArray(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	store := adapter.NewStore(
		parleyRuntime.ParticipantMetaPath(p, "room-1", "codex"),
		parleyRuntime.ParticipantEventsPath(p, "room-1", "codex"),
	)
	if err := store.SaveMeta(adapter.Meta{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	out, err := executeForTest("inbox", "--peek")
	if err != nil {
		t.Fatalf("inbox --peek: %v\n%s", err, out)
	}

	var body struct {
		Status string        `json:"status"`
		Events []model.Event `json:"events"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	assertNoTopLevelOK(t, out)
	if body.Status != "empty" || body.Events == nil || len(body.Events) != 0 {
		t.Fatalf("body = %#v, want empty status and empty events array", body)
	}
}

func TestSendMissingSocketReturnsAdapterNotRunningJSON(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}

	out, err := executeForTest("send", "hello")
	if err == nil {
		t.Fatal("expected send without adapter socket to fail")
	}
	assertJSONErrorCode(t, out, "adapter_not_running")
}

func TestSendSessionMissingSocketReturnsAdapterNotRunningJSON(t *testing.T) {
	p := useParleyHome(t)
	store := adapter.NewStore(
		parleyRuntime.ParticipantMetaPath(p, "room-1", "codex"),
		parleyRuntime.ParticipantEventsPath(p, "room-1", "codex"),
	)
	if err := store.SaveMeta(adapter.Meta{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: "psn_test", RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	out, err := executeForTest("send", "--session", "psn_test", "hello")
	if err == nil {
		t.Fatal("expected send without adapter socket to fail")
	}
	assertJSONErrorCode(t, out, "adapter_not_running")
}

func TestSendWithFilePassesFilesAndMessageToAdapter(t *testing.T) {
	p := useShortParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	tracePath := filepath.Join(t.TempDir(), "trace.json")
	if err := os.WriteFile(tracePath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write trace: %v", err)
	}
	requests := serveParticipantControlForMainTest(t, p, "room-1", "codex", func(req adapter.ControlRequest) adapter.ControlResponse {
		if req.Type != "send" {
			return adapter.ControlResponse{OK: false, Error: "unexpected request"}
		}
		return adapter.ControlResponse{OK: true, Status: "sent"}
	})

	out, err := executeForTest("send", "--file", tracePath, "inspect")
	if err != nil {
		t.Fatalf("send --file: %v\n%s", err, out)
	}
	req := <-requests
	if req.Text != "inspect" || len(req.Files) != 1 || req.Files[0] != tracePath {
		t.Fatalf("control request = %#v, want message and file path", req)
	}
	assertTopLevelStatus(t, out, "sent")
}

func TestSendFileOnlyIsValid(t *testing.T) {
	p := useShortParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	tracePath := filepath.Join(t.TempDir(), "trace.json")
	if err := os.WriteFile(tracePath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write trace: %v", err)
	}
	requests := serveParticipantControlForMainTest(t, p, "room-1", "codex", func(req adapter.ControlRequest) adapter.ControlResponse {
		return adapter.ControlResponse{OK: true, Status: "sent"}
	})

	out, err := executeForTest("send", "--file", tracePath)
	if err != nil {
		t.Fatalf("send file only: %v\n%s", err, out)
	}
	req := <-requests
	if req.Text != "" || len(req.Files) != 1 || req.Files[0] != tracePath {
		t.Fatalf("control request = %#v, want file-only send", req)
	}
}

func TestSendFileRejectsDirectoryAndSymlink(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	out, err := executeForTest("send", "--file", dir, "inspect")
	if err == nil {
		t.Fatalf("expected directory send to fail\n%s", out)
	}
	assertArtifactSendValidationError(t, out, dir, "artifact_must_be_file")

	out, err = executeForTest("send", "--file", link, "inspect")
	if err == nil {
		t.Fatalf("expected symlink send to fail\n%s", out)
	}
	assertArtifactSendValidationError(t, out, link, "artifact_must_be_regular_file")
}

func TestSendFileValidationReportsAllErrorsAndSkipsAdapter(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	out, err := executeForTest("send", "--file", dir, "--file", link, "inspect")
	if err == nil {
		t.Fatalf("expected invalid send to fail\n%s", out)
	}
	assertJSONErrorCode(t, out, "invalid_artifacts")
	assertArtifactSendValidationError(t, out, dir, "artifact_must_be_file")
	assertArtifactSendValidationError(t, out, link, "artifact_must_be_regular_file")
}

func TestSendFileValidationReportsTooLargeAndBatchLimitAsFileErrors(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	dir := t.TempDir()
	tooLarge := filepath.Join(dir, "too-large.bin")
	if err := os.WriteFile(tooLarge, []byte(""), 0o600); err != nil {
		t.Fatalf("write too large file: %v", err)
	}
	if err := os.Truncate(tooLarge, artifact.MaxFileBytes+1); err != nil {
		t.Fatalf("truncate too large file: %v", err)
	}

	out, err := executeForTest("send", "--file", tooLarge, "inspect")
	if err == nil {
		t.Fatalf("expected too-large send to fail\n%s", out)
	}
	assertArtifactSendValidationError(t, out, tooLarge, "artifact_too_large")

	var files []string
	args := []string{"send"}
	for i := 0; i < artifact.MaxFilesPerMessage+1; i++ {
		path := filepath.Join(dir, fmt.Sprintf("file-%02d.txt", i))
		if err := os.WriteFile(path, []byte("data"), 0o600); err != nil {
			t.Fatalf("write batch file: %v", err)
		}
		files = append(files, path)
		args = append(args, "--file", path)
	}
	args = append(args, "inspect")

	out, err = executeForTest(args...)
	if err == nil {
		t.Fatalf("expected too-many-artifacts send to fail\n%s", out)
	}
	assertArtifactSendValidationError(t, out, files[0], "too_many_artifacts")
}

func TestSendFileValidationReportsOversizedBatchAsFileErrors(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	dir := t.TempDir()
	var files []string
	args := []string{"send"}
	for i := 0; i < 3; i++ {
		path := filepath.Join(dir, fmt.Sprintf("large-%02d.bin", i))
		if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
			t.Fatalf("write large file: %v", err)
		}
		if err := os.Truncate(path, 90*1024*1024); err != nil {
			t.Fatalf("truncate large file: %v", err)
		}
		files = append(files, path)
		args = append(args, "--file", path)
	}
	args = append(args, "inspect")

	out, err := executeForTest(args...)
	if err == nil {
		t.Fatalf("expected oversized batch send to fail\n%s", out)
	}
	assertArtifactSendValidationError(t, out, files[0], "artifact_batch_too_large")
}

func TestUploadFailureDoesNotExposeStagedIDsAndMarksMessageUncommitted(t *testing.T) {
	p := useShortParleyHome(t)
	if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: "psn_test", RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	tracePath := filepath.Join(t.TempDir(), "trace.json")
	if err := os.WriteFile(tracePath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write trace: %v", err)
	}
	serveParticipantControlForMainTest(t, p, "room-1", "codex", func(req adapter.ControlRequest) adapter.ControlResponse {
		if req.Type != "send" {
			return adapter.ControlResponse{OK: false, Error: "unexpected request"}
		}
		return adapter.ControlResponse{OK: false, Error: "one or more artifacts failed to upload; no message was sent"}
	})

	out, err := executeForTest("send", "--session", "psn_test", "--file", tracePath, "inspect")
	if err == nil {
		t.Fatalf("expected send failure\n%s", out)
	}
	var body struct {
		Status           string `json:"status"`
		MessageCommitted bool   `json:"message_committed"`
		Error            struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if body.Status != "error" || body.Error.Code != "artifact_upload_failed" || body.MessageCommitted {
		t.Fatalf("send failure body = %#v", body)
	}
	if strings.Contains(string(out), "art_") {
		t.Fatalf("send failure exposed staged artifact id: %s", out)
	}
}

func TestUploadFailureReportsFileLevelErrorsWithoutStagedIDs(t *testing.T) {
	p := useShortParleyHome(t)
	if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: "psn_test", RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	tracePath := filepath.Join(t.TempDir(), "trace.json")
	if err := os.WriteFile(tracePath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write trace: %v", err)
	}
	serveParticipantControlForMainTest(t, p, "room-1", "codex", func(req adapter.ControlRequest) adapter.ControlResponse {
		if req.Type != "send" {
			return adapter.ControlResponse{OK: false, Error: "unexpected request"}
		}
		return adapter.ControlResponse{
			OK:    false,
			Error: "one or more artifacts failed to upload; no message was sent",
			Files: []adapter.ArtifactFileResult{{
				Path:   tracePath,
				Status: "error",
				Error:  &adapter.ControlError{Code: "artifact_too_large", Message: "artifact exceeds limit"},
			}},
			MessageCommitted: boolPtr(false),
		}
	})

	out, err := executeForTest("send", "--session", "psn_test", "--file", tracePath, "inspect")
	if err == nil {
		t.Fatalf("expected send failure\n%s", out)
	}
	var body struct {
		Status           string `json:"status"`
		MessageCommitted bool   `json:"message_committed"`
		Files            []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
			Error  struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if body.Status != "error" || body.MessageCommitted || len(body.Files) != 1 {
		t.Fatalf("send failure body = %#v", body)
	}
	if body.Files[0].Path != tracePath || body.Files[0].Status != "error" || body.Files[0].Error.Code != "artifact_too_large" || body.Files[0].Error.Message == "" {
		t.Fatalf("file error = %#v", body.Files[0])
	}
	if strings.Contains(string(out), "art_") {
		t.Fatalf("send failure exposed staged artifact id: %s", out)
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func TestAdapterSendInvalidBatchSkipsUploads(t *testing.T) {
	var uploadCount int
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		uploadCount++
		_ = json.NewEncoder(w).Encode(artifact.Metadata{ID: "art_uploaded", Name: "valid.txt"})
	}))
	defer httpSrv.Close()
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.txt")
	if err := os.WriteFile(valid, []byte("valid"), 0o600); err != nil {
		t.Fatalf("write valid: %v", err)
	}
	invalidDir := filepath.Join(dir, "not-a-file")
	if err := os.Mkdir(invalidDir, 0o700); err != nil {
		t.Fatalf("mkdir invalid dir: %v", err)
	}
	store := adapter.NewStore(filepath.Join(dir, "meta.json"), filepath.Join(dir, "events.jsonl"))
	if err := store.SaveMeta(adapter.Meta{RoomID: "room-1", Name: "codex", ArtifactEndpoint: httpSrv.URL}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	rt := &participantAdapterRuntime{cfg: participantDaemonConfig{Name: "codex"}, store: store}

	resp := rt.handleControl(adapter.ControlRequest{Type: "send", Text: "inspect", Files: []string{valid, invalidDir}})
	if resp.OK {
		t.Fatalf("response = %#v, want invalid batch failure", resp)
	}
	if uploadCount != 0 {
		t.Fatalf("upload count = %d, want 0 for failed preflight", uploadCount)
	}
}

func TestChangedFileDuringUploadFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Fatalf("read upload body: %v", err)
		}
		if err := os.WriteFile(path, []byte("after"), 0o600); err != nil {
			t.Fatalf("mutate source: %v", err)
		}
		_ = json.NewEncoder(w).Encode(artifact.Metadata{ID: "art_uploaded", Name: "trace.json"})
	}))
	defer httpSrv.Close()
	store := adapter.NewStore(filepath.Join(dir, "meta.json"), filepath.Join(dir, "events.jsonl"))
	if err := store.SaveMeta(adapter.Meta{RoomID: "room-1", Name: "codex", ArtifactEndpoint: httpSrv.URL}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	rt := &participantAdapterRuntime{cfg: participantDaemonConfig{Name: "codex"}, store: store}

	_, err := rt.uploadArtifact(path)
	if err == nil {
		t.Fatal("uploadArtifact succeeded after source mutation")
	}
	var controlErr adapter.ControlError
	if !errors.As(err, &controlErr) || controlErr.Code != "artifact_upload_failed" {
		t.Fatalf("uploadArtifact err = %v, want artifact_upload_failed", err)
	}
}

func TestArtifactFetchAfterEndpointStopsReturnsEndpointError(t *testing.T) {
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("still running"))
	}))
	endpoint := httpSrv.URL
	httpSrv.Close()
	dir := t.TempDir()
	store := adapter.NewStore(filepath.Join(dir, "meta.json"), filepath.Join(dir, "events.jsonl"))
	if err := store.SaveMeta(adapter.Meta{RoomID: "room-1", Name: "codex", ArtifactEndpoint: endpoint}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	rt := &participantAdapterRuntime{cfg: participantDaemonConfig{Name: "codex"}, store: store}

	resp := rt.handleControl(adapter.ControlRequest{Type: "artifact_fetch", ArtifactIDs: []string{"art_one"}})
	if !resp.OK || resp.Status != "error" || len(resp.Results) != 1 || resp.Results[0].Error == nil {
		t.Fatalf("response = %#v, want per-artifact endpoint error", resp)
	}
	if resp.Results[0].Error.Code != "artifact_endpoint_unreachable" {
		t.Fatalf("error = %#v, want artifact_endpoint_unreachable", resp.Results[0].Error)
	}
}

func TestArtifactFetchPassesMultipleIDsToAdapter(t *testing.T) {
	p := useShortParleyHome(t)
	if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: "psn_test", RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	requests := serveParticipantControlForMainTest(t, p, "room-1", "codex", func(req adapter.ControlRequest) adapter.ControlResponse {
		if req.Type != "artifact_fetch" {
			return adapter.ControlResponse{OK: false, Error: "unexpected request"}
		}
		return adapter.ControlResponse{OK: true, Status: "downloaded"}
	})

	out, err := executeForTest("artifact", "fetch", "--session", "psn_test", "art_one", "art_two")
	if err != nil {
		t.Fatalf("artifact fetch: %v\n%s", err, out)
	}
	req := <-requests
	if len(req.ArtifactIDs) != 2 || req.ArtifactIDs[0] != "art_one" || req.ArtifactIDs[1] != "art_two" {
		t.Fatalf("artifact ids = %#v, want [art_one art_two]", req.ArtifactIDs)
	}
	assertTopLevelStatus(t, out, "downloaded")
}

func TestArtifactFetchMultipleIDsRejectsFileOut(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: "psn_test", RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	outFile := filepath.Join(t.TempDir(), "trace.json")
	if err := os.WriteFile(outFile, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write out file: %v", err)
	}

	out, err := executeForTest("artifact", "fetch", "--session", "psn_test", "--out", outFile, "art_one", "art_two")
	if err == nil {
		t.Fatalf("expected artifact fetch with file output to fail\n%s", out)
	}
	assertJSONErrorCode(t, out, "invalid_output_path")
}

func TestArtifactFetchAllFailuresReturnsErrorStatus(t *testing.T) {
	rt := newArtifactFetchRuntimeForTest(t, nil)

	resp := rt.handleControl(adapter.ControlRequest{
		Type:        "artifact_fetch",
		ArtifactIDs: []string{"art_missing", "art_gone"},
	})
	if !resp.OK {
		t.Fatalf("response = %#v, want OK transport response", resp)
	}
	if resp.Status != "error" {
		t.Fatalf("status = %q, want error for all failed fetches", resp.Status)
	}
	if len(resp.Results) != 2 || resp.Results[0].Status != "error" || resp.Results[1].Status != "error" {
		t.Fatalf("results = %#v, want per-artifact errors", resp.Results)
	}
}

func TestArtifactFetchFailureJSONUsesStructuredErrors(t *testing.T) {
	rt := newArtifactFetchRuntimeForTest(t, nil)

	resp := rt.handleControl(adapter.ControlRequest{
		Type:        "artifact_fetch",
		ArtifactIDs: []string{"art_missing"},
	})
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var body struct {
		Results []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, data)
	}
	if len(body.Results) != 1 || body.Results[0].Error.Code != "artifact_unavailable" || body.Results[0].Error.Message == "" {
		t.Fatalf("fetch response = %s, want structured artifact_unavailable error", data)
	}
}

func TestArtifactFetchPartialResults(t *testing.T) {
	rt := newArtifactFetchRuntimeForTest(t, map[string]string{"art_ok": "trace.json"})

	resp := rt.handleControl(adapter.ControlRequest{
		Type:        "artifact_fetch",
		ArtifactIDs: []string{"art_ok", "art_missing"},
	})
	if !resp.OK {
		t.Fatalf("response = %#v, want OK transport response", resp)
	}
	if resp.Status != "partial" {
		t.Fatalf("status = %q, want partial", resp.Status)
	}
	if len(resp.Results) != 2 || resp.Results[0].Status != "downloaded" || resp.Results[1].Status != "error" {
		t.Fatalf("results = %#v, want mixed download/error", resp.Results)
	}
}

func TestArtifactFetchSingleIDExistingDirectoryWritesInsideDirectory(t *testing.T) {
	rt := newArtifactFetchRuntimeForTest(t, map[string]string{"art_one": "trace.json"})
	outDir := t.TempDir()

	results := rt.fetchArtifacts([]string{"art_one"}, outDir)
	if len(results) != 1 || results[0].Status != "downloaded" {
		t.Fatalf("results = %#v, want one downloaded artifact", results)
	}
	want := filepath.Join(outDir, "trace.json")
	if results[0].Path != want {
		t.Fatalf("path = %q, want %q", results[0].Path, want)
	}
	if data, err := os.ReadFile(want); err != nil || string(data) != "bytes for art_one" {
		t.Fatalf("read fetched artifact data = %q err = %v", data, err)
	}
}

func TestArtifactFetchExplicitFileRefusesOverwrite(t *testing.T) {
	rt := newArtifactFetchRuntimeForTest(t, map[string]string{"art_one": "trace.json"})
	outFile := filepath.Join(t.TempDir(), "trace.json")
	if err := os.WriteFile(outFile, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	results := rt.fetchArtifacts([]string{"art_one"}, outFile)
	if len(results) != 1 || results[0].Status != "error" {
		t.Fatalf("results = %#v, want output overwrite error", results)
	}
	if data, err := os.ReadFile(outFile); err != nil || string(data) != "existing" {
		t.Fatalf("existing output changed to %q err=%v", data, err)
	}
}

func TestArtifactFetchMissingParentFails(t *testing.T) {
	rt := newArtifactFetchRuntimeForTest(t, map[string]string{"art_one": "trace.json"})
	outFile := filepath.Join(t.TempDir(), "missing", "trace.json")

	results := rt.fetchArtifacts([]string{"art_one"}, outFile)
	if len(results) != 1 || results[0].Status != "error" {
		t.Fatalf("results = %#v, want missing parent error", results)
	}
}

func TestArtifactFetchDefaultDirUsesFreshCollisionSafePath(t *testing.T) {
	rt := newArtifactFetchRuntimeForTest(t, map[string]string{"art_one": "trace.json"})
	defaultDir := rt.store.DefaultDownloadsDir()
	if err := os.MkdirAll(defaultDir, 0o700); err != nil {
		t.Fatalf("MkdirAll downloads: %v", err)
	}
	existing := filepath.Join(defaultDir, "trace.json")
	if err := os.WriteFile(existing, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing default output: %v", err)
	}

	results := rt.fetchArtifacts([]string{"art_one"}, "")
	if len(results) != 1 || results[0].Status != "downloaded" {
		t.Fatalf("results = %#v, want downloaded collision-safe file", results)
	}
	if filepath.Base(results[0].Path) != "trace-1.json" {
		t.Fatalf("path = %q, want trace-1.json", results[0].Path)
	}
	if data, err := os.ReadFile(existing); err != nil || string(data) != "existing" {
		t.Fatalf("existing default output changed to %q err=%v", data, err)
	}
}

func TestArtifactFetchDuplicateBasenamesUseCollisionSafeNames(t *testing.T) {
	rt := newArtifactFetchRuntimeForTest(t, map[string]string{
		"art_one": "trace.json",
		"art_two": "trace.json",
	})
	outDir := filepath.Join(t.TempDir(), "downloads")

	results := rt.fetchArtifacts([]string{"art_one", "art_two"}, outDir)
	if len(results) != 2 || results[0].Status != "downloaded" || results[1].Status != "downloaded" {
		t.Fatalf("results = %#v, want two downloaded artifacts", results)
	}
	if results[0].Path == results[1].Path {
		t.Fatalf("duplicate basename paths matched: %#v", results)
	}
	if filepath.Base(results[0].Path) != "trace.json" || filepath.Base(results[1].Path) != "trace-1.json" {
		t.Fatalf("paths = %q, %q; want collision-safe trace names", results[0].Path, results[1].Path)
	}
}

func TestSessionsIncludesArtifactEndpointFields(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveRoomRuntime(p, parleyRuntime.RoomRuntime{
		RoomID:            "room-1",
		LocalHost:         "127.0.0.1",
		LocalPort:         49231,
		ArtifactLocalPort: 49232,
		ArtifactPath:      "/rooms/room-1/artifacts",
	}); err != nil {
		t.Fatalf("SaveRoomRuntime: %v", err)
	}
	if err := parleyRuntime.SaveSession(p, parleyRuntime.Session{ID: "psn_test", RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	store := adapter.NewStore(
		parleyRuntime.ParticipantMetaPath(p, "room-1", "codex"),
		parleyRuntime.ParticipantEventsPath(p, "room-1", "codex"),
	)
	if err := store.SaveMeta(adapter.Meta{
		RoomID:           "room-1",
		Name:             "codex",
		Descriptor:       "parley://127.0.0.1:49231/room-1",
		ArtifactEndpoint: "http://127.0.0.1:49232/rooms/room-1/artifacts",
	}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	out, err := executeForTest("sessions")
	if err != nil {
		t.Fatalf("sessions: %v\n%s", err, out)
	}
	var body struct {
		Sessions []struct {
			ArtifactEndpoint  string `json:"artifact_endpoint"`
			ArtifactLocalPort int    `json:"artifact_local_port"`
			ArtifactPath      string `json:"artifact_path"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if len(body.Sessions) != 1 || body.Sessions[0].ArtifactEndpoint != "http://127.0.0.1:49232/rooms/room-1/artifacts" {
		t.Fatalf("sessions = %#v", body.Sessions)
	}
	if body.Sessions[0].ArtifactLocalPort != 49232 || body.Sessions[0].ArtifactPath != "/rooms/room-1/artifacts" {
		t.Fatalf("artifact endpoint metadata = %#v", body.Sessions[0])
	}
}

func newArtifactFetchRuntimeForTest(t *testing.T, names map[string]string) *participantAdapterRuntime {
	t.Helper()
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := filepath.Base(r.URL.Path)
		name, ok := names[id]
		if !ok {
			http.Error(w, `{"error":{"code":"artifact_unavailable","message":"artifact is not available"}}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		_, _ = fmt.Fprintf(w, "bytes for %s", id)
	}))
	t.Cleanup(httpSrv.Close)

	dir := t.TempDir()
	store := adapter.NewStore(filepath.Join(dir, "meta.json"), filepath.Join(dir, "events.jsonl"))
	if err := store.SaveMeta(adapter.Meta{
		RoomID:           "room-1",
		Name:             "codex",
		ArtifactEndpoint: httpSrv.URL,
	}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	return &participantAdapterRuntime{
		cfg:   participantDaemonConfig{Name: "codex"},
		store: store,
	}
}

func TestPartialParticipationFlagsDoNotMixWithActive(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-a", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}

	out, err := executeForTest("status", "--room", "room-b")
	if err == nil {
		t.Fatal("expected partial participation flags to fail")
	}
	assertJSONErrorCode(t, out, "ambiguous_participation")
}

func TestSocketCommandPartialParticipationFlagsReturnAmbiguous(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-a", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}

	out, err := executeForTest("send", "--room", "room-b", "hello")
	if err == nil {
		t.Fatal("expected partial socket command flags to fail")
	}
	assertJSONErrorCode(t, out, "ambiguous_participation")
}

func TestWaitRequiresTimeout(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}

	out, err := executeForTest("wait")
	if err == nil {
		t.Fatal("expected wait without timeout to fail")
	}
	assertJSONErrorCode(t, out, "missing_required_flag")
}

func TestWaitSocketTimeoutReturnsTerminalState(t *testing.T) {
	p := useShortParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	socketPath := parleyRuntime.ParticipantSocketPath(p, "room-1", "codex")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		t.Fatalf("MkdirAll socket dir: %v", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	out, err := executeForTest("wait", "--timeout", "25ms")
	if err != nil {
		t.Fatalf("wait timeout: %v\n%s", err, out)
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	default:
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	assertNoTopLevelOK(t, out)
	if body.Status != "timeout" {
		t.Fatalf("body = %#v, want timeout terminal state", body)
	}
}

func TestWaitDoesNotAdvanceSeenCursor(t *testing.T) {
	dir := t.TempDir()
	store := adapter.NewStore(
		filepath.Join(dir, "participant.json"),
		filepath.Join(dir, "events.jsonl"),
	)
	if err := store.SaveMeta(adapter.Meta{RoomID: "room-1", Name: "me", LastSeenSeq: 0}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	for _, ev := range []model.Event{
		{Seq: 1, Type: model.EventParticipantJoined, RoomID: "room-1", Actor: "alice"},
		{Seq: 2, Type: model.EventMessage, RoomID: "room-1", Actor: "alice", Payload: model.MessagePayload{Text: "hello"}},
	} {
		if err := store.AppendLocal(ev); err != nil {
			t.Fatalf("AppendLocal seq %d: %v", ev.Seq, err)
		}
	}
	rt := &participantAdapterRuntime{
		cfg:    participantDaemonConfig{Name: "me"},
		store:  store,
		notify: make(chan struct{}, 1),
	}

	events, timedOut, err := rt.wait("25ms")
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if timedOut {
		t.Fatal("wait timed out, want ready events")
	}
	if len(events) != 2 || events[1].Seq != 2 {
		t.Fatalf("events = %#v, want batch through message seq 2", events)
	}
	meta, err := store.LoadMeta()
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if meta.LastSeenSeq != 0 {
		t.Fatalf("LastSeenSeq = %d, want wait to leave cursor unchanged", meta.LastSeenSeq)
	}
	inbox, err := store.Inbox(true)
	if err != nil {
		t.Fatalf("Inbox peek: %v", err)
	}
	if len(inbox) != 2 || inbox[1].Seq != 2 {
		t.Fatalf("inbox = %#v, want wait events to remain unread", inbox)
	}
}

func TestArtifactEndpointUsesDescriptorHost(t *testing.T) {
	got := deriveArtifactEndpoint(
		descriptor.Descriptor{Host: "tunnel.example", Port: 4000, RoomID: "room-1"},
		parleyRuntime.RoomRuntime{RoomID: "room-1", ArtifactLocalPort: 5000, ArtifactPath: "/rooms/room-1/artifacts"},
	)
	if got != "http://tunnel.example:5000/rooms/room-1/artifacts" {
		t.Fatalf("endpoint = %q, want descriptor host with artifact port", got)
	}
}

func TestInfoIncludesArtifactEndpointMetadataAndLimits(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	if err := parleyRuntime.SaveRoomRuntime(p, parleyRuntime.RoomRuntime{
		RoomID:            "room-1",
		LocalHost:         "127.0.0.1",
		LocalPort:         49231,
		ArtifactLocalPort: 49232,
		ArtifactPath:      "/rooms/room-1/artifacts",
		ArtifactLimits:    artifact.DefaultLimits(),
	}); err != nil {
		t.Fatalf("SaveRoomRuntime: %v", err)
	}
	store := adapter.NewStore(
		parleyRuntime.ParticipantMetaPath(p, "room-1", "codex"),
		parleyRuntime.ParticipantEventsPath(p, "room-1", "codex"),
	)
	if err := store.SaveMeta(adapter.Meta{
		RoomID:           "room-1",
		Name:             "codex",
		ArtifactEndpoint: "http://127.0.0.1:49232/rooms/room-1/artifacts",
	}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	out, err := executeForTest("info")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}

	var body struct {
		Status            string          `json:"status"`
		ArtifactLocalPort int             `json:"artifact_local_port"`
		ArtifactPath      string          `json:"artifact_path"`
		ArtifactLimits    artifact.Limits `json:"artifact_limits"`
		Participant       adapter.Meta    `json:"participant"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if body.Status != "info" || body.ArtifactLocalPort != 49232 || body.ArtifactPath != "/rooms/room-1/artifacts" {
		t.Fatalf("info artifact fields = %#v", body)
	}
	if body.ArtifactLimits != artifact.DefaultLimits() {
		t.Fatalf("limits = %#v, want defaults", body.ArtifactLimits)
	}
	if body.Participant.ArtifactEndpoint != "http://127.0.0.1:49232/rooms/room-1/artifacts" {
		t.Fatalf("participant endpoint = %q", body.Participant.ArtifactEndpoint)
	}
}

func TestInfoCorruptRuntimeReturnsRuntimeError(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	roomDir, err := p.EnsureRoomDir("room-1")
	if err != nil {
		t.Fatalf("EnsureRoomDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(roomDir, "runtime.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt runtime: %v", err)
	}

	out, err := executeForTest("info")
	if err == nil {
		t.Fatal("expected info with corrupt runtime to fail")
	}
	assertJSONErrorCode(t, out, "runtime_error")
}

func TestStatusCorruptParticipantMetaReturnsRuntimeError(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	metaPath := parleyRuntime.ParticipantMetaPath(p, "room-1", "codex")
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o700); err != nil {
		t.Fatalf("MkdirAll meta dir: %v", err)
	}
	if err := os.WriteFile(metaPath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt meta: %v", err)
	}

	out, err := executeForTest("status")
	if err == nil {
		t.Fatal("expected status with corrupt participant meta to fail")
	}
	assertJSONErrorCode(t, out, "runtime_error")
}

func TestHistoryLimitReturnsBoundedTranscriptEvents(t *testing.T) {
	p := useParleyHome(t)
	if err := parleyRuntime.SaveActive(p, parleyRuntime.ActiveParticipation{RoomID: "room-1", Name: "codex"}); err != nil {
		t.Fatalf("SaveActive: %v", err)
	}
	log := eventlog.New(parleyRuntime.RoomEventsPath(p, "room-1"))
	events := []model.Event{
		{Type: model.EventMessage, RoomID: "room-1", Actor: "alice", Payload: model.MessagePayload{Text: "one"}},
		{Type: model.EventType("internal.debug"), RoomID: "room-1", Actor: "system"},
		{Type: model.EventParticipantJoined, RoomID: "room-1", Actor: "bob"},
		{Type: model.EventMessage, RoomID: "room-1", Actor: "carol", Payload: model.MessagePayload{Text: "two"}},
	}
	for _, ev := range events {
		if _, err := log.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	out, err := executeForTest("history", "--limit", "2")
	if err != nil {
		t.Fatalf("history: %v\n%s", err, out)
	}

	var body struct {
		Status string        `json:"status"`
		Events []model.Event `json:"events"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	assertNoTopLevelOK(t, out)
	if body.Status != "history" {
		t.Fatalf("status = %q, want history", body.Status)
	}
	if len(body.Events) != 2 {
		t.Fatalf("events = %#v, want 2", body.Events)
	}
	if body.Events[0].Type != model.EventParticipantJoined || body.Events[1].Type != model.EventMessage {
		t.Fatalf("events = %#v, want last two transcript events", body.Events)
	}
}

func TestStartLaunchesRoomDaemonAndPrintsInvite(t *testing.T) {
	p := useParleyHome(t)
	original := launchRoomDaemon
	t.Cleanup(func() { launchRoomDaemon = original })
	launchRoomDaemon = func(cfg roomDaemonConfig) (int, error) {
		if cfg.Topic != "debug parser" || cfg.Name != "codex" || cfg.Role != "host" {
			t.Fatalf("room daemon config = %#v", cfg)
		}
		if cfg.RoomID == "" {
			t.Fatal("room daemon config missing room id")
		}
		if err := parleyRuntime.SaveRoomRuntime(p, parleyRuntime.RoomRuntime{
			RoomID:            cfg.RoomID,
			Topic:             cfg.Topic,
			LocalHost:         "127.0.0.1",
			LocalPort:         49231,
			ServerPID:         12345,
			ArtifactLocalPort: 49232,
			ArtifactPath:      "/rooms/" + cfg.RoomID + "/artifacts",
			ArtifactLimits:    artifact.DefaultLimits(),
		}); err != nil {
			t.Fatalf("SaveRoomRuntime: %v", err)
		}
		return 12345, nil
	}

	out, err := executeForTest("start", "--topic", "debug parser", "--name", "codex", "--role", "host")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}

	var body struct {
		Status              string          `json:"status"`
		RoomID              string          `json:"room_id"`
		Name                string          `json:"name"`
		SessionID           string          `json:"session_id"`
		CommandArgs         string          `json:"command_args"`
		Descriptor          string          `json:"descriptor"`
		LocalPort           int             `json:"local_port"`
		ArtifactLocalPort   int             `json:"artifact_local_port"`
		ArtifactPath        string          `json:"artifact_path"`
		ArtifactLimits      artifact.Limits `json:"artifact_limits"`
		ServerPID           int             `json:"server_pid"`
		JoinCommandTemplate string          `json:"join_command_template"`
		AgentInstruction    string          `json:"agent_instruction"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	assertNoTopLevelOK(t, out)
	if body.Status != "started" || body.LocalPort != 49231 || body.ServerPID != 12345 {
		t.Fatalf("start response = %#v", body)
	}
	if body.ArtifactLocalPort != 49232 || body.ArtifactPath != "/rooms/"+body.RoomID+"/artifacts" {
		t.Fatalf("artifact endpoint fields = port %d path %q", body.ArtifactLocalPort, body.ArtifactPath)
	}
	if body.ArtifactLimits != artifact.DefaultLimits() {
		t.Fatalf("artifact limits = %#v, want defaults", body.ArtifactLimits)
	}
	if body.Name != "codex" {
		t.Fatalf("name = %q, want codex", body.Name)
	}
	if body.Descriptor != "parley://127.0.0.1:49231/"+body.RoomID {
		t.Fatalf("descriptor = %q, room id %q", body.Descriptor, body.RoomID)
	}
	if body.SessionID == "" || !strings.Contains(body.CommandArgs, body.SessionID) {
		t.Fatalf("session fields = %q %q", body.SessionID, body.CommandArgs)
	}
	session, err := parleyRuntime.LoadSession(p, body.SessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if session.RoomID != body.RoomID || session.Name != "codex" {
		t.Fatalf("session = %#v, want room %q codex", session, body.RoomID)
	}
	if !strings.Contains(body.JoinCommandTemplate, body.Descriptor) || !strings.Contains(body.AgentInstruction, body.Descriptor) {
		t.Fatalf("invite fields missing descriptor: %#v", body)
	}
	active, err := parleyRuntime.LoadActive(p)
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if active.RoomID != body.RoomID || active.Name != "codex" {
		t.Fatalf("active = %#v, want room %q codex", active, body.RoomID)
	}
}

func TestStartGeneratesNameWhenOmitted(t *testing.T) {
	p := useParleyHome(t)
	original := launchRoomDaemon
	t.Cleanup(func() { launchRoomDaemon = original })
	launchRoomDaemon = func(cfg roomDaemonConfig) (int, error) {
		assertGeneratedName(t, cfg.Name)
		if err := parleyRuntime.SaveRoomRuntime(p, parleyRuntime.RoomRuntime{
			RoomID:    cfg.RoomID,
			Topic:     cfg.Topic,
			LocalHost: "127.0.0.1",
			LocalPort: 49231,
			ServerPID: 12345,
		}); err != nil {
			t.Fatalf("SaveRoomRuntime: %v", err)
		}
		return 12345, nil
	}

	out, err := executeForTest("start", "--topic", "debug parser")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}
	var body struct {
		RoomID    string `json:"room_id"`
		Name      string `json:"name"`
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	assertGeneratedName(t, body.Name)
	session, err := parleyRuntime.LoadSession(p, body.SessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if session.RoomID != body.RoomID || session.Name != body.Name {
		t.Fatalf("session = %#v, want room %q name %q", session, body.RoomID, body.Name)
	}
	active, err := parleyRuntime.LoadActive(p)
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if active.RoomID != body.RoomID || active.Name != body.Name {
		t.Fatalf("active = %#v, want room %q name %q", active, body.RoomID, body.Name)
	}
}

func TestSendRequiresMessage(t *testing.T) {
	out, err := executeForTest("send")
	if err == nil {
		t.Fatal("expected send without message to fail")
	}
	assertJSONErrorCode(t, out, "invalid_arguments")
}

func TestNoArgCommandValidationReturnsJSON(t *testing.T) {
	out, err := executeForTest("status", "extra")
	if err == nil {
		t.Fatal("expected status with extra arg to fail")
	}
	assertJSONErrorCode(t, out, "invalid_arguments")
}

func TestWaitBadTimeoutReturnsJSON(t *testing.T) {
	out, err := executeForTest("wait", "--timeout", "nope")
	if err == nil {
		t.Fatal("expected wait with bad timeout to fail")
	}
	assertJSONErrorCode(t, out, "invalid_arguments")
}

func TestUnknownCommandReturnsJSON(t *testing.T) {
	out, err := executeForTest("bogus")
	if err == nil {
		t.Fatal("expected unknown command to fail")
	}
	assertJSONErrorCode(t, out, "invalid_arguments")
}

func TestUnknownFlagReturnsJSON(t *testing.T) {
	out, err := executeForTest("status", "--bogus")
	if err == nil {
		t.Fatal("expected unknown flag to fail")
	}
	assertJSONErrorCode(t, out, "invalid_arguments")
}

func TestJoinLaunchesParticipantDaemon(t *testing.T) {
	p := useParleyHome(t)
	original := launchParticipantDaemon
	t.Cleanup(func() { launchParticipantDaemon = original })
	launchParticipantDaemon = func(cfg participantDaemonConfig) (int, error) {
		if cfg.Descriptor.String() != "parley://127.0.0.1:1234/room-1" ||
			cfg.Name != "alice" ||
			cfg.Role != "reviewer" ||
			cfg.Directory != "/tmp/project" ||
			cfg.Repo != "https://github.com/example/repo" {
			t.Fatalf("participant daemon config = %#v", cfg)
		}
		store, err := parleyRuntime.ParticipantStore(p, cfg.Descriptor.RoomID, cfg.Name)
		if err != nil {
			t.Fatalf("ParticipantStore: %v", err)
		}
		if err := store.SaveMeta(adapter.Meta{
			RoomID:     cfg.Descriptor.RoomID,
			Name:       cfg.Name,
			Role:       cfg.Role,
			Descriptor: cfg.Descriptor.String(),
			Status:     "online",
		}); err != nil {
			t.Fatalf("SaveMeta: %v", err)
		}
		return 23456, nil
	}

	out, err := executeForTest(
		"join",
		"parley://127.0.0.1:1234/room-1",
		"--name", "alice",
		"--role", "reviewer",
		"--dir", "/tmp/project",
		"--repo", "https://github.com/example/repo",
	)
	if err != nil {
		t.Fatalf("join: %v\n%s", err, out)
	}
	var body struct {
		Status      string       `json:"status"`
		RoomID      string       `json:"room_id"`
		Name        string       `json:"name"`
		SessionID   string       `json:"session_id"`
		CommandArgs string       `json:"command_args"`
		Descriptor  string       `json:"descriptor"`
		PID         int          `json:"pid"`
		Participant adapter.Meta `json:"participant"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	assertNoTopLevelOK(t, out)
	if body.Status != "joined" || body.RoomID != "room-1" || body.Name != "alice" || body.PID != 23456 {
		t.Fatalf("join response = %#v", body)
	}
	if body.Participant.Role != "reviewer" || body.Participant.Status != "online" {
		t.Fatalf("participant = %#v", body.Participant)
	}
	if body.SessionID == "" || !strings.Contains(body.CommandArgs, body.SessionID) {
		t.Fatalf("session fields = %q %q", body.SessionID, body.CommandArgs)
	}
	session, err := parleyRuntime.LoadSession(p, body.SessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if session.RoomID != "room-1" || session.Name != "alice" {
		t.Fatalf("session = %#v, want alice in room-1", session)
	}
	active, err := parleyRuntime.LoadActive(p)
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if active.RoomID != "room-1" || active.Name != "alice" {
		t.Fatalf("active = %#v, want alice in room-1", active)
	}
}

func TestInviteMissingRuntimeReturnsJSON(t *testing.T) {
	useParleyHome(t)
	out, err := executeForTest("invite", "--room", "room-1")
	if err == nil {
		t.Fatal("expected invite without runtime metadata to fail")
	}
	assertJSONErrorCode(t, out, "room_runtime_not_found")
}

func TestInboxWithoutActiveParticipationReturnsJSON(t *testing.T) {
	useParleyHome(t)
	out, err := executeForTest("inbox", "--peek")
	if err == nil {
		t.Fatal("expected inbox without active participation to fail")
	}
	assertJSONErrorCode(t, out, "no_active_participation")
}

func TestHistoryFlagsWithoutActiveParticipationReturnJSON(t *testing.T) {
	useParleyHome(t)
	for _, args := range [][]string{
		{"history", "--limit", "10"},
		{"history", "--all"},
	} {
		out, err := executeForTest(args...)
		if err == nil {
			t.Fatalf("expected %v without active participation to fail", args)
		}
		assertJSONErrorCode(t, out, "no_active_participation")
	}
}

func TestJoinValidatesDescriptor(t *testing.T) {
	out, err := executeForTest("join", "--name", "alice", "not-a-descriptor")
	if err == nil {
		t.Fatal("expected invalid descriptor to fail")
	}
	assertJSONErrorCode(t, out, "invalid_descriptor")
}

func assertJSONErrorCode(t *testing.T, out []byte, want string) {
	t.Helper()

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json error: %v\n%s", err, out)
	}
	if body.Error.Code != want {
		t.Fatalf("error.code = %q, want %q\n%s", body.Error.Code, want, out)
	}
}

func assertArtifactSendValidationError(t *testing.T, out []byte, wantPath, wantCode string) {
	t.Helper()

	var body struct {
		Status           string `json:"status"`
		MessageCommitted bool   `json:"message_committed"`
		Error            struct {
			Code string `json:"code"`
		} `json:"error"`
		Files []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
			Error  struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json error: %v\n%s", err, out)
	}
	if body.Status != "error" || body.MessageCommitted {
		t.Fatalf("artifact send status = %q committed = %v\n%s", body.Status, body.MessageCommitted, out)
	}
	if body.Error.Code == "" {
		t.Fatalf("artifact send error code missing\n%s", out)
	}
	for _, file := range body.Files {
		if file.Path == wantPath {
			if file.Status != "error" || file.Error.Code != wantCode || file.Error.Message == "" {
				t.Fatalf("file error = %#v, want code %q\n%s", file, wantCode, out)
			}
			if strings.Contains(string(out), "art_") {
				t.Fatalf("send failure exposed staged artifact id: %s", out)
			}
			return
		}
	}
	t.Fatalf("file error for %q not found\n%s", wantPath, out)
}
