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

On startup, reconnect, or `events_lost`, subscribe first and keep draining events while reading pending requests through a separate SSH call. Events are invalidation signals, not the queue. Coalesce wakeups; repeat a read if another event arrived while it was in flight. Refresh saved profiles for new/disabled hosts, without timer-based room discovery.

## State placement

State is per Herdr server, not globally shared by the local UI. On each host use session-scoped `HERDR_PLUGIN_STATE_DIR` files:

- `rooms/<room-id>.json`: room name/ID, hosting Herdr socket, terminal/native identity, host participant session, both source ports, closed status.
- `requests/<request-id>.json`: immutable Join request, original pane/workspace/terminal/conversation identity.
- `outcomes/<request-id>.json`: normalized request digest, controller claim, mutation intent, and complete/uncertain result.
- `state.json`: managed peers, discovered room cache, and known bindings/routes. The cache is not the authoritative room registry.

`HERDR_PLUGIN_CONFIG_DIR` stores the local coordinator role and managed-session ownership. Pin the selected host's `PARLEY_STATE_DIR` for every Parley operation. Directories are `0700`, records `0600`, with locked atomic replacement and symlink/corruption rejection.

Start advertises `parley_room`, `parley_port`, and `parley_artifact_port` via `pane.report_metadata`. Remote Join changes the workspace's `parley_request` token to a fresh random marker. All values fit the 80-character token limit; no payload, file path, or SSH target is stored in a marker. Tokens disappear after Herdr restart and cannot replace durable files.

A marker wakes a read of **all** pending requests for that managed session, so coalesced events cannot erase requests. Write the request before signalling. A failed signal retains the request and reports `pending_signal`; retry the same request with a fresh marker. A crash before signalling is recovered by a later wakeup, reconnect, or explicit retry/status recovery. Form timeout reports pending with its request ID; it does not cancel an uncertain mutation.

## Start sequence

1. Capture and validate the original existing agent identity on the selected server.
2. Journal intent, run host-local `parley start --topic`, and persist returned room/session/endpoints.
3. Advertise room metadata on that host pane.
4. Return room ID and existing session command instructions in the popup.

Start needs no central registration, callback, worker, or tunnel. Failed metadata reporting retains the room and warns that discovery will use the durable fallback. Unknown Start outcomes must reconcile local runtime state, never blindly create another room.

## Join sequence

**Local Join:** save a durable request and notify the local coordinator via private local IPC. It fetches local and enabled remote pane metadata on demand, validates the matching durable room record and live host identity, acquires/probes both routes, and calls participant-host `parley join` with the remapped artifact endpoint. Persist the binding/outcome and display session instructions.

**Remote Join:** the form saves its request in the remote server's plugin state directory and reports the workspace marker. The local listener receives the event over its own outbound SSH relay, fetches pending requests through a separate fixed helper call, validates/claims the request, and resolves the room on demand. It creates SSH forwards locally, then calls the remote helper to Join. Publish the result back through outbound SSH; the form reads only its own outcome file.

Same-host Join can run directly on the selected server without the coordinator or SSH. A local room may therefore join locally even if no remote subscriptions exist.

Lookup uses room ID and authoritative machine/Herdr session/terminal/native conversation identity. Pane IDs are server-scoped and can change after moves. Normalize host-local `machine_id:"self"` records to the saved-profile peer, never trust submitted machine/socket claims. Missing/stale pane tokens trigger host-specific helper reads of durable room records. Verify live room status and both source endpoints before forwarding. Duplicate IDs across hosts fail as `room_ambiguous`; unavailable enabled hosts make global resolution incomplete rather than silently picking a first match.

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

Apply once per endpoint. Parse allocated remote ports; for local ports retry a bind race at most three times. Probe from the participant host, then join and verify room ID. If the second forward or Join fails with a known outcome, cancel newly acquired forwards and leave no orphan participant. Retain claims/resources if mutation is uncertain until explicit recovery.

Reuse/ref-count routes for a room plus participant machine/Herdr session. Release owned routes on rollback, confirmed inactive bindings, coordinator shutdown, or plugin disable. Existing `parley leave`/`stop` remain available; coordinator status/reconciliation checks known bindings for stale leases without discovering rooms. Detaching the UI leaves the coordinator/masters running. Never stop existing agents or unrelated tunnels.

## Required Parley change

Add `parley join --artifact-endpoint <http-url>` and pass it through public Join, hidden participant daemon, daemon argv, and adapter initialization. Otherwise the adapter uses the room's original artifact port, which may not be the forwarded port.

Keep existing derivation when the flag is absent. Validate absolute HTTP, host/numeric port, no credentials/query/fragment, and exactly `/rooms/<room-id>/artifacts`. Supply loopback URLs from the coordinator; retain original server endpoint metadata.

## Trusted boundaries and failures

Accept fixed structured Start/Join/status requests with random IDs; internal rollback may call existing leave. Reject unknown fields/operations, malformed or oversized JSON, unsafe IDs, forged origin, and corrupt records. Origin comes from the local socket or the SSH-managed host/session from which the coordinator fetched a request.

Resolve the trusted installed executable and session socket from the plugin registry/probe. Quote only that executable for the remote shell; send user fields as stdin JSON. The helper derives state paths internally and exposes only integration records. No callback authentication protocol is needed.

Claim a request before any route/participant mutation. Same ID/body replays the outcome; changed body is `request_conflict`. Journal host-local CLI intent before mutation and persist results before returning them. Use a reserved participant name and existing runtime session records to reconcile a lost Join response. If the state cannot prove the outcome, return `operation_uncertain`, never auto-create another participant.

A dead event relay reconnects and rereads the queue. A dead route is reported `route_unavailable`; an offline adapter is `participant_disconnected`. This phase does not promise adapter auto-resume, complete unread replay, or transparent collaboration across tunnel outages. Explicit recovery must not silently discard a participant's existing state.

## Verification boundary and acceptance

The prior 13-check fixture used two isolated macOS Herdr 0.9.3 servers over loopback OpenSSH. It proved metadata reads, remote plugin state writes, restart persistence/startup hooks, outbound request/result exchange without a callback, and actual Parley messages/direct HTTP artifact bytes over both `-L` and `-R`. It did not prove the reactive event relay, Linux/user SSH policies, complete claims/orchestration, supervision, or the CLI artifact override.

Before dependent implementation, prove a remote action's workspace marker reaches a plugin-owned local subscriber and that pending requests survive subscription reconnect. Record `events_lost` framing/recovery and ephemeral metadata fallback. No room scan or route may occur at quiet startup.

Acceptance: Start on either host; Join an existing opposite-host pane by room ID; create both correct loopback forwards automatically; exchange a message and fetch artifact bytes through remapped ports; handle duplicate requests and partial failures without duplicate participants/orphan forwards; survive UI detach; and clean up only owned routes. Message exchange is a transport check, not a new hook feature.

## Deferred work

Message send/receive hooks, automatic prompting, readiness gating, notification workers, participant resume/unread replay, automatic adapter recovery after a tunnel outage, new-agent launch, different-remote-host relay, hosted discovery, concurrent controllers, and Windows support.

## Sources

- [Herdr plugins](https://herdr.dev/docs/plugins/) and [connecting machines](https://herdr.dev/docs/connecting-machines/): host-local actions, startup and plugin state.
- [Herdr socket API](https://herdr.dev/docs/socket-api/): metadata persistence/limits, API-only workspace events, subscriptions and `events_lost` recovery.
- [Herdr CLI](https://herdr.dev/docs/cli-reference/): saved profiles and remote metadata reads.
- [OpenSSH ssh(1)](https://man.openbsd.org/ssh) and [ssh_config(5)](https://man.openbsd.org/ssh_config): forwarding, owned masters and bind/failure behavior.
- Repository: `cmd/parley/join.go`, `daemon_commands.go`, `daemon_process.go`, `participant_daemon.go`, `participant_artifacts.go`; `internal/runtime/runtime.go` for existing session reconciliation.
