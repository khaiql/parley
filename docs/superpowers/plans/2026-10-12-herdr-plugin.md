# Parley Herdr Automatic SSH Routing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans for native implementation, or superpowers:subagent-driven-development if explicitly chosen. Steps use checkbox syntax; track execution in the repository issue tracker.

**Goal:** Start rooms on the initiating host and Join existing Herdr panes by room ID with both SSH ports opened automatically.

**Architecture:** Start persists a host-local room and advertises pane metadata. Automatic local coordinator startup establishes outbound event subscriptions; remote Join writes a durable request and emits a workspace marker. On Join, the coordinator fetches room metadata, creates two loopback forwards, calls the participant-host helper, and writes an outcome back over outbound SSH. No reverse callback or message notification hooks.

**Tech Stack:** Go 1.26.1, existing Cobra, Go standard library, Herdr CLI/socket API, OpenSSH and existing Parley runtime stores.

**Spec:** `docs/superpowers/specs/2026-10-12-herdr-plugin-design.md` (revised 2026-10-13; supersedes the original broader plan).

## Global Constraints

- Go 1.26.1; existing Cobra dependency; no new production dependencies.
- Initial platforms: macOS and Linux.
- Initial Herdr minimum: 0.9.3; verify required CLI/socket capabilities during activation.
- Host the room on the initiating agent's machine; attach existing panes without launching or restarting agents.
- Support local/local, local/remote, remote/local, and same-remote-machine joins. Reject different-remote-machine routing with `unsupported_route`.
- One local controller per managed remote Herdr session; reject conflicting ownership with `controller_conflict`.
- SSH always originates on the local machine. Bind both forwarded endpoints to `127.0.0.1`.
- Preserve existing Parley CLI, descriptors, JSON conventions, and message/cursor behavior.
- No message hooks, automatic agent prompts, notification workers, or participant replay/resume changes in this phase.

## Review Focus

- Simultaneous distinct request IDs or direct/queued Join for one binding must return one session/lease without another CLI Join; Tasks 3 and 6.
- One forward fails or a local port is occupied: roll back or remap, never announce usable endpoints early; Task 5.
- Missing metadata, conflicting room IDs, unavailable hosts or moved/replaced panes must not route to the wrong room/conversation; Tasks 4 and 6.
- Coalesced events, `events_lost`, requests before subscription, or commit-before-marker crashes must recover from durable requests; Tasks 1, 3, 4 and 6.
- Shared per-user plugin roots, corrupt state, forged origin or another controller must not cross session namespaces or overwrite ownership; Tasks 1, 3 and 4.

## Execution order and files

Seven dependent tasks cover transport proof, required artifact remapping, durable state, subscriber/host calls, forwarding, Start/Join, and packaging. No new messaging subsystem is part of this plan.

| Files | Responsibility |
| --- | --- |
| `docs/herdr-transport-proof.md` | Actual proof, versions/envelopes and unverified claims |
| `cmd/parley/join.go`, `daemon_commands.go`, `daemon_process.go`, `participant_daemon.go`, `participant_artifacts.go` | Remapped artifact URL end to end |
| `internal/herdr/types.go`, `store.go`, `control.go` | Durable host records, claims, private local IPC |
| `internal/herdr/client.go`, `process.go`, `peer.go`, `events.go` | Fixed host operations, identity, ownership and outbound subscriptions |
| `internal/herdr/ssh.go` | Lazy owned masters, both ports, rollback/refcounts |
| `internal/herdr/broker.go` | Host-local Start, on-demand lookup, reactive Join and outcomes |
| `internal/herdr/form.go` | Original-pane popup and input/result display |
| `cmd/parley/herdr.go`, `herdr_rpc.go`, `main.go` | CLI wiring and fixed helper dispatch |
| `herdr-plugin.toml`, `README.md`, `docs/herdr-plugin.md` | Installation, startup, Start/Join and operational limits |
| Adjacent tests plus `internal/herdr/integration_test.go` | Focused regressions and full setup flow |

Keep existing runtime/descriptor/adapter code; only artifact URL propagation changes core Parley. Do not change the wire replay protocol, add `resume`, create a worker, or edit agent hook/skill configuration. These are implementation instructions, not claims that new files already exist.

---

## Task 1: Prove the reactive event relay and OpenSSH forwarding

**Files:**
- Create: `docs/herdr-transport-proof.md`
- Temporary only: minimal test plugin and socket subscriber in a private fixture directory; do not ship fixture credentials/scripts.

**Interfaces:**
- Consumes: saved SSH profile, local/remote Herdr 0.9.3 or newer, and the spec's native metadata API.
- Produces: observed machine/agent/subscription envelopes, installed helper/session discovery rules, marker delivery/recovery evidence, dynamic-port cancellation syntax, and detach survival results.

- [ ] **Step 1: Separate existing proof from outstanding claims.**

The 2026-10-12 fixture passed 13 primitive checks on two isolated macOS servers over loopback SSH: metadata reads, plugin request writes, restart persistence/startup hooks, outbound request/result exchange, and Parley messages/direct HTTP artifact bytes through `-L`/`-R`. It did not test event subscriptions, Linux, real user hosts, complete orchestration, or the CLI endpoint override. Record those limits; do not mark this reactive task complete from that fixture.

Capture actual schemas and versions on both endpoints:

```sh
herdr --version
herdr machine list --json
herdr api schema --json
herdr agent list
herdr --machine <profile-id> agent list
herdr --machine <profile-id> plugin list --json
ssh -V
```

Use an actual saved profile ID. Capture sanitized `terminal_id`, `pane_id`, `agent_status`, `agent_session.value`, plugin root, and socket-resolution envelopes. Confirm `workspace.report_metadata`, `pane.report_metadata`, `events.subscribe`, and `agent.get`; do not assume a documented event-stream CLI exists.

- [ ] **Step 2: Prove remote action queue commit and marker delivery.**

Use a throwaway manifest with a pane action running the following standard-library script. It saves a private request on that host, then reports only a random marker:

```python
import hashlib, json, os, pathlib, socket, uuid
context = json.loads(os.environ['HERDR_PLUGIN_CONTEXT_JSON'])
request_id = uuid.uuid4().hex
canonical_socket = str(pathlib.Path(os.environ['HERDR_SOCKET_PATH']).resolve(strict=True))
session_key = hashlib.sha256(canonical_socket.encode('utf-8')).hexdigest()
state = pathlib.Path(os.environ['HERDR_PLUGIN_STATE_DIR']) / 'sessions' / session_key
queue = state / 'requests'
queue.mkdir(mode=0o700, parents=True, exist_ok=True)
request = {'id': request_id, 'op': 'join', 'room_id': 'room-proof',
           'pane_id': context.get('focused_pane_id')}
temporary = queue / (request_id + '.tmp')
with temporary.open('x') as file:
    os.chmod(temporary, 0o600)
    json.dump(request, file)
    file.flush()
    os.fsync(file.fileno())
temporary.replace(queue / (request_id + '.json'))
with socket.socket(socket.AF_UNIX) as connection:
    connection.settimeout(5)
    connection.connect(canonical_socket)
    document = {'id': request_id, 'method': 'workspace.report_metadata',
                'params': {'workspace_id': os.environ['HERDR_WORKSPACE_ID'],
                           'source': 'plugin:parley-proof',
                           'tokens': {'parley_request': request_id}}}
    connection.sendall(json.dumps(document).encode() + b'\n')
    response = json.loads(connection.makefile('rb').readline())
    assert 'error' not in response, response
```

The throwaway request is a transport fixture, not production `Request`. Run the script through a remote-selected Herdr action, not merely an SSH shell, and confirm host-specific context.

Before invoking it, run this subscriber remotely through an SSH connection initiated locally. Pin its socket path to the verified session; do not inherit an unrelated default session:

```python
import json, socket, sys
with socket.socket(socket.AF_UNIX) as connection:
    connection.connect(sys.argv[1])
    connection.sendall(json.dumps({'id': 'join-events',
        'method': 'events.subscribe',
        'params': {'subscriptions': [{'type': 'workspace.metadata_updated'}]}
    }).encode() + b'\n')
    for line in connection.makefile('rb'):
        document = json.loads(line)
        print(json.dumps(document), flush=True)
```

Expected: local stdout receives subscription acknowledgement and the action's workspace metadata event. Native events do not invoke manifest metadata hooks; the subscriber is plugin code. Do not build production against Herdr's internal `remote-api-bridge`; use a fixed socket-subscription helper.

- [ ] **Step 3: Test queue recovery and routing independently.**

Run two named Herdr sessions for the same OS user with shared XDG/plugin roots, not two isolated XDG roots. Confirm both receive identical injected plugin root directories but derive different session keys from their canonical sockets. Run action/startup/probe on each and prove their requests, config, role and controller records cannot cross namespaces, including after restart.

Commit a request before subscribing, acknowledge the subscription, then read that session's pending files over a separate outbound SSH call. Update the marker twice before the first read; fetch both requests. Disconnect/reconnect, remove ephemeral metadata by restarting the fixture server, and prove durable room/request files remain accessible. Force `events_lost` if the fixture permits; otherwise record it as unverified and cover it with a synthetic frame in Task 4.

Start an owned SSH master with no callback/socket/room forwards. Add a protocol forward and a separate HTTP artifact forward only for Join:

```text
ssh -M -S <private-control-path> -N
    -o BatchMode=yes -o ExitOnForwardFailure=yes -o ControlPersist=no
    -o ServerAliveInterval=30 -o ServerAliveCountMax=3 <target>
ssh -S <private-control-path> -O forward
    -R 127.0.0.1:0:127.0.0.1:<local-test-port> <target>
```

Record assigned-port output and exact cancellation syntax. Repeat with `-L`, including occupied local port and partial two-port failure. Close the UI and check subscriber/master survival. Bound every fixture process, reap it, unlink the throwaway plugin, and remove owned credentials/resources.

- [ ] **Step 4: Commit observed contracts.**

Document versions, sanitized envelopes, helper/session resolution, startup-vs-link behavior, stream acknowledgement/error frames, and results. Reactive implementation is gated on successful remote event delivery and durable recovery reads. Missing native agent identity requires the existing Herdr harness integration, never inference from labels.

```sh
git add docs/herdr-transport-proof.md
git commit -m "docs: verify reactive Herdr event transport contract"
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

## Task 3: Define private host records, durable requests, and local IPC

**Files:**
- Create: `internal/herdr/types.go`, `store.go`, `store_test.go`, `control.go`, `control_test.go`

**Interfaces:**
- Produces the exact contracts below; later tasks consume them.
- Produces `SessionKey(canonicalSocket string) (string,error)`, `SessionDirs(configRoot, stateRoot, canonicalSocket string) (string,string,error)` returning config/state session directories, `HostBindingKey(roomID string, target AgentTarget) string`, and `(*Store).WithBinding(ctx context.Context, key string, fn func() error) error`.
- Produces `NewStore(dir string) *Store`, `(*Store).Read() (State,error)`, `(*Store).Update(func(*State) error) error`, `(*Store).Enqueue(Request) error`, `(*Store).Pending() ([]Request,error)`, `(*Store).Claim(requestID, digest, controllerID string) (Outcome,error)`, and `(*Store).Complete(requestID string, outcome Outcome) error`.
- Produces `DecodeRequest(r io.Reader, origin Peer) (Request,error)`, `ServeLocal(ctx context.Context, path string, origin Peer, dispatch func(context.Context, Peer, Request) (Response,error)) error`, and `CallLocal(ctx context.Context, path string, req Request) (Response,error)`.

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
    WorkspaceID string `json:"workspace_id"`
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
    Status string `json:"status"` // online, disconnected, left
    JoinPhase string `json:"join_phase"` // intent, complete, uncertain
    JoinRequestID string `json:"join_request_id"`
}
type SessionConfig struct {
    Role string `json:"role"` // controller or participant
    ControllerID string `json:"controller_id"`
    MachineID string `json:"machine_id"`
    HerdrSocket string `json:"herdr_socket"`
    SessionKey string `json:"session_key"`
}
type Peer struct {
    Machine Machine `json:"machine"`
    Config SessionConfig `json:"config"`
    Binary string `json:"binary"`
}
type Request struct {
    ID string `json:"id"`
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
    ControllerID string `json:"controller_id"`
    Phase string `json:"phase"` // claimed, intent, complete, uncertain
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

`MachineID="local"` identifies the controller host; remote IDs are opaque saved-profile IDs. Host-local action records use `self` until their origin is normalized from a trusted `Peer`. Normalize before digest/binding identity computation. Never trust a submitted SSH target/socket or derive identity from a label. Host-local helper calls use the pinned session socket; profile names are resolved only by the local coordinator.

- [ ] **Step 1: Add failing storage and request boundary tests.**

```go
func TestCorruptStateIsNotReset(t *testing.T) {
    dir := t.TempDir()
    if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{"), 0o600); err != nil { t.Fatal(err) }
    store := NewStore(dir)
    if _, err := store.Read(); err == nil { t.Fatal("corrupt state accepted") }
    if err := store.Update(func(*State) error { return nil }); err == nil { t.Fatal("corrupt state overwritten") }
    data, _ := os.ReadFile(filepath.Join(dir, "state.json"))
    if string(data) != "{" { t.Fatal("original state destroyed") }
}
func TestTwoPendingRequestsSurviveOneMarker(t *testing.T) {
    store := NewStore(t.TempDir())
    target := AgentTarget{MachineID:"self", HerdrSocket:"/private/herdr.sock", TerminalID:"term_a", Kind:"codex", NativeSession:"a"}
    for _, id := range []string{"request-a", "request-b"} {
        if err := store.Enqueue(Request{ID:id, Op:"join", RoomID:"room-a", Target:target}); err != nil { t.Fatal(err) }
    }
    pending, err := store.Pending()
    if err != nil || len(pending) != 2 { t.Fatalf("pending=%v err=%v", pending, err) }
}
```

Add `TestSessionNamespaceIsolation`: two canonical socket paths and identical config/state roots produce distinct session directories; `Enqueue`, `Pending`, ownership, rooms and restart reads do not cross them. Two aliases canonicalized to the same socket produce the same key. Reject relative/unclean socket paths and never derive a namespace from request JSON. Test startup in one participant session cannot read a sibling controller config.

Add `TestBindingLockAcrossStores` using two `Store` instances and two processes on one session directory. Hold `join:<host-key>` in the first process, attempt it in the second with a deadline, and assert the callback never runs before release; cancellation leaves the first transaction intact. After releasing it, the second reads the first persisted binding intent rather than creating a fresh one. Host key tests require `self` and controller-normalized machine IDs to hash identically, but different sessions/terminals/kinds/conversations to hash differently.

Add changed-body request ID rejection, concurrent claims by different controllers, idempotent claim by the same controller, symlink request/outcome rejection, and corrupt outcome preservation. Decoder tests reject unknown fields, `op:"exec"`, multiple JSON objects, invalid UTF-8, 65,537 bytes, topic over 512 bytes, unsafe IDs over 128 bytes, and forged origin/socket. Every rejected decode leaves dispatch count zero.

- [ ] **Step 2: Run failing tests.**

```sh
go test ./internal/herdr -run 'TestCorruptState|TestTwoPending|TestClaim|TestDecodeRequest|TestStore|TestSessionNamespace|TestBindingLock|TestHostBindingKey' -count=1
```

- [ ] **Step 3: Implement private records and fixed boundaries.**

On the selected host, resolve the trusted server socket with `filepath.EvalSymlinks`, then `filepath.Abs`/`Clean`; persist the verified canonical path for offline recovery. Apply this exact namespace derivation in action, form, startup, and helper:

```go
func SessionKey(canonicalSocket string) (string, error) {
    if !filepath.IsAbs(canonicalSocket) || canonicalSocket != filepath.Clean(canonicalSocket) {
        return "", fmt.Errorf("session socket must be canonical and absolute")
    }
    sum := sha256.Sum256([]byte(canonicalSocket))
    return hex.EncodeToString(sum[:]), nil
}
func SessionDirs(configRoot, stateRoot, canonicalSocket string) (string, string, error) {
    key, err := SessionKey(canonicalSocket)
    if err != nil { return "", "", err }
    return filepath.Join(configRoot, "sessions", key), filepath.Join(stateRoot, "sessions", key), nil
}
```

`NewStore` receives only the derived session state directory. Put `config.json` and `controller.json` under the derived config directory; all records/logs/locks remain within that namespace. Preserve the participant's separately trusted `PARLEY_STATE_DIR`; do not move or reset existing Parley runtime files. Trusted setup or a native action calls `Store.PinRuntimeRoot` before durable request submission, persisting an immutable private `runtime-root.json`. Fixed SSH helpers read that pin before runtime access regardless of their environment. An unpinned native action can pin its trusted default; no manual participant setup is required. Keep Unix IPC/master paths short in a private runtime directory and validate the full session key in their handshake. Never use a PID/inode/controller ID as the session namespace or scan sibling namespaces.

Use a short-lived session data lock, mode `0600` temporary files, `Sync`, close, and rename. Directories are `0700`. Persist immutable requests under `requests/<id>.json` and outcomes under `outcomes/<id>.json`; state contains rooms/bindings/peers and local intent indexes. `Pending` returns every non-complete request, not the latest metadata marker. `Claim` compares digest/controller under the same lock before side effects. Reject path separators in IDs; helpers derive paths internally. Missing files initialize maps, corrupt files fail visibly. Avoid separate generic persistence abstractions.

`HostBindingKey` hashes the JSON string tuple `[session_key, room_id, terminal_id, kind, native_session]` with full SHA-256 hex; it takes an already-validated canonical target. Exclude controller machine IDs. `WithBinding` hashes its prefixed key into `locks/<hash>.lock`, rejects symlinks, and uses an OS file lock across processes, with bounded nonblocking acquisition retries honoring context cancellation. Acquire a `route:` lock before a `join:` lock, never in reverse order. The callback may take/release the short session data lock to persist an intent/result, but that data lock is never held across a subprocess. OS lock release after crash does not erase persisted intents.

`DecodeRequest` uses a 64 KiB limit, `utf8.Valid`, `DisallowUnknownFields`, exactly one object, and operation-specific validation for `start`, `join`, `status`. Set origin machine/session from the trusted peer after rejecting incompatible claims. Hash normalized request JSON with SHA-256. No token authentication is added: IPC is local/private and remote operations arrive through SSH-authenticated fixed helpers. Local socket requests have five-second read/write deadlines; operations have a 30-second deadline and uncertain mutation handling.

- [ ] **Step 4: Verify private local IPC and restart behavior.**

Start `ServeLocal` under a temporary short private socket, use `CallLocal` for valid status/invalid join, and assert exact error codes, shutdown, modes, and refusal to replace a live/symlink socket. Restart the store after a claim and retain its controller/digest/intent. No network callback server is created.

```sh
go test ./internal/herdr -run 'TestCorruptState|TestTwoPending|TestClaim|TestDecodeRequest|TestStore|TestLocal|TestSessionNamespace|TestBindingLock|TestHostBindingKey' -count=1 -race
```

- [ ] **Step 5: Commit.**

```sh
git add internal/herdr
git commit -m "feat: add durable Herdr requests and private local control"
```

## Task 4: Build outbound event subscriptions and managed host calls

**Files:**
- Create: `internal/herdr/process.go`, `client.go`, `client_test.go`, `peer.go`, `peer_test.go`, `events.go`, `events_test.go`, `ssh.go`
- Create: `cmd/parley/herdr.go`, `herdr_rpc.go`, `herdr_test.go`
- Modify: `cmd/parley/main.go`

**Interfaces:**
- Consumes: Task 1 observed envelopes, Task 3 contracts/store/local IPC.
- Produces `type Run func(context.Context, string, []string, []byte) ([]byte,error)` and `RunProcess` with that signature.
- Produces `Client{HerdrBinary string, ParleyBinary string, LocalMachineID string, HerdrSocket string, Run Run}`, `(*Client).Machines(ctx context.Context) ([]Machine,error)`, `(*Client).Agent(ctx context.Context, target AgentTarget) (AgentTarget,string,error)`, and `(*Client).Parley(ctx context.Context, machine Machine, args []string, stdin []byte) ([]byte,error)`.
- Produces `ManagePeer(ctx context.Context, client *Client, store *Store, machine Machine) (Peer,error)` and `Master{Command *exec.Cmd, ControlPath string, Target string}`, `StartMaster(ctx context.Context, peer Peer) (*Master,error)`, `(*Master).Close() error`.
- Produces `(*Client).OpenEvents(ctx context.Context, peer Peer) (io.ReadCloser,error)`, `ReadEvents(ctx context.Context, stream io.Reader, started func() error, wake func() error) error`, and `(*Client).SignalRequest(ctx context.Context, target AgentTarget, requestID string) error`.
- Produces fixed helper `herdr rpc` operations: `probe`, `probe-route`, `claim-controller`, `release-controller`, `events`, `requests`, `claim-request`, `outcome`, `rooms`, `room`, `start`, `join`, `leave`, `binding`. Paths/argv are internal typed mappings, never caller-controlled helper fields.

- [ ] **Step 1: Add parser, identity, controller, and event tests.**

Use the verified machine/agent fixtures:

```json
[{"id":"11111111111111111111111111111111","label":"workbox","target":"workbox","session":"agents","enabled":true,"selected":false}]
```

```json
{"result":{"agent":{"pane_id":"w1:p2","terminal_id":"term_a","agent":"codex","agent_status":"idle","agent_session":{"source":"hook","agent":"codex","kind":"id","value":"conversation_a"}}}}
```

Fake `Run` verifies `--machine` precedes remote API commands; host-local identity calls use the pinned socket, never a remote selector. Test identical pane IDs across profiles, terminal-preserving pane moves, absent native identity (`agent_identity_required`), and replaced conversation (`agent_replaced`).

Test disabled/missing/incompatible helper, corrupt controller ownership, a different controller (`controller_conflict` with unchanged record), and trusted helper paths containing spaces/single quotes. `selected` never determines ownership.

Use `io.Pipe` plus actual Task 1 event envelopes. Assert no queue read before subscription acknowledgement, then one authoritative read on startup; two coalesced metadata events preserve two pending requests; `events_lost`/EOF force resubscription before rereading; another wake while a read is in flight triggers another read. A quiet healthy stream performs no room queries and creates no route. Unsupported subscription returns an explicit capability error rather than silently polling rooms.

- [ ] **Step 2: Run failing tests.**

```sh
go test ./internal/herdr ./cmd/parley -run 'TestMachines|TestAgentIdentity|TestManagePeer|TestEvents|TestHerdrRPC' -count=1
```

- [ ] **Step 3: Implement bounded host calls and fixed event relay.**

`RunProcess` uses `exec.CommandContext`, direct argv/stdin JSON, bounded stderr and 8 MiB stdout. Oversized output errors rather than truncating valid JSON. Kill/reap on cancellation. Do not use local `sh -c` or a finite stdout accumulator for long-lived events.

`ManagePeer` obtains the installed helper's absolute path from plugin listing, probes compatible capabilities and its selected-session socket, and verifies the helper-derived canonical socket/session key before claiming that session for the local controller through its namespaced config record. `probe` must resolve the saved profile's session explicitly using the verified Herdr mechanism, not whichever shell default happens to be active. No callback path/token is exchanged and no tunnel master is started here. A conflicting record must be explicitly released, never overwritten because a controller appears offline.

The remote command is the shell-quoted trusted executable plus fixed `herdr rpc`; structured fields go on stdin. Typed dispatch permits only the operations listed above. State roots/socket come from verified managed-session config. `probe` derives its namespace from the resolved saved session, without needing a pre-existing config; it returns the canonical socket/full session key. Every later helper resolves that same session and checks its key/config before state access. A caller may supply the trusted saved session selector, never a socket/root override. `probe-route` accepts validated routed endpoints and tests them from the participant host; it is not an arbitrary network probe. `requests` lists all pending request files; `room`/`rooms` expose only validated plugin records, not filesystem paths. Per-request claim and outcome writes are atomic and idempotent.

`rpc events` connects to its pinned Herdr Unix socket, sends exactly this request, then relays its acknowledgement/events until cancellation or API failure:

```json
{"id":"parley-requests","method":"events.subscribe","params":{"subscriptions":[{"type":"workspace.metadata_updated"}]}}
```

The local `OpenEvents` starts outbound `ssh -T` to the saved target with the fixed helper command and separate stdout/stderr pipes. Its reader owns child cancellation/reaping; normal SSH host-key/authentication rules remain intact. The relay never exposes a public Herdr socket or accepts arbitrary API methods. Decode the Task 1 observed framing, require `subscription_started`, bound each frame to 1 MiB, and report malformed/error frames visibly. Continue draining events while queue reads happen on separate bounded helper calls; coalesce wakeups instead of blocking the reader behind a long Join. On EOF/`events_lost`, reconnect with one-to-30-second backoff and perform subscribe-then-reconcile again. Do not replay buffered payloads as authoritative state.

`SignalRequest` commits no mutation: it reports a new `parley_request` marker via the host-local API after `Enqueue` succeeds. Persist/report `pending_signal` on failure. Repeated signals for the same durable request may use a fresh marker to ensure a metadata change.

`StartMaster` is route-only: fixed `-M/-S/-N` argv from the spec, no initial forward, readiness checked through its own control socket. Its lifetime context is the coordinator, not a Join call. `Close` cancels/reaps only its owned child. Task 5 creates it lazily on first cross-host lease.

- [ ] **Step 4: Verify command data separation and quiet startup.**

Submit topic text with literal backticks, `$(...)`, quotes, and newlines; only stdin JSON contains it, no side-effect file appears. Assert helper command strings contain only trusted path/fixed entrypoint, no request-provided paths/targets/args. Event cancellation reaps its child and does not affect Herdr's shared SSH connection. Before any Join, assert no `-L`, `-R`, or room lookup; allow only capability/ownership/subscription and pending-request recovery calls.

```sh
go test ./internal/herdr ./cmd/parley -run 'TestMachines|TestAgentIdentity|TestManagePeer|TestEvents|TestHerdrRPC|TestQuietStartup' -count=1 -race
```

- [ ] **Step 5: Commit.**

```sh
git add internal/herdr cmd/parley
git commit -m "feat: subscribe to remote Herdr requests over outbound SSH"
```

## Task 5: Acquire and release both SSH endpoints

**Files:**
- Modify: `internal/herdr/ssh.go`
- Create: `internal/herdr/ssh_test.go`, `probe.go`, `probe_test.go`

**Interfaces:**
- Consumes: Task 3 `Peer`, `Room`, `AgentTarget`; Task 4 `Run`, `StartMaster`.
- Produces `RouteKind(hostMachineID, participantMachineID string) (string,error)` returning `direct`, `reverse`, or `local`.
- Produces `TunnelManager{Run Run, Masters map[string]*Master}` with `Acquire(ctx context.Context, peer Peer, room Room, target AgentTarget) (Endpoints,error)`, `Release(ctx context.Context, roomID string, target AgentTarget) error`, and `Close() error`.
- Produces `ProbeArtifact(ctx context.Context, rawEndpoint, roomID string) error`, using a validated HTTP artifact base URL and the expected room ID.
- Manager keys masters/leases by machine plus Herdr session; routes additionally include room ID. Process handles and control paths never appear in public results.

- [ ] **Step 1: Add direction, allocation, and rollback tests.**

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

Fake `Run` records argv. First `-R` returns `51001\n`, second fails: cancel the first allocated forward and the second attempted zero-port tuple, then report error. Successful acquisition returns protocol port 51001/artifact port 51002, never original server ports. Local candidates retry at most three times only after authoritative no-effect evidence or successful exact-candidate cancellation. Test a lost allocation response followed by successful cleanup before retry, and failed cleanup retaining `route_unavailable` without retry. A generic mux exit255 and failed cancellation never prove absence. Add malformed allocation, master exit, HTTP/protocol probe failure, cancellation failure retained in status, same-host no SSH, and different remote host rejection.

Add `TestArtifactProbe` using the real `server.ArtifactHandler` backed by an artifact repository that fails the test on any repository method call. Matching room empty POST must return 400 with the exact missing-participant error; a distinct room must return the exact 404 route error. Neither request stages/cleans/fetches an artifact. A generic 200, generic 404, wrong JSON error, identical fake 400 for both IDs, redirect, oversized body, or timeout fails the probe.

Two different bindings sharing the route create two forwards total; one release retains them and final release cancels both. Repeated request for the same binding does not increment refcount again. Distinct Herdr sessions on a single saved host do not share an ownership key.

- [ ] **Step 2: Run failing tests.**

```sh
go test ./internal/herdr -run 'TestRouteKind|TestTunnel|TestArtifactProbe' -count=1
```

- [ ] **Step 3: Implement lazy masters and two-port leases.**

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

Validate both source endpoints as loopback with numeric ports and matching room ID. For cross-host acquisition choose the remote side: participant peer for `-R`, hosting peer for `-L`. Create its plugin-owned master lazily, with no initial forwards. Call `-O forward -R 127.0.0.1:0:127.0.0.1:<source-port>` once per endpoint; parse Task 1 allocation output. For `-L`, reserve an ephemeral local port and close the reservation. Record each attempted tuple before dispatch. On allocation error, cancel the exact attempted candidate, including the original `-R` zero-port tuple. Retry local candidates at most three times only after authoritative no-effect evidence or successful exact-candidate cancellation. Generic mux exit255 and failed cancellation never prove absence; retain uncertain owned routes as `route_unavailable` for explicit recovery instead of retrying. Never reuse Herdr's master or weaken host-key checks.

Use `descriptor.Descriptor.String`, `net.JoinHostPort`, and the existing artifact path for returned endpoints. Probe protocol connectivity and artifact routing from the participant host through the fixed `probe-route` helper. `ProbeArtifact` sends an empty POST to `<artifact-base>/staged`, without a participant query or body. The real handler first checks the path's room ID, then rejects the missing participant before touching its repository:

```text
POST /rooms/<expected-room-id>/artifacts/staged
HTTP 400; Content-Type: application/json
{"error":{"code":"bad_request","message":"participant query parameter is required"}}

POST /rooms/<distinct-probe-room-id>/artifacts/staged
HTTP 404; Content-Type: application/json
{"error":{"code":"artifact_unavailable","message":"artifact route is not available"}}
```

Generate a distinct safe negative room ID inside the helper and substitute only the room path component, preserving the same forwarded host/port. Require both exact statuses and decoded JSON errors. Parse Content-Type as `application/json`, reject unknown/extra JSON fields and multiple values, disable redirects, cap each body at 64 KiB, and use five-second request timeouts. Never send a file, participant parameter, DELETE or consuming request. A plain GET to the artifact base, TCP success, or a generic 404 is not enough. This is validation of the existing unauthenticated route, not a new health/authentication endpoint.

Task 6 validates the protocol room ID returned by Join. Roll back newly acquired forwards if allocation or either probe fails. Use actual allocated-port cancellation syntax from Task 1.

Protect lease maps with a mutex and serialize acquisition per key, with finite subprocess deadlines. No lock spans an unbounded process. Refcounts track known distinct bindings, not repeated requests. `Close` reaps only owned children. Expose dead routes as `route_unavailable`; do not automatically recreate participants or promise unread recovery.

- [ ] **Step 4: Verify real routes independently of fake argv.**

Using Task 1 hosts, start Parley on each endpoint and Join/message/fetch artifact through both directions. Occupy an original artifact port to prove remapping. Final release closes only acquired ports and leaves Herdr's SSH connection usable. Clean real two-direction forwarding remains required. Native automatic retry of a rejected bind candidate with failed absent-tuple cancellation is not claimed: if cleanup/absence cannot be proved, visible `route_unavailable` and explicit recovery satisfy the amended failure acceptance. Fake successful cancellation proves the conditional retry branch, not native absence.

```sh
go test ./internal/herdr -run 'TestRouteKind|TestTunnel|TestArtifactProbe' -count=1 -race
```

- [ ] **Step 5: Commit.**

```sh
git add internal/herdr/ssh.go internal/herdr/ssh_test.go internal/herdr/probe.go internal/herdr/probe_test.go
git commit -m "feat: open both Parley SSH ports on demand"
```

## Task 6: Implement host-local Start and reactive Join

**Files:**
- Create: `internal/herdr/broker.go`, `broker_test.go`
- Modify: `internal/herdr/store.go`, `client.go`, `cmd/parley/herdr.go`, `herdr_rpc.go`

**Interfaces:**
- Consumes: Task 3 requests/claims, `HostBindingKey` and `Store.WithBinding`; Task 4 typed host calls/subscriptions; Task 5 `TunnelManager` and artifact probe.
- Produces `HostOperations{Store *Store, Client *Client}` with `Start(ctx context.Context, req Request) (Response,error)` and `Join(ctx context.Context, req Request, endpoints Endpoints) (Response,error)` for host-local journaling/mutation.
- Produces `Broker{Store *Store, Client *Client, Tunnels *TunnelManager}` with `Handle(ctx context.Context, peer Peer, req Request) (Response,error)`, `Drain(ctx context.Context, peer Peer) error`, `ResolveRoom(ctx context.Context, roomID string) (Room,error)`, and `Reconcile(ctx context.Context) error`.
- Produces `BindingKey(roomID string, target AgentTarget) string`, based on room/machine/Herdr socket/terminal/native conversation, not pane ID.

- [ ] **Step 1: Add Start/Join, discovery, and crash-boundary tests.**

Fake CLI Start response:

```json
{"status":"started","room_id":"room-1","topic":"debug parser","session_id":"psn_host","name":"host","descriptor":"parley://127.0.0.1:49001/room-1","local_port":49001,"artifact_local_port":49002,"artifact_path":"/rooms/room-1/artifacts"}
```

Fake Join response:

```json
{"status":"joined","room_id":"room-1","session_id":"psn_remote","name":"agent","participant":{"artifact_endpoint":"http://127.0.0.1:51002/rooms/room-1/artifacts","status":"online"}}
```

Use real temporary stores and fake `Run`. Start executes only on its selected host, writes room/binding and pane tokens, with no coordinator registration, route, worker, or prompt. Join from the original host pane returns that completed host session without launching another adapter. Join from an empty coordinator cache discovers the room on demand. Clear tokens/restart Herdr: durable room fallback still validates the live host. Same name/distinct IDs works; identical IDs on different hosts is `room_ambiguous`. Unreachable enabled hosts produce resolution incomplete, not first-match selection.

```go
func TestEmptyQueueDoesNotDiscoverRooms(t *testing.T) {
    calls := 0
    client := &Client{Run:func(context.Context, string, []string, []byte) ([]byte,error) {
        calls++
        return nil, fmt.Errorf("unexpected subprocess")
    }}
    broker := &Broker{Store:NewStore(t.TempDir()), Client:client}
    peer := Peer{Machine:Machine{ID:"local"}}
    if err := broker.Drain(context.Background(), peer); err != nil { t.Fatal(err) }
    if calls != 0 { t.Fatalf("quiet queue performed %d calls", calls) }
}
```

Submit Join twice with one request ID, then another ID for the same binding: exactly one adapter/session and one route lease. Add `TestConcurrentJoinSameBinding` with different IDs: use a channel barrier to block the first fake CLI Join after intent commit, invoke the second while it is blocked, then release the first. Assert one CLI Join, both responses contain the same session, and only one lease binding reference. Repeat through separate helper processes and with direct `self` Join racing queued controller-normalized Join; both must use one host key/lock. Add crash after intent/CLI success before binding persistence: the next request reconciles the original participant instead of joining. If the first adapter disconnects during that race, the second returns `participant_disconnected` and leaves `LastSeenSeq` unchanged. Changed body is `request_conflict`. Claim races mutate only once. Two pending requests under one marker both complete; requests before subscription are reconciled; commit-before-marker crash recovers on reconnect/explicit retry. Forged origin fails before any SSH call.

Inject lost Start/Join response after mutation, failure of outcome publication, room ID mismatch, and known Join failure after forwarding. No blind retry/duplicate participant. Known failure rolls back; uncertain mutation retains a visible claim and resources. No automatic agent prompt or consuming message command occurs.

- [ ] **Step 2: Run failing tests.**

```sh
go test ./internal/herdr -run 'TestHostStart|TestResolveRoom|TestBroker|TestBindingKey|TestQueueRecovery|TestEmptyQueue|TestConcurrentJoin' -count=1
```

- [ ] **Step 3: Implement host-local mutation and on-demand lookup.**

Start captures original native identity, reserves `s_` plus the first 16 hex characters of SHA-256 over JSON `[SessionKey, requestID]` as its host name, and journals that name/full request digest before host-local `parley start --topic --name`. On a lost response, use `runtime.ListSessions` and `runtime.LoadRoomRuntime` to recover exactly one matching reserved host name; missing or ambiguous runtime evidence remains `operation_uncertain`. Never blindly start again. Persist room plus its `HostBindingKey` binding with `JoinPhase:"complete"` and the Start request ID/session, then advertise `parley_room`, `parley_port`, `parley_artifact_port`, then return the actual room/session and fixed command instructions in the response. Failed metadata reporting retains the room and warns; no worker/prompt is launched.

For Join, persist immutable request on the action's host. Local form uses private IPC; remote form calls `workspace.report_metadata` after commit. `Drain` reads **all** pending requests from that origin, normalizes `self` against the trusted peer, validates/claims each request, and calls `Handle`. A dirty wakeup while a read is in flight causes another read. Run Drain after subscription acknowledgement, reconnect/`events_lost`, and marker events. There is no periodic room discovery.

`ResolveRoom` reads authoritative local/enabled pinned remote room records on demand through fixed `room`/`rooms` helpers when Join needs a room. Pane tokens are display hints and need not be queried first. Validate a matching durable room record, live native host identity, live room status, and both source endpoints. Missing/stale tokens and Herdr restarts use the same fixed helper `rooms`/`room` reads; any cache is revalidated before mutation. Metadata-only rooms without durable plugin records are not discovered; plugin Start always persists first. No token may supply an SSH target, binary path, or state path. Same-host Join can resolve the selected host directly without global lookup/coordinator.

In `Broker.Handle`, hold `Store.WithBinding(ctx, "route:"+BindingKey(...), ...)` across existing-binding lookup, acquisition/probes, helper Join and result/lease accounting. A completed existing binding returns before acquisition; repeated request IDs do not add references. Use the participant host's fixed Join helper with routed descriptor/artifact override. Direct same-host forms call the exact same `HostOperations.Join` method used by that helper.

`HostOperations.Join` computes the controller-independent `HostBindingKey` and enters `Store.WithBinding(ctx, "join:"+hostKey, ...)`. Under this host lock, read/reconcile binding state before invoking any CLI. A completed online binding returns its original session without another Join. Persisted `JoinPhase:"intent"` or `"uncertain"` must reconcile or return `operation_uncertain`; completed-but-offline state is `participant_disconnected`, never automatic rejoin. Reserve `h_` plus the first 16 hex characters of the full SHA-256 host key as the participant name, validate collisions against the full host key, and persist a binding with `JoinPhase:"intent"`, `JoinRequestID`, target, name and routed endpoints before calling existing `parley join --name`. Do not hold the general state lock during that call, but keep the host binding lock until the complete binding/result is durable. Recheck native target identity before mutation. Keep private control paths short and validate the participant socket path length before launch.

To reconcile a lost result, use `runtime.ListSessions`, `runtime.LoadSession`, and `runtime.ParticipantStore` under the trusted Parley root; accept exactly one matching reserved name/room and a live adapter. Save its session with `JoinPhase:"complete"` before responding. If the state cannot prove a live original participant, keep the intent/uncertain binding and fail visibly; never use a fresh Join to reset the cursor. A process crash releases the OS lock but retains the intent for the next holder. Lock ordering is coordinator `route:` then participant `join:`; direct calls acquire only `join:`. No new public resume/replay protocol is introduced.

Known successful Join created by this attempt with room mismatch or subsequent failure calls existing leave for that session, then cancels only this attempt's newly acquired routes. Never leave a binding or release a lease returned from another completed request. Check the persisted `JoinRequestID` and binding membership when deciding rollback ownership. Uncertain side effects retain intent/lease for explicit recovery. Publish outcome atomically on the request's host. The form reads its own file with a bounded wait and reports pending on timeout. Repeating the request retrieves its stored outcome.

`Reconcile` checks only known bindings/uncertain intents and owned route liveness. On confirmed inactive participant/closed room, release its lease. Offline existing adapters are `participant_disconnected`; do not auto-rejoin or consume/reset their state. Status/explicit recovery can trigger queue reconciliation; host silence never creates a replacement room. Preserve existing manual leave/stop behavior.

- [ ] **Step 4: Verify both directions and no deferred behavior.**

```sh
go test ./internal/herdr ./cmd/parley -run 'TestHostStart|TestResolveRoom|TestBroker|TestBindingKey|TestQueueRecovery|TestEmptyQueue|TestHerdr|TestConcurrentJoin' -count=1 -race
```

Assert `-R` is created locally for a remote Join to a local room, `-L` locally for local Join to a remote room, and no route on same host. Kill fake SSH at claim/first-forward/Join/outcome boundaries and verify no second participant. Assert no `agent prompt`, `inbox`, `wait`, worker launch, or replay field is issued.

- [ ] **Step 5: Commit.**

```sh
git add internal/herdr cmd/parley
git commit -m "feat: start Herdr rooms and route Join requests reactively"
```

## Task 7: Package Start/Join actions and prove automatic port opening

**Files:**
- Create: `herdr-plugin.toml`, `internal/herdr/form.go`, `form_test.go`, `integration_test.go`, `docs/herdr-plugin.md`
- Modify: `cmd/parley/herdr.go`, `herdr_rpc.go`, `herdr_test.go`, `internal/herdr/broker.go`, `README.md`, `docs/herdr-transport-proof.md`

**Interfaces:**
- Consumes: coordinator, request subscriber, host-local operations and tunnel manager.
- Produces `herdr setup`, `herdr startup`, `herdr action <start|join>`, `herdr form`, `herdr status`, hidden `herdr broker`, and fixed hidden `herdr rpc`.
- Produces `ReadForm(r io.Reader, op string) (string,error)` and `LaunchHelper(ctx context.Context, binary string, args []string, readySocket string) error` for coordinator launch only.

- [ ] **Step 1: Add form/context, lifecycle, and integration tests.**

```go
func TestReadFormCancelAndInput(t *testing.T) {
    if _, err := ReadForm(strings.NewReader("\x1b"), "join"); err == nil { t.Fatal("escape accepted") }
    got, err := ReadForm(strings.NewReader("room-1\n"), "join")
    if err != nil || got != "room-1" { t.Fatalf("%q %v", got, err) }
}
```

Capture pane `w1:p2`/terminal/native identity before popup; submitting a form from another UI pane still targets the original terminal. Escape, EOF, empty ID/topic, and topic over 512 bytes must not mutate. Startup twice launches one coordinator within that canonical socket namespace; a sibling participant session sharing injected roots cannot see its controller role and launches none. Activation on an already-running server works immediately. Missing local controller config gives an actionable setup instruction, not a remotely launched second controller.

Integration tests use real production CLI/adapters with synthetic local/remote roots, fake typed Herdr/SSH control, and loopback proxies. Create on either endpoint, enqueue Join, drive actual subscriber frames, check both routed ports/artifact bytes and exactly one session across duplicate requests. Simulated event loss must resubscribe/read durable state, not replay event payloads as a queue. Startup with an empty queue performs no room scan/forward. Keep real SSH evidence separate from fake argv tests.

- [ ] **Step 2: Run failing tests.**

```sh
go test ./internal/herdr ./cmd/parley -run 'TestReadForm|TestPopupTarget|TestHerdrStartup|TestIntegration' -count=1
```

- [ ] **Step 3: Add minimal manifest and lifecycle wiring.**

```toml
id = "parley"
name = "Parley"
version = "0.1.0"
min_herdr_version = "0.9.3"
description = "Start and join rooms with automatic SSH port forwarding"
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

[[panes]]
id = "form"
title = "Parley"
placement = "popup"
width = "70%"
height = 12
command = ["bin/parley", "herdr", "form"]
```

No manifest `[[events]]` message/readiness hook is installed. The startup hook establishes the coordinator's request stream; workspace marker subscriptions use the socket API.

`action` captures original identity and passes validated `PARLEY_TARGET_JSON`/`PARLEY_ACTION` to `plugin pane open --entrypoint form`. The form runs host-local Start or calls the shared binding transaction for direct same-host Join; otherwise writes/signals a request and awaits its outcome. Derive config/state directories from the captured canonical server socket before any access; popup context must not choose another namespace. Display room ID, session ID, fixed Parley command instructions, and request ID for pending/uncertain work. Do not call `agent.prompt` or install/update harness hooks/skills.

Use fixed `stty` argv to save/restore terminal settings and support Escape/backspace/Enter on macOS/Linux, including exit/signal cleanup. Keep ordinary commands JSON-only. Reject untrusted path/argv fields before any mutation.

`setup` derives its canonical socket/session key and records the local controller role/socket/random ID in that session's config directory, then starts it immediately. `startup` restores configured local coordinator only; participant/unconfigured sessions are no-op. `LaunchHelper` uses the repository's detached process pattern, private logs, ready socket handshake, no inherited action pipes, and only its new child handle for failure cleanup. Never kill a persisted PID.

Coordinator refreshes enabled profiles every 30 seconds and plugin-enabled status every two seconds. This manages subscriptions, not room discovery. On relay loss reconnect with bounded backoff and read pending requests after acknowledgement. Known-binding status reconciliation may release stale routes; it never starts another adapter. Plugin disable closes only owned event readers and SSH masters. Herdr UI detach must leave them alive. Host-key/auth failures remain visible, never bypassed.

- [ ] **Step 4: Document installation and exact scope.**

```sh
herdr plugin install khaiql/parley
parley herdr setup
```

Install on every participating host; initial manifest needs Go. For local development build `bin/parley` then link the absolute checkout because link does not run builds. Setup is local coordinator activation/configuration, not a room-specific port command. Explain automatic later startup, first activation on an existing server, remote-only UI constraint, Start/Join, status/pending recovery, SSH permission errors, same-host direct route, and unsupported different-remote-host joins.

State clearly: returned commands still use existing Parley messaging. Automatic receive hooks, readiness prompts, participant resume/replay, and adapter reconnection are deferred. Do not modify the repository skill or installed harness configuration in this phase.

- [ ] **Step 5: Run release gates and record the live matrix.**

```sh
go test ./internal/herdr -run TestIntegration -count=1 -race
go build ./...
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
go test ./... -timeout 30s -race
```

| Scenario | Required real evidence |
| --- | --- |
| Quiet startup | Subscription active, no room query or forward |
| Local room / remote Join | Remote action marker reaches local listener; two `-R` forwards; message/artifact bytes |
| Remote room / local Join | On-demand lookup; two `-L` forwards; message/artifact bytes |
| Same host | No SSH master or forward acquired |
| Coalesced/lost events | Every durable request handled once after subscribe/reconcile |
| Missing metadata | Durable room fallback after server restart |
| Duplicate/lost result | Same session/outcome; no blind participant recreation |
| Concurrent distinct IDs / direct vs queued | Host binding lock permits one CLI Join; disconnect does not reset the cursor |
| Shared plugin roots / two named sessions | State, config, startup role and controller ownership remain isolated |
| Artifact probe | Exact matching/wrong-room errors; zero repository mutations; generic/redirect replies rejected |
| Partial forward / occupied port | Rollback or bounded remap; no orphan participant |
| UI detach / disable | Helpers survive detach; only owned resources close on disable |
| Dead route/adapter | Visible unavailable/disconnected status; no automatic rejoin |

Use real Herdr/OpenSSH for both directions and event relay; fake tests do not prove them. A macOS/Linux release requires at least one macOS controller/Linux remote run. Record missing tools/credentials or an unforced `events_lost` as unverified. Do not claim notification/replay behavior was tested or shipped.

- [ ] **Step 6: Commit.**

```sh
git add herdr-plugin.toml internal/herdr cmd/parley README.md docs/herdr-plugin.md docs/herdr-transport-proof.md
git commit -m "feat: package Herdr automatic SSH room setup"
```

## Self-review result

- Spec maps to all seven tasks: startup/event transport (1, 4, 7), state/origin/claims (3, 4), routing/endpoint override (2, 5), Start/Join/discovery (6), UX/lifecycle/acceptance (7).
- Review Focus cases have tests in their owning tasks, including coalesced events, missing tokens, quiet startup, partial forwarding, and lost mutation results.
- Review fixes specify socket-derived session directories (1, 3, 4, 7), participant-host binding transactions plus coordinator lease serialization (3, 6), and the existing non-mutating artifact route probe (5). Tests explicitly cover two sessions sharing roots, concurrent direct/queued requests, and both HTTP responses without artifact mutations.
- Shared contracts precede consumers. There are no callback tokens, notification workers, `agent.prompt` calls, or replay protocol changes in the implementation scope.
- The previous macOS primitive proof is explicitly separated from unverified reactive relay and production Linux behavior.
- A failed tunnel does not promise transparent participant recovery; that work is deferred alongside message hooks.

## Execution handoff

Review these two documents before implementation. Native execution suits the shared state/subscriber/lease contracts. Implementation is a separate task; this revision does not build/install the plugin or change the HTML walkthrough.
