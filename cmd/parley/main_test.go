package main

import (
	"bytes"
	"encoding/json"
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

func TestStartDocumentedFlagsReturnNotImplementedJSON(t *testing.T) {
	out, err := executeForTest("start", "--topic", "debug parser", "--name", "codex", "--role", "host")
	if err == nil {
		t.Fatal("expected start runtime stub to return not_implemented")
	}
	assertJSONErrorCode(t, out, "not_implemented")
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

func TestJoinMetadataFlagsReturnNotImplementedJSON(t *testing.T) {
	out, err := executeForTest(
		"join",
		"parley://127.0.0.1:1234/room-1",
		"--name", "alice",
		"--role", "reviewer",
		"--dir", "/tmp/project",
		"--repo", "https://github.com/example/repo",
	)
	if err == nil {
		t.Fatal("expected join runtime stub to return not_implemented")
	}
	assertJSONErrorCode(t, out, "not_implemented")
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
