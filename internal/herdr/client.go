package herdr

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const processWaitDelay = 2 * time.Second
const PluginID = "parley"

var RequiredCapabilities = []string{"events", "requests", "claim-request", "outcome", "claim-controller", "release-controller"}

type Client struct {
	HerdrBinary    string
	ParleyBinary   string
	LocalMachineID string
	HerdrSocket    string
	Run            Run
	managed        sync.Map
}

func (c *Client) runner() Run {
	if c.Run != nil {
		return c.Run
	}
	return RunProcess
}
func (c *Client) Machines(ctx context.Context) ([]Machine, error) {
	b, e := c.runner()(ctx, c.HerdrBinary, []string{"machine", "list", "--json"}, nil)
	if e != nil {
		return nil, e
	}
	var m []Machine
	e = json.Unmarshal(b, &m)
	for _, v := range m {
		if e != nil {
			break
		}
		e = validateMachine(v)
	}
	return m, e
}
func validateMachine(m Machine) error {
	if e := safeID(m.ID); e != nil {
		return e
	}
	if m.ID == "self" || m.Target == "" || strings.HasPrefix(m.Target, "-") || strings.ContainsAny(m.Target, "\x00\r\n") || strings.ContainsAny(m.Target, " \t") {
		return fmt.Errorf("invalid saved target")
	}
	return validateSession(m.Session)
}
func validateSession(s string) error {
	if s == "" {
		return fmt.Errorf("saved session required")
	}
	return safeID(s)
}
func (c *Client) Agent(ctx context.Context, t AgentTarget) (AgentTarget, string, error) {
	if t.MachineID == "" || t.MachineID == "self" {
		t.MachineID = c.LocalMachineID
	}
	if e := safeID(t.MachineID); e != nil {
		return t, "", e
	}
	target := t.TerminalID
	if target == "" {
		target = t.PaneID
	}
	if e := safeID(target); e != nil {
		return t, "", e
	}
	var b []byte
	var e error
	if t.MachineID == c.LocalMachineID {
		if t.HerdrSocket != "" && t.HerdrSocket != c.HerdrSocket {
			return t, "", fmt.Errorf("session_mismatch")
		}
		t.HerdrSocket = c.HerdrSocket
		b, e = socketAPI(ctx, c.HerdrSocket, "agent.get", map[string]string{"target": target})
	} else {
		raw, ok := c.managed.Load(t.MachineID)
		if !ok {
			return t, "", fmt.Errorf("session_unverified")
		}
		peer := raw.(Peer)
		if t.HerdrSocket != "" && t.HerdrSocket != peer.Config.HerdrSocket {
			return t, "", fmt.Errorf("session_mismatch")
		}
		probe, e := c.Call(ctx, peer, RPCRequest{Operation: "probe"})
		if e != nil {
			return t, "", e
		}
		var verified ProbeResult
		if e = json.Unmarshal(probe, &verified); e != nil {
			return t, "", e
		}
		if verified.Config.HerdrSocket != peer.Config.HerdrSocket || verified.Config.SessionKey != peer.Config.SessionKey {
			return t, "", fmt.Errorf("session_mismatch")
		}
		t.HerdrSocket = peer.Config.HerdrSocket
		b, e = c.runner()(ctx, c.HerdrBinary, []string{"--machine", t.MachineID, "agent", "get", target}, nil)
	}
	if e != nil {
		return t, "", e
	}
	var wire struct {
		Result struct {
			Agent struct {
				PaneID      string `json:"pane_id"`
				WorkspaceID string `json:"workspace_id"`
				TerminalID  string `json:"terminal_id"`
				Kind        string `json:"agent"`
				Status      string `json:"agent_status"`
				Session     *struct {
					Kind  string `json:"kind"`
					Value string `json:"value"`
				} `json:"agent_session"`
			} `json:"agent"`
		} `json:"result"`
		Error *apiError `json:"error"`
	}
	if e = json.Unmarshal(b, &wire); e != nil {
		return t, "", e
	}
	if wire.Error != nil {
		return t, "", wire.Error
	}
	a := wire.Result.Agent
	if (a.Kind != "codex" && a.Kind != "claude") || a.TerminalID == "" || a.Session == nil || a.Session.Value == "" {
		return t, "", fmt.Errorf("agent_identity_required")
	}
	if t.TerminalID != "" && t.TerminalID != a.TerminalID || t.Kind != "" && t.Kind != a.Kind || t.NativeSession != "" && t.NativeSession != a.Session.Value {
		return t, "", fmt.Errorf("agent_replaced")
	}
	t.PaneID = a.PaneID
	t.WorkspaceID = a.WorkspaceID
	t.TerminalID = a.TerminalID
	t.Kind = a.Kind
	t.NativeSession = a.Session.Value
	if e := validateRequest(Request{ID: "identity", Op: "join", RoomID: "identity", Target: t}); e != nil {
		return t, "", fmt.Errorf("agent_identity_required: %w", e)
	}
	return t, a.Status, nil
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }
func socketAPI(ctx context.Context, socket, method string, params any) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn, e := dialSocket(ctx, socket)
	if e != nil {
		return nil, e
	}
	defer conn.Close()
	if e = json.NewEncoder(conn).Encode(map[string]any{"id": "parley", "method": method, "params": params}); e != nil {
		return nil, e
	}
	s := bufio.NewScanner(conn)
	s.Buffer(make([]byte, 4096), 1024*1024)
	if !s.Scan() {
		if e = s.Err(); e != nil {
			return nil, e
		}
		return nil, io.EOF
	}
	if !utf8.Valid(s.Bytes()) {
		return nil, fmt.Errorf("malformed API frame: invalid UTF-8")
	}
	b := append([]byte(nil), s.Bytes()...)
	var frame struct {
		Error *apiError `json:"error"`
	}
	if e = json.Unmarshal(b, &frame); e != nil {
		return nil, e
	}
	if frame.Error != nil {
		return nil, frame.Error
	}
	return b, nil
}
func dialSocket(ctx context.Context, socket string) (net.Conn, error) {
	if _, e := SessionKey(socket); e != nil {
		return nil, e
	}
	conn, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", socket)
	if e != nil {
		return nil, e
	}
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if e = conn.SetDeadline(deadline); e != nil {
		conn.Close()
		return nil, e
	}
	go func() { <-ctx.Done(); conn.Close() }()
	return conn, nil
}
func (c *Client) helper(ctx context.Context, m Machine) (string, error) {
	if e := validateMachine(m); e != nil {
		return "", e
	}
	b, e := c.runner()(ctx, c.HerdrBinary, []string{"--machine", m.ID, "plugin", "list", "--json"}, nil)
	if e != nil {
		return "", e
	}
	return installedHelper(b)
}
func installedHelper(b []byte) (string, error) {
	var w struct {
		Result struct {
			Plugins []struct {
				ID      string `json:"plugin_id"`
				Enabled bool   `json:"enabled"`
				Root    string `json:"plugin_root"`
			} `json:"plugins"`
		} `json:"result"`
	}
	if e := json.Unmarshal(b, &w); e != nil {
		return "", e
	}
	for _, p := range w.Result.Plugins {
		if p.ID == PluginID {
			if !p.Enabled {
				return "", fmt.Errorf("helper_disabled")
			}
			if !filepath.IsAbs(p.Root) || filepath.Clean(p.Root) != p.Root || strings.ContainsRune(p.Root, 0) {
				return "", fmt.Errorf("invalid installed helper root")
			}
			return filepath.Join(p.Root, "bin", "parley"), nil
		}
	}
	return "", fmt.Errorf("helper_missing")
}
func shellQuote(s string) string         { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func helperCommand(binary string) string { return shellQuote(binary) + " herdr rpc" }
func (c *Client) Call(ctx context.Context, p Peer, r RPCRequest) ([]byte, error) {
	if e := validateMachine(p.Machine); e != nil {
		return nil, e
	}
	if !filepath.IsAbs(p.Binary) || filepath.Clean(p.Binary) != p.Binary || strings.ContainsRune(p.Binary, 0) {
		return nil, fmt.Errorf("invalid installed helper")
	}
	r.Session = p.Machine.Session
	if e := r.Validate(); e != nil {
		return nil, e
	}
	b, e := json.Marshal(r)
	if e != nil {
		return nil, e
	}
	return c.runner()(ctx, "ssh", []string{"-T", "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", "--", p.Machine.Target, helperCommand(p.Binary)}, b)
}

// Parley accepts only a fixed helper operation; operation data belongs in stdin.
func (c *Client) Parley(ctx context.Context, m Machine, args []string, stdin []byte) ([]byte, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("fixed helper operation required")
	}
	var r RPCRequest
	if e := decodeRPC(bytes.NewReader(stdin), &r); e != nil {
		return nil, e
	}
	if r.Operation != "" && r.Operation != args[0] {
		return nil, fmt.Errorf("operation mismatch")
	}
	r.Operation = args[0]
	binary, e := c.helper(ctx, m)
	if e != nil {
		return nil, e
	}
	return c.Call(ctx, Peer{Machine: m, Binary: binary}, r)
}
func (c *Client) SignalRequest(ctx context.Context, t AgentTarget, id string) error {
	if e := safeID(id); e != nil {
		return e
	}
	if t.MachineID != c.LocalMachineID && t.MachineID != "self" {
		return fmt.Errorf("host-local signal required")
	}
	if t.HerdrSocket != c.HerdrSocket {
		return fmt.Errorf("session_mismatch")
	}
	if e := safeID(t.WorkspaceID); e != nil {
		return e
	}
	marker := make([]byte, 16)
	if _, e := rand.Read(marker); e != nil {
		return e
	}
	_, e := socketAPI(ctx, c.HerdrSocket, "workspace.report_metadata", map[string]any{"workspace_id": t.WorkspaceID, "source": "plugin:parley", "tokens": map[string]string{"parley_request": id + ":" + hex.EncodeToString(marker)}})
	if e != nil {
		return fmt.Errorf("pending_signal: %w", e)
	}
	return nil
}
