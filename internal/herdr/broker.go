package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/khaiql/parley/internal/paths"
	runtime "github.com/khaiql/parley/internal/runtime"
)

// BindingKey identifies the coordinator route; HostBindingKey excludes profile aliases.
func BindingKey(roomID string, t AgentTarget) string {
	return hashBytes([]byte(t.MachineID + ":" + terminalBindingKey(roomID, t)))
}

type HostOperations struct {
	Store  *Store
	Client *Client
}
type Broker struct {
	Store   *Store
	Client  *Client
	Tunnels *TunnelManager
	mu      sync.Mutex
}

func failure(code string, e error) Response {
	r := Response{Status: "error", Code: code}
	if e != nil {
		r.Message = e.Error()
	}
	return r
}
func (c *Client) runtimePaths() (paths.Paths, error) {
	c.rootOnce.Do(func() {
		if c.RuntimeRoot == "" {
			c.RuntimeRoot = paths.DefaultRoot()
		}
	})
	root, e := filepath.Abs(c.RuntimeRoot)
	if e != nil {
		return paths.Paths{}, e
	}
	if root != filepath.Clean(root) {
		return paths.Paths{}, fmt.Errorf("invalid runtime root")
	}
	return paths.New(root), nil
}
func (h *HostOperations) localCLI(ctx context.Context, args []string) ([]byte, error) {
	c := h.Client
	p, e := h.runtimePaths()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if c.Run != nil {
		return c.Run(ctx, c.ParleyBinary, args, nil)
	}
	return runProcessEnv(ctx, c.ParleyBinary, args, nil, appendEnvironment(os.Environ(), "PARLEY_STATE_DIR", p.Root))
}

func (h *HostOperations) liveRoom(ctx context.Context, room Room) (Room, error) {
	if room.Closed {
		return room, fmt.Errorf("room_closed")
	}
	if _, _, e := validateEndpoints(room.Source, room.ID); e != nil {
		return room, e
	}
	t, _, e := h.Client.Agent(ctx, room.Host)
	if e != nil {
		return room, e
	}
	room.Host = t
	p, e := h.Store.pinnedPaths()
	if e != nil {
		return room, fmt.Errorf("runtime_root_unpinned: %w", e)
	}
	meta, e := runtime.LoadRoomRuntime(p, room.ID)
	if e != nil {
		return room, e
	}
	expected := Endpoints{Descriptor: fmt.Sprintf("parley://%s:%d/%s", meta.LocalHost, meta.LocalPort, meta.RoomID), ArtifactURL: fmt.Sprintf("http://%s:%d%s", meta.LocalHost, meta.ArtifactLocalPort, meta.ArtifactPath)}
	if meta.RoomID != room.ID || expected != room.Source {
		return room, fmt.Errorf("room_runtime_mismatch")
	}
	if e = probeRoute(ctx, room.Source, room.ID); e != nil {
		return room, e
	}
	return room, nil
}
func (h *HostOperations) runtimePaths() (paths.Paths, error) {
	p, e := h.Store.pinnedPaths()
	if e == nil {
		return p, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return p, e
	}
	p, e = h.Client.runtimePaths()
	if e != nil {
		return p, e
	}
	if e = h.Store.PinRuntimeRoot(p.Root); e != nil {
		return p, e
	}
	return p, nil
}

// Host journals identify the native socket/terminal, independent of controller aliases.
func hostRequest(req Request) Request { req.Target.MachineID = "self"; return req }
func hostDigest(req Request) string   { return RequestDigest(hostRequest(req)) }
func (h *HostOperations) saveBinding(binding Binding) error {
	binding.Target.MachineID = "self"
	return h.Store.saveBinding(binding)
}
func hostResponse(response Response, machine string) Response {
	if response.Binding != nil {
		binding := *response.Binding
		binding.Target.MachineID = machine
		response.Binding = &binding
	}
	if response.Room != nil {
		room := *response.Room
		room.Host.MachineID = machine
		response.Room = &room
	}
	return response
}
func (h *HostOperations) journal(req Request) error {
	return h.Store.locked(func() error {
		dir, e := h.Store.recordDir("requests")
		if e != nil {
			return e
		}
		path := filepath.Join(dir, req.ID+".json")
		var prior Request
		e = readJSON(path, &prior)
		if errors.Is(e, os.ErrNotExist) {
			return writeJSON(path, hostRequest(req))
		}
		if e != nil {
			return e
		}
		normalized, e := normalize(prior, (&Broker{Client: h.Client}).localPeer())
		if e != nil {
			return e
		}
		if hostDigest(normalized) != hostDigest(req) {
			return fmt.Errorf("request_conflict")
		}
		return nil
	})
}
func appendEnvironment(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, v := range env {
		if len(v) <= len(key) || v[:len(key)+1] != key+"=" {
			out = append(out, v)
		}
	}
	return append(out, key+"="+value)
}

func (h *HostOperations) identity(ctx context.Context, req Request) (Request, error) {
	if e := validateRequest(req); e != nil {
		return req, e
	}
	if req.Target.MachineID != "self" && req.Target.MachineID != h.Client.LocalMachineID {
		return req, fmt.Errorf("forged origin machine")
	}
	t, _, e := h.Client.Agent(ctx, req.Target)
	req.Target = t
	return req, e
}
func liveSocket(ctx context.Context, path string) bool {
	c, e := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", path)
	if e != nil {
		return false
	}
	_ = c.Close()
	return true
}
func validateSocket(path string) error {
	if len(path) > 103 {
		return fmt.Errorf("runtime_socket_too_long")
	}
	return nil
}
func (h *HostOperations) original(ctx context.Context, b Binding) (Binding, string, error) {
	p, e := h.runtimePaths()
	if e != nil {
		return b, "operation_uncertain", e
	}
	sessions, e := runtime.ListSessions(p)
	if e != nil {
		return b, "operation_uncertain", e
	}
	var matches []runtime.Session
	for _, s := range sessions {
		if s.RoomID == b.RoomID && s.Name == b.ParticipantName && (b.SessionID == "" || s.ID == b.SessionID) {
			matches = append(matches, s)
		}
	}
	if len(matches) != 1 {
		return b, "operation_uncertain", fmt.Errorf("original session evidence missing or ambiguous")
	}
	store, e := runtime.ParticipantStore(p, b.RoomID, b.ParticipantName)
	if e != nil {
		return b, "operation_uncertain", e
	}
	meta, e := store.LoadMeta()
	if e != nil {
		return b, "operation_uncertain", e
	}
	if meta.RoomID != b.RoomID || meta.Name != b.ParticipantName {
		return b, "operation_uncertain", fmt.Errorf("participant evidence mismatch")
	}
	if meta.Status != "online" || !liveSocket(ctx, runtime.ParticipantSocketPath(p, b.RoomID, b.ParticipantName)) {
		return b, "participant_disconnected", nil
	}
	b.SessionID = matches[0].ID
	b.Target.MachineID = h.Client.LocalMachineID
	b.JoinPhase = "complete"
	b.Status = "online"
	return b, "", nil
}

func joinedResponse(b Binding) Response {
	return Response{Status: "joined", Binding: &b, Message: "Use parley --session " + b.SessionID + " for existing participant commands."}
}

type hostFailure struct {
	Digest   string   `json:"digest"`
	Response Response `json:"response"`
}

func knownNoEffect(e *apiError) bool {
	if e == nil {
		return false
	}
	switch e.Code {
	case "invalid_arguments", "invalid_descriptor", "invalid_artifact_endpoint", "missing_required_flag":
		return true
	}
	return false
}
func knownHostFailure(code string) bool {
	return code == "agent_replaced" || code == "room_mismatch" || knownNoEffect(&apiError{Code: code})
}
func (h *HostOperations) priorFailure(req Request) (Response, bool, error) {
	var record hostFailure
	e := readJSON(filepath.Join(h.Store.dir, "join-failures", req.ID+".json"), &record)
	if errors.Is(e, os.ErrNotExist) {
		return Response{}, false, nil
	}
	if e != nil {
		return Response{}, false, e
	}
	if record.Digest != hostDigest(req) || record.Response.Status != "error" || !knownHostFailure(record.Response.Code) {
		return Response{}, false, fmt.Errorf("request_conflict")
	}
	return record.Response, true, nil
}

// JoinFailureResult is read-only recovery evidence, including the current host owner.
type JoinFailureResult struct {
	Status   string    `json:"status"`
	Response *Response `json:"response,omitempty"`
	Binding  *Binding  `json:"binding,omitempty"`
}

func (h *HostOperations) ReadJoinFailure(ctx context.Context, req Request) (JoinFailureResult, error) {
	var result JoinFailureResult
	req, e := normalize(req, (&Broker{Client: h.Client}).localPeer())
	if e != nil {
		return result, e
	}
	if req.Op != "join" {
		return result, fmt.Errorf("join request required")
	}
	key := HostBindingKey(req.RoomID, req.Target)
	e = h.Store.WithBinding(ctx, "join:"+terminalBindingKey(req.RoomID, req.Target), func() error {
		var stored Request
		if e := readJSON(filepath.Join(h.Store.dir, "requests", req.ID+".json"), &stored); e != nil {
			return e
		}
		stored, e := normalize(stored, (&Broker{Client: h.Client}).localPeer())
		if e != nil {
			return e
		}
		if RequestDigest(stored) != RequestDigest(req) || HostBindingKey(stored.RoomID, stored.Target) != key {
			return fmt.Errorf("request_conflict")
		}
		failed, ok, e := h.priorFailure(req)
		if e != nil {
			return e
		}
		result.Status = "absent"
		if !ok {
			return nil
		}
		result.Status = "failed"
		result.Response = &failed
		binding, e := h.Store.Binding(key)
		if e == nil {
			binding.Target.MachineID = h.Client.LocalMachineID
			result.Binding = &binding
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		return nil
	})
	return result, e
}
func (h *HostOperations) recordFailure(req Request, response Response) error {
	return h.Store.locked(func() error {
		dir, e := h.Store.recordDir("join-failures")
		if e != nil {
			return e
		}
		return writeJSON(filepath.Join(dir, req.ID+".json"), hostFailure{hostDigest(req), response})
	})
}

// clearFailedIntent runs under the host join lock and removes only proven no-effect intent.
func (h *HostOperations) clearFailedIntent(key string, prior Binding) (bool, error) {
	if prior.SessionID != "" || prior.JoinRequestID == "" {
		return false, nil
	}
	var originalReq Request
	if e := readJSON(filepath.Join(h.Store.dir, "requests", prior.JoinRequestID+".json"), &originalReq); e != nil {
		return false, e
	}
	originalReq, e := normalize(originalReq, (&Broker{Client: h.Client}).localPeer())
	if e != nil {
		return false, e
	}
	failed, ok, e := h.priorFailure(originalReq)
	if e != nil {
		return false, e
	}
	if !ok || !knownHostFailure(failed.Code) {
		return false, nil
	}
	if HostBindingKey(originalReq.RoomID, originalReq.Target) != key {
		return false, fmt.Errorf("binding key mismatch")
	}
	if e = h.Store.Update(func(state *State) error { delete(state.Bindings, key); return nil }); e != nil {
		return false, e
	}
	return true, nil
}
func (h *HostOperations) Join(ctx context.Context, req Request, endpoints Endpoints) (Response, error) {
	var result Response
	err := h.Store.WithBinding(ctx, terminalLockKey(req.Target), func() error {
		var err error
		result, err = h.join(ctx, req, endpoints)
		return err
	})
	if result.Binding != nil {
		result.Binding.Topic = req.RoomTopic
	}
	return h.withHandoff(ctx, result, err)
}

// Legacy keys remain intact while new requests identify the terminal only.
func (h *HostOperations) reuseTerminalBinding(req *Request, endpoints Endpoints) (string, error) {
	prior, err := h.Store.BindingForTarget(req.RoomID, req.Target)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "binding_ambiguous", err
	}
	if prior.Target.NativeSession != req.Target.NativeSession {
		req.Target.NativeSession = prior.Target.NativeSession
	}
	if prior.Routed != (Endpoints{}) && prior.Routed != endpoints || prior.RoomHost != nil && req.RoomHost != nil && *prior.RoomHost != *req.RoomHost {
		return "room_mismatch", fmt.Errorf("existing participant belongs to a different room host or route")
	}
	return "", nil
}

func (h *HostOperations) join(ctx context.Context, req Request, endpoints Endpoints) (Response, error) {
	originalReq, e := normalize(req, (&Broker{Client: h.Client}).localPeer())
	if e != nil {
		return failure("invalid_request", e), nil
	}
	req, e = h.identity(ctx, originalReq)
	if e != nil {
		return failure("invalid_request", e), nil
	}
	if req.Op != "join" {
		return failure("invalid_request", fmt.Errorf("join required")), nil
	}
	if _, _, e = validateEndpoints(endpoints, req.RoomID); e != nil {
		return failure("invalid_request", e), nil
	}
	if e = h.journal(originalReq); e != nil {
		return failure("request_conflict", e), nil
	}
	var response Response
	e = h.Store.WithBinding(ctx, "join:"+terminalBindingKey(req.RoomID, req.Target), func() error {
		if code, err := h.reuseTerminalBinding(&req, endpoints); code != "" {
			response = failure(code, err)
			return nil
		}
		key := HostBindingKey(req.RoomID, req.Target)
		if prior, ok, e := h.priorFailure(originalReq); e != nil {
			return e
		} else if ok {
			response = prior
			return nil
		}
		prior, e := h.Store.Binding(key)
		if e == nil {
			cleared, clearErr := h.clearFailedIntent(key, prior)
			if clearErr != nil {
				return clearErr
			}
			if cleared {
				e = os.ErrNotExist
			}
		}
		if e == nil {
			original, code, e := h.original(ctx, prior)
			if code != "" {
				response = failure(code, e)
				return nil
			}
			if e = h.saveBinding(original); e != nil {
				return e
			}
			response = joinedResponse(original)
			return nil
		}
		if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		p, e := h.runtimePaths()
		if e != nil {
			return e
		}
		b := Binding{RoomID: req.RoomID, RoomHost: req.RoomHost, Target: req.Target, ParticipantName: "h_" + key[:16], StateDir: p.Root, Routed: endpoints, JoinPhase: "intent", JoinRequestID: req.ID}
		if e = validateSocket(runtime.ParticipantSocketPath(p, b.RoomID, b.ParticipantName)); e != nil {
			response = failure("runtime_socket_too_long", e)
			return nil
		}
		if e = h.saveBinding(b); e != nil {
			return e
		}
		if _, e = h.identity(ctx, req); e != nil {
			response = failure("agent_replaced", e)
			if e = h.recordFailure(originalReq, response); e != nil {
				return e
			}
			return h.Store.Update(func(state *State) error {
				current, ok := state.Bindings[key]
				if ok && current.JoinRequestID == req.ID && current.SessionID == "" {
					delete(state.Bindings, key)
				}
				return nil
			})
		}
		sessions, listErr := runtime.ListSessions(p)
		if listErr != nil {
			return listErr
		}
		for _, session := range sessions {
			if session.Name == b.ParticipantName {
				response = failure("operation_uncertain", fmt.Errorf("reserved participant name already exists"))
				return nil
			}
		}
		data, runErr := h.localCLI(ctx, []string{"join", endpoints.Descriptor, "--name", b.ParticipantName, "--artifact-endpoint", endpoints.ArtifactURL})
		var wire struct {
			Status    string    `json:"status"`
			RoomID    string    `json:"room_id"`
			SessionID string    `json:"session_id"`
			Error     *apiError `json:"error"`
		}
		decodeErr := json.Unmarshal(data, &wire)
		if decodeErr == nil && knownNoEffect(wire.Error) && runErr == nil {
			response = failure(wire.Error.Code, wire.Error)
			if e = h.recordFailure(originalReq, response); e != nil {
				return e
			}
			if e = h.Store.Update(func(s *State) error { delete(s.Bindings, key); return nil }); e != nil {
				return e
			}
			return nil
		}
		if runErr != nil || decodeErr != nil || wire.Status != "joined" || wire.SessionID == "" {
			b.JoinPhase = "uncertain"
			if e = h.saveBinding(b); e != nil {
				return e
			}
			original, code, e := h.original(ctx, b)
			if code != "" {
				response = failure(code, e)
				return nil
			}
			b = original
		} else {
			if safeID(wire.SessionID) != nil {
				return fmt.Errorf("invalid joined session")
			}
			if wire.RoomID != req.RoomID {
				leaveData, leaveErr := h.localCLI(ctx, []string{"--session", wire.SessionID, "leave"})
				var left struct {
					OK     bool   `json:"ok"`
					Status string `json:"status"`
				}
				e = leaveErr
				if e == nil && (json.Unmarshal(leaveData, &left) != nil || !left.OK || left.Status != "left") {
					e = fmt.Errorf("leave outcome unproved")
				}
				if e != nil {
					response = failure("operation_uncertain", e)
					return nil
				}
				if e = h.Store.Update(func(s *State) error { delete(s.Bindings, key); return nil }); e != nil {
					return e
				}
				response = failure("room_mismatch", nil)
				return h.recordFailure(originalReq, response)
			}
			b.SessionID = wire.SessionID
			b.JoinPhase = "complete"
			b.Status = "online"
		}
		if e = h.saveBinding(b); e != nil {
			return e
		}
		response = joinedResponse(b)
		return nil
	})
	return hostResponse(response, h.Client.LocalMachineID), e
}

func reservedStartName(socket, requestID string) string {
	key, _ := SessionKey(socket)
	data, _ := json.Marshal([2]string{key, requestID})
	return "s_" + hashBytes(data)[:16]
}

type startIntent struct {
	Digest   string    `json:"digest"`
	Name     string    `json:"name"`
	Request  Request   `json:"request"`
	RoomID   string    `json:"room_id,omitempty"`
	Response *Response `json:"response,omitempty"`
}

func (h *HostOperations) Start(ctx context.Context, req Request) (Response, error) {
	var result Response
	err := h.Store.WithBinding(ctx, terminalLockKey(req.Target), func() error {
		var err error
		result, err = h.start(ctx, req)
		return err
	})
	if result.Binding != nil {
		result.Binding.Topic = req.Topic
	}
	return h.withHandoff(ctx, result, err)
}
func (h *HostOperations) start(ctx context.Context, req Request) (Response, error) {
	originalReq, e := normalize(req, (&Broker{Client: h.Client}).localPeer())
	if e != nil {
		return failure("invalid_request", e), nil
	}
	req, e = h.identity(ctx, originalReq)
	if e != nil {
		return failure("invalid_request", e), nil
	}
	if req.Op != "start" || req.Topic == "" {
		return failure("invalid_request", fmt.Errorf("topic required")), nil
	}
	var response Response
	e = h.Store.WithBinding(ctx, "join:start:"+req.ID, func() error {
		var intent startIntent
		var intentPath string
		fresh := false
		e := h.Store.locked(func() error {
			dir, e := h.Store.recordDir("starts")
			if e != nil {
				return e
			}
			intentPath = filepath.Join(dir, req.ID+".json")
			e = readJSON(intentPath, &intent)
			if errors.Is(e, os.ErrNotExist) {
				fresh = true
				intent = startIntent{Digest: hostDigest(originalReq), Name: reservedStartName(req.Target.HerdrSocket, req.ID), Request: hostRequest(originalReq)}
				return writeJSON(intentPath, intent)
			}
			return e
		})
		if e != nil {
			return e
		}
		if intent.Digest != hostDigest(originalReq) {
			response = failure("request_conflict", nil)
			return nil
		}
		if intent.Response != nil {
			response = *intent.Response
			return nil
		}
		saveResponse := func() error {
			saved := hostResponse(response, "self")
			intent.Response = &saved
			return h.Store.locked(func() error { return writeJSON(intentPath, intent) })
		}
		p, e := h.runtimePaths()
		if e != nil {
			return e
		}
		if e = validateSocket(runtime.ParticipantSocketPath(p, "room-00000000000000000000", intent.Name)); e != nil {
			response = failure("runtime_socket_too_long", e)
			return nil
		}
		var room Room
		var sessionID string
		if fresh {
			data, runErr := h.localCLI(ctx, []string{"start", "--topic", req.Topic, "--name", intent.Name})
			var wire struct {
				Status       string    `json:"status"`
				RoomID       string    `json:"room_id"`
				SessionID    string    `json:"session_id"`
				Descriptor   string    `json:"descriptor"`
				ArtifactPort int       `json:"artifact_local_port"`
				ArtifactPath string    `json:"artifact_path"`
				Error        *apiError `json:"error"`
			}
			decodeErr := json.Unmarshal(data, &wire)
			if runErr == nil && decodeErr == nil && knownNoEffect(wire.Error) {
				response = failure(wire.Error.Code, wire.Error)
				return saveResponse()
			}
			if runErr == nil && decodeErr == nil && wire.Status == "started" && safeID(wire.RoomID) == nil && safeID(wire.SessionID) == nil {
				room = Room{ID: wire.RoomID, Topic: req.Topic, Host: req.Target, HostSessionID: wire.SessionID, Source: Endpoints{Descriptor: wire.Descriptor, ArtifactURL: fmt.Sprintf("http://127.0.0.1:%d%s", wire.ArtifactPort, wire.ArtifactPath)}}
				sessionID = wire.SessionID
			}
		}
		if sessionID == "" {
			sessions, e := runtime.ListSessions(p)
			if e != nil {
				return e
			}
			matches := []runtime.Session{}
			for _, s := range sessions {
				if s.Name == intent.Name {
					matches = append(matches, s)
				}
			}
			if len(matches) != 1 {
				response = failure("operation_uncertain", fmt.Errorf("reserved host evidence missing or ambiguous"))
				return nil
			}
			s := matches[0]
			meta, e := runtime.LoadRoomRuntime(p, s.RoomID)
			if e != nil || !liveSocket(ctx, runtime.ServerSocketPath(p, s.RoomID)) {
				response = failure("operation_uncertain", e)
				return nil
			}
			room = Room{ID: s.RoomID, Topic: meta.Topic, Host: req.Target, HostSessionID: s.ID, Source: Endpoints{Descriptor: fmt.Sprintf("parley://127.0.0.1:%d/%s", meta.LocalPort, s.RoomID), ArtifactURL: fmt.Sprintf("http://127.0.0.1:%d%s", meta.ArtifactLocalPort, meta.ArtifactPath)}}
			sessionID = s.ID
		}
		if _, _, e = validateEndpoints(room.Source, room.ID); e != nil {
			response = failure("operation_uncertain", e)
			return nil
		}
		b := Binding{RoomID: room.ID, Target: req.Target, SessionID: sessionID, ParticipantName: intent.Name, StateDir: p.Root, Routed: room.Source, Status: "online", JoinPhase: "complete", JoinRequestID: req.ID}
		room.Host.MachineID = "self"
		if e = h.Store.saveRoom(room); e != nil {
			return e
		}
		if e = h.saveBinding(b); e != nil {
			return e
		}
		d, u, _ := validateEndpoints(room.Source, room.ID)
		_, metadataErr := socketAPI(ctx, h.Client.HerdrSocket, "pane.report_metadata", map[string]any{"pane_id": req.Target.PaneID, "source": "plugin:parley", "tokens": map[string]string{"parley_room": room.ID, "parley_port": strconv.Itoa(d.Port), "parley_artifact_port": u.Port()}})
		response = Response{Status: "started", Room: &room, Binding: &b, Message: "Use parley --session " + b.SessionID + " for existing participant commands."}
		if metadataErr != nil {
			response.Message += " Metadata unavailable; durable room discovery remains available."
		}
		return saveResponse()
	})
	return hostResponse(response, h.Client.LocalMachineID), e
}
func normalize(req Request, peer Peer) (Request, error) {
	data, e := json.Marshal(req)
	if e != nil {
		return req, e
	}
	return DecodeRequest(bytes.NewReader(data), peer)
}
func (b *Broker) peerCall(ctx context.Context, p Peer, r RPCRequest, out any) error {
	r.SessionKey = p.Config.SessionKey
	r.ControllerID = p.Config.ControllerID
	data, e := b.Client.Call(ctx, p, r)
	if e != nil {
		return e
	}
	var wire struct {
		Error *apiError `json:"error"`
	}
	// Durable requests are a JSON array; only object responses carry error envelopes.
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) != 0 && trimmed[0] == '{' {
		if e = json.Unmarshal(data, &wire); e != nil {
			return e
		}
		if wire.Error != nil {
			return wire.Error
		}
	}
	if r.Operation == "requests" && (len(trimmed) == 0 || trimmed[0] != '[') {
		return fmt.Errorf("invalid requests response: array required")
	}
	return json.Unmarshal(data, out)
}
func (b *Broker) localPeer() Peer {
	key, _ := SessionKey(b.Client.HerdrSocket)
	return Peer{Machine: Machine{ID: b.Client.LocalMachineID}, Config: SessionConfig{MachineID: b.Client.LocalMachineID, HerdrSocket: b.Client.HerdrSocket, SessionKey: key, ControllerID: b.Client.LocalMachineID}}
}
func (b *Broker) validateRoom(ctx context.Context, room Room) (Room, error) {
	if room.Host.MachineID == b.Client.LocalMachineID || room.Host.MachineID == "self" {
		live, err := (&HostOperations{b.Store, b.Client}).liveRoom(ctx, room)
		live.Host.MachineID = b.Client.LocalMachineID
		return live, err
	}
	state, e := b.Store.Read()
	if e != nil {
		return room, e
	}
	p, ok := state.Peers[room.Host.MachineID]
	if !ok {
		return room, fmt.Errorf("session_unverified")
	}
	if room.Host.HerdrSocket != p.Config.HerdrSocket {
		return room, fmt.Errorf("session_mismatch")
	}
	var live Room
	if e = b.peerCall(ctx, p, RPCRequest{Operation: "room", RoomID: room.ID}, &live); e != nil {
		return room, e
	}
	if live.ID != room.ID || live.Host.MachineID != p.Machine.ID || live.Host.HerdrSocket != p.Config.HerdrSocket {
		return room, fmt.Errorf("room_identity_mismatch")
	}
	// Recheck identity through the activated targeted probe as well as the host runtime read.
	target, _, e := b.Client.Agent(ctx, live.Host)
	if e != nil {
		return room, e
	}
	live.Host = target
	return live, nil
}
func (b *Broker) ResolveRoom(ctx context.Context, id string) (Room, error) {
	if e := safeID(id); e != nil {
		return Room{}, e
	}
	rooms, e := b.Store.Rooms()
	if e != nil {
		return Room{}, e
	}
	state, e := b.Store.Read()
	if e != nil {
		return Room{}, e
	}
	candidates := []Room{}
	incomplete := false
	machines, machineErr := b.Client.Machines(ctx)
	if machineErr != nil {
		incomplete = true
	}
	enabled := map[string]bool{}
	for _, machine := range machines {
		if machine.Enabled {
			enabled[machine.ID] = true
			if _, ok := state.Peers[machine.ID]; !ok {
				incomplete = true
			}
		}
	}
	if room, ok := rooms[id]; ok {
		room, e = b.validateRoom(ctx, room)
		if e != nil {
			incomplete = true
		} else {
			candidates = append(candidates, room)
		}
	}
	for _, p := range state.Peers {
		if !p.Machine.Enabled && !enabled[p.Machine.ID] {
			continue
		}
		var records map[string]Room
		if e = b.peerCall(ctx, p, RPCRequest{Operation: "rooms"}, &records); e != nil {
			incomplete = true
			continue
		}
		if room, ok := records[id]; ok {
			if room.ID != id || room.Host.MachineID != p.Machine.ID || room.Host.HerdrSocket != p.Config.HerdrSocket {
				incomplete = true
				continue
			}
			room, e = b.validateRoom(ctx, room)
			if e != nil {
				incomplete = true
			} else {
				candidates = append(candidates, room)
			}
		}
	}
	if len(candidates) > 1 {
		return Room{}, fmt.Errorf("room_ambiguous")
	}
	if incomplete {
		return Room{}, fmt.Errorf("room_resolution_incomplete")
	}
	if len(candidates) == 0 {
		return Room{}, fmt.Errorf("room_missing")
	}
	return candidates[0], nil
}
func (b *Broker) Drain(ctx context.Context, peer Peer) error {
	var requests []Request
	var e error
	if peer.Machine.ID == b.Client.LocalMachineID || b.Client.LocalMachineID == "" && peer.Machine.ID == "local" {
		requests, e = b.Store.Pending()
	} else {
		e = b.peerCall(ctx, peer, RPCRequest{Operation: "requests"}, &requests)
	}
	if e != nil {
		return e
	}
	for _, req := range requests {
		if _, e = b.Handle(ctx, peer, req); e != nil {
			return e
		}
	}
	return nil
}
func (b *Broker) joinFailure(ctx context.Context, peer Peer, req Request) (JoinFailureResult, error) {
	if peer.Machine.ID == b.Client.LocalMachineID {
		return (&HostOperations{b.Store, b.Client}).ReadJoinFailure(ctx, req)
	}
	var result JoinFailureResult
	e := b.peerCall(ctx, peer, RPCRequest{Operation: "join-failure", Request: &req}, &result)
	if e != nil {
		return result, e
	}
	if result.Status != "absent" && result.Status != "failed" || result.Status == "failed" && (result.Response == nil || result.Response.Status != "error" || !knownHostFailure(result.Response.Code)) {
		return result, fmt.Errorf("invalid failure recovery evidence")
	}
	if result.Binding != nil && (HostBindingKey(result.Binding.RoomID, result.Binding.Target) != HostBindingKey(req.RoomID, req.Target) || result.Binding.Target.MachineID != req.Target.MachineID) {
		return result, fmt.Errorf("binding key mismatch")
	}
	return result, nil
}
func (b *Broker) releaseFailed(ctx context.Context, req Request, evidence JoinFailureResult) error {
	key := HostBindingKey(req.RoomID, req.Target)
	if evidence.Binding != nil && (evidence.Binding.JoinRequestID != req.ID || evidence.Binding.JoinPhase == "complete") {
		return nil
	}
	cached, e := b.Store.Binding(key)
	if e == nil && (cached.JoinRequestID != req.ID || cached.JoinPhase == "complete") {
		return nil
	}
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	b.mu.Lock()
	tunnels := b.Tunnels
	b.mu.Unlock()
	if tunnels != nil {
		if e = tunnels.Release(ctx, req.RoomID, req.Target); e != nil {
			return e
		}
	}
	return b.Store.Update(func(state *State) error {
		current, ok := state.Bindings[key]
		if ok && current.JoinRequestID == req.ID && current.SessionID == "" && current.JoinPhase != "complete" {
			delete(state.Bindings, key)
		}
		return nil
	})
}
func (b *Broker) checkBindingHost(ctx context.Context, req Request, existing Binding) (string, error) {
	if req.RoomHost == nil {
		return "", nil
	}
	selected, err := b.ResolveSelectedRoom(ctx, req.RoomID, *req.RoomHost)
	if err != nil {
		return "room_unavailable", err
	}
	if !bindingMatchesRoom(existing, selected, b.Client.LocalMachineID) {
		return "room_mismatch", fmt.Errorf("existing participant belongs to another host")
	}
	return "", nil
}

func (b *Broker) Handle(ctx context.Context, peer Peer, req Request) (Response, error) {
	req, e := normalize(req, peer)
	if e != nil {
		return failure("invalid_request", e), nil
	}
	switch req.Op {
	case "catalog", "probe", "status":
		return b.readRequest(ctx, peer, req)
	}
	if req.Op == "start" {
		if req.Target.MachineID != b.Client.LocalMachineID {
			return failure("host_local_start_required", nil), nil
		}
		return (&HostOperations{b.Store, b.Client}).Start(ctx, req)
	}
	if _, _, err := b.Client.Agent(ctx, req.Target); err != nil {
		return failure("agent_unavailable", err), nil
	}
	var response Response
	e = b.Store.WithBinding(ctx, "route:"+BindingKey(req.RoomID, req.Target), func() error {
		var outcome Outcome
		local := peer.Machine.ID == b.Client.LocalMachineID
		if local {
			outcome, e = b.Store.ClaimRequest(req, peer.Config.ControllerID)
		} else {
			e = b.peerCall(ctx, peer, RPCRequest{Operation: "claim-request", RequestID: req.ID, Digest: hostDigest(req)}, &outcome)
		}
		if e != nil {
			if e.Error() == "request_conflict" {
				response = failure("request_conflict", e)
				return nil
			}
			return e
		}
		if outcome.Phase == "complete" {
			response = b.completedResponse(ctx, req, outcome)
			return nil
		}
		finish := func(r Response, phase string) error {
			response = r
			outcome.Phase = phase
			outcome.Response = hostResponse(r, "self")
			if local {
				return b.Store.Complete(req.ID, outcome)
			}
			var published Outcome
			return b.peerCall(ctx, peer, RPCRequest{Operation: "outcome", RequestID: req.ID, Outcome: &outcome}, &published)
		}
		recovered, recoveryErr := b.joinFailure(ctx, peer, req)
		if recoveryErr != nil {
			return recoveryErr
		}
		if recovered.Status == "failed" {
			if e = b.releaseFailed(ctx, req, recovered); e != nil {
				return finish(failure("route_unavailable", e), "uncertain")
			}
			return finish(*recovered.Response, "complete")
		}
		var existing Binding
		if local {
			existing, e = b.Store.BindingForTarget(req.RoomID, req.Target)
		} else {
			e = b.peerCall(ctx, peer, RPCRequest{Operation: "binding", Request: &req}, &existing)
		}
		if e == nil {
			if existing.RoomID != req.RoomID || !SameTerminal(existing.Target, req.Target) {
				return fmt.Errorf("binding key mismatch")
			}
			if code, err := b.checkBindingHost(ctx, req, existing); code != "" {
				return finish(failure(code, err), "complete")
			}
			if local {
				response, e = (&HostOperations{b.Store, b.Client}).Join(ctx, req, existing.Routed)
			} else {
				e = b.peerCall(ctx, peer, RPCRequest{Operation: "join", Request: &req, Endpoints: &existing.Routed}, &response)
			}
			if e != nil {
				return e
			}
			phase := "complete"
			if response.Code == "operation_uncertain" {
				phase = "uncertain"
			}
			return finish(response, phase)
		}
		if !errors.Is(e, os.ErrNotExist) && !strings.Contains(e.Error(), "binding_missing") {
			return e
		}
		room, e := b.requestRoom(ctx, req, local)
		if e != nil {
			return finish(failure(e.Error(), e), "complete")
		}
		if e = b.Store.Update(func(state *State) error { state.Rooms[room.ID] = room; return nil }); e != nil {
			return e
		}
		if outcome.Phase == "uncertain" || outcome.Phase == "intent" {
			return finish(failure("operation_uncertain", nil), "uncertain")
		}
		if _, _, e = b.Client.Agent(ctx, req.Target); e != nil {
			return finish(failure("agent_replaced", e), "complete")
		}
		outcome.Phase = "intent"
		if e = finish(Response{Status: "pending"}, "intent"); e != nil {
			return e
		}
		b.mu.Lock()
		if b.Tunnels == nil {
			b.Tunnels = &TunnelManager{}
		}
		tunnels := b.Tunnels
		b.mu.Unlock()
		routePeer := peer
		if room.Host.MachineID != b.Client.LocalMachineID && req.Target.MachineID == b.Client.LocalMachineID {
			state, e := b.Store.Read()
			if e != nil {
				return e
			}
			routePeer = state.Peers[room.Host.MachineID]
		}
		endpoints, e := tunnels.Acquire(ctx, routePeer, room, req.Target)
		if e != nil {
			var retained *AcquisitionError
			phase := "complete"
			if errors.As(e, &retained) {
				phase = "uncertain"
			}
			code := "route_unavailable"
			if e.Error() == "unsupported_route" {
				code = "unsupported_route"
			}
			return finish(failure(code, e), phase)
		}
		if !local {
			cached := Binding{RoomID: req.RoomID, Target: req.Target, ParticipantName: "h_" + HostBindingKey(req.RoomID, req.Target)[:16], Routed: endpoints, JoinPhase: "intent", JoinRequestID: req.ID}
			if e = b.Store.saveBinding(cached); e != nil {
				return finish(failure("operation_uncertain", e), "uncertain")
			}
		}
		if local {
			response, e = (&HostOperations{b.Store, b.Client}).Join(ctx, req, endpoints)
		} else {
			e = b.peerCall(ctx, peer, RPCRequest{Operation: "join", Request: &req, Endpoints: &endpoints}, &response)
		}
		if e != nil {
			return finish(failure("operation_uncertain", e), "uncertain")
		}
		if response.Code == "operation_uncertain" {
			return finish(response, "uncertain")
		}
		if response.Binding != nil && !local {
			if response.Binding.RoomID != req.RoomID || !SameTerminal(response.Binding.Target, req.Target) {
				return finish(failure("operation_uncertain", fmt.Errorf("binding key mismatch")), "uncertain")
			}
			if e = b.Store.saveBinding(*response.Binding); e != nil {
				return finish(failure("operation_uncertain", e), "uncertain")
			}
		}
		if response.Status == "error" {
			if e = tunnels.Release(ctx, req.RoomID, req.Target); e != nil {
				return finish(failure("route_unavailable", e), "uncertain")
			}
		}
		return finish(response, "complete")
	})
	return response, e
}
func (b *Broker) Reconcile(ctx context.Context) error {
	state, e := b.Store.Read()
	if e != nil {
		return e
	}
	for key, record := range state.Bindings {
		if HostBindingKey(record.RoomID, record.Target) != key {
			return fmt.Errorf("binding key mismatch")
		}
		if record.Target.MachineID == "self" && record.Target.HerdrSocket == b.Client.HerdrSocket {
			record.Target.MachineID = b.Client.LocalMachineID
		}
		closed := false
		if room, roomErr := b.Store.Room(record.RoomID); roomErr == nil {
			closed = room.Closed
		} else if !errors.Is(roomErr, os.ErrNotExist) {
			return roomErr
		} else if cached, ok := state.Rooms[record.RoomID]; ok && cached.Host.MachineID != b.Client.LocalMachineID {
			if source, ok := state.Peers[cached.Host.MachineID]; ok {
				var rooms map[string]Room
				if b.peerCall(ctx, source, RPCRequest{Operation: "rooms"}, &rooms) == nil {
					if room, ok := rooms[record.RoomID]; ok && room.ID == record.RoomID && room.Host.MachineID == source.Machine.ID && room.Host.HerdrSocket == source.Config.HerdrSocket {
						closed = room.Closed
					}
				}
			}
		}
		if closed {
			e = b.Store.WithBinding(ctx, "route:"+BindingKey(record.RoomID, record.Target), func() error {
				b.mu.Lock()
				tunnels := b.Tunnels
				b.mu.Unlock()
				if tunnels != nil {
					return tunnels.Release(ctx, record.RoomID, record.Target)
				}
				return nil
			})
			if e != nil {
				return e
			}
			continue
		}
		if record.Target.MachineID != b.Client.LocalMachineID {
			peer, ok := state.Peers[record.Target.MachineID]
			if !ok {
				continue
			}
			e = b.Store.WithBinding(ctx, "route:"+BindingKey(record.RoomID, record.Target), func() error {
				var current Binding
				if e = b.peerCall(ctx, peer, RPCRequest{Operation: "binding", BindingKey: key}, &current); e != nil {
					return nil
				}
				if HostBindingKey(current.RoomID, current.Target) != key {
					return fmt.Errorf("binding key mismatch")
				}
				if e = b.Store.saveBinding(current); e != nil {
					return e
				}
				if current.Status == "offline" || current.Status == "left" {
					b.mu.Lock()
					tunnels := b.Tunnels
					b.mu.Unlock()
					if tunnels != nil {
						return tunnels.Release(ctx, current.RoomID, current.Target)
					}
				}
				return nil
			})
			if e != nil {
				return e
			}
			continue
		}
		e = b.Store.WithBinding(ctx, "route:"+BindingKey(record.RoomID, record.Target), func() error {
			return b.Store.WithBinding(ctx, "join:"+terminalBindingKey(record.RoomID, record.Target), func() error {
				current, e := b.Store.Binding(key)
				if e != nil {
					return e
				}
				original, code, e := (&HostOperations{b.Store, b.Client}).original(ctx, current)
				if e != nil {
					return nil
				}
				if code == "participant_disconnected" {
					b.mu.Lock()
					tunnels := b.Tunnels
					b.mu.Unlock()
					if tunnels != nil {
						return tunnels.Release(ctx, record.RoomID, record.Target)
					}
					return nil
				}
				if code == "" {
					original.Target.MachineID = current.Target.MachineID
					return b.Store.saveBinding(original)
				}
				return nil
			})
		})
		if e != nil {
			return e
		}
	}
	b.mu.Lock()
	tunnels := b.Tunnels
	b.mu.Unlock()
	if tunnels != nil {
		for _, status := range tunnels.Status() {
			if status == "route_unavailable" {
				return fmt.Errorf("route_unavailable")
			}
		}
	}
	return nil
}

func (b *Broker) requestRoom(ctx context.Context, req Request, local bool) (Room, error) {
	if req.RoomHost != nil {
		return b.ResolveSelectedRoom(ctx, req.RoomID, *req.RoomHost)
	}
	if local {
		room, err := b.Store.Room(req.RoomID)
		if err == nil {
			return b.validateRoom(ctx, room)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Room{}, err
		}
	}
	return b.ResolveRoom(ctx, req.RoomID)
}

func (b *Broker) completedResponse(ctx context.Context, req Request, outcome Outcome) Response {
	response := hostResponse(outcome.Response, req.Target.MachineID)
	if req.Target.MachineID == b.Client.LocalMachineID {
		response, _ = (&HostOperations{b.Store, b.Client}).withHandoff(ctx, response, nil)
	}
	return response
}

func (b *Broker) readRequest(ctx context.Context, peer Peer, req Request) (Response, error) {
	switch req.Op {
	case "catalog":
		return b.serveCatalog(ctx, peer, req)
	case "probe":
		return Response{Status: "ready", Version: HelperVersion}, nil
	default:
		return Response{Status: "status"}, b.Reconcile(ctx)
	}
}
