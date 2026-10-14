package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/khaiql/parley/internal/herdr"
)

func TestHerdrRPCRejectBeforeProcess(t *testing.T) {
	calls := 0
	c := &herdr.Client{Run: func(context.Context, string, []string, []byte) ([]byte, error) { calls++; return nil, nil }}
	for _, s := range []string{`{"operation":"probe","session":"agents","path":"/tmp"}`, `{"operation":"join","session":"agents"}`} {
		if _, e := runHerdrRPC(context.Background(), strings.NewReader(s), &bytes.Buffer{}, c); e == nil {
			t.Fatal(s)
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}

// Exercise the CLI and its native socket boundary, including failures before OpenForm.
func TestHerdrActionVisibleFailure(t *testing.T) {
	for _, tc := range []struct{ name, code, hint string }{
		{"shell", "herdr_failed", "agent pane"},
		{"identity", "herdr_failed", "/hooks"},
		{"modal", "herdr_failed", "modal"},
		{"version", "helper_incompatible", "0.9.3"},
		{"helper", "helper_unavailable", "install"},
		{"context", "native_context_required", "Herdr"},
		{"notification", "herdr_failed", "agent pane"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := nativeActionFixture(t, tc.name)
			cmd := newRootCmd()
			var out, diagnostic bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&diagnostic)
			cmd.SetArgs([]string{"herdr", "action", "start"})
			if err := execute(cmd); err == nil {
				t.Fatal("failed action reported success")
			}
			var envelope struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope.Error.Code != tc.code {
				t.Fatalf("out=%s diagnostic=%s err=%v", &out, &diagnostic, err)
			}
			if tc.name == "notification" && !strings.Contains(envelope.Error.Message, "notification_failed") {
				t.Fatal("undelivered notification not diagnosed", envelope.Error.Message)
			}
			select {
			case frame := <-frames:
				if frame.Method != "notification.show" || frame.Params["sound"] != "none" || !strings.Contains(strings.ToLower(fmt.Sprint(frame.Params["body"])), strings.ToLower(tc.hint)) {
					t.Fatalf("missing actionable notification: %+v", frame)
				}
			case <-time.After(time.Second):
				t.Fatal("failure returned without a visible notification")
			}
		})
	}
}

func TestHerdrActionCapturesOriginalTarget(t *testing.T) {
	for _, op := range []string{"start", "join"} {
		t.Run(op, func(t *testing.T) {
			frames := nativeActionFixture(t, "success")
			cmd := newRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"herdr", "action", op})
			if err := execute(cmd); err != nil || !strings.Contains(out.String(), "opened") {
				t.Fatal(out.String(), err)
			}
			frame := <-frames
			env := frame.Params["env"].(map[string]any)
			var target herdr.AgentTarget
			if err := json.Unmarshal([]byte(env["PARLEY_TARGET_JSON"].(string)), &target); err != nil {
				t.Fatal(err)
			}
			if frame.Method != "plugin.pane.open" || env["PARLEY_ACTION"] != op || target.PaneID != "w1:p1" || target.TerminalID != "term_original" || target.NativeSession != "original" || target.HerdrSocket != os.Getenv("HERDR_SOCKET_PATH") {
				t.Fatalf("popup lost original agent: %+v %+v", frame, target)
			}
		})
	}
}

func TestHerdrFormFailedOutcome(t *testing.T) {
	frames := nativeActionFixture(t, "success")
	t.Setenv("PARLEY_ACTION", "join")
	t.Setenv("PARLEY_TARGET_JSON", fmt.Sprintf(`{"machine_id":"self","herdr_socket":%q,"workspace_id":"w1","pane_id":"w1:p1","terminal_id":"term_original","kind":"codex","native_session":"original"}`, os.Getenv("HERDR_SOCKET_PATH")))
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader("missing-room\n\n"))
	cmd.SetArgs([]string{"herdr", "form"})
	if err := execute(cmd); err == nil {
		t.Fatal("failed room setup exited successfully", out.String())
	}
	if !strings.Contains(out.String(), "setup_required") || !strings.Contains(out.String(), "Press Enter to close") {
		t.Fatal("form did not retain actionable outcome", out.String())
	}
	select {
	case frame := <-frames:
		if frame.Method != "notification.show" || !strings.Contains(fmt.Sprint(frame.Params["body"]), "setup_required") {
			t.Fatal(frame)
		}
	case <-time.After(time.Second):
		t.Fatal("failed form closed without notification")
	}
}

func TestHerdrFormCancellationDoesNotNotify(t *testing.T) {
	for _, input := range []string{"\x1b", ""} {
		t.Run(fmt.Sprintf("input_%q", input), func(t *testing.T) {
			frames := nativeActionFixture(t, "success")
			t.Setenv("PARLEY_ACTION", "join")
			t.Setenv("PARLEY_TARGET_JSON", fmt.Sprintf(`{"machine_id":"self","herdr_socket":%q,"workspace_id":"w1","pane_id":"w1:p1","terminal_id":"term_original","kind":"codex","native_session":"original"}`, os.Getenv("HERDR_SOCKET_PATH")))
			cmd := newRootCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetIn(strings.NewReader(input))
			cmd.SetArgs([]string{"herdr", "form"})
			if err := execute(cmd); err == nil {
				t.Fatal("missing JSON cancellation")
			}
			select {
			case frame := <-frames:
				t.Fatal("cancellation shown as failure", frame)
			default:
			}
		})
	}
}

type nativeActionFrame struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

func nativeActionFixture(t *testing.T, mode string) <-chan nativeActionFrame {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "parley-action-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "h.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	frames := make(chan nativeActionFrame, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer conn.Close()
				var frame nativeActionFrame
				if json.NewDecoder(conn).Decode(&frame) != nil {
					return
				}
				reply := `{"result":{"type":"subscription_started"}}`
				switch frame.Method {
				case "agent.get":
					reply = `{"result":{"agent":{"pane_id":"w1:p1","workspace_id":"w1","terminal_id":"term_original","agent":"codex","agent_status":"idle","agent_session":{"agent":"codex","kind":"id","value":"original"}}}}`
					if mode == "identity" {
						reply = `{"result":{"agent":{"pane_id":"w1:p1","terminal_id":"term_original","agent":"codex","agent_status":"idle"}}}`
					} else if mode == "shell" || mode == "notification" {
						reply = `{"error":{"code":"agent_not_found","message":"agent target w1:p1 not found"}}`
					}
				case "plugin.pane.open":
					if mode == "modal" {
						reply = `{"error":{"code":"ui_busy","message":"another modal is active"}}`
					} else {
						frames <- frame
					}
				case "notification.show":
					frames <- frame
					reply = `{"result":{"type":"notification_show","shown":true,"reason":"shown"}}`
					if mode == "notification" {
						reply = `{"result":{"type":"notification_show","shown":false,"reason":"busy"}}`
					}
				}
				fmt.Fprintln(conn, reply)
			}()
		}
	}()
	version := "0.9.3"
	if mode == "version" {
		version = "0.9.2"
	}
	inventory := fmt.Sprintf(`{"sessions":[{"name":"remote-agents","running":true,"socket_path":%q}]}`, socket)
	if mode == "context" {
		inventory = `{"sessions":[]}`
	}
	binary := filepath.Join(root, "herdr")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in\n--version) printf 'herdr %s\\n';;\n*'session list'*) printf '%%s\\n' '%s';;\n*'plugin list'*) printf '%%s\\n' '{\"result\":{\"plugins\":[{\"plugin_id\":\"parley\",\"enabled\":true,\"plugin_root\":\"%s\"}]}}';;\n*'plugin config-dir'*) printf '%%s\\n' '%s/config';;\nesac\n", version, inventory, root, root)
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_BIN_PATH", binary)
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_SESSION", "default") // Injected remote socket must win over this local default.
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("PARLEY_STATE_DIR", filepath.Join(root, "runtime"))
	if mode == "helper" {
		t.Setenv("HERDR_BIN_PATH", "")
		t.Setenv("PATH", root+"/missing")
	}
	return frames
}
func TestHerdrRPCRejectedHelperChild(t *testing.T) {
	if os.Getenv("PARLEY_RPC_REJECTION_CHILD") != "1" {
		return
	}
	cmd := newRootCmd()
	cmd.SetArgs([]string{"herdr", "rpc"})
	cmd.SetIn(os.Stdin)
	cmd.SetErr(os.Stderr)
	if e := cmd.Execute(); e != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
