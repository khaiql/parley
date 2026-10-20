# Herdr Idle Message Fallback Implementation Plan

> For agentic workers: implement inline with `superpowers:executing-plans`, test-first, followed by an independent whole-change review. The user explicitly requested both planning and implementation; no additional implementation approval is required. Numbered steps are the execution order, not a separate task tracker.

**Goal:** Remind a verified existing agent to consume its Parley inbox after 30 seconds of continuous readiness and unread messages, without trusting stale pane locations after disconnect or reboot.

**Architecture:** Extend the existing coordinator and authenticated fixed helper. Enrollment records an authoritative native conversation independently of routing identity. A host worker combines a readiness subscription, non-consuming participant waits, bounded pending-inbox reconciliation, and a durable recipient submission journal.

**Tech stack:** Go 1.26.1, standard library, existing Cobra and Herdr socket/SSH helpers. No new production dependencies.

**Spec:** `docs/superpowers/specs/2026-10-20-herdr-idle-message-fallback-design.md`.

## Global constraints

- Work on `codex/herdr-idle-message-fallback`; preserve main and installed plugins/agents.
- Base implementation on merged `origin/main` (`2a25e93`); original spec branch lacked plugin code.
- Herdr 0.9.3 or newer; verify remote helper protocol/capabilities. Version the helper extension.
- Agent consumes inbox. Plugin never acknowledges messages, starts agents, rejoins participants, or restores missed room traffic.
- Readiness grace is 30 seconds; reconcile pending work every five seconds. Empty inboxes use bounded Parley waits instead of a spin loop.
- Native conversation identifies recipient. Terminal/pane identifies its verified current location. Missing or ambiguous identity suspends delivery.
- Invalidate observation generation on disconnect. Restart grace after reconnect and ignore stale callbacks.
- Accepted/uncertain attempts suppress resubmission until covered messages are consumed or retry is explicitly requested.
- Existing Herdr API retains a final verification/submission race; do not claim atomic guarded delivery.
- No beads database exists in this checkout. Do not initialize unrelated repository infrastructure; retain execution evidence in `/private/tmp` and commits.

## Review focus

- Reused pane with another conversation must receive no fallback input, including after SSH reconnect.
- A verified original conversation in a new pane can be located without rewriting routing binding identity.
- Interrupted submissions must survive restart as uncertain and cannot be blindly resent.
- Several rooms for one conversation must share a submission and durable coverage record.
- Agent inbox consumption during grace, host disconnection, missing enrollment, and partial cursor progress must not generate repeated reminders or alter unread state.

## Files and responsibilities

| File | Responsibility |
| --- | --- |
| `internal/herdr/notification_identity.go`, `_test.go` | Native conversation parsing, unique fresh resolution, binding enrollment and participant-host observations |
| `internal/herdr/notification_store.go`, `_test.go` | Atomic authoritative attempt journal, recovery, explicit retry and status |
| `internal/herdr/notifications.go`, `_test.go` | Monotonic scheduling, batching, suppression and host worker |
| `internal/herdr/notification_transport.go`, `_test.go` | Fixed local/remote operations, live helper-side checks and readiness relay |
| `internal/herdr/peer.go`, `client.go`, `events.go`, `lifecycle.go`, `handoff.go` | Authenticated dispatch, helper version, shared stream parsing, coordinator lifecycle and Start/Join enrollment |
| `cmd/parley/herdr.go`, `herdr_rpc.go`, relevant tests | Notification diagnostics and explicit enrollment/retry commands; fixed stream dispatch |
| `docs/herdr-plugin.md`, design spec | Workflow, upgrade, unsupported recovery and proof limits |

## Task 1: Verified recipients and observations

**Interfaces:** `NativeConversation` contains source, agent, kind and value. `NotificationEnrollment` pins the original `Binding` identity and native conversation. `HostOperations.EnrollNotifications(ctx, binding)` records it once; `HostOperations.NotificationBindings(ctx)` lists only local validated plugin bindings. `HostOperations.ObserveNotification(ctx, key)` returns current verified native agent, readiness sequence, participant connectivity, seen cursor and eligible unread range.

1. Write tests using a real temporary Herdr socket and real participant stores. Cover missing references, duplicate native references, replacement in same pane, original conversation moved to another terminal, legacy bindings, own/lifecycle events and file-only messages. Test that enrollment does not overwrite an existing identity.
2. Run `go test ./internal/herdr -run 'TestNotificationIdentity|TestNotificationObservation|TestNotificationEnrollment' -count=1`; observe missing feature failures.
3. Implement native resolution from authoritative `agent.list` and `agent.get`, validating the pinned host/server and exact native reference. Preserve original routing key; keep enrollment separate from routing participation. Read only the original participant store and require a live adapter socket.
4. Integrate best-effort enrollment in successful Start/Join handoff, retaining room success if identity is unavailable. Signal existing workspace invalidation after durable enrollment; report fallback availability in form/status.
5. Rerun focused tests and existing handoff tests. Commit the tested identity layer.

## Task 2: Scheduling and authoritative submission journal

**Interfaces:** `NotificationAttempt` holds attempt ID, recipient identity, covered binding keys/seen/through sequences, outcome and optional predecessor ID. Store methods atomically prepare/read/update attempts, reconcile consumption, and request explicit retry. A `notificationSchedule` observes monotonic times and returns due eligible observations; it never owns terminal input.

1. Add deterministic clock tests for 30-second ready/unread grace, busy/blocked/unknown cancellation, done/idle preservation, burst batching, cursor consumption, outstanding suppression, connection-generation reset and obsolete results.
2. Add store tests for prepared-as-uncertain restart, accepted replay, partial/full consumption, cursor regression, corrupt records, shared room coverage and durable replacement-before-supersede retry recovery.
3. Run `go test ./internal/herdr -run 'TestNotificationSchedule|TestNotificationAttempt' -count=1`; verify failures before implementation.
4. Implement private atomic journals under `notifications/attempts/`. Treat journal outcomes as authority and per-binding status as derived. Serialize same-recipient operations and keep ambiguous failures unresolved. Explicit retry names the original attempt and retains predecessor evidence.
5. Rerun focused tests and commit the tested state machine/store.

## Task 3: Authenticated delivery and coordinator lifecycle

**Interfaces:** Fixed helper operations enumerate enrolled bindings, observe/wait on one binding, relay readiness and submit a fixed reminder for a bounded list of original enrolled bindings. Request payloads contain identifiers, never arbitrary text, commands, sockets or paths. The notification host worker owns cancellation/generation and uses existing local or remote transport.

1. Add tests for strict helper-field validation and ownership, fresh helper-side native identity verification, same-conversation batching, empty-inbox races, rejected versus uncertain acknowledgements, cancellation, reconnect and stale-generation suppression.
2. Run `go test ./internal/herdr ./cmd/parley -run 'TestNotificationTransport|TestNotificationWorker|TestHerdrNotification' -count=1`; verify failures.
3. Implement bounded operations outside the controller-file lock after ownership verification. Helper-generated reminders contain only trusted explicit-session inbox instructions. Validate `agent_prompted` acknowledgement and target; preserve ambiguity when any input may have been submitted.
4. Add a readiness stream built from freshly resolved enrolled recipients. Herdr requires a pane ID for each `pane.agent_status_changed` subscription; subscribe to each current pane and topology/metadata invalidations. Rebuild subscriptions after enrollment or topology changes. Reuse stream framing/keepalives and transport cancellation.
5. Wire one worker per configured host/session into the coordinator. Subscribe before observations; run one non-consuming bounded wait per empty binding; reconcile only pending bindings every five seconds and at deadlines. A stream loss cancels worker generation before reconnect. No cross-host clock comparison or remote scheduling worker.
6. Add explicit enrollment and uncertain-attempt retry commands plus diagnostic status. Document automatic registration for new Start/Join and conservative behavior for legacy/missing native references.
7. Run focused and existing event/RPC/lifecycle suites; commit the complete path.

## Task 4: Integration proof, review and delivery

1. Exercise real Parley adapters and room transport against a temporary Herdr API fixture: normal listener consumes before grace; missing listener gets one reminder; no cursor mutation; own/lifecycle events ignored; replacement/reconnect/reboot-shaped restoration sends no input; moved original conversation resolves only with online original participation.
2. Run `go build ./...`, repository lint command, and `go test ./... -timeout 30s -race`. Record and investigate baseline failures rather than masking them. Baseline observed `TestHerdrRPCEventRestart` and `TestHerdrRPCCleanupLeavesParticipantAndStopsRoom`; isolated reruns passed before feature code.
3. Dispatch one fresh code reviewer against the feature delta, plan and spec. Fix material findings with regression proof and rerun affected gates.
4. Document live-proof limits. Do not install a new plugin, restart existing coordinators, submit to user agents, reboot hosts or claim real remote/reboot acceptance without running that proof in a disposable environment.
5. Commit on the feature branch and push it as repository instructions require. Do not merge main or publish a release.

## Stop condition

The fallback path, identity safeguards, durable recovery, lifecycle integration and diagnostics are implemented and exercised; build, lint and race suite pass; independent review has no unresolved material findings. Real remote/harness/reboot coverage that cannot be run is explicitly reported as unverified, not represented by fake fixtures.
