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
	err := cmd.Execute()
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
