# Disposable Herdr and OpenSSH end-to-end tests

Run the production Parley CLI, coordinator, helpers, room servers, participant adapters, SSH forwards and Herdr servers without linking anything into an existing installation:

```sh
python3 -u scripts/test-herdr-e2e.py
```

Requirements: Go matching `go.mod`, Python 3, Herdr 0.9.3, OpenSSH `ssh`, `sshd`, and `ssh-keygen`. The suite runs on a POSIX host that permits local Unix sockets, PTYs, process inspection and a loopback TCP listener. It does not require Docker, an SSH account on another machine, an LLM account, or a running Herdr session. Do not run it inside the default 30-second Go test gate: it deliberately exercises the unchanged 30-second production grace many times.

The script builds a private plugin bundle and starts two separate headless Herdr sessions with private configuration, plugin registry, state and runtime directories. The controller saves a test-only SSH profile. A real OpenSSH daemon listens only on `127.0.0.1` with generated host/client keys, an account allow-list and no password authentication. Its forced command selects the participant namespace. A small `ssh` wrapper supplies private identity and known-host settings, then executes the actual OpenSSH client with the production arguments. Strict host verification stays enabled. SSH server strict directory checking is disabled because `/tmp` is a shared parent; the generated authorized-key file and containing test directory remain private to the current user.

Two namespaces on one kernel simulate two hosts; they do not create network or PID namespaces. Local and reverse port forwarding, key authentication, SSH helper processes and terminal delivery are real. The fixture asserts different routed endpoints and verifies the receiving adapter's own mirrored sequence and downloaded artifact bytes, rather than trusting shared server history.

The foreground agent is a deterministic input recorder, not a model. It is detected by the real Herdr process detector, renders recognizable Claude lifecycle screens, and reports native conversation references through Herdr's public integration API. Codex-shaped unknown-state cases use the same recorder under a separate process name. Prompts travel through real `agent.prompt` calls into real PTYs. The recorder never executes the supplied inbox instructions; scenarios explicitly run normal Parley commands when testing consumption. This separates terminal delivery from an LLM choosing to read or answer.

## Running and diagnosing

The script prints its evidence directory and retains it on success or failure. Supply a **new, short** path to choose that directory:

```sh
python3 -u scripts/test-herdr-e2e.py --output /tmp/ph-check
python3 -u scripts/test-herdr-e2e.py --scenario remote-reminder
python3 -u scripts/test-herdr-e2e.py --scenario transport
python3 -u scripts/test-herdr-e2e.py --scenario eligibility
python3 -u scripts/test-herdr-e2e.py --scenario registration
python3 -u scripts/test-herdr-e2e.py --scenario identity
python3 -u scripts/test-herdr-e2e.py --scenario recovery
python3 -u scripts/test-herdr-e2e.py --scenario shutdown
```

Each selection creates its own controller, participant, keys, bundle and room. Do not reuse an evidence directory. The short path is necessary for production Unix-socket path limits. The output includes native API responses, production form transcripts, SSH server authentication/session logs, remote command execution logs, actual agent input, original room mirrors, and durable notification journals. Private test keys stay inside that directory; the CI artifact upload excludes them.

Negative scenarios wait for an authoritative watcher/participant observation before testing a full grace interval. They check actual input counts while requiring the coordinator and native servers to remain alive. A deliberately injected real prompt first proves that the negative assertion detects stray input. A local canary shares the remote recipient's pane ID and native conversation reference; it must receive no additional reminders throughout the suite. Positive scenarios require both recorded input and a new accepted durable attempt. Known 30-second connection backoff can add recovery time; observation barriers distinguish reconnection delay from the 30-second grace. Allow up to 45 minutes for the complete combined run. Run ordinary Go quality gates separately when host load is high.

A pass-through socket proxy forwards native requests and replies unchanged except at named fault boundaries. It can reject one registration invalidation, lose a reply only after native `agent_prompted` acceptance, or hold that acceptance while the coordinator is killed. Every injected fault records its trigger. Actual SSH helpers and endpoint processes are also terminated in recovery cases; these are not SSH mocks.

Cleanup signals only PIDs descended from the test SSH daemon or processes whose executable/config paths match the unique test bundle. A forced coordinator crash can leave an SSH master behind, so cleanup also matches that run's private SSH configuration. No existing Herdr server, user pane, plugin link, SSH profile, room, cursor or hook trust is changed.

## Feature and acceptance coverage

Numbers refer to the [fallback design's acceptance conditions](superpowers/specs/2026-10-20-herdr-idle-message-fallback-design.md#acceptance-and-verification).

| Conditions | Executable evidence |
| --- | --- |
| 1 | `agent-listener-coexistence`: real non-consuming waits and normal inbox consumption prevent fallback. |
| 2 | `remote-reminder`, `same-host-reminder`: actual terminal input after grace, new accepted journal, unchanged original cursor. |
| 3 | `file-only-message-and-new-batch`, `busy-resets-grace-focus-does-not`: full new-batch grace, bursts retain the original deadline, readiness restarts grace. |
| 4 | `working-blocked-unknown-never-prompt`, `busy-resets-grace-focus-does-not`: native detection, cancellation, ready/focus presentation. |
| 5 | Real consumption/replacement scenarios; precise final-preflight races remain covered by `TestNotificationTransportFreshIdentityAndAcknowledgement` and scheduler tests. The upstream atomic check/input gap remains. |
| 6 | `own-and-lifecycle-filtering`, `file-only-message-and-new-batch`, cursor checks in `remote-reminder`. |
| 7 | Empty-inbox native read counts in `own-and-lifecycle-filtering`; real concurrent waits and the adapter broadcast regression. |
| 8 | `shared-batch-and-outstanding-suppression`, consumption followed by a new batch; partial cursor progress is also exercised by `TestNotificationScheduleOutstandingAndFreshGeneration`. |
| 9 | Shared recipient coverage and one input in `shared-batch-and-outstanding-suppression`; atomic journal/replacement faults in the notification attempt Go regressions. |
| 10 | `same-pane-replacement-rejected`, `duplicate-native-reference-rejected`, `original-relocation-and-old-terminal-cleanup`, `explicit-legacy-enrollment`; identical pane/native references on two hosts plus `TestNotificationAttemptHostIsolation` cover host-key collisions. |
| 11 | `real-ssh-helper-disconnect-and-fresh-grace`, coordinator restart; explicit `events_lost` frames and obsolete callbacks remain deterministic Go event/worker regressions. |
| 12 | `lost-ack-suppression-and-explicit-retry` and the prepared crash scenario. Filesystem atomic-write boundary cases remain Go journal regressions. |
| 13 | Actual Start/Join forms, `failed-registration-and-coalesced-invalidation-recovery`, `explicit-legacy-enrollment`. No second Join repairs a missing watcher. |
| 14 | Actual retry CLI and preserved original attempt in recovery; concurrent/shared rejected-retry coverage in `TestNotificationAttemptPartialRetryPreservesUnavailableBinding` and `TestNotificationAttemptRecoveryAndRetry`. |
| 15 | `leave-cancels-delivery`, `room-stop-cancels-pending-delivery`, `plugin-disable-stops-coordinator`, owned-process cleanup. |
| 16 | Real replacement and endpoint loss, followed by restored-shell rejection. |
| 17 | Real native identity duplication, relocation to a new terminal/workspace, and old-terminal cleanup with the original participant preserved. |
| 18 | Actual SSH helper interruption and prepared/uncertain journals; deterministic generation-channel tests cover deliberately delayed stale responses. |
| 19 | Remote SSH/Herdr/adapter process loss with persisted disk, restored shell, and a resumed original native reference with its original adapter offline. No new participant or cursor reset is allowed. |

`durable-cursor-regression-suspends` adds an explicit rollback fault against private participant metadata. The suite also checks room catalog/form delivery, both forwarding directions, binary artifact receipt, and fixed trusted reminder text. Run the Go suite alongside it for exhaustive parser, ownership, corruption, cursor, generation and persistence boundary cases:

```sh
go build ./...
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
go test ./... -timeout 30s -race
python3 scripts/test-herdr-install.py
```

## CI and limits

[The dedicated workflow](../.github/workflows/herdr-e2e.yml) provisions OpenSSH and a checksum-pinned Herdr 0.9.3 Linux binary, then runs the same command. It is available through `workflow_dispatch` and relevant pull requests. A workflow file existing is not evidence that its Linux run passed; record the actual run separately.

The restart scenario models loss of the remote SSH server, native server, agents and Parley processes while preserving disk. It does not reboot a kernel, emulate suspend/resume, or test an interrupted filesystem. No fixture can remove Herdr's final identity-check/input race without an upstream guarded prompt API. The suite proves delivery to the intended synthetic terminal and safe suspension at tested boundaries; it does not prove GUI keyboard shortcuts, LLM task execution, or an unexercised platform.

The first real SSH run exposed an adapter wakeup bug: the non-consuming notification waiter could consume the buffered wakeup needed by a concurrent send acknowledgement. The adapter now broadcasts store changes to every waiter and subscribes before reading, so publication cannot fall between the read and wait. `TestParticipantConcurrentWaitsCannotStealAcknowledgement` failed before the fix and passes with the race detector afterward.

The existing 30-second Go race gate also needed five independent, isolated tests to run in parallel. Their assertions and deadlines are unchanged; the Herdr package subsequently completed in 19.978 seconds. The slow native/SSH acceptance suite remains separate.

## Execution evidence

Verified on 2026-10-21 (Australia/Sydney), macOS arm64, Herdr 0.9.3 and OpenSSH 10.2p1 / LibreSSL 3.3.6:

- `python3 -u scripts/test-herdr-e2e.py`: **27 unique checks and the combined suite passed**, exit 0, in 1,685.1 seconds (28 minutes). All scenario groups shared one fixture, including relocation before SSH/coordinator/restart recovery.
- Evidence retained at `/private/tmp/ph-e2e-8dliq3m9`; console log at `/private/tmp/parley-herdr-e2e-final.log`. `evidence.jsonl` ends with `cleanup_complete`; a separate process inventory found zero remaining processes referencing that run.
- The executable was built from the feature worktree based on `ad1668e`, including the adapter broadcast fix. No production code changed during the combined run. Keys, binaries and runtime evidence are not committed.
- Build, golangci-lint, the exact `go test ./... -timeout 30s -race` gate, and all six installer checks passed. Python modules parse and workflow YAML parses.
- Independent review covered host isolation, negative assertions, real SSH forwarding, crash boundaries, cleanup and test scheduling. Material findings were fixed before the completed combined run.

Linux CI execution and physical/kernel reboot remain unverified. The workflow provisions the same suite for Linux; its existence is not a Linux pass result.
