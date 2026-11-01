# Parley Herdr automatic SSH routing design

Status: proposed implementation design, revised 2026-10-13. Scope narrowed to room Start/Join and automatic SSH port opening. Listener startup remains background infrastructure for remote Join. Message send/receive hooks and notification automation are deferred.

## Intended outcome and scope

From an existing Herdr agent pane, Start creates a named Parley room on that pane's machine and returns its room ID. Join takes that ID, finds the hosting machine, opens both required SSH ports automatically, and starts the participant adapter on the joining pane's machine. Return the participant session and its existing Parley command instructions in the popup; do not automatically prompt the agent in this phase.

Install Parley and its helper/plugin on each participating machine. The two main cases are a local room with a remote collaborator and a remote room with a local collaborator. The local machine originates SSH in both cases. Herdr's selected remote server runs the action; it cannot directly launch a local SSH process.

## Constraints

- Go 1.26.1; existing Cobra dependency; no new production dependencies.
- Initial platforms: macOS and Linux.
- Initial Herdr minimum: 0.9.3; verify required CLI/socket capabilities during activation.
- Host the room on the initiating agent's machine; attach existing panes without launching or restarting agents.
- Support local/local, local/remote, remote/local, and same-remote-machine joins. Reject different-remote-machine routing with `unsupported_route`.
- One local controller per managed remote Herdr session; reject conflicting ownership with `controller_conflict`.
- SSH always originates on the local machine. Bind both forwarded endpoints to `127.0.0.1`.
- Preserve existing Parley CLI, descriptors, JSON conventions, and message/cursor behavior.
- No message hooks, automatic agent prompts, notification workers, or participant replay/resume changes in this phase.

## Components

- **Host-local action/form:** captures the original pane's terminal/native conversation before opening a popup. Start runs locally on that selected server. Cross-machine Join commits a request in that server's plugin state directory.
- **Local coordinator:** runs detached on the user's local machine. Owns remote event subscriptions, request processing, and plugin-owned tunnel masters. It resolves room metadata when Join is requested, not during startup or periodic room discovery.
- **Fixed SSH helper:** installed on each remote host. Exposes typed state/Parley operations and a read-only Herdr event relay over SSH initiated locally. It never accepts arbitrary commands, paths, environment overrides, or raw API methods.

There is no reverse callback socket, peer-token exchange, or room-independent `-R` control forward. Herdr's own UI SSH stream is not exposed automatically to plugins; we must build and own the subscriber/relay.

## Listener startup, not startup room discovery

The configured local plugin session's `[[startup]]` hook launches one coordinator. It reads enabled saved profiles, resolves installed helper capabilities and the correct remote Herdr session socket, then establishes `events.subscribe` connections. It creates no room forwards and performs no room metadata scan. Previously known active bindings may be checked during recovery; that is not discovery of new rooms.

Remote startup hooks do not launch another coordinator. The local role/socket/controller ID are explicitly configured once through `parley herdr setup`; startup then restores them automatically. On an already-running Herdr server, setup activates the coordinator immediately because linking/enabling does not invoke startup hooks. Local Start/Join can ensure an already-configured coordinator is running. A remote-only UI still needs a configured local coordinator session.

One managed remote session has one durable controller owner record. Claim it via a locked fixed helper call. Reject another controller and require explicit ownership release before changing controllers; do not infer ownership from the UI's selected machine or an apparently offline controller.

Subscribe to `workspace.metadata_updated`. This native API event does **not** invoke manifest event hooks. Implement `parley herdr rpc events` to connect to the pinned remote Unix socket, send the fixed subscription, and relay acknowledgement/events through outbound SSH. Herdr's internal `remote-api-bridge` shows the transport is possible but is not a stable public plugin streaming command to depend on.

After a verified native subscription acknowledgement is forwarded, the helper emits the fixed internal frame `{"id":"parley-requests","result":{"type":"parley_transport_keepalive"}}` with a newline every two seconds. The correlated reader accepts this exact frame only after its own fresh acknowledgement and never treats it as a queue invalidation. The frame belongs only to helper output; it is never written into native metadata, messages, requests, or room storage. Output failure or cancellation closes and joins the owned native reader and stops the timer. This lets a quiet native subscription detect a closed SSH output without a metadata wakeup.

On startup, reconnect, or `events_lost`, subscribe first and keep draining events while reading pending requests through a separate SSH call. Events are invalidation signals, not the queue. Coalesce wakeups; repeat a read if another event arrived while it was in flight. Refresh saved profiles for new/disabled hosts, without timer-based room discovery.

## State placement

Herdr API state is per server, but `HERDR_PLUGIN_STATE_DIR` and `HERDR_PLUGIN_CONFIG_DIR` are shared per OS user/plugin on a host. The plugin must add an explicit session namespace; it cannot use either injected directory directly as a session store.

Resolve the selected server's socket on that host, canonicalizing it with `filepath.EvalSymlinks` followed by an absolute clean path while the server is reachable. Persist that verified canonical path; do not recompute a different default during an outage. Define `session_key = lowercase hex(SHA-256(canonical_socket_path UTF-8))`. The key is stable across restarts at the same socket path, independent of PID, inode, pane, saved-profile label, or controller ID. A changed socket path is a different session and needs activation.

Use `<HERDR_PLUGIN_STATE_DIR>/sessions/<session_key>/` as the state root and `<HERDR_PLUGIN_CONFIG_DIR>/sessions/<session_key>/` as the configuration root. Action, form, startup and SSH helper all derive the same key from the verified server socket. Never accept a namespace/root path from a queued request or search sibling namespaces to find a record. A helper probe returns its canonical socket/key, and the coordinator rejects a mismatch before reading state or claiming ownership.

Within the session state root use:

- `rooms/<room-id>.json`: room name/ID, hosting Herdr socket, terminal/native identity, host participant session, both source ports, closed status.
- `requests/<request-id>.json`: immutable Join request, original pane/workspace/terminal/conversation identity.
- `outcomes/<request-id>.json`: normalized request digest, controller claim, mutation intent, and complete/uncertain result.
- `state.json`: managed peers, discovered room cache, and known bindings/routes. The cache is not the authoritative room registry.

The session configuration root contains `coordinator.json` for the local role/pinned socket and `controller.json` for managed-session ownership. All room/request/outcome/binding records, locks, logs, and ownership records belong to that session. Use separately allocated short private IPC/control socket paths whose handshake identifies this full session key; never truncate the session identity to fit a filesystem socket path. Trusted setup or the native action pins the selected host's `PARLEY_STATE_DIR` once in private immutable `runtime-root.json` before request submission. Every fixed SSH helper reads that pin before runtime access; SSH environment and request data cannot change it. An unpinned native action may pin its trusted local default. No manual participant setup is required. Directories are `0700`, records `0600`, with locked atomic replacement and symlink/corruption rejection.

Start advertises `parley_room`, `parley_port`, and `parley_artifact_port` via `pane.report_metadata`. Remote Join changes the workspace's `parley_request` token to a fresh random marker. All values fit the 80-character token limit; no payload, file path, or SSH target is stored in a marker. Tokens disappear after Herdr restart and cannot replace durable files.

A marker wakes a read of **all** pending requests for that managed session, so coalesced events cannot erase requests. Write the request before signalling. A failed signal retains the request and reports `pending_signal`; retry the same request with a fresh marker. A crash before signalling is recovered by a later wakeup, reconnect, or explicit retry/status recovery. Form timeout reports pending with its request ID; it does not cancel an uncertain mutation.

## Start sequence

1. Capture and validate the original existing agent identity on the selected server.
2. Reserve `s_` plus the first 16 hex characters of SHA-256 over JSON `[SessionKey, requestID]`, journal the full normalized request digest, run host-local `parley start --topic`, and persist returned room/session/endpoints plus a completed host binding under the same host-local binding key used by Join.
3. Advertise room metadata on that host pane.
4. Return room ID and existing session command instructions in the popup.

Start needs no central registration, callback, worker, or tunnel. Failed metadata reporting retains the room and warns that discovery will use the durable fallback. Unknown Start outcomes must reconcile local runtime state, never blindly create another room.

## Join sequence

**Local Join:** save a durable request and notify the local coordinator via private local IPC. It reads authoritative local and enabled pinned remote room records on demand, validates the matching durable room record and live host identity, acquires/probes both routes, and calls participant-host `parley join` with the remapped artifact endpoint. Persist the binding/outcome and display session instructions.

**Remote Join:** the form saves its request in the remote server's plugin state directory and reports the workspace marker. The local listener receives the event over its own outbound SSH relay, fetches pending requests through a separate fixed helper call, validates/claims the request, and resolves the room on demand. It creates SSH forwards locally, then calls the remote helper to Join. Publish the result back through outbound SSH; the form reads only its own outcome file.

Same-host Join can run directly on the selected server without the coordinator or SSH. A local room may therefore join locally even if no remote subscriptions exist. Direct and queued Join must both use the same participant-host binding transaction below; the direct path does not bypass its lock.

Lookup uses room ID and authoritative machine/Herdr session/terminal/native conversation identity. Pane IDs are server-scoped and can change after moves. Normalize host-local `machine_id:"self"` records to the saved-profile peer, never trust submitted machine/socket claims. Lookup always reads host-specific durable room records through fixed `room`/`rooms` helpers, so missing/stale pane tokens need no alternate discovery path. Metadata-only rooms without durable plugin records are not discovered; plugin Start always persists first. Verify live room status and both source endpoints before forwarding. Duplicate IDs across hosts fail as `room_ambiguous`; unavailable enabled hosts make global resolution incomplete rather than silently picking a first match.

## Routing

| Room host | Joining participant | Route |
| --- | --- | --- |
| Local | Local | Direct loopback |
| Remote A | Remote A | Direct loopback |
| Local | Remote A | Local coordinator starts two `-R` forwards to A |
| Remote A | Local | Local coordinator starts two `-L` forwards from A |
| Remote A | Remote B | `unsupported_route` |

Forward both Parley's TCP protocol port and artifact HTTP port. Create a plugin-owned SSH master lazily for a cross-machine Join; never use Herdr's shared master. Use a short private control path, `BatchMode=yes`, `ExitOnForwardFailure=yes`, `ControlPersist=no`, and server keepalives. No forwards are attached during listener startup.

```text
# Local room, remote collaborator: expose local endpoints on remote loopback.
ssh -S <owned-master> -O forward -R 127.0.0.1:0:127.0.0.1:<local-source-port> <target>
# Remote room, local collaborator: expose remote endpoints on local loopback.
ssh -S <owned-master> -O forward -L 127.0.0.1:<local-port>:127.0.0.1:<remote-source-port> <target>
```

Apply once per endpoint. Parse allocated remote ports. Retry local candidates at most three times only after authoritative no-effect evidence or successful cancellation of the exact attempted candidate. A generic mux exit255 or failed cancellation never proves absence; retain uncertain owned routes as `route_unavailable` for explicit recovery instead of retrying. Probe from the participant host, then join and verify room ID.

For artifact HTTP, send an empty `POST /rooms/<room-id>/artifacts/staged` with no query string, credentials, or request body. The existing route validates the room first, then rejects the missing participant before invoking artifact storage. Require HTTP 400, JSON content type, and exactly `{"error":{"code":"bad_request","message":"participant query parameter is required"}}`. As a negative control, send the same request with a distinct helper-generated room ID; require HTTP 404 and exactly `{"error":{"code":"artifact_unavailable","message":"artifact route is not available"}}`. Generic responses, redirects, or identical replies to both IDs fail the probe. Disable redirects, use five-second timeouts, and bound each response to 64 KiB. This verifies expected room routing through the existing unauthenticated API without staging/consuming artifacts or adding an endpoint.

If the second forward or Join fails with a known outcome, cancel newly acquired forwards and leave no orphan participant. Retain claims/resources if mutation is uncertain until explicit recovery.

Reuse/ref-count routes for a room plus participant machine/Herdr session. Release owned routes on rollback, confirmed inactive bindings, coordinator shutdown, or plugin disable. Existing `parley leave`/`stop` remain available; coordinator status/reconciliation checks known bindings for stale leases without discovering rooms. Detaching the UI leaves the coordinator/masters running. Never stop existing agents or unrelated tunnels.

## Required Parley change

Add `parley join --artifact-endpoint <http-url>` and pass it through public Join, hidden participant daemon, daemon argv, and adapter initialization. Otherwise the adapter uses the room's original artifact port, which may not be the forwarded port.

Keep existing derivation when the flag is absent. Validate absolute HTTP, host/numeric port, no credentials/query/fragment, and exactly `/rooms/<room-id>/artifacts`. Supply loopback URLs from the coordinator; retain original server endpoint metadata.

## Trusted boundaries and failures

Accept fixed structured Start/Join/status requests with random IDs; internal rollback may call existing leave. Reject unknown fields/operations, malformed or oversized JSON, unsafe IDs, forged origin, and corrupt records. Origin comes from the local socket or the SSH-managed host/session from which the coordinator fetched a request.

Resolve the trusted installed executable and session socket from the plugin registry/probe. Quote only that executable for the remote shell; send user fields as stdin JSON. The helper derives state paths internally and exposes only integration records. No callback authentication protocol is needed.

Activation requires the fixed read-only `join-failure` capability. It accepts the original normalized Join request under verified controller/session ownership, holds the same host `join:` lock, verifies the stored request digest and host key, and returns its exact durable failure or explicit absence plus current binding ownership. It never launches a CLI, acquires a route, pins a root, or mutates an operation record. Recovery checks `JoinRequestID` and current binding membership before releasing a failed attempt; a newer successful binding keeps its lease. Absence cannot authorize a fresh Join for an old uncertain/intent outcome.

Claim a request before any route/participant mutation. Same ID/body replays the outcome; changed body is `request_conflict`. Request claims are not binding locks: different request IDs may still refer to one participant.

Define a host-local binding key from the session key, room ID, terminal ID, agent kind, and native conversation using an unambiguous JSON string tuple and SHA-256. Exclude controller machine/profile IDs so direct `self` and queued normalized requests use the same host key. The participant helper holds a cross-process file lock for that key across the existing-binding check, intent reconciliation, reserved-name creation, CLI Join, and binding/result persistence. Do not hold the general state-file lock across a subprocess. Bound lock waits by the request context.

An online completed binding returns its original session without another Join. An incomplete intent is reconciled; uncertain or disconnected existing bindings return `operation_uncertain` or `participant_disconnected`, never a fresh Join that could reset unread state. Persist the binding intent before the CLI; keep it after crashes even though the OS releases its lock. Reserved names detect collisions but do not provide mutual exclusion. Direct action and SSH helper enter the same transaction.

The coordinator also serializes operations for a binding across route acquisition, helper Join, and result/lease accounting. Use a separate `route:` lock key, acquire it before the host `join:` lock, and never acquire them in reverse order. A repeated completed binding adds no lease reference. Rollback can remove only the current attempt's new resources, not a binding/lease already committed by another request.

Journal host-local CLI intent before mutation and persist results before returning them. Use a reserved participant name and existing runtime session records to reconcile a lost Join response. If the state cannot prove the outcome, return `operation_uncertain`, never auto-create another participant.

A dead event relay reconnects and rereads the queue. A dead route is reported `route_unavailable`; an offline adapter is `participant_disconnected`. This phase does not promise adapter auto-resume, complete unread replay, or transparent collaboration across tunnel outages. Explicit recovery must not silently discard a participant's existing state.

## Verification boundary and acceptance

The prior 13-check fixture used two isolated macOS Herdr 0.9.3 servers over loopback OpenSSH. It proved metadata reads, remote plugin state writes, restart persistence/startup hooks, outbound request/result exchange without a callback, and actual Parley messages/direct HTTP artifact bytes over both `-L` and `-R`. It did not prove the reactive event relay, Linux/user SSH policies, complete claims/orchestration, supervision, or the CLI artifact override.

Test two named Herdr sessions under the same XDG/plugin roots: requests, room lookup, startup role, state and controller ownership must remain isolated, including after restart. Test simultaneous distinct request IDs for one binding, direct versus queued Join, and disconnect/crash during the binding transaction. Require one original session/lease and no cursor reset. Test artifact probe positive/negative responses against the real handler, generic/redirect/wrong-room servers, and zero artifact repository calls.

Before dependent implementation, prove a remote action's workspace marker reaches a plugin-owned local subscriber and that pending requests survive subscription reconnect. Record `events_lost` framing/recovery and ephemeral metadata fallback. No room scan or route may occur at quiet startup.

Acceptance: Start on either host; Join an existing opposite-host pane by room ID; create both correct loopback forwards automatically; exchange a message and fetch artifact bytes through remapped ports; handle duplicate requests and partial failures without duplicate participants/orphan forwards; survive UI detach; and clean up only owned routes. Message exchange is a transport check, not a new hook feature.

## Deferred work

Message send/receive hooks, automatic prompting, readiness gating, notification workers, participant resume/unread replay, automatic adapter recovery after a tunnel outage, new-agent launch, different-remote-host relay, hosted discovery, concurrent controllers, and Windows support.

## Sources

- [Herdr plugins](https://herdr.dev/docs/plugins/) and [connecting machines](https://herdr.dev/docs/connecting-machines/): host-local actions, startup and plugin state.
- [Herdr socket API](https://herdr.dev/docs/socket-api/): metadata persistence/limits, API-only workspace events, subscriptions and `events_lost` recovery.
- [Herdr CLI](https://herdr.dev/docs/cli-reference/): saved profiles and remote metadata reads.
- [Herdr plugin paths](https://github.com/herdrdev/herdr/blob/v0.9.3/src/plugin_paths.rs#L13-L23) and [injected directories](https://github.com/herdrdev/herdr/blob/v0.9.3/src/app/api/plugins/env.rs#L13-L25): per-user/plugin roots require explicit session namespaces.
- [OpenSSH ssh(1)](https://man.openbsd.org/ssh) and [ssh_config(5)](https://man.openbsd.org/ssh_config): forwarding, owned masters and bind/failure behavior.
- Repository: `cmd/parley/join.go`, `daemon_commands.go`, `daemon_process.go`, `participant_daemon.go`, `participant_artifacts.go`; `internal/runtime/runtime.go` for existing session reconciliation.

## Receive-notification follow-up

The separate [idle message fallback design](2026-10-20-herdr-idle-message-fallback-design.md) specifies agent-owned listening with delayed plugin reminders. It does not expand the original routing phase.
