package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/adapter"
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

func TestVersionJSON(t *testing.T) {
	out, err := executeForTest("version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}

	var body struct {
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

func TestJoinRequiresName(t *testing.T) {
	out, err := executeForTest("join", "parley://127.0.0.1:1234/room-1")
	if err == nil {
		t.Fatal("expected join without --name to fail")
	}
	assertJSONErrorCode(t, out, "missing_required_flag")
	if !strings.Contains(string(out), "--name") {
		t.Fatalf("expected --name in error output, got %s", out)
	}
}

func TestStatusWithoutActiveParticipationReturnsJSON(t *testing.T) {
	useParleyHome(t)
	out, err := executeForTest("status")
	if err == nil {
		t.Fatal("expected status without active participation to fail")
	}
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
		OK     bool          `json:"ok"`
		Status string        `json:"status"`
		Events []model.Event `json:"events"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if !body.OK || body.Status != "ok" || len(body.Events) != 1 || body.Events[0].Seq != 1 {
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
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if !body.OK || body.Status != "timeout" {
		t.Fatalf("body = %#v, want timeout terminal state", body)
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
		Events []model.Event `json:"events"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
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

	out, err := executeForTest("start", "--topic", "debug parser", "--name", "codex", "--role", "host")
	if err != nil {
		t.Fatalf("start: %v\n%s", err, out)
	}

	var body struct {
		OK                  bool   `json:"ok"`
		Status              string `json:"status"`
		RoomID              string `json:"room_id"`
		Descriptor          string `json:"descriptor"`
		LocalPort           int    `json:"local_port"`
		ServerPID           int    `json:"server_pid"`
		JoinCommandTemplate string `json:"join_command_template"`
		AgentInstruction    string `json:"agent_instruction"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if !body.OK || body.Status != "started" || body.LocalPort != 49231 || body.ServerPID != 12345 {
		t.Fatalf("start response = %#v", body)
	}
	if body.Descriptor != "parley://127.0.0.1:49231/"+body.RoomID {
		t.Fatalf("descriptor = %q, room id %q", body.Descriptor, body.RoomID)
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

func TestStartRequiresName(t *testing.T) {
	out, err := executeForTest("start", "--topic", "debug parser")
	if err == nil {
		t.Fatal("expected start without --name to fail")
	}
	assertJSONErrorCode(t, out, "missing_required_flag")
	if !strings.Contains(string(out), "--name") {
		t.Fatalf("expected --name in error output, got %s", out)
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
		OK          bool         `json:"ok"`
		Status      string       `json:"status"`
		RoomID      string       `json:"room_id"`
		Name        string       `json:"name"`
		Descriptor  string       `json:"descriptor"`
		PID         int          `json:"pid"`
		Participant adapter.Meta `json:"participant"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if !body.OK || body.Status != "joined" || body.RoomID != "room-1" || body.Name != "alice" || body.PID != 23456 {
		t.Fatalf("join response = %#v", body)
	}
	if body.Participant.Role != "reviewer" || body.Participant.Status != "online" {
		t.Fatalf("participant = %#v", body.Participant)
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
