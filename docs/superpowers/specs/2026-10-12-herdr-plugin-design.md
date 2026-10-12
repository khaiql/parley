# Parley Herdr plugin design

Status: proposed design accompanying the implementation plan. Review both documents before implementation. The user requested the plan directly after discussing this architecture; the transport proof below has not been run.

## Intended outcome

Create a named Parley room from an existing Herdr agent, get a room ID, and attach another existing agent using that ID. Start the server on the initiating agent's machine. Install Parley and its plugin/helper once on each participating machine. Joining should resolve machine identity, arrange SSH access, create the participant adapter, deliver its session instructions, and listen for messages without repeated human prompts.

The two cross-machine cases are a local room with a remote collaborator and a remote room with a local collaborator. Both SSH connections originate on the local machine. Closing the Herdr UI must not kill the helpers. Suspending either endpoint can interrupt connectivity; the feature does not promise collaboration with an unavailable room host.

## Constraints

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

## Architecture

Ship `herdr-plugin.toml` at the repository root. Build the existing binary to `bin/parley` during plugin installation. Add the `parley herdr` command family; keep integration code in `internal/herdr`. No change to Herdr itself is assumed.

There are three process roles:

1. **Action/form:** a short-lived command or a terminal popup on the selected Herdr server. Captures the original agent identity before opening a popup, then sends a structured room operation.
2. **Local broker:** a detached process started by explicit local setup and restored by the local plugin startup hook. Owns the room registry, pairings, SSH masters, and forwards. Periodically reconciles enabled saved machine profiles. It does not rely on the visible Herdr UI remaining open.
3. **Participant worker:** a detached process on each participant's machine. Owns message waiting, readiness checks, notification deduplication, and participant adapter recovery. It uses that participant's Herdr socket, not whichever machine the UI later selects.

Herdr plugin hooks execute on the selected server. A remote action therefore calls the local broker through a separate private SSH callback channel. The callback exposes only the Parley operation protocol, never the local Herdr API socket.

## Bootstrap and callback channel

Run `parley herdr setup` once from a local Herdr shell after installation. Record the local Herdr socket, plugin directories, binary paths, and a random controller ID. The broker reads `herdr machine list --json`; the documented implementation returns an array containing `id`, `label`, `target`, `session`, `enabled`, and `selected`.

For each enabled machine with the matching Parley helper installed, obtain its absolute helper path and session socket through the fixed remote `parley herdr rpc` entrypoint. Resolve that executable from the registered plugin root using Herdr's remote plugin listing; do not assume `bin/parley` is on the SSH PATH. Exchange structured JSON on stdin/stdout. Quote the trusted absolute executable path for the remote login shell; never interpolate request JSON into a shell command.

Create a per-controller private directory on each side, mode `0700`. Its short absolute path must fit both Unix socket and OpenSSH control-path limits. Local broker binds a per-peer Unix socket and starts an owned OpenSSH master with a reverse stream-local forward:

```text
ssh -M -S <private-control-path> -N
    -o BatchMode=yes -o ExitOnForwardFailure=yes
    -o ControlPersist=no -o StreamLocalBindMask=0177
    -o ServerAliveInterval=30 -o ServerAliveCountMax=3
    -R <remote-private-callback.sock>:<local-private-callback.sock>
    <saved-ssh-target>
```

The initial callback channel is created independently of any room. A pairing record on the remote session stores its controller ID, callback path, and a 256-bit random peer token in a mode `0600` file. The local broker derives the requesting machine/session from the listener and pairing; the request cannot choose a different SSH target. Constant-time token comparison and operation validation precede mutation. Duplicate setup is idempotent. An unreachable or incompatible machine is marked unavailable without stopping other machines.

An unconfigured startup hook exits without becoming a controller. Remote startup hooks preserve pairing state but never start a local broker. Pairing a second active controller is rejected rather than silently overwriting a callback destination. Host-key and authentication failures require normal SSH recovery; do not weaken checking or automatically install/update/restart Herdr.

The first implementation task proves this flow against a real Herdr remote server and OpenSSH. If it fails, update this design and the plan before building the dependent integration. Capability verification is not evidence that the proof already passed.

## Room identity and routing

The broker stores the existing Parley room ID, display name/topic, hosting machine/session, host participant session, protocol endpoint, artifact endpoint, and owning bindings. IDs are controller-scoped discovery keys, not global addresses. `start` runs on the initiating machine and registers returned endpoint metadata. Remote-created rooms register through the callback broker. Two rooms may have the same display name; IDs remain distinct.

| Host | Participant | Transport |
| --- | --- | --- |
| Local | Local | Direct loopback |
| Remote A | Remote A | Direct loopback |
| Local | Remote A | Local broker adds two `-R` forwards to A |
| Remote A | Local | Local broker adds two `-L` forwards from A |
| Remote A | Remote B | Reject with `unsupported_route` |

Use the SSH master owned by this plugin, not Herdr's shared master. Add forwards with `ssh -S <path> -O forward`. Remote forwards request port `0` and parse the assigned port returned by OpenSSH. Local forwards select an ephemeral local port, release the reservation, and attempt forwarding; retry a bind race at most three times. Keep actual allocated ports in the lease, not the requested zero port. The live proof must verify how the supported OpenSSH build cancels dynamically allocated forwards.

Acquire both forwards before joining. Probe protocol reachability and artifact HTTP connectivity, not just process liveness. On a partial failure, cancel the first forward and leave no participant behind. Reuse a lease for the same room and participant machine/session. Reference-count bindings; cancel only after the final binding leaves. Broker shutdown closes only its own masters.

An additive `parley join --artifact-endpoint <http-url>` override carries the remapped artifact port to the adapter. Without it, existing derivation remains unchanged. Accept only an absolute HTTP URL with a host and numeric port, no credentials/query/fragment, and exactly `/rooms/<room-id>/artifacts`. The broker supplies loopback URLs. Never rewrite room metadata to pretend a tunnel port is a server port.

## Action flow and trusted boundaries

Actions `parley.start`, `parley.join`, `parley.leave`, and `parley.stop` use a terminal popup for input and visible results. The action captures pane context before opening the popup; a popup has no agent pane identity of its own. Forms preserve terminal settings, support Escape cancellation, and show structured failures in plain language. Do not use a new TUI dependency.

Resolve the target using the captured Herdr session socket and `agent get`. Store machine ID, terminal ID, agent kind, and native agent session value when present. Re-resolve pane ID from terminal ID after pane moves. Existing Claude/Codex integrations provide native session identity; if it is absent, require it before enabling automatic prompting and show `agent_identity_required`. A pane ID or label alone is not a safe persistent binding.

Remote callback operations are `start`, `join`, `leave`, `stop`, and `status`. Every mutation includes a random request ID. Reject unknown fields, unknown operations, oversized requests, invalid IDs, mismatched agent identity, unregistered rooms, and stop requests from a non-owner. A token permits only operations originating from its paired machine/session. No request accepts a binary path, SSH target, shell text, environment overrides, or raw Herdr method.

Keep a durable request journal before invoking mutating subprocesses. A repeated request ID with the same body returns the stored outcome; a different body is `request_conflict`. If a crash makes a mutation's outcome uncertain, reconcile its recorded intent with existing Parley sessions and binding/room records. Do not blindly repeat `start`, `join`, or prompts. An irreconcilable result is `operation_uncertain` and visible for explicit recovery.

## Messages and recovery

The participant worker calls local `parley wait --session <id> --timeout 30s`. When unread external messages exist, it records the highest unread sequence and waits for the same agent to be `idle` or `done`. Recheck identity and state immediately before `agent prompt`. The prompt is a constant instruction containing only the validated session ID: read `inbox` and treat room messages as collaborator input. Do not paste room text as shell code or control instructions.

While working, blocked, or unknown, keep one pending notification. Native Herdr status hooks signal the local worker; also reconcile state every two seconds so missed hooks cannot strand work. A listener must detect arrival while the agent is already idle. `wait` and `inbox --peek` never advance the seen cursor. Only the agent consumes `inbox` or the existing `send` response. Avoid a tight loop when `wait` repeatedly returns the same unread batch.

After a successful prompt submission, persist the notified sequence. Repeated unchanged unread messages do not generate another prompt. If submission is ambiguous (timeout/crash), mark notification uncertain and show it in status; do not auto-submit the same batch again. New messages arriving during a turn may produce one subsequent notification after readiness. Removing/replacing the agent or changing its native conversation suspends delivery until explicit reattachment.

Add `parley resume --session <id>` with descriptor/artifact endpoint overrides. It reuses the saved participant name and session record, refuses a live adapter or a participant that explicitly left, and requests server replay after the persisted seen cursor. Add an optional `replay_after_seq` join field, absent for ordinary joins. Under the server join lock, replay all events after that sequence without the fresh-join 50-event limit. Resume never calls `MarkReceivedSeen`; duplicate events use existing adapter deduplication. The plugin requires compatible helpers on both endpoints before using replay.

When the broker restores forwards with new ports, the worker reads the current binding endpoint and resumes using it. Retry transport failures with exponential backoff from one to 30 seconds and cancellation on leave/stop; do not retry validation failures. An explicit room stop is terminal. An unavailable host is reported as unavailable; never silently create a replacement room. Keep room and binding records for recovery after broker restart. Host participant leave currently ends the room; preserve that behavior and make it clear in the form.

## Storage and lifecycle

Use `HERDR_PLUGIN_CONFIG_DIR` for controller/session pairing config and `HERDR_PLUGIN_STATE_DIR` for room records, bindings, request outcomes, and notification cursors. Namespace records by controller and Herdr session. Paths and directories are not automatically shared between hosts. Pin the same `PARLEY_STATE_DIR` used by the participant so worker and agent see the same adapter metadata.

Use locked, atomic file replacement like existing Parley runtime stores. Refuse symlinks and malformed records; never reset corrupt state to empty and lose ownership. Use socket handshakes and child-process handles instead of killing stored PIDs. Shutdown workers and forwarding leases on leave/stop. Detaching a TUI does not shut them down. Disabling the plugin suppresses further prompts and releases helper resources without stopping agents or unrelated rooms; detect disabled state during reconciliation. Explicit setup/pairing also works after plugin installation on an already-running Herdr server because startup hooks do not run on link/enable.

## Verification and completion criteria

Automated tests cover endpoint validation/remapping, replay beyond 50 events, lost response deduplication, identity replacement, both forwarding directions, partial acquisition rollback, and reader-only notification behavior. Real OpenSSH tests prove stream-local callback forwarding, dynamic remote port allocation/cancellation, two-port routing, and tunnel restoration. Real Herdr checks prove invoking join from a remote selected pane reaches the local helper and that closing the UI leaves processes running.

Acceptance: create a room on either endpoint; join an existing agent on the opposite endpoint by ID; exchange messages and fetch an artifact despite remapped ports; notify an already-idle agent once; defer while busy/blocked; recover a tunnel without losing unread events or changing session ID; leave/stop clean up only owned resources. Record missing credentials or tools as unverified, not as success.

## Deferred scope

No new-agent launch, remote-to-remote relay, hosted discovery service, multiple simultaneous local controllers, Windows transport, agent-specific mid-turn injection, or generic remote command runner. These are excluded from this first release.

## Sources and repository evidence

- [Herdr plugins](https://herdr.dev/docs/plugins/): executable manifests, startup lifecycle, popup context, config/state directories.
- [Herdr connecting machines](https://herdr.dev/docs/connecting-machines/): selected-server execution and server-scoped IDs.
- [Herdr CLI](https://herdr.dev/docs/cli-reference/): machine listing and remote API forwarding.
- [Herdr socket API](https://herdr.dev/docs/socket-api/): agent identity, subscriptions, authoritative reconciliation.
- [OpenSSH ssh(1)](https://man.openbsd.org/ssh): stream-local forwarding, multiplex forwarding, dynamic remote port allocation.
- [OpenSSH ssh_config(5)](https://man.openbsd.org/ssh_config): control paths, bind modes, failure and keepalive behavior.
- `cmd/parley/participant_artifacts.go`: artifact endpoint currently uses descriptor host and original artifact port.
- `cmd/parley/participant_daemon.go`: adapters exit on connection loss; fresh join marks snapshot seen.
- `internal/server/server.go`: fresh join history is limited to 50 events.
- `internal/adapter/adapter.go`: locked event deduplication and cursor-preserving metadata merge.
