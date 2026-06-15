package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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

func TestRuntimeCommandReturnsNotImplementedJSON(t *testing.T) {
	out, err := executeForTest("status")
	if err == nil {
		t.Fatal("expected status to return not_implemented")
	}
	assertJSONErrorCode(t, out, "not_implemented")
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

func TestInviteRoomFlagReturnsNotImplementedJSON(t *testing.T) {
	out, err := executeForTest("invite", "--room", "room-1")
	if err == nil {
		t.Fatal("expected invite runtime stub to return not_implemented")
	}
	assertJSONErrorCode(t, out, "not_implemented")
}

func TestInboxPeekFlagReturnsNotImplementedJSON(t *testing.T) {
	out, err := executeForTest("inbox", "--peek")
	if err == nil {
		t.Fatal("expected inbox runtime stub to return not_implemented")
	}
	assertJSONErrorCode(t, out, "not_implemented")
}

func TestHistoryFlagsReturnNotImplementedJSON(t *testing.T) {
	for _, args := range [][]string{
		{"history", "--limit", "10"},
		{"history", "--all"},
	} {
		out, err := executeForTest(args...)
		if err == nil {
			t.Fatalf("expected %v runtime stub to return not_implemented", args)
		}
		assertJSONErrorCode(t, out, "not_implemented")
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
