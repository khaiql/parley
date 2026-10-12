package herdr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/khaiql/parley/internal/descriptor"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"
)

type ProbeResult struct {
	Version      int           `json:"version"`
	Config       SessionConfig `json:"config"`
	Capabilities []string      `json:"capabilities"`
	Agent        *AgentTarget  `json:"agent,omitempty"`
	AgentStatus  string        `json:"agent_status,omitempty"`
}
type RPCRequest struct {
	Operation    string     `json:"operation"`
	Target       string     `json:"target,omitempty"`
	Session      string     `json:"session"`
	SessionKey   string     `json:"session_key,omitempty"`
	ControllerID string     `json:"controller_id,omitempty"`
	MachineID    string     `json:"machine_id,omitempty"`
	RequestID    string     `json:"request_id,omitempty"`
	Digest       string     `json:"digest,omitempty"`
	Outcome      *Outcome   `json:"outcome,omitempty"`
	RoomID       string     `json:"room_id,omitempty"`
	BindingKey   string     `json:"binding_key,omitempty"`
	Request      *Request   `json:"request,omitempty"`
	Endpoints    *Endpoints `json:"endpoints,omitempty"`
}

func decodeRPC(r io.Reader, v *RPCRequest) error {
	b, e := io.ReadAll(io.LimitReader(r, requestLimit+1))
	if e != nil {
		return e
	}
	if len(b) > requestLimit || !utf8.Valid(b) {
		return fmt.Errorf("request too large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e = d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("exactly one helper request required")
	}
	return nil
}
func DecodeRPC(r io.Reader) (RPCRequest, error) {
	var v RPCRequest
	e := decodeRPC(r, &v)
	if e == nil {
		e = v.Validate()
	}
	return v, e
}
func (r RPCRequest) Validate() error {
	if e := validateSession(r.Session); e != nil {
		return e
	}
	allowed := RPCRequest{Operation: r.Operation, Session: r.Session}
	switch r.Operation {
	case "probe":
		if r.Target != "" {
			allowed.Target = r.Target
			allowed.SessionKey = r.SessionKey
			allowed.ControllerID = r.ControllerID
			if e := safeID(r.Target); e != nil {
				return e
			}
		}
	case "claim-controller":
		allowed.ControllerID = r.ControllerID
		allowed.MachineID = r.MachineID
		allowed.SessionKey = r.SessionKey
		if e := safeID(r.ControllerID); e != nil {
			return e
		}
		if e := safeID(r.MachineID); e != nil {
			return e
		}
		if r.MachineID == "self" {
			return fmt.Errorf("normalized machine required")
		}
	case "release-controller", "events", "requests", "rooms":
		allowed.ControllerID = r.ControllerID
		allowed.SessionKey = r.SessionKey
	case "claim-request":
		allowed.ControllerID = r.ControllerID
		allowed.SessionKey = r.SessionKey
		allowed.RequestID = r.RequestID
		allowed.Digest = r.Digest
		if e := safeID(r.RequestID); e != nil {
			return e
		}
		if !validDigest(r.Digest) {
			return fmt.Errorf("invalid digest")
		}
	case "outcome":
		allowed.ControllerID = r.ControllerID
		allowed.SessionKey = r.SessionKey
		allowed.RequestID = r.RequestID
		allowed.Outcome = r.Outcome
		if e := safeID(r.RequestID); e != nil {
			return e
		}
		if r.Outcome != nil {
			if e := validateOutcome(*r.Outcome); e != nil {
				return e
			}
		}
	case "room":
		allowed.ControllerID = r.ControllerID
		allowed.SessionKey = r.SessionKey
		allowed.RoomID = r.RoomID
		if e := safeID(r.RoomID); e != nil {
			return e
		}
	case "binding":
		allowed.ControllerID = r.ControllerID
		allowed.SessionKey = r.SessionKey
		allowed.BindingKey = r.BindingKey
		if !validDigest(r.BindingKey) {
			return fmt.Errorf("invalid binding key")
		}
	case "start", "join", "leave", "probe-route":
		return fmt.Errorf("capability_unavailable: %s", r.Operation)
	default:
		return fmt.Errorf("unsupported helper operation")
	}
	if r.Operation != "probe" || r.Target != "" {
		if !validDigest(r.SessionKey) {
			return fmt.Errorf("invalid session key")
		}
		if e := safeID(r.ControllerID); e != nil {
			return e
		}
	}
	if !reflect.DeepEqual(allowed, r) {
		return fmt.Errorf("fields not allowed for helper operation")
	}
	return nil
}
func ManagePeer(ctx context.Context, c *Client, s *Store, m Machine) (Peer, error) {
	p := Peer{Machine: m}
	if !m.Enabled {
		return p, fmt.Errorf("helper_disabled")
	}
	if e := safeID(c.LocalMachineID); e != nil {
		return p, e
	}
	binary, e := c.helper(ctx, m)
	if e != nil {
		return p, e
	}
	p.Binary = binary
	b, e := c.Call(ctx, p, RPCRequest{Operation: "probe"})
	if e != nil {
		return p, e
	}
	var probe ProbeResult
	if e = json.Unmarshal(b, &probe); e != nil {
		return p, e
	}
	key, e := SessionKey(probe.Config.HerdrSocket)
	if e != nil || key != probe.Config.SessionKey || probe.Version != 1 {
		return p, fmt.Errorf("helper_incompatible")
	}
	for _, wanted := range RequiredCapabilities {
		found := false
		for _, got := range probe.Capabilities {
			found = found || got == wanted
		}
		if !found {
			return p, fmt.Errorf("capability_unavailable: %s", wanted)
		}
	}
	// A changed saved profile/session cannot silently replace an activated session.
	state, e := s.Read()
	if e != nil {
		return p, e
	}
	if old, ok := state.Peers[m.ID]; ok && old.Config.SessionKey != key {
		return p, fmt.Errorf("session_mismatch")
	}
	controller := state.ControllerID
	if controller == "" {
		controller = c.LocalMachineID
	}
	if e := safeID(controller); e != nil {
		return p, e
	}
	b, e = c.Call(ctx, p, RPCRequest{Operation: "claim-controller", ControllerID: controller, MachineID: m.ID, SessionKey: key})
	if e != nil {
		return p, e
	}
	if e = json.Unmarshal(b, &p.Config); e != nil {
		return p, e
	}
	if p.Config.ControllerID != controller || p.Config.MachineID != m.ID || p.Config.SessionKey != key || p.Config.HerdrSocket != probe.Config.HerdrSocket {
		return p, fmt.Errorf("session_mismatch")
	}
	e = s.Update(func(state *State) error { state.Peers[m.ID] = p; return nil })
	if e == nil {
		c.managed.Store(m.ID, p)
	}
	return p, e
}

// HostSession contains only paths derived on this host, never RPC-supplied paths.
type HostSession struct {
	Config    SessionConfig
	ConfigDir string
	Store     *Store
}

func ResolveHost(ctx context.Context, c *Client, session string) (HostSession, error) {
	var h HostSession
	if e := validateSession(session); e != nil {
		return h, e
	}
	b, e := c.runner()(ctx, c.HerdrBinary, []string{"--session", session, "session", "list", "--json"}, nil)
	if e != nil {
		return h, fmt.Errorf("%w: %w", ErrHostUnavailable, e)
	}
	var list struct {
		Sessions []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
			Socket  string `json:"socket_path"`
		} `json:"sessions"`
	}
	if e = json.Unmarshal(b, &list); e != nil {
		return h, e
	}
	socket := ""
	for _, s := range list.Sessions {
		if s.Name == session && s.Running {
			if socket != "" {
				return h, fmt.Errorf("ambiguous session")
			}
			socket = s.Socket
		}
	}
	if socket == "" {
		return h, fmt.Errorf("%w: saved session unavailable", ErrHostUnavailable)
	}
	socket, e = filepath.EvalSymlinks(socket)
	if e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return h, fmt.Errorf("%w: %w", ErrHostUnavailable, e)
		}
		return h, e
	}
	socket, e = filepath.Abs(socket)
	if e != nil {
		return h, e
	}
	key, e := SessionKey(socket)
	if e != nil {
		return h, e
	}
	// Verify reachability of the exact resolved socket; never fall back to shell defaults.
	b, e = socketAPI(ctx, socket, "events.subscribe", subscriptionParams())
	if e != nil {
		var api *apiError
		if errors.As(e, &api) {
			if api.Code == "events_lost" {
				return h, fmt.Errorf("%w: %w", ErrHostUnavailable, e)
			}
			return h, fmt.Errorf("capability_unavailable: %w", e)
		}
		return h, fmt.Errorf("%w: %w", ErrHostUnavailable, e)
	}
	var ack struct {
		Result struct {
			Type string `json:"type"`
		} `json:"result"`
	}
	if e = json.Unmarshal(b, &ack); e != nil || ack.Result.Type != "subscription_started" {
		return h, fmt.Errorf("capability_unavailable: events")
	}
	b, e = c.runner()(ctx, c.HerdrBinary, []string{"--session", session, "plugin", "list", "--json"}, nil)
	if e != nil {
		return h, fmt.Errorf("%w: %w", ErrHostUnavailable, e)
	}
	if _, e = installedHelper(b); e != nil {
		return h, e
	}
	b, e = c.runner()(ctx, c.HerdrBinary, []string{"--session", session, "plugin", "config-dir", PluginID}, nil)
	if e != nil {
		return h, fmt.Errorf("%w: %w", ErrHostUnavailable, e)
	}
	configRoot := strings.TrimSpace(string(b))
	if !filepath.IsAbs(configRoot) || filepath.Clean(configRoot) != configRoot {
		return h, fmt.Errorf("invalid plugin config root")
	}
	stateRoot := os.Getenv("XDG_STATE_HOME")
	if stateRoot == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return h, e
		}
		stateRoot = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(stateRoot) {
		return h, fmt.Errorf("invalid plugin state root")
	}
	configDir, stateDir, e := SessionDirs(configRoot, filepath.Join(stateRoot, "herdr", "plugins", PluginID), socket)
	if e != nil {
		return h, e
	}
	h = HostSession{Config: SessionConfig{HerdrSocket: socket, SessionKey: key}, ConfigDir: configDir, Store: NewStore(stateDir)}
	return h, nil
}
func (h HostSession) controller(ctx context.Context, fn func(*SessionConfig) error) error {
	if e := privateDir(h.ConfigDir); e != nil {
		return e
	}
	return lockFile(ctx, filepath.Join(h.ConfigDir, "controller.lock"), func() error {
		var c SessionConfig
		e := readJSON(filepath.Join(h.ConfigDir, "controller.json"), &c)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if e == nil {
			if c.SessionKey != h.Config.SessionKey || c.HerdrSocket != h.Config.HerdrSocket || c.Role != "participant" || safeID(c.ControllerID) != nil || safeID(c.MachineID) != nil || c.MachineID == "self" {
				return fmt.Errorf("corrupt controller ownership")
			}
		}
		return fn(&c)
	})
}
func (h HostSession) Dispatch(ctx context.Context, r RPCRequest) (any, error) {
	if e := r.Validate(); e != nil {
		return nil, e
	}
	if r.Operation == "probe" && r.Target == "" {
		return ProbeResult{Version: 1, Config: h.Config, Capabilities: append([]string(nil), RequiredCapabilities...)}, nil
	}
	if r.SessionKey != h.Config.SessionKey {
		return nil, fmt.Errorf("session_mismatch")
	}
	var owner SessionConfig
	var result any
	e := h.controller(ctx, func(c *SessionConfig) error {
		if r.Operation == "claim-controller" {
			if c.ControllerID != "" && (c.ControllerID != r.ControllerID || c.MachineID != r.MachineID) {
				return fmt.Errorf("controller_conflict")
			}
			*c = h.Config
			c.Role = "participant"
			c.ControllerID = r.ControllerID
			c.MachineID = r.MachineID
			if e := writeJSON(filepath.Join(h.ConfigDir, "controller.json"), c); e != nil {
				return e
			}
		} else if c.ControllerID != r.ControllerID {
			return fmt.Errorf("controller_conflict")
		}
		owner = *c
		if r.Operation == "release-controller" {
			result = owner
			return os.Remove(filepath.Join(h.ConfigDir, "controller.json"))
		}
		var e error
		if e := ctx.Err(); e != nil {
			return e
		}
		result, e = h.dispatchOwned(ctx, r, owner)
		return e
	})
	return result, e
}
func (h HostSession) dispatchOwned(ctx context.Context, r RPCRequest, owner SessionConfig) (any, error) {
	switch r.Operation {
	case "probe":
		c := Client{LocalMachineID: owner.MachineID, HerdrSocket: h.Config.HerdrSocket}
		agent, status, e := c.Agent(ctx, AgentTarget{MachineID: owner.MachineID, HerdrSocket: h.Config.HerdrSocket, PaneID: r.Target})
		if e != nil {
			return nil, e
		}
		return ProbeResult{Version: 1, Config: owner, Capabilities: append([]string(nil), RequiredCapabilities...), Agent: &agent, AgentStatus: status}, nil
	case "claim-controller", "release-controller":
		return owner, nil
	case "requests":
		requests, e := h.Store.Pending()
		if e != nil {
			return nil, e
		}
		out := make([]Request, 0, len(requests))
		origin := Peer{Machine: Machine{ID: owner.MachineID}, Config: owner}
		for _, request := range requests {
			b, e := json.Marshal(request)
			if e != nil {
				return nil, e
			}
			normalized, e := DecodeRequest(bytes.NewReader(b), origin)
			if e != nil {
				return nil, e
			}
			out = append(out, normalized)
		}
		return out, nil
	case "claim-request":
		var request Request
		if e := readJSON(filepath.Join(h.Store.dir, "requests", r.RequestID+".json"), &request); e != nil {
			return nil, e
		}
		data, e := json.Marshal(request)
		if e != nil {
			return nil, e
		}
		normalized, e := DecodeRequest(bytes.NewReader(data), Peer{Machine: Machine{ID: owner.MachineID}, Config: owner})
		if e != nil {
			return nil, e
		}
		if normalized.ID != r.RequestID || RequestDigest(normalized) != r.Digest {
			return nil, fmt.Errorf("request_conflict")
		}
		return h.Store.Claim(r.RequestID, r.Digest, r.ControllerID)
	case "outcome":
		if r.Outcome != nil {
			if r.Outcome.ControllerID != r.ControllerID {
				return nil, fmt.Errorf("controller_conflict")
			}
			if e := h.Store.Complete(r.RequestID, *r.Outcome); e != nil {
				return nil, e
			}
		}
		state, e := h.Store.Read()
		if e != nil {
			return nil, e
		}
		o, ok := state.Requests[r.RequestID]
		if !ok {
			return nil, fmt.Errorf("outcome_missing")
		}
		return o, nil
	case "rooms":
		records, e := h.Store.Rooms()
		if e != nil {
			return nil, e
		}
		for id, room := range records {
			room, e = h.validRoom(room, owner)
			if e != nil {
				return nil, e
			}
			records[id] = room
		}
		return records, nil
	case "room":
		room, e := h.Store.Room(r.RoomID)
		if e != nil {
			return nil, e
		}
		return h.validRoom(room, owner)
	case "binding":
		state, e := h.Store.Read()
		if e != nil {
			return nil, e
		}
		binding, ok := state.Bindings[r.BindingKey]
		if !ok {
			return nil, fmt.Errorf("binding_missing")
		}
		if e := safeID(binding.RoomID); e != nil {
			return nil, e
		}
		target, e := h.normalizedTarget(binding.Target, owner)
		if e != nil {
			return nil, e
		}
		binding.Target = target
		return binding, nil
	case "events":
		return nil, fmt.Errorf("stream operation requires RelayEvents")
	}
	return nil, fmt.Errorf("capability_unavailable")
}
func (h HostSession) VerifyController(ctx context.Context, r RPCRequest) error {
	if e := r.Validate(); e != nil {
		return e
	}
	if r.SessionKey != h.Config.SessionKey {
		return fmt.Errorf("session_mismatch")
	}
	return h.controller(ctx, func(c *SessionConfig) error {
		if c.ControllerID != r.ControllerID {
			return fmt.Errorf("controller_conflict")
		}
		return nil
	})
}

func (h HostSession) normalizedTarget(target AgentTarget, owner SessionConfig) (AgentTarget, error) {
	b, e := json.Marshal(Request{ID: "identity", Op: "join", RoomID: "identity", Target: target})
	if e != nil {
		return target, e
	}
	request, e := DecodeRequest(bytes.NewReader(b), Peer{Machine: Machine{ID: owner.MachineID}, Config: owner})
	return request.Target, e
}
func (h HostSession) validRoom(room Room, owner SessionConfig) (Room, error) {
	if e := safeID(room.ID); e != nil {
		return room, e
	}
	if e := safeID(room.HostSessionID); e != nil {
		return room, e
	}
	target, e := h.normalizedTarget(room.Host, owner)
	if e != nil {
		return room, e
	}
	room.Host = target
	parsed, e := descriptor.Parse(room.Source.Descriptor)
	if e != nil || parsed.RoomID != room.ID || parsed.Host != "127.0.0.1" {
		return room, fmt.Errorf("corrupt room descriptor")
	}
	u, e := url.Parse(room.Source.ArtifactURL)
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/rooms/"+room.ID+"/artifacts" {
		return room, fmt.Errorf("corrupt room artifact endpoint")
	}
	return room, nil
}
