# Herdr idle fallback for Parley messages

Status: source implementation complete and independently reviewed on `codex/herdr-idle-message-fallback`, 2026-10-20. Automated build, lint, race, installer, and disposable-adapter checks pass. A [disposable real Herdr/OpenSSH suite](../../herdr-e2e.md) now provides executable transport, identity and process-restart checks; see its execution evidence and limits. Physical host reboot remains unverified. This follows the [Herdr automatic SSH routing design](2026-10-12-herdr-plugin-design.md). The routing plugin now exists in the base implementation; this document defines its separate receive-notification follow-up.

Revised after the disconnect and host-reboot discussion: a restored connection or surviving pane never establishes recipient identity. Native conversation identity must be freshly verified before fallback delivery can resume.

## Intended outcome

The agent owns normal Parley listening, inbox reads, and room replies. The Herdr plugin supplements that workflow when unread room messages remain while the agent is ready for input. A missing or forgotten agent listener must not require the human to notice and restart it.

The plugin waits through a grace period, then submits a fixed reminder telling the existing agent to read its Parley inbox. It does not inject room message bodies or impersonate their authors. The agent reads the original messages through Parley and responds through its normal workflow.

Delivery does not pause because a human has focused the pane. Herdr `idle` and `done` are both ready states. `blocked`, `working`, and `unknown` are not eligible.

## Scope and design decisions

Agreed behavior:

- Agent listening remains primary; plugin delivery is a fallback.
- Use readiness events and a locally measured grace period rather than requiring a Herdr `ready_since` field.
- Preserve inbox ownership: only the agent consumes messages.
- Bind delivery to the existing native conversation, not whichever agent happens to occupy a pane later.
- Invalidate the live pane mapping on disconnect. Reconnection must locate and verify the original conversation again, without assuming it remains in the same pane.
- Suspend delivery when the original agent is gone, replaced, or unverifiable. Do not send room input to a restored shell or a different conversation.
- Support local and remote participants through the existing local-coordinator architecture.
- Ignore pane focus when deciding whether to deliver.

The following are proposed implementation defaults, not previously agreed timing guarantees:

- A 30-second grace period after both readiness and unread work have been observed.
- A five-second inbox reconciliation interval only while a binding has pending unread work or an outstanding notification.
- One outstanding notification per binding, with submissions serialized and eligible bindings batched per native conversation.
- Restart grace after process restart, subscription loss, or any uncertainty about continuous readiness.

These are initial constants, not new user settings. This design changes no room protocol, public Parley command semantics, artifact transport, participant identity, or agent configuration. It does not launch agents, detect listener processes, answer approval dialogs, automatically reconnect participant adapters, or promise exactly-once prompt processing.

## Verified Herdr capabilities

The installed Herdr 0.9.3 schema exposes:

- `agent.get`: `agent_status`, `state_change_seq`, `completion_seq`, `terminal_id`, and optional native `agent_session`.
- `events.subscribe` with `pane.agent_status_changed`; the event carries the pane, workspace, and current status.
- `agent.wait` for exact status sets and `agent.prompt` for submitting input to an existing agent.

Neither the agent record nor the status event provides a readiness transition timestamp. `state_change_seq` and `completion_seq` are sequence values, not timestamps or Parley message sequences. The coordinator must measure elapsed readiness itself.

`agent.prompt --wait` submits first and waits afterward. It is not a wait-until-idle delivery primitive. A prompt can be submitted while an agent is working; an already blocked agent is rejected. The current request has no expected-terminal, expected-native-conversation, or idle-only guard.

These facts were checked against the installed schema and published documentation. Live event delivery and end-to-end notification behavior are exercised by the disposable suite linked above, with the acceptance-to-test mapping recorded there. Activation must verify the selected running servers support the required fields and subscription, rather than trusting only the local CLI version.

Existing terminal-and-agent-kind checks are insufficient for this feature. The separately inspected Parley plugin implementation does not use native conversation identity as participation authority. Fallback delivery therefore needs its own verified native recipient identity; it cannot treat existing binding validation or the presence of a legacy native-session storage field as proof. A binding without a freshly verified native reference remains valid for its existing room workflow but is ineligible for automatic reminders. This requirement does not claim that the currently installed plugin already enforces it.

## Ownership and placement

Use the local coordinator described by the routing design. One coordinator owns fallback state and submission scheduling for its configured Herdr server session and managed remote sessions. Reuse the existing controller ownership rules; do not introduce a second independent controller.

On a managed host, serialize notification input with controller release and claim under the existing ownership lock. A request that authenticated before release cannot submit input after release completes. Read-only observations and bounded waits do not retain that lock.

| Data or work | Owner and location |
| --- | --- |
| Room transcript and room sequence | Room server on the room host |
| Participant mirror, received cursor, seen cursor | Existing Parley adapter on the participant host |
| Agent status and native identity | Herdr server on the agent host |
| Binding, notification journal, grace timers | Local coordinator |
| Remote reads, waits, and prompt submission | Fixed helper on the participant host, invoked over coordinator-owned outbound SSH |

There is no remote notification scheduler or duplicate notification journal. A remote helper may serve a bounded wait or event stream, but does not decide when to prompt. All elapsed-time calculations use the local coordinator's monotonic clock; remote clocks need not agree.

The local coordinator must be running for fallback delivery. If it stops, agent-owned listening can continue and connected adapters continue retaining messages. After recovery, the coordinator inspects existing participant state and starts a fresh grace period. This does not repair a disconnected adapter or recover messages it never received.

Herdr startup hooks are not supervised daemons. The routing coordinator's launch, ownership, shutdown, and recovery mechanism is a prerequisite, not a capability supplied automatically by Herdr. Do not implement this feature as a blocking startup hook or assume linking/enabling a plugin starts its worker.

## Stable bindings

Only participants with a validated plugin Start/Join binding are eligible. Do not discover arbitrary Parley sessions or attach to unrelated panes. Start/Join must make the automatic room reminder behavior visible as part of the plugin workflow.

A binding contains:

- The participant machine identity and canonical Herdr server session key.
- Agent kind and verified native conversation reference, including its source, reference kind, and value.
- Room ID, Parley participant session ID, and participant name.
- The terminal ID recorded at enrollment, retained for audit and existing binding-key compatibility.
- A separately resolved live terminal ID and pane ID as refreshable delivery addresses, valid only for the current verified connection.

Keep the routing design's original host-local binding key as the storage handle and normalize saved-profile aliases to that identity. Do not recompute that key or rewrite routing participation when a verified conversation appears in a different terminal. Notification recipient identity is the participant machine, canonical Herdr server session, agent kind, and native conversation reference; a terminal or pane is its current location. Room message sequences are scoped to the room and participant cursors to the original participant session. A new native conversation or Parley session creates a different binding; it never inherits notification state.

Before submission, resolve the original native conversation from authoritative Herdr state on the pinned participant host/server and require exactly one matching live agent. Verify its agent kind and native reference, then resolve its current terminal and pane. A cached pane may be a lookup hint only while the connection is verified; its occupant must still pass the same checks. Pane moves or a genuinely resumed original conversation can update the delivery address without changing recipient identity. Missing native identity, a different conversation, ambiguous resolution, or an unavailable server suspends delivery. Never fall back to the focused pane, a pane with the old ID, another agent of the same kind, or a newly created participant.

### Registering existing bindings

Start and direct same-host Join can commit bindings without passing through the routing coordinator. They must therefore signal binding changes after the durable host-local binding transaction succeeds: local IPC for the local server, or a fresh random `parley_binding` workspace metadata marker for a managed remote server. Subscribe to those invalidations before reconciling bindings. Markers contain no message data or identity authority and are not the registry.

Add a fixed helper `bindings` operation that lists validated plugin binding records in the pinned host/session namespace. Reconcile this list on activation, binding invalidation, subscription reconnect or `events_lost`, and explicit recovery/status refresh. Normalize host-local identities through the established managed peer. A signal triggers a read of all binding records in that session, so coalesced markers cannot hide registrations. Removed or inactive records cancel their watchers.

This enumerates existing plugin bindings, not rooms or unrelated Parley sessions; it performs no room discovery, route creation, or new Join. Existing routing-phase quiet-startup guarantees continue to apply to room discovery and forwarding. A failed signal retains the binding and reports fallback registration as pending; a later signal, reconnect, or explicit refresh recovers it. Start/Join must distinguish a valid room participation from unavailable fallback when no local coordinator is configured or reachable.

## Per-binding state

Persist attempts under `notifications/attempts/`, cursor high-water records under `notifications/cursors/`, and derived host diagnostics under `notifications/status/` within the local coordinator's session namespace. The participant host keeps its immutable native enrollment under `notifications/enrollments/`; it does not run a second scheduler or maintain a competing attempt journal. Reuse the routing design's private directories, locked atomic writes, schema validation, and controller ownership. Do not mix records from different Herdr sessions.

Validate the complete attempt archive on recovery, new enrollment and submission. Between those boundaries, retain accepted high-water marks and only outstanding intents for active bindings. Periodic reconciliation rereads mutable outstanding intents to observe explicit CLI retries and late acknowledgements from a canceled generation; it must not reopen every historical attempt on every five-second tick. Consumption and supersession remove intents from this active view without deleting their durable records.

Durable state contains:

- Binding identity and record version.
- Last observed Parley seen cursor, for diagnostics and reconciliation.
- `last_notified_seq`, the highest eligible message sequence covered by a successfully accepted reminder; initially absent.
- At most one pending attempt: random attempt ID, the seen cursor at preparation, covered-through message sequence, authorized readiness sequence and current terminal/pane, and outcome (`prepared`, `accepted`, `uncertain`, or definitely rejected).
- A bounded diagnostic reason when delivery is suspended or requires explicit recovery.

The coordinator also keeps ephemeral state:

- Current verified agent status, identity, and Herdr state sequence.
- A connection generation for each managed host/server and the generation in which each live delivery address was verified.
- `ready_since`, measured with a monotonic clock.
- `unread_since`, measured with the same clock.
- Current pending message range and next deadline.

Never persist monotonic timestamps as restartable deadlines. Wall-clock times may be written for diagnostics but do not authorize delivery after restart. Herdr sequence values, Parley seen cursors, and notified message sequences are distinct fields and must never be compared with each other.

## Observing readiness

Keep a `pane.agent_status_changed` subscription for bound panes, plus the lifecycle signals needed to invalidate moved, exited, or replaced targets. The local coordinator receives remote subscriptions through the existing outbound SSH event relay. Do not assume a native subscription event also invokes a manifest event hook.

Subscribe before reading authoritative agent state. Continue draining events while a separate request reads state. Events invalidate cached state; they are not an unconditional replay onto the snapshot. Serialize refreshes and refresh again when events arrive during a read.

On a verified transition from a non-ready state to `idle` or `done`, record `ready_since = now`. An `idle`/`done` presentation change with the same or exactly adjacent state sequence does not restart grace. Leaving the ready set cancels the delivery deadline. A sequence gap, regression, or same-status sequence change restarts grace conservatively because intervening activity cannot be excluded.

At startup, an already-ready agent starts grace at observation time; the plugin cannot recover its earlier idle duration. Reconnect, `events_lost`, server replacement, or native identity uncertainty invalidate readiness history. Resubscribe and reconcile before starting a fresh timer. Do not preserve readiness across an outage merely because the final status is still idle.

Tag subscriptions, asynchronous state/inbox reads, delivery addresses, and timers with their connection generation. Advance that generation when continuity is lost, and ignore obsolete callbacks when scheduling delivery or updating live observations. A queued timer from before disconnect must never become eligible merely because a new connection is online. A previously prepared or submitted notification is still durable work: preserve and reconcile its outcome rather than discarding it as a stale callback.

The implementation represents a generation with a distinct cancellable context and isolated result channels. It cancels and drains the old generation before starting another. At submission, the helper compares the freshly observed Herdr state sequence and terminal/pane with the readiness snapshot authorized by the scheduler. An intervening working/idle cycle or relocation rejects submission and restarts grace rather than reusing the old deadline.

## Observing unread messages

Reuse existing non-consuming `parley wait --session <id>` and `inbox --peek --session <id>` behavior on the participant host. Do not create another participant or read another participant's cursor. A fixed helper may return a validated summary derived from existing adapter metadata and peeked events: participant identity/status, seen cursor, and pending eligible message sequences. It never advances the cursor.

An eligible event is a `message` from another participant. File-only messages qualify. Own messages, joins, leaves, and room lifecycle records do not independently trigger reminders. Do not use the highest room event sequence as the highest pending message sequence.

When no eligible unread messages exist, hold one bounded non-consuming wait per binding. After a normal timeout, revalidate the binding and rearm the wait. After `ready`, reconcile the inbox; if messages remain, stop reissuing waits and mark the binding pending. An agent consuming messages between wait completion and the peek is normal and requires no notification.

While pending, reconcile at most every five seconds and at delivery deadlines. This detects consumption by the normal listener. New arrivals can be included in the next reconciliation; they do not need another concurrent wait. Once the pending inbox is empty and no attempt needs reconciliation, return to the blocking-wait path.

This hybrid is deliberate: readiness uses Herdr events; message arrival uses existing Parley waits; cursor progress while unread work remains uses bounded reconciliation. Existing `wait` returns immediately for an already-unread message, so blindly rearming it would spin. No new public watch API is required.

On room closure, participant leave, or confirmed adapter disconnection, cancel delivery and report that condition. An empty file-backed peek alone does not prove the adapter is connected. Recovery uses the same participant session and must not reset unread state.

## Grace and eligibility

When eligible unread messages are first observed, set `unread_since = now`. Further arrivals do not restart that timer. If the inbox becomes empty, clear it. After the agent finishes consuming a previously notified batch, start a fresh unread grace period for any remaining messages not covered by that batch.

A binding with no unresolved notification becomes eligible at:

```text
max(ready_since, unread_since) + 30 seconds
```

Both timestamps must be present, identity must still match, and the participant must be connected. A long-idle agent therefore still gives its normal listener 30 seconds to collect a newly observed message. A message waiting through active work gets a new readiness grace period when the agent settles.

At the deadline, reread current agent identity/status and the pending inbox. If they no longer qualify, cancel or recompute the deadline. Human focus neither resets the timer nor prevents delivery.

## Submission and acknowledgement

Serialize submissions per native conversation, including when it has bindings to several rooms. Batch all bindings currently eligible for that conversation into one fixed reminder. Include validated room/session identifiers and the existing explicit-session inbox instructions. Do not include room message text, artifact bytes, arbitrary room-supplied commands, or shell interpolation.

For each covered binding:

1. Reconcile the inbox and select the highest eligible pending message sequence `N`.
2. Persist a `prepared` attempt containing the current seen cursor and `through_seq = N` before submitting any input.
3. Require the current connection generation, revalidate the original conversation and readiness, and resolve its current delivery address immediately before `agent.prompt`. The participant-host helper must perform these identity checks against live local Herdr state as well; it must not blindly submit to a pane address captured by the coordinator before SSH disconnected. Use structured arguments through the pinned host helper, not raw pane input.
4. If submission succeeds, persist `accepted` and update `last_notified_seq = N`.
5. Let the agent read `parley inbox --session <id>` and respond normally. Never consume the inbox in the coordinator.

The reminder states that messages may already have been read by the time the agent sees it. An empty inbox then requires no room response. A reminder is not authorization to perform arbitrary room-requested actions.

An accepted attempt stays outstanding until the participant's seen cursor reaches or passes its `through_seq`. While it is outstanding, suppress further reminders for that binding, including for newer messages. Once the cursor covers the batch, clear the attempt and reevaluate any later pending messages. Partial cursor progress is visible but does not count as acknowledgement of the whole batch.

Outstanding lookup is scoped by participant machine, canonical Herdr server session, original binding key, and Parley participant session. A host-local binding key alone cannot identify notification state across machines.

`last_notified_seq` describes notification coverage, not message consumption. A successful prompt acknowledges terminal submission, not execution or understanding. The authoritative seen cursor means the inbox was consumed, not that the agent completed the requested work. No component infers task completion from these values.

For every submission, write one authoritative recipient attempt to `notifications/attempts/<attempt-id>.json`, containing every covered binding and its cursor/range. Atomically replace this record to publish its outcome. Binding references and `last_notified_seq` are derived caches: recovery rebuilds them from attempt records before scheduling delivery. This avoids requiring an atomic transaction across several binding files. If a notification covers several rooms, recovery must retain that shared submission rather than send independent retries after a partial cache write. Keep this representation within the existing coordinator store, rather than adding a separate service.

## Failures and recovery

- A definitely rejected submission that wrote no input, such as an already-blocked rejection, does not advance `last_notified_seq`. Refresh state and wait for a new eligible period before another attempt; do not retry in a tight loop.
- Transport failure, timeout, coordinator crash after preparation, or failure to persist an accepted result is an uncertain submission. Retain its journal and do not blindly retry. If the seen cursor covers the recorded batch, resolve it as consumed without claiming prompt success. Otherwise expose the attempt through plugin status and require an explicit retry. There is no dismiss operation that silently makes the same unread range eligible again. A retry warns that the previous prompt may already have arrived, restarts grace, and repeats all normal checks.
- An explicit retry prepares one replacement recipient attempt with a reference to its predecessor and current unread coverage. Persist the replacement before marking the predecessor superseded. Recovery recognizes that reference even if the predecessor cache was not updated; only the replacement may proceed. Preserve the predecessor's uncertain outcome for diagnostics. Reject another concurrent retry of the same attempt. Command or action spelling is a builder choice, but retry must name the recipient attempt ID and remain scoped to its original validated bindings.
- A shared retry retains every predecessor binding's coverage, including temporarily unavailable rooms. It must not globally supersede a shared intent with a partial record that releases another room's suppression. The helper revalidates the retained coverage before input; an unavailable participant can therefore suspend the shared retry.
- A definitely rejected replacement retains the predecessor's delivery uncertainty and shared suppression. It does not automatically retry. A further explicit retry names that replacement attempt, preserves the entire coverage chain, and restarts grace. Ordinary first-attempt rejections have no uncertain predecessor and can be reconsidered automatically after fresh grace.
- Keep accepted and uncertain attempt records across restart. Reconcile them before enabling any new delivery. Reconstruct readiness and unread grace from fresh observations.
- Corrupt records, unexpected cursor regression, or a changed participant session suspend that binding. Do not reset cursors, discard journals, or rejoin automatically.
- Loss of a remote subscription or helper connection cancels deadlines for affected bindings. Other hosts continue normally. Recovery revalidates ownership, session identity, participant connectivity, and current state.
- Leave, room stop, plugin disable, and coordinator shutdown cancel owned waits, relays, and timers. Retain unresolved submission outcomes for diagnosis; stop issuing prompts. Cleanup must not stop agents or unrelated tunnels.

### SSH reconnect and host reboot

Herdr may reconnect automatically once a remote machine boots. That restores reachability, not proof that the original agent or Parley adapter survived. A network-only interruption may leave those processes running; a host reboot ends them. A restored workspace, tab, pane, or terminal display must never substitute for native conversation verification.

Treat Herdr observation transport, the Parley room tunnel, and an in-flight prompt request as separate failure boundaries:

| Boundary | Required handling |
| --- | --- |
| Herdr subscription or helper disconnect | Invalidate live delivery addresses and readiness, advance connection generation, and suspend affected delivery. |
| Parley room tunnel disconnect | Verify the original participant's adapter connectivity independently. Restoring SSH does not establish room participation or replay missing messages. |
| Prompt submission interrupted | Preserve an uncertain attempt unless the outcome proves input was rejected or accepted. Closing SSH does not prove the remote helper stopped or that input was not written. |

After reconnect, subscribe first and reconcile authoritative state. Search only the pinned host/server for the original verified native conversation. If its original pane now contains a shell or a different conversation, do not send input there. If the original conversation is uniquely present elsewhere, verify its live terminal/pane and the original Parley participant session before installing a new delivery address. Start fresh grace only after all checks succeed.

If no matching conversation exists, expose `recipient_missing`; if identity cannot be established or several agents match, expose a suspended identity diagnostic. Keep notification journals and participant cursors. Do not start an agent, adopt a replacement conversation, or retry old room instructions using the old pane. Explicit enrollment of a different conversation is a new binding and cannot inherit the predecessor's pending attempts or notification coverage.

If Herdr resumes the original conversation but the Parley adapter is disconnected, retain `participant_disconnected` and send no reminder until that original participation is verifiably online. Native agent resume does not restart its old background listener or establish participant recovery. Same-participant adapter restart, replay of messages missed during downtime, and boot-time room/coordinator restoration remain separate recovery work; this feature must not silently rejoin or reset unread state to simulate recovery.

These rules prevent delivery based on stale pane assumptions. They do not make identity verification and input submission atomic; the final API race described below remains an explicit limitation.

Current Herdr APIs leave a final race between identity/readiness checks and input submission. Another conversation or human action can intervene. Preflight checks reduce this risk but cannot remove it. A stronger guarantee requires a Herdr server-side prompt operation guarded by expected terminal, native conversation, and ready status. That upstream enhancement is deferred and must not be claimed by this implementation.

## Required integration changes

Extend the proposed routing coordinator and fixed helper, rather than building a second worker. Add typed operations for known-binding enumeration/registration, binding-scoped readiness reads/subscriptions, bounded Parley wait, non-consuming inbox summary, and fixed reminder submission. The coordinator constructs the reminder; the helper validates allowed bindings and reconstructs permitted commands from trusted session data. Do not expose arbitrary shell commands, state roots, socket overrides, or a general raw API proxy.

Remote waits and subscriptions must not monopolize the connection used for prompt submission or authoritative reads. Drain streams independently, use bounded waits, and cancel them when a binding is removed. Maintain one active watcher per binding and one readiness stream per managed Herdr session where supported.

Plugin status must show the bound target, participant session, current observation state, pending message sequence, outstanding coverage, and any suspended/uncertain outcome. Expose remaining grace only when timing continuity is valid. State explicitly that remote fallback depends on the local coordinator. Do not display a fabricated server-provided idle timestamp.

Enrollment and recovered delivery must read the native reference from fresh Herdr responses. Do not silently reuse terminal-only validation from the current plugin implementation, reconstruct conversation identity from a pane label, or assume a saved terminal ID names the original conversation. Legacy bindings without verifiable native identity stay ineligible until explicitly enrolled with a verified reference.

The source extension uses helper protocol 5 and fixed `notification-bindings`, `notification-events`, `notification-observe`, `notification-wait`, and `notification-prompt` operations. Enrollment is stored under `notifications/enrollments/<original-binding-key>.json`; original routing keys and participation remain unchanged. Existing terminal cleanup must preserve an enrolled original conversation that is freshly verified in another terminal instead of leaving its participant or stopping its room solely because the old terminal disappeared.

The existing Parley skill continues to tell agents to listen and consume inboxes. Plugin operation must not depend on the agent starting a listener, and must not install or rewrite harness hooks or skills.

## Acceptance and verification

Use a fake monotonic clock for scheduling tests and deterministic adapters/helpers for failure boundaries. Verify:

1. A functioning agent listener consumes messages before grace; no prompt is sent.
2. A missing listener leaves a message unread; exactly one accepted reminder covers that batch after continuous readiness and unread grace.
3. A newly observed message for a long-idle agent still gets the full grace period. Bursts do not postpone delivery indefinitely.
4. Working, blocked, and unknown states cancel delivery. Confirmed done/idle presentation changes and pane focus do not restart or suppress it.
5. A status change, consumption, or identity replacement just before the final check prevents an obsolete submission. Tests do not claim to eliminate the remaining API race.
6. Own messages and lifecycle records never trigger reminders; file-only messages do. Notifications never advance the seen cursor.
7. Existing unread messages do not cause a busy wait loop. Pending reconciliation is bounded, and empty bindings return to non-consuming waits.
8. New messages during an outstanding attempt remain unread and do not generate repeated reminders. Full consumption releases suppression; later uncovered messages become eligible after fresh grace.
9. Two bindings for one conversation cannot submit concurrently; eligible rooms are batched and their shared attempt survives partial persistence failures.
10. A pane move retains the original conversation binding. Pane reuse, missing identity, native session change, and identical pane IDs on different hosts cannot retarget delivery. A terminal-only legacy binding cannot enable fallback without verified native enrollment.
11. Restart, `events_lost`, delayed invalidations, and disconnect invalidate timer continuity. Subscribe-then-reconcile recovery cannot immediately submit using stale readiness.
12. Crash before preparation, after preparation, during submission, and after terminal acceptance produce the documented accepted, rejected, or uncertain outcomes. No blind retry or cursor reset occurs.
13. Start and direct same-host Join register watchers even when they bypass the routing coordinator. Coalesced or failed markers recover from durable binding records without room discovery, another Join, or new forwards.
14. Explicit retry of an uncertain shared attempt retains its predecessor, cannot race with another retry, and recovers correctly between replacement persistence and predecessor update. No dismiss action causes an automatic resend.
15. Leave, stop, disable, and shutdown prevent new prompts and clean up only owned watchers/resources.
16. Disconnect followed by reconnect with the same pane ID but only a shell, or a different native conversation of the same agent kind, produces no input. A ready status or submitted historical handoff receipt cannot override that rejection.
17. The original native conversation appearing uniquely in a different pane/terminal is located through fresh state. No prompt targets its previous pane; delivery resumes only with a verified original participant session and fresh grace. Duplicate native references suspend delivery.
18. A delayed status event, inbox response, or delivery timer from a previous connection generation cannot reinstall a stale pane mapping or cause a prompt. Interrupted prepared submissions remain journaled and are not retried automatically.
19. A reboot restoring layout without an agent leaves delivery suspended. A reboot resuming the original conversation with an offline participant adapter also leaves delivery suspended; no replacement participant is created and no seen cursor changes.

Before claiming live support, prove same-host delivery and a local coordinator notifying an agent on a remote participant host through real Herdr/OpenSSH. Verify both local-room/remote-agent and remote-room/local-agent cases using the original participant sessions. Exercise agent-listener coexistence, a human-focused pane, native conversation replacement, remote disconnection, and process restart. Use a disposable remote environment for host reboot, restored-shell, and resumed-conversation cases; never reboot a user's working host for acceptance. If no such environment is available, mark reboot safety unverified. Record versions and any unforced loss/crash cases as unverified.

For implementation, run the repository's required build, lint, and race-test gates after focused tests pass. Documentation review alone proves none of the runtime acceptance conditions.

## Sources

- [Herdr socket API: event subscriptions and recovery](https://herdr.dev/docs/socket-api/#event-subscriptions).
- [Herdr agent automation: lifecycle states and prompt/wait semantics](https://herdr.dev/docs/agent-automation/#choose-the-control-surface).
- [Herdr CLI: agents](https://herdr.dev/docs/cli-reference/#agents).
- [Herdr plugin startup hooks](https://herdr.dev/docs/plugins/#startup-hooks).
- [Herdr remote reconnection and cached state](https://herdr.dev/docs/connecting-machines/).
- [Herdr session restore and native conversation resume](https://herdr.dev/docs/session-state/).
- Installed `herdr 0.9.3`, `herdr api schema --json`: `AgentInfo`, `AgentPromptParams`, and `PaneAgentStatusChangedEvent`.
- Repository: `internal/adapter/adapter.go`, `internal/adapter/control.go`, `cmd/parley/participant_commands.go`, and the linked routing design.
