# Parley Herdr Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Track execution in the repository's configured issue tracker; checkboxes describe the requested plan, not a replacement tracker.

**Goal:** Start a room on an existing Herdr agent's machine, join existing local or SSH agents by room ID, automate both SSH forwarding directions, and deliver message notifications without manual polling.

**Architecture:** A local broker maintains room discovery and plugin-owned SSH connections; remote plugin actions call it through a private reverse-forwarded Unix socket. Participant workers run alongside their existing agents, wait on Parley, and prompt only the same ready conversation. Small additive Parley changes support artifact port remapping and cursor-preserving session recovery.

**Tech Stack:** Go 1.26.1, existing Cobra, Go standard library, Herdr CLI/socket API, OpenSSH, existing Parley JSON runtime/event stores.

**Spec:** `docs/superpowers/specs/2026-10-12-herdr-plugin-design.md` (proposed; review alongside this plan before execution).

## Global Constraints

- Go 1.26.1; existing Cobra dependency; no new production dependencies.
- Initial platforms: macOS and Linux.
- Initial Herdr minimum: 0.9.3; additionally verify required CLI/schema capabilities during setup.
- Use the initiating agent's machine as the room host.
- Attach existing agents; do not launch or restart agents.
- Support local/local, local/remote, remote/local, and same-remote-machine participants. Reject different-remote-machine routing with `unsupported_route`.
- One local controller per paired remote Herdr session. Reject a second active controller with `controller_conflict`.
- Keep existing Parley CLI behavior, JSON status conventions, descriptors, and non-consuming `wait` behavior.
- Bind TCP forwards to `127.0.0.1`; never expose Parley's unauthenticated endpoints publicly.
- Retain unread events and participant session identity across transport recovery.
- Plugin actions do not execute arbitrary commands supplied by remote requests.

## Review Focus

- A remote join submitted twice, or a lost mutation response: return the existing binding, never create duplicate participants; Task 7.
- A requested port already occupied, or only one of two forwards succeeds: remap safely or roll back, never report a usable room prematurely; Task 6.
- An agent exits, changes native conversation, or its pane moves: follow the terminal on moves and suspend on replacement; Task 8.
- More than 50 events arrive during a tunnel outage: preserve every unread event and the existing session ID; Tasks 3 and 9.
- A corrupt pairing or a different local controller targets an already-paired remote session: fail visibly without overwriting ownership; Tasks 4 and 5.

---

## Execution order and file responsibilities

This is one integrated workflow. Endpoint remapping and replay are independently testable compatibility changes; the callback and forwarding layers are dependent on the same broker. Do not create a separate hosted service or independently deployable transport daemon.

| File | Responsibility |
| --- | --- |
| `docs/herdr-transport-proof.md` | Actual Herdr/OpenSSH capability results, versions, and observed response shapes |
| `cmd/parley/join.go` | New validated artifact endpoint flag |
| `cmd/parley/daemon_commands.go`, `daemon_process.go`, `participant_daemon.go` | Carry endpoint override and replay mode into adapters |
| `cmd/parley/resume.go` | Resume a saved participant without generating another session |
| `internal/protocol/protocol.go`, `internal/server/server.go` | Add optional join replay and return complete backlog |
| `internal/herdr/types.go`, `store.go` | Integration contracts and private durable records |
| `internal/herdr/process.go`, `client.go` | Bounded subprocess execution and typed Herdr/Parley host calls |
| `internal/herdr/control.go`, `pair.go` | Restricted callback protocol and per-session pairing |
| `internal/herdr/ssh.go` | Owned SSH masters, allocated forwards, cancellation and rollback |
| `internal/herdr/broker.go` | Room operation ordering, intent journal, binding/lease ownership |
| `internal/herdr/worker.go` | Local participant wait, safe notification, adapter recovery |
| `internal/herdr/form.go` | Small terminal form and result display |
| `cmd/parley/herdr.go`, `herdr_rpc.go` | CLI wiring and fixed remote helper dispatch |
| `herdr-plugin.toml` | Build, actions, popup, startup, and lifecycle hook entrypoints |
| `README.md`, `skills/parley/SKILL.md`, `docs/herdr-plugin.md` | Installation and operational behavior |
| Adjacent `*_test.go` files | Focused test cycles for each deliverable |

Use current function names and patterns rather than refactoring existing command files. Keep callback transport separate from `internal/adapter/control.go`: the latter's message types and timeouts are participant-specific and lack callback authentication/size limits.

## Task 1: Prove remote action dispatch and OpenSSH forwarding

**Files:**
- Create: `docs/herdr-transport-proof.md`
- Temporary only: two minimal test plugins and an echo listener in a private temporary directory; do not ship these.

**Interfaces:**
- Consumes: an existing saved SSH profile, real local/remote Herdr 0.9.3 or newer, and an existing remote agent.
- Produces: verified machine/agent JSON fixtures, absolute local/remote plugin paths, callback-forward syntax, allocated-port/cancellation behavior, and detach survival evidence. Later tasks use these observations to implement parsers and commands.

- [ ] **Step 1: Capture actual schemas and versions.**

Run on the controller, using a real profile ID from the first command:

```sh
herdr machine list --json
herdr api schema --json
herdr agent list
herdr --machine <profile-id> agent list
herdr --machine <profile-id> plugin list --json
ssh -V
```

Inspect agent responses for `terminal_id`, `pane_id`, `agent`, `agent_status`, and `agent_session.value`. Use the real existing pane ID, not `--current`, in remote commands. Record schema-supported `agent.get`, `agent.prompt`, `plugin.pane.open`, and `pane.agent_status_changed`. Verify minimum version using `herdr --version` on both machines. Capture sanitized JSON in the proof document; do not store usernames, paths, hostnames, or conversation IDs from the user's real environment as repository fixtures.

- [ ] **Step 2: Link a throwaway remote action.**

Use this manifest in a trusted temporary checkout on the remote host:

```toml
id = "parley.transport-proof"
name = "Parley transport proof"
version = "0.0.1"
min_herdr_version = "0.9.3"
platforms = ["linux", "macos"]

[[actions]]
id = "context"
title = "Parley transport proof"
contexts = ["pane"]
command = ["python3", "context.py"]
```

```python
import json, os, socket
context = json.loads(os.environ["HERDR_PLUGIN_CONTEXT_JSON"])
with socket.socket(socket.AF_UNIX) as client:
    client.settimeout(5)
    client.connect(os.environ["PARLEY_PROOF_CALLBACK"])
    client.sendall(json.dumps({"pane": context.get("focused_pane_id")}).encode() + b"\n")
    print(client.recv(4096).decode())
```

Set `PARLEY_PROOF_CALLBACK` in the remote test server's launch environment, or put the trusted absolute test socket path in this throwaway script. Invoke the action from the remotely selected pane and check plugin logs. Expected: command runs remotely and identifies the original selected agent pane.

- [ ] **Step 3: Run the callback forward and dynamic-port proof.**

Use a local standard-library echo server:

```python
import json, os, socket, sys
path = sys.argv[1]
server = socket.socket(socket.AF_UNIX)
server.bind(path)
os.chmod(path, 0o600)
server.listen()
while True:
    conn, _ = server.accept()
    with conn:
        conn.settimeout(5)
        request = conn.makefile("rb").readline(65537)
        json.loads(request)
        conn.sendall(b'{"status":"received_by_local"}\n')
```

Prepare short private `0700` directories on both hosts. Launch the owned SSH master exactly as in the spec, with the reverse stream-local forward. Add a remote TCP forward using:

```text
ssh -S <master.sock> -O forward
    -R 127.0.0.1:0:127.0.0.1:<local-test-port> <target>
```

Expected: stdout contains one numeric allocated remote port. Connect remotely and verify echo bytes. Cancel the forward using the syntax verified by this OpenSSH version; confirm the port no longer accepts connections. Test `-L` similarly, occupying the first requested local port to verify error detection. Closing the Herdr UI must leave the owned SSH master and remote server running. Kill only the test master with its own `-O exit`, then unlink the test plugin and delete the private test files.

- [ ] **Step 4: Record pass/fail evidence and settle parsers.**

Document versions, commands, sanitized envelopes, socket lengths, stream-local forwarding permissions, how to cancel allocated forwards, and whether the selected remote action reaches the local listener. If the proof cannot run, mark it unverified and stop implementation before Task 5. If native session identity is absent, install/reload the existing Herdr agent integration as a setup prerequisite; do not invent identity from pane labels.

- [ ] **Step 5: Commit the evidence.**

```sh
git add docs/herdr-transport-proof.md
git commit -m "docs: verify Herdr plugin transport contract"
```

## Task 2: Allow artifact endpoint remapping on join

**Files:**
- Modify: `cmd/parley/join.go`, `daemon_commands.go`, `daemon_process.go`, `participant_daemon.go`, `participant_artifacts.go`
- Create: `cmd/parley/participant_endpoint_test.go`
- Modify: `cmd/parley/main_test.go`, `README.md`

**Interfaces:**
- Consumes: existing `descriptor.Descriptor`, `deriveArtifactEndpoint`, and participant daemon launch seams.
- Produces: `participantDaemonConfig.ArtifactEndpoint string`, `validateArtifactEndpoint(raw, roomID string) error`, and `join --artifact-endpoint <url>`.

- [ ] **Step 1: Add failing validation and CLI propagation tests.**

```go
func TestArtifactEndpointOverrideValidation(t *testing.T) {
    cases := []struct { raw string; valid bool }{
        {"http://127.0.0.1:49002/rooms/room-1/artifacts", true},
        {"http://[::1]:49002/rooms/room-1/artifacts", true},
        {"https://127.0.0.1:49002/rooms/room-1/artifacts", false},
        {"http://user:secret@127.0.0.1:49002/rooms/room-1/artifacts", false},
        {"http://127.0.0.1:49002/rooms/other/artifacts", false},
        {"http://127.0.0.1:49002/rooms/room-1/artifacts?x=1", false},
        {"http://127.0.0.1:49002/rooms/room-1/artifacts#x", false},
        {"http://127.0.0.1:0/rooms/room-1/artifacts", false},
    }
    for _, c := range cases {
        if got := validateArtifactEndpoint(c.raw, "room-1"); (got == nil) != c.valid {
            t.Fatalf("%q: error=%v, valid=%v", c.raw, got, c.valid)
        }
    }
}
```

Extend `TestJoinLaunchesParticipantDaemon` with the new flag and assert `cfg.ArtifactEndpoint`. Add a separate case with no override so the existing endpoint remains unchanged. Assert invalid input fails before the launch seam is called.

- [ ] **Step 2: Run the failing tests.**

```sh
go test ./cmd/parley -run 'TestArtifactEndpointOverride|TestJoinLaunchesParticipantDaemon' -count=1
```

Expected: missing validator/config field or unsupported flag.

- [ ] **Step 3: Implement the flag through every daemon boundary.**

Validator body after parsing with `url.Parse`:

```go
if u.Scheme != "http" || u.Hostname() == "" || u.User != nil ||
    u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") ||
    u.Opaque != "" || u.Path != artifactHTTPPath(roomID) {
    return fmt.Errorf("invalid artifact endpoint")
}
port, err := strconv.Atoi(u.Port())
if err != nil || port < 1 || port > 65535 {
    return fmt.Errorf("artifact endpoint requires a valid port")
}
```

Add `ArtifactEndpoint string` to the daemon config, flags on both public join and hidden participant daemon, and argv propagation in `startParticipantDaemonProcess`. Validate at the public and hidden command boundaries. Adapter initialization uses the override when nonempty and the existing derivation otherwise. Do not add it to the room's advertised server metadata. Document the optional flag beside the existing tunnel instructions.

- [ ] **Step 4: Verify remapped HTTP bytes, not just config plumbing.**

Use `httptest.NewServer` and a real room server/adapter in `cmd/parley/participant_endpoint_test.go`. Make the advertised artifact port deliberately unreachable, supply the test server's URL with the exact room artifact path, send a file and fetch it through the adapter. Assert bytes/SHA match. Re-run existing artifact endpoint and send/fetch tests:

```sh
go test ./cmd/parley -run 'TestArtifactEndpoint|TestJoin|TestArtifactFetch|TestSend' -count=1
```

- [ ] **Step 5: Commit.**

```sh
git add cmd/parley README.md
git commit -m "feat: allow artifact endpoint overrides for tunneled joins"
```

## Task 3: Resume the same participant with complete unread replay

**Files:**
- Create: `cmd/parley/resume.go`, `cmd/parley/resume_test.go`
- Modify: `cmd/parley/main.go`, `daemon_commands.go`, `daemon_process.go`, `participant_daemon.go`
- Modify: `internal/protocol/protocol.go`, `protocol_test.go`, `internal/server/server.go`, `server_internal_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `runtime.LoadSession`, `runtime.ParticipantStore`, `Store.LoadMeta`, `Store.AppendLocal`, and current daemon launch/control checks.
- Produces: `resume --session <id> [--descriptor <descriptor>] [--artifact-endpoint <url>]`; `participantDaemonConfig.ReplayAfterSeq *int64`; `protocol.JoinRequest.ReplayAfterSeq *int64` with JSON name `replay_after_seq`.

- [ ] **Step 1: Write failing replay and identity tests.**

Wire-format test:

```go
func TestJoinReplayZeroIsDistinctFromFreshJoin(t *testing.T) {
    zero := int64(0)
    data, err := EncodeLine(JoinRequest{RoomID: "room-1", Name: "alice", ReplayAfterSeq: &zero})
    if err != nil { t.Fatal(err) }
    if !bytes.Contains(data, []byte(`"replay_after_seq":0`)) { t.Fatal(string(data)) }
    fresh, err := EncodeLine(JoinRequest{RoomID: "room-1", Name: "alice"})
    if err != nil { t.Fatal(err) }
    if bytes.Contains(fresh, []byte("replay_after_seq")) { t.Fatal(string(fresh)) }
}
```

In `resume_test.go`, use the existing `useShortParleyHome`, launch seam, real event log, and control fixture helpers. Persist a session and participant metadata with `LastSeenSeq=2`. Add 75 later message events. Assert resume returns the original session/name, launches with replay cursor 2, preserves cursor 2, and leaves all 75 messages available. Add live-adapter rejection, explicit-left rejection, corrupt-session rejection, and mismatched room override rejection.

In server tests, append more than 50 events and join with replay cursor. Assert every sequence after the cursor is returned, in order, including lifecycle events. A fresh join keeps its existing history limit. A negative cursor or cursor beyond the current log returns `bad_request` before participant creation.

- [ ] **Step 2: Run tests to verify failure.**

```sh
go test ./internal/protocol ./internal/server ./cmd/parley -run 'TestJoinReplay|TestResume' -count=1
```

- [ ] **Step 3: Implement replay without changing fresh join semantics.**

In `handleJoin`, before publishing the participant join, select history under the existing mutex:

```go
if req.ReplayAfterSeq != nil {
    // Validate against latest persisted sequence before any join mutation.
    events, err = s.log.AfterSeq(*req.ReplayAfterSeq, 0)
} else {
    events, _, err = s.historyLocked(protocol.HistoryRequest{Limit: joinHistorySize}, 0)
}
```

Preserve current duplicate-online-name validation. Add hidden daemon `--replay-after-seq`; use `Flags().Changed` so zero remains a real replay cursor. Propagate it to the join request. Adapter initialization appends replayed events using existing deduplication and calls `MarkReceivedSeen` only when `ReplayAfterSeq == nil`.

`resumeCmd` loads existing session/meta, rejects `left` and an active control socket, validates endpoint overrides and same room ID, launches with existing name/role and `&meta.LastSeenSeq`, and returns `status: resumed`, the same `session_id`, and `command_args`. Do not call `NewSessionID` or overwrite active global participation. Add `resumeCmd()` to `newRootCmd` and document recovery scope. Updated plugin helpers on both endpoints are required for replay; do not claim compatibility with an older server silently ignoring the new field.

- [ ] **Step 4: Run a real adapter recovery regression.**

In package-main tests, launch the production `runParticipantAdapter` through a controllable TCP proxy, disconnect it, publish 75 messages from a remaining participant, relaunch in replay mode, and read the production participant mirror. Assert all unread sequences, original session, non-consuming wait, and artifact override survive. This test must not rely only on `internal/e2e`'s test-owned `AdapterHandle` implementation.

```sh
go test ./cmd/parley ./internal/protocol ./internal/server ./internal/adapter -run 'TestResume|TestJoinReplay|TestWait|TestSaveMeta' -count=1 -race
```

- [ ] **Step 5: Commit.**

```sh
git add cmd/parley internal/protocol internal/server README.md
git commit -m "feat: resume participant sessions without consuming unread events"
```

## Task 4: Define private integration state and callback contracts

**Files:**
- Create: `internal/herdr/types.go`, `store.go`, `store_test.go`, `control.go`, `control_test.go`

**Interfaces:**
- Produces the exact types below; all later tasks consume them.
- Produces `NewStore(dir string) *Store`, `(*Store).Update(func(*State) error) error`, and `(*Store).Read() (State, error)`.
- Produces `ServePeer(ctx context.Context, path string, peer Peer, dispatch func(context.Context, Peer, Request) (Response, error)) error` and `CallPeer(ctx context.Context, pair Pairing, req Request) (Response, error)`.

```go
type Machine struct {
    ID string `json:"id"`
    Label string `json:"label"`
    Target string `json:"target"`
    Session string `json:"session"`
    Enabled bool `json:"enabled"`
}
type AgentTarget struct {
    MachineID string `json:"machine_id"`
    HerdrSocket string `json:"herdr_socket"`
    PaneID string `json:"pane_id"`
    TerminalID string `json:"terminal_id"`
    Kind string `json:"kind"`
    NativeSession string `json:"native_session"`
}
type Endpoints struct {
    Descriptor string `json:"descriptor"`
    ArtifactURL string `json:"artifact_url"`
}
type Room struct {
    ID string `json:"id"`
    Topic string `json:"topic"`
    Host AgentTarget `json:"host"`
    HostSessionID string `json:"host_session_id"`
    Source Endpoints `json:"source"`
    Closed bool `json:"closed"`
}
type Binding struct {
    RoomID string `json:"room_id"`
    Target AgentTarget `json:"target"`
    SessionID string `json:"session_id"`
    ParticipantName string `json:"participant_name"`
    StateDir string `json:"state_dir"`
    Routed Endpoints `json:"routed"`
    NotifiedSeq int64 `json:"notified_seq"`
    Delivery string `json:"delivery"` // pending, submitted, uncertain, suspended, left
}
type Pairing struct {
    ControllerID string `json:"controller_id"`
    Callback string `json:"callback"`
    Token string `json:"token"`
    HerdrSocket string `json:"herdr_socket"`
}
type Peer struct { Machine Machine; Pair Pairing; Binary string }
type Request struct {
    ID string `json:"id"`
    Token string `json:"token"`
    Op string `json:"op"`
    RoomID string `json:"room_id,omitempty"`
    Topic string `json:"topic,omitempty"`
    Target AgentTarget `json:"target"`
}
type Response struct {
    Status string `json:"status"`
    Code string `json:"code,omitempty"`
    Message string `json:"message,omitempty"`
    Room *Room `json:"room,omitempty"`
    Binding *Binding `json:"binding,omitempty"`
}
type Outcome struct {
    Digest string `json:"digest"`
    Phase string `json:"phase"` // intent, complete, uncertain
    Response Response `json:"response"`
}
type State struct {
    ControllerID string `json:"controller_id"`
    Peers map[string]Peer `json:"peers"`
    Rooms map[string]Room `json:"rooms"`
    Bindings map[string]Binding `json:"bindings"`
    Requests map[string]Outcome `json:"requests"`
}
```

`MachineID="local"` identifies the configured controller host. Remote IDs are opaque Herdr profile IDs. Never derive them from labels. Add explicit JSON tags to `Peer` fields when persisting; `Token` never appears in user-facing status/log output. A peer's authoritative machine/session overrides request claims.

- [ ] **Step 1: Add failing private-state and callback tests.**

```go
func TestCorruptStateIsNotReset(t *testing.T) {
    dir := t.TempDir()
    if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{"), 0o600); err != nil { t.Fatal(err) }
    s := NewStore(dir)
    if _, err := s.Read(); err == nil { t.Fatal("corrupt state accepted") }
    if err := s.Update(func(*State) error { return nil }); err == nil { t.Fatal("corrupt state overwritten") }
    data, _ := os.ReadFile(filepath.Join(dir, "state.json"))
    if string(data) != "{" { t.Fatal("original state destroyed") }
}
```

Define `DecodeRequest(r io.Reader, peer Peer) (Request, error)` in `control.go`. Test unknown JSON fields, `op:"exec"`, invalid token, a 65,537-byte line, multiple JSON values, forged machine/socket identity, topic over 512 bytes, IDs over 128 bytes, and malformed UTF-8. Dispatch counter must remain zero on every rejection. Concurrent store updates must preserve both room entries.

- [ ] **Step 2: Run failing tests.**

```sh
go test ./internal/herdr -run 'TestCorruptState|TestDecodeRequest|TestStoreConcurrent' -count=1
```

- [ ] **Step 3: Implement state and request boundaries.**

Use a per-directory `flock`, a mode `0600` temporary file, `Sync`, close, then rename. Directory mode `0700`. Use `Lstat` to reject symlink records and non-socket callback paths; refuse replacing a live callback socket. Missing state creates initialized maps; malformed state returns an error. Reuse existing runtime-store patterns, without exporting a generic persistence framework.

Use `bufio.Reader` with a strict 64 KiB line cap, a five-second connection read/write deadline, `utf8.Valid` before decoding, `json.Decoder.DisallowUnknownFields`, and require EOF after one object. Accept exactly `start`, `join`, `leave`, `stop`, `status`. Check the 32-byte hex token with `subtle.ConstantTimeCompare`. For remote requests, reject mismatched `MachineID`/`HerdrSocket`; fill authoritative values from `Peer`. Validate operation-specific required fields before dispatch. Bound each operation context to 30 seconds; mutation timeout is potentially uncertain. Responses use `status:"error"` and a stable code; never echo the token or raw submitted document.

- [ ] **Step 4: Verify real Unix-socket request/response behavior.**

Start `ServePeer` with a temporary short socket and dispatch counter. Use `CallPeer` for valid status and invalid join; assert exact error codes, deadlines, private file modes, and context shutdown. Run:

```sh
go test ./internal/herdr -run 'TestCorruptState|TestDecodeRequest|TestStore|TestPeer' -count=1 -race
```

- [ ] **Step 5: Commit.**

```sh
git add internal/herdr
git commit -m "feat: add private Herdr integration state and callback protocol"
```

## Task 5: Pair remote sessions and resolve agent identity

**Files:**
- Create: `internal/herdr/process.go`, `client.go`, `client_test.go`, `pair.go`, `pair_test.go`, `ssh.go`
- Create: `cmd/parley/herdr.go`, `herdr_rpc.go`, `herdr_test.go`
- Modify: `cmd/parley/main.go`

**Interfaces:**
- Consumes: Task 4 types, store, and callback server.
- Produces `type Run func(context.Context, string, []string, []byte) ([]byte, error)` and `RunProcess` with that signature.
- Produces `Client{HerdrBinary string, ParleyBinary string, LocalMachineID string, HerdrSocket string, Run Run}`, `(*Client).Machines(ctx context.Context) ([]Machine,error)`, `(*Client).Agent(ctx context.Context, target AgentTarget) (AgentTarget,string,error)`, and `(*Client).Parley(ctx context.Context, machine Machine, args []string, stdin []byte) ([]byte,error)`.
- Produces `Master{Command *exec.Cmd, ControlPath string, Target string}`, `StartMaster(ctx context.Context, peer Peer, localCallback string) (*Master,error)`, `(*Master).Close() error`, `Pair(ctx context.Context, client *Client, store *Store, machine Machine) (Peer,*Master,error)`, and `herdrCmd() *cobra.Command`.
- Produces hidden `parley herdr rpc` handling fixed helper requests: `probe`, `pair`, `unpair`, `start`, `join`, `resume`, `leave`, `stop`, `binding`, `worker-start`, `worker-stop`. This RPC is SSH-user-local, not forwarded through the public callback.

- [ ] **Step 1: Add parser, identity, and pairing rejection tests.**

Machine fixture from verified Herdr shape:

```json
[{"id":"11111111111111111111111111111111","label":"workbox","target":"workbox","session":"agents","enabled":true,"selected":false}]
```

Agent get fixture:

```json
{"result":{"agent":{"pane_id":"w1:p2","terminal_id":"term_a","agent":"codex","agent_status":"idle","agent_session":{"source":"hook","agent":"codex","kind":"id","value":"conversation_a"}}}}
```

Use a `Run` closure to assert `--machine` precedes `agent get` remotely and that a local call has an explicit socket and no inherited remote selector. Decode `agent_session.value` rather than inventing `session_id`. Repeat with identical pane IDs on two machine profiles, a moved pane found by `terminal_id`, a different conversation value, and missing native identity. Reject automatic attachment without native identity using `agent_identity_required`.

Create an existing active pairing, attempt pairing a different controller, and assert `controller_conflict` with unchanged files. Add disabled/missing/incompatible helper, corrupt pairing, and helper paths containing spaces and single quotes.

- [ ] **Step 2: Run failing tests.**

```sh
go test ./internal/herdr ./cmd/parley -run 'TestMachines|TestAgentIdentity|TestPair|TestHerdrRPC' -count=1
```

- [ ] **Step 3: Implement typed calls and fixed RPC dispatch.**

`RunProcess` uses `exec.CommandContext`, direct argv, stdin bytes, bounded combined stderr diagnostics, and a stdout cap of 8 MiB. Kill and reap on cancellation. Oversized output is an error, not silently truncated valid JSON. Do not use `sh -c` locally.

`Machines` parses the JSON array and ignores `selected` for ownership. `Agent` parses the verified `.result.agent` envelope, resolves moved panes by listing agents on that same machine/session, compares terminal/kind/native identity, and returns a stable `agent_replaced` error on mismatch. The controller client has `LocalMachineID="local"`; a remote worker client has its actual paired machine ID. Calls to that client's own machine use its pinned socket, not `--machine`; only the controller routes to other profiles. Test this distinction so a remote worker cannot accidentally resolve a controller-side profile locally.

Remote Parley commands enter only through `rpc`: resolve the absolute registered plugin executable once, quote the fixed command path for the remote shell, and encode helper operation fields on stdin. An RPC switch maps enum operations to existing Cobra commands or worker launch code. Never accept an `args`, `command`, `path`, or `env` field from a callback caller. Pin `PARLEY_STATE_DIR` to the trusted per-session integration config. Capture the remote Herdr socket in `probe`; scope pairing records by that socket, not the user's global shell environment.

`StartMaster` implements the spec's fixed SSH argv with `exec.CommandContext`, waits for the owned control socket and an authenticated callback health check, and retains its child handle for shutdown. Its process context is the broker lifetime, not a short-lived callback request; use a separate readiness deadline. `Close` cancels/reaps that child, never a persisted PID. `Pair` validates the enabled profile, creates/reuses its own master and callback socket, writes pairing config only after callback health succeeds, and persists the peer. Return the live `Master` to the broker for routing; do not serialize its process handle. On failure remove only newly created owned resources. Record unavailable machines without failing the whole broker. Implement explicit local `herdr setup` and `herdr status` now; startup restoration can call setup's existing launch path in Task 9. `herdrCmd` attaches to `newRootCmd`.

- [ ] **Step 4: Prove fake subprocesses cannot move request data into shell text.**

Submit a topic containing literal backticks, `$(...)`, newlines, and quotes. Assert it appears only in stdin JSON, no local side-effect file exists, and the only remote command string contains the quoted trusted helper path plus fixed `herdr rpc`. Assert local help/errors follow Parley's status-first convention. Run pairing/identity tests with `-race`.

- [ ] **Step 5: Commit.**

```sh
git add internal/herdr cmd/parley
git commit -m "feat: pair Herdr sessions through a restricted SSH callback"
```

## Task 6: Acquire and release two-port SSH routes

**Files:**
- Modify: `internal/herdr/ssh.go`
- Create: `internal/herdr/ssh_test.go`
- Modify: `internal/herdr/pair.go`

**Interfaces:**
- Consumes: paired `Peer`, `Room`, `AgentTarget`, and `Run`.
- Produces `RouteKind(hostMachineID, participantMachineID string) (string,error)` returning `direct`, `reverse`, or `local`.
- Produces `TunnelManager{Run Run, Masters map[string]*Master}` with `Acquire(ctx context.Context, peer Peer, room Room, target AgentTarget) (Endpoints,error)`, `Release(ctx context.Context, roomID, participantMachineID string) error`, and `Close() error`. Register each master returned by Task 5's `Pair` under its machine ID.
- Manager holds allocated forward specifications and reference counts; expose neither control sockets nor PIDs through callback responses.

- [ ] **Step 1: Write route and rollback tests.**

```go
func TestRouteKind(t *testing.T) {
    cases := []struct { host, participant, want string; bad bool }{
        {"local", "local", "direct", false},
        {"a", "a", "direct", false},
        {"local", "a", "reverse", false},
        {"a", "local", "local", false},
        {"a", "b", "", true},
    }
    for _, c := range cases {
        got, err := RouteKind(c.host, c.participant)
        if got != c.want || (err != nil) != c.bad { t.Fatalf("%+v: %q %v", c, got, err) }
    }
}
```

Fake `Run` records argv. First remote forward returns `"51001\n"`, second fails; assert the allocated first forward is cancelled and Acquire returns an error. Successful pair returns protocol descriptor with 51001 and artifact URL with 51002, not original server ports. Repeated acquire creates no new forwards; first release retains them and second release cancels both. Add local bind race retry exhausted after three attempts, SSH exit during setup, malformed allocated-port output, and cancellation failure retained as an observable cleanup error.

- [ ] **Step 2: Run failing tests.**

```sh
go test ./internal/herdr -run 'TestRouteKind|TestTunnel' -count=1
```

- [ ] **Step 3: Implement forwarding and ownership.**

The route-selection body is deliberately small:

```go
switch {
case hostMachineID == participantMachineID:
    return "direct", nil
case hostMachineID == "local":
    return "reverse", nil
case participantMachineID == "local":
    return "local", nil
default:
    return "", fmt.Errorf("unsupported_route")
}
```

For reverse routes use `-O forward -R 127.0.0.1:0:127.0.0.1:<source-port>`, one call per endpoint, parsing the exact Task 1 behavior. For local routes use `-O forward -L 127.0.0.1:<selected-port>:127.0.0.1:<source-port>`. Never reuse Herdr's control socket. Use verified cancellation syntax and keep source/allocated ports per lease. Build descriptors through `descriptor.Descriptor.String` and HTTP URLs through `net.JoinHostPort` plus the existing artifact path.

Probe the resulting protocol port from the participant host and issue an HTTP request to the remapped artifact service from that same host; a connection/authentication-related response proves transport, whereas an unrelated listener's response does not. Verify the room ID with a protocol join in Task 7 and leave immediately on mismatch. Roll back the first forward when the second fails. Protect lease maps with a mutex, but do not hold it across an unbounded subprocess. Serialize acquisition for the same lease key.

- [ ] **Step 4: Run real forwarding checks using Task 1 hosts.**

Start actual Parley servers on each endpoint, acquire routes, and perform join/message/artifact fetch through both directions. Occupy an original artifact port before joining to prove remapping. Confirm final release closes only acquired ports and leaves Herdr's own SSH connection available.

- [ ] **Step 5: Commit.**

```sh
git add internal/herdr
git commit -m "feat: manage bidirectional Parley SSH forwarding leases"
```

## Task 7: Implement start/join/leave/stop with durable outcomes

**Files:**
- Create: `internal/herdr/broker.go`, `broker_test.go`
- Modify: `internal/herdr/types.go`, `store.go`, `client.go`, `cmd/parley/herdr.go`, `herdr_rpc.go`

**Interfaces:**
- Consumes: Task 4 requests/state, Task 5 `Client`/RPC, Task 6 `TunnelManager`.
- Produces `Broker{Store *Store, Client *Client, Tunnels *TunnelManager}` with `Handle(ctx context.Context, peer Peer, req Request) (Response,error)` and `Reconcile(ctx context.Context) error`.
- Produces canonical binding key `BindingKey(roomID string, target AgentTarget) string`, using room, machine/session, terminal, native conversation; pane ID is not its identity.
- Worker start/stop RPCs consume a complete `Binding` and return ready/stopped statuses; Task 8 supplies worker implementation.

- [ ] **Step 1: Test duplicate requests and partial-failure ownership.**

Use real temporary `Store`, fake `Run`, and actual `Broker.Handle`. Fake host start returns existing Parley JSON:

```json
{"status":"started","room_id":"room-1","topic":"debug parser","session_id":"psn_host","name":"host","descriptor":"parley://127.0.0.1:49001/room-1","local_port":49001,"artifact_local_port":49002,"artifact_path":"/rooms/room-1/artifacts"}
```

Fake join returns:

```json
{"status":"joined","room_id":"room-1","session_id":"psn_remote","name":"agent","participant":{"artifact_endpoint":"http://127.0.0.1:51002/rooms/room-1/artifacts","status":"online"}}
```

Submit one join twice with the same request ID: count exactly one join and one worker. Submit a new request ID for the same binding: return the same session. Reuse request ID with a different body: `request_conflict`. Inject a lost start/join response after the side effect and assert no blind retry. After restart, reconcile the durable intent against sessions and return known outcome or `operation_uncertain`. Add unauthorized stop, worker launch failure rollback, room mismatch, two rooms with the same topic, and last-participant tunnel release.

- [ ] **Step 2: Run failing tests.**

```sh
go test ./internal/herdr -run 'TestBroker|TestBindingKey' -count=1
```

- [ ] **Step 3: Implement operation ordering and intent journaling.**

Canonicalize a request without its token, hash it with SHA-256, and persist an `intent` before a mutating call. Serialize operations on the same request and binding. A matching complete outcome returns immediately; differing digest rejects. In-flight intent after a crash reconciles, never automatically repeats a mutation whose response was lost.

Start on `peer`'s actual host using existing `parley start --topic`; save room endpoint fields; bind the initiating agent to its returned host session. Join looks up registered room, resolves the actual target agent identity, acquires both endpoints, calls local/remote existing `join` with artifact override, persists the binding, and starts the worker. If failure occurs after known successful join, explicitly leave then release newly acquired leases. If outcome is uncertain, retain intent/lease for recovery rather than falsely announcing cleanup.

Leave shuts down only the binding worker, calls leave for its session, marks the binding left, then releases its route. Host leave closes the room because the existing host adapter owns room lifetime. Stop checks host ownership, stops all workers for that room, calls existing stop on the hosting machine, marks room closed, and releases only its leases. A status response removes tokens and private callback/control paths.

`Reconcile` verifies paired helpers, current room control status, outstanding intents, and known bindings. Missing host means unavailable, not a new room. On a disabled plugin stop owned workers/SSH masters without stopping agents or unrelated rooms. Add polling interval and process management in Task 9.

- [ ] **Step 4: Verify two directions using fake hosts and real state.**

```sh
go test ./internal/herdr ./cmd/parley -run 'TestBroker|TestBindingKey|TestHerdr' -count=1 -race
```

Assert host start argv/remote RPC goes to initiating host in both cases and status-first JSON includes stable room/session IDs. Kill a fake SSH process during a journaled join and verify restart does not create a second participant.

- [ ] **Step 5: Commit.**

```sh
git add internal/herdr cmd/parley
git commit -m "feat: orchestrate Herdr room operations with durable outcomes"
```

## Task 8: Deliver unread notifications to the same ready agent

**Files:**
- Create: `internal/herdr/worker.go`, `worker_test.go`
- Modify: `internal/herdr/client.go`, `store.go`, `cmd/parley/herdr.go`, `herdr_rpc.go`

**Interfaces:**
- Consumes: complete persisted `Binding`, `Client.Agent`, existing Parley `wait`, `inbox --peek`, `status`, and `resume`.
- Produces `Worker{Store *Store, Client *Client}` with `Run(ctx context.Context, bindingKey string) error` and `Signal(ctx context.Context, terminalID string) error`.
- Produces pure `NotificationAction(binding Binding, current AgentTarget, agentStatus string, unreadSeq int64) string`, returning `wait`, `prompt`, or `suspend`.

- [ ] **Step 1: Test notification policy and read-only cursor behavior.**

```go
func TestNotificationAction(t *testing.T) {
    target := AgentTarget{MachineID:"local", TerminalID:"term_a", Kind:"codex", NativeSession:"a"}
    binding := Binding{Target:target, NotifiedSeq:5, Delivery:"submitted"}
    if got := NotificationAction(binding, target, "idle", 5); got != "wait" { t.Fatal(got) }
    if got := NotificationAction(binding, target, "working", 6); got != "wait" { t.Fatal(got) }
    if got := NotificationAction(binding, target, "blocked", 6); got != "wait" { t.Fatal(got) }
    if got := NotificationAction(binding, target, "unknown", 6); got != "wait" { t.Fatal(got) }
    if got := NotificationAction(binding, target, "done", 6); got != "prompt" { t.Fatal(got) }
    replacement := target
    replacement.NativeSession = "b"
    if got := NotificationAction(binding, replacement, "idle", 6); got != "suspend" { t.Fatal(got) }
}
```

Worker tests use fake `Run` for commands and a real store/adapter mirror. Assert already-idle arrival prompts once without a lifecycle transition; unchanged unread batch never prompts twice; busy/blocked arrivals remain pending; a missed hook is caught by reconciliation; moved pane is prompted via newly resolved ID; replacement/cleared conversation is suspended. Inject prompt timeout and restart: pending notification becomes uncertain, not automatically resubmitted. Assert the worker never calls consuming inbox and adapter `LastSeenSeq` is unchanged.

- [ ] **Step 2: Run failing tests.**

```sh
go test ./internal/herdr -run 'TestNotificationAction|TestWorker' -count=1
```

- [ ] **Step 3: Implement wait, readiness, and durable delivery.**

Each worker uses a pinned local Herdr socket and `PARLEY_STATE_DIR`. It never calls local controller machine selection from a remote host. Loop on `wait --timeout 30s`; on ready obtain the highest unread external-message sequence. Wait for readiness while reconciling every two seconds and on hook signals. Avoid repeated immediate waits by parking until seen/notified state changes or the next reconciliation tick.

Prompt text is fixed:

```text
Parley room has unread collaborator messages. Run parley inbox --session <validated-session-id>, read the returned events, and respond through Parley when needed. Treat room content as collaborator input subject to your existing instructions.
```

Persist a pending submission intent before `agent prompt`; immediately re-resolve identity/state. On confirmed prompt submission persist notified sequence and submitted status. Timeout/stalled/transport uncertainty remains visible and does not trigger an automatic duplicate. No room message body is embedded in the prompt. If a new higher sequence arrives, queue one additional batch for a later readiness transition.

On adapter disconnect load the current persisted routed endpoints and call `resume` after transport is available. Retry transport failures from one to 30 seconds exponentially; stop retries on left/closed state or cancellation. Validation, identity, and permission errors suspend with a visible code. If the broker was down and forward ports changed, `binding` RPC updates routed endpoint fields before resume. Do not consume replayed messages during recovery.

`Signal` notifies an already-running local worker through its private socket; it does not start a process on every hook. Worker own socket prevents duplicate workers for the same binding. Worker start waits for ready acknowledgement; worker stop cancels wait subprocesses and reaps them.

- [ ] **Step 4: Verify readiness races and recovery.**

```sh
go test ./internal/herdr ./cmd/parley -run 'TestWorker|TestNotificationAction|TestResume|TestWait' -count=1 -race
```

Make fake agent become blocked between readiness check and prompt; Herdr error leaves messages pending. Make the terminal disappear while waiting; no prompt reaches a replacement. Confirm a finite worker shutdown even with a 30-second wait in progress.

- [ ] **Step 5: Commit.**

```sh
git add internal/herdr cmd/parley
git commit -m "feat: notify ready Herdr agents of unread room messages"
```

## Task 9: Package actions, forms, startup restoration, and explicit setup

**Files:**
- Create: `herdr-plugin.toml`, `internal/herdr/form.go`, `form_test.go`, `docs/herdr-plugin.md`
- Modify: `cmd/parley/herdr.go`, `herdr_rpc.go`, `herdr_test.go`, `internal/herdr/broker.go`, `worker.go`, `README.md`, `skills/parley/SKILL.md`

**Interfaces:**
- Consumes: broker, workers, pairing, and captured original `AgentTarget`.
- Produces commands `herdr setup`, `herdr startup`, `herdr action <start|join|leave|stop>`, `herdr form`, `herdr event`, `herdr status`, hidden `herdr broker`, hidden `herdr worker`.
- Produces `ReadForm(r io.Reader, op string) (string,error)` and `LaunchHelper(ctx context.Context, binary string, args []string, readySocket string) error` in `cmd/parley/herdr.go` for owned broker/worker launch.

- [ ] **Step 1: Test popup target preservation and lifecycle wiring.**

Capture action context for `w1:p2` and native session `a`. Open the popup with a different visible UI resource; submit join and assert request target remains original `term_a`/session `a`. Escape, empty topic/ID, EOF, and 513-byte topic cancel/reject without starting rooms. Startup called twice creates one broker. Startup on a paired remote session creates no broker. Enabling/linking on an already-running server followed by explicit setup works without waiting for restart.

Use a fixture manifest parser only in tests via `herdr plugin link` during the live task; do not add a production TOML dependency. Form tests feed actual byte sequences:

```go
func TestReadFormCancelAndInput(t *testing.T) {
    if _, err := ReadForm(strings.NewReader("\x1b"), "join"); err == nil { t.Fatal("escape accepted") }
    got, err := ReadForm(strings.NewReader("room-1\n"), "join")
    if err != nil || got != "room-1" { t.Fatalf("%q %v", got, err) }
}
```

- [ ] **Step 2: Run failing command/form tests.**

```sh
go test ./cmd/parley ./internal/herdr -run 'TestHerdrAction|TestHerdrStartup|TestReadForm|TestPopupTarget' -count=1
```

- [ ] **Step 3: Add the manifest and minimal form.**

```toml
id = "parley"
name = "Parley"
version = "0.1.0"
min_herdr_version = "0.9.3"
description = "Start and join agent rooms with automatic SSH routing"
platforms = ["linux", "macos"]

[[build]]
command = ["go", "build", "-o", "bin/parley", "./cmd/parley"]

[[startup]]
command = ["bin/parley", "herdr", "startup"]

[[actions]]
id = "start"
title = "Parley: Start room"
contexts = ["pane"]
command = ["bin/parley", "herdr", "action", "start"]

[[actions]]
id = "join"
title = "Parley: Join room"
contexts = ["pane"]
command = ["bin/parley", "herdr", "action", "join"]

[[actions]]
id = "leave"
title = "Parley: Leave room"
contexts = ["pane"]
command = ["bin/parley", "herdr", "action", "leave"]

[[actions]]
id = "stop"
title = "Parley: Stop room"
contexts = ["pane"]
command = ["bin/parley", "herdr", "action", "stop"]

[[panes]]
id = "form"
title = "Parley"
placement = "popup"
width = "70%"
height = 12
command = ["bin/parley", "herdr", "form"]

[[events]]
on = "pane.agent_status_changed"
command = ["bin/parley", "herdr", "event"]
```

`action` resolves the original agent, encodes the validated target into `PARLEY_TARGET_JSON`, passes `PARLEY_ACTION` and target to `plugin pane open --entrypoint form`, and exits. `form` submits through its session pairing or local broker. It shows returned room ID/session or a short actionable error. Stop requires owner and a concrete room selection; host leave warns that current Parley lifecycle closes the room. Prevent accidental destructive selection by displaying the room name/ID before confirmation in the form.

Use existing terminal `stty` on macOS/Linux to save settings, enter a minimal input mode supporting Escape/backspace/Enter, and restore on every exit and signal. Terminal control commands must have fixed argv and never incorporate form input. Keep normal JSON-only behavior for non-form commands. `herdr event` reads event JSON, obtains terminal identity, and signals registered workers; unbound agents are ignored.

`LaunchHelper` follows existing daemon process pattern: detached session, private logs, no inherited action stdout pipes, ready socket handshake, kill/reap only its newly launched child on startup failure. Do not kill a stored PID. Broker reconciles enabled profiles every 30 seconds and own plugin-enabled state every two seconds; workers also suppress prompting when disabled. Local startup restores configured broker only; missing config is a no-op. Explicit setup starts it immediately, auto-pairs installed enabled hosts, and prints unavailable hosts with recovery instructions. A dead SSH master reconnects with backoff and restores owned forwards; binding endpoints are updated atomically before workers resume.

- [ ] **Step 4: Add setup and operational documentation.**

Document actual installation:

```sh
herdr plugin install khaiql/parley
```

Run installation on each machine once; installation requires Go for this initial manifest. Local development uses `go build -o bin/parley ./cmd/parley` then `herdr plugin link <absolute-repository-path>` because link does not run build steps. Explain local-only `parley herdr setup`, automatic pairing, Herdr native identity integration prerequisite, actions/keybindings, status recovery, SSH permission failures, controller-scoped IDs, same-host direct routing, unsupported remote-to-remote routing, local suspension, and current host-leave semantics. The globally installed skill is not modified by implementation unless explicitly requested; update only the repository skill.

- [ ] **Step 5: Verify detach and outage recovery with real Herdr.**

Link the built plugin locally/remotely, run setup, create local room, select existing remote agent, invoke join action, exchange messages, and fetch artifact. Repeat with remote room/local collaborator. Close the UI and prove broker/worker/SSH process continue. Terminate only the owned SSH master; publish 75 messages from a remaining room participant while disconnected. Reopen connectivity and assert same session ID, all unread messages, remapped artifact URL, and one pending notification. Disable plugin and prove no further prompts, owned helper cleanup, and no agent termination.

- [ ] **Step 6: Commit.**

```sh
git add herdr-plugin.toml internal/herdr cmd/parley docs/herdr-plugin.md README.md skills/parley/SKILL.md
git commit -m "feat: package Parley room workflows as a Herdr plugin"
```

## Task 10: Complete integration proof and release gates

**Files:**
- Create: `internal/herdr/integration_test.go`
- Modify: `docs/herdr-plugin.md`, `docs/herdr-transport-proof.md`
- Modify only if necessary: `.github/workflows/ci.yml` for explicitly provisioned SSH integration fixtures; never require developer SSH credentials in ordinary CI.

**Interfaces:**
- Consumes: the complete public CLI/plugin workflow.
- Produces: automated regression proof plus actual manual evidence for the release acceptance matrix.

- [ ] **Step 1: Add integration tests without duplicating production adapters.**

Build the real Parley binary into a test temporary directory. Use synthetic local and remote roots, a fake Herdr executable implementing the verified agent/machine envelopes, and loopback TCP proxies for network faults. Tests call actual CLI commands and production broker/worker/adapters. Keep real OpenSSH/Herdr smoke evidence separate; fake SSH argv tests cannot prove system forwarding works.

Start with these executable production-CLI transport tests. They intentionally use real loopback proxies, not an SSH simulation claiming to prove OpenSSH. Put the code in `internal/herdr/integration_test.go`, package `herdr`, with the standard-library imports it uses:

```go
func TestIntegrationLocalRoomRemoteJoin(t *testing.T) {
    integrationExchange(t, true)
}
func TestIntegrationRemoteRoomLocalJoin(t *testing.T) {
    integrationExchange(t, false)
}

func integrationExchange(t *testing.T, hostLocal bool) {
    t.Helper()
    root, err := os.MkdirTemp("/tmp", "ph-")
    if err != nil { t.Fatal(err) }
    t.Cleanup(func() { _ = os.RemoveAll(root) })
    binary := filepath.Join(root, "parley")
    build := exec.Command("go", "build", "-o", binary, "./cmd/parley")
    build.Dir = "../.."
    if out, err := build.CombinedOutput(); err != nil { t.Fatalf("build: %v %s", err, out) }
    local, remote := filepath.Join(root, "l"), filepath.Join(root, "r")
    host, participant := local, remote
    if !hostLocal { host, participant = remote, local }
    run := func(state string, args ...string) map[string]json.RawMessage {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        cmd := exec.CommandContext(ctx, binary, args...)
        cmd.Env = append(os.Environ(), "PARLEY_STATE_DIR="+state)
        out, err := cmd.CombinedOutput()
        if err != nil { t.Fatalf("%v: %v %s", args, err, out) }
        var result map[string]json.RawMessage
        if err := json.Unmarshal(out, &result); err != nil { t.Fatal(err) }
        return result
    }
    text := func(result map[string]json.RawMessage, field string) string {
        var value string
        if err := json.Unmarshal(result[field], &value); err != nil { t.Fatalf("%s: %v", field, err) }
        return value
    }
    number := func(result map[string]json.RawMessage, field string) int {
        var value int
        if err := json.Unmarshal(result[field], &value); err != nil { t.Fatalf("%s: %v", field, err) }
        return value
    }
    started := run(host, "start", "--topic", "integration")
    room := text(started, "room_id")
    hostSession := text(started, "session_id")
    t.Cleanup(func() {
        ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
        defer cancel()
        cmd := exec.CommandContext(ctx, binary, "stop", "--room", room)
        cmd.Env = append(os.Environ(), "PARLEY_STATE_DIR="+host)
        if out, err := cmd.CombinedOutput(); err != nil { t.Logf("cleanup: %v %s", err, out) }
    })
    protocolPort := proxyForIntegration(t, number(started, "local_port"))
    artifactPort := proxyForIntegration(t, number(started, "artifact_local_port"))
    descriptor := fmt.Sprintf("parley://127.0.0.1:%d/%s", protocolPort, room)
    artifactURL := fmt.Sprintf("http://127.0.0.1:%d/rooms/%s/artifacts", artifactPort, room)
    joined := run(participant, "join", descriptor, "--artifact-endpoint", artifactURL)
    session := text(joined, "session_id")
    source := filepath.Join(root, "trace.txt")
    payload := []byte("remapped artifact bytes\n")
    if err := os.WriteFile(source, payload, 0o600); err != nil { t.Fatal(err) }
    sent := run(host, "send", "--session", hostSession, "--file", source, "inspect")
    var events []struct {
        Payload struct { Artifacts []struct { ID string `json:"id"` } `json:"artifacts"` } `json:"payload"`
    }
    if err := json.Unmarshal(sent["events"], &events); err != nil { t.Fatal(err) }
    artifactID := ""
    for _, event := range events {
        if len(event.Payload.Artifacts) > 0 { artifactID = event.Payload.Artifacts[0].ID }
    }
    if artifactID == "" { t.Fatal("send returned no committed artifact") }
    ready := run(participant, "wait", "--session", session, "--timeout", "2s")
    if text(ready, "status") != "ready" { t.Fatalf("wait: %s", ready["status"]) }
    destination := filepath.Join(root, "download.txt")
    run(participant, "artifact", "fetch", "--session", session, artifactID, "--out", destination)
    downloaded, err := os.ReadFile(destination)
    if err != nil { t.Fatal(err) }
    if sha256.Sum256(downloaded) != sha256.Sum256(payload) { t.Fatal("artifact bytes differ") }
    status := run(participant, "status", "--session", session)
    var state struct { LastSeenSeq int64 `json:"last_seen_seq"` }
    if err := json.Unmarshal(status["state"], &state); err != nil { t.Fatal(err) }
    inbox := run(participant, "inbox", "--session", session, "--peek")
    var unread []json.RawMessage
    if err := json.Unmarshal(inbox["events"], &unread); err != nil { t.Fatal(err) }
    if len(unread) == 0 { t.Fatal("wait/fetch consumed unread messages") }
    after := run(participant, "status", "--session", session)
    var later struct { LastSeenSeq int64 `json:"last_seen_seq"` }
    if err := json.Unmarshal(after["state"], &later); err != nil { t.Fatal(err) }
    if later.LastSeenSeq != state.LastSeenSeq { t.Fatal("peek advanced cursor") }
}

func proxyForIntegration(t *testing.T, sourcePort int) int {
    t.Helper()
    listener, err := net.Listen("tcp", "127.0.0.1:0")
    if err != nil { t.Fatal(err) }
    var mu sync.Mutex
    var active []net.Conn
    done := make(chan struct{})
    go func() {
        defer close(done)
        for {
            incoming, err := listener.Accept()
            if err != nil { return }
            outgoing, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", sourcePort), time.Second)
            if err != nil { _ = incoming.Close(); continue }
            mu.Lock()
            active = append(active, incoming, outgoing)
            mu.Unlock()
            go func(a, b net.Conn) {
                defer a.Close()
                defer b.Close()
                copied := make(chan struct{})
                go func() { _, _ = io.Copy(b, a); _ = b.Close(); close(copied) }()
                _, _ = io.Copy(a, b)
                _ = a.Close()
                _ = b.Close()
                <-copied
            }(incoming, outgoing)
        }
    }()
    t.Cleanup(func() {
        _ = listener.Close()
        <-done
        mu.Lock()
        defer mu.Unlock()
        for _, conn := range active { _ = conn.Close() }
    })
    return listener.Addr().(*net.TCPAddr).Port
}
```

The code proves production CLI endpoint remapping, artifact bytes, and read-only cursor behavior with separate endpoint state roots. It does not prove broker SSH direction or Herdr UI execution; Tasks 6–9 and the live matrix cover those. Extend the existing Task 7 broker tests to run these real CLI endpoints through fake typed Herdr responses and assert duplicate join still returns one binding/session. Reuse Task 4 corrupt-state test at broker startup and assert no mutating subprocess call occurs. Keep every process bounded and reaped; use channels/deadlines rather than fixed sleeps.

- [ ] **Step 2: Run the integration tests, then the repository gates.**

```sh
go test ./internal/herdr -run TestIntegration -count=1 -race
go build ./...
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
go test ./... -timeout 30s -race
```

Expected: all pass. Do not claim the real SSH/Herdr matrix passed from these tests alone.

- [ ] **Step 3: Record the live acceptance matrix.**

| Scenario | Required evidence |
| --- | --- |
| Room local, collaborator remote | Remote action reaches local broker; two reverse forwards; message and artifact exchange |
| Room remote, collaborator local | Room remains remote; two local forwards; message and artifact exchange |
| Same host | No SSH route acquired |
| Idle arrival | One notification without a state transition |
| Working/blocked arrival | Pending until ready; no input into approval UI |
| Agent replacement / pane move | Replacement suspended; moved terminal safely resolved |
| Tunnel outage, 75 events | Same participant/session, every unread event retained |
| Controller restart | Pairing restored, intents reconciled, no duplicate start/join/prompt |
| Partial forward / occupied port | Rollback or remap; no orphan participant |
| Leave / stop / plugin disable | Only owned helpers/leases cleaned up; agents survive |

Record real versions and observations, with explicit unverified rows if credentials/tools are unavailable. A release claiming complete automation requires both cross-machine rows and recovery to pass on macOS/Linux, with at least one macOS controller and one Linux remote host.

- [ ] **Step 4: Review scope and commit.**

Check no new production dependency, generic remote shell execution, Windows promise, new-agent launcher, hosted relay, or silently consumed notification cursor slipped in. Commit only task files:

```sh
git add internal/herdr/integration_test.go docs/herdr-plugin.md docs/herdr-transport-proof.md
git commit -m "test: verify Herdr room routing and recovery workflows"
```

Complete the repository's issue/status and integration workflow after the approved execution method finishes. Do not publish a release or install changes onto unrelated hosts as part of planning.

## Self-review result

- Spec sections map to Tasks 1–10: bootstrap/pairing (1, 4, 5, 9), routing (2, 6), room operations (7), message delivery (8), recovery (3, 8, 9), storage/security (4, 5, 7), packaging/UX (9), acceptance (10).
- Review Focus has explicit tests in its owning tasks, including the greater-than-50-event replay and corrupt-state cases.
- Shared types and signatures are declared before dependent tasks. Production package-main adapter recovery is tested directly; the existing e2e test adapter is not mistaken for production code.
- Transport feasibility is a gating proof, not a completed assertion. Current remote invocation/pairing UX is subject to that proof.
- The plan covers the agreed two-machine scenarios. Remote-to-remote routing and multiple controllers are explicit exclusions.

## Execution handoff

Review this plan and its proposed companion spec before implementation. Native execution is recommended: pairing, route leases, and durable outcomes share interfaces, so one implementer can keep those contracts consistent. A whole-branch independent review should focus on SSH ownership, callback authorization, and unread replay. Subagent-driven execution remains an option if per-task independent review is preferred.
