# Herdr fallback end-to-end verification plan

> Execute inline with `superpowers:executing-plans`. The user requested an executable end-to-end test environment for every feature, including simulated remote SSH. Extend the existing feature branch; do not modify installed plugins or existing sessions.

**Goal:** Exercise the production Parley CLI, coordinator, helpers, room adapters, SSH transport, and Herdr server through reproducible disposable scenarios.

**Architecture:** Use the installed headless Herdr binary and a temporary loopback OpenSSH daemon with generated keys. Two isolated Herdr/config/state namespaces represent controller and participant hosts. A deterministic foreground agent renders lifecycle screens for native detection, reports conversation identity through Herdr's public integration API, and records actual PTY input. No LLM account or real conversation is used. Docker is optional infrastructure, not a required production dependency; the Docker daemon is unavailable in the current environment.

**Tech stack:** Existing Go binary, Python standard library orchestration, OpenSSH client/server, Herdr 0.9.3. Keep the slow suite outside the default 30-second Go test gate and expose one documented invocation.

**Spec:** [Fallback design and its 19 acceptance conditions](../specs/2026-10-20-herdr-idle-message-fallback-design.md).

## Constraints and evidence

- Bind SSH only to loopback; use temporary host/client keys, explicit host verification, no password authentication, and an account allow-list.
- Every process, config, socket, artifact and PID belongs to the harness. Stop only owned processes. Preserve diagnostics on failure.
- Run actual `ssh`, `sshd`, `herdr`, and the built Parley binary. An isolated SSH configuration wrapper may forward arguments to the real client; it must not fake remote command execution.
- Exercise the unchanged 30-second production grace. Use explicit readiness barriers and durable statuses rather than assuming sleep alone proves a negative case.
- A process restart models loss of host processes while preserving disk. It is not a physical/kernel reboot. A deterministic agent proves terminal delivery and identity routing, not LLM behavior or GUI keyboard shortcuts.
- A coverage matrix maps every acceptance condition to real-process scenarios and existing deterministic fault regressions. Do not label unforced crash boundaries as exercised.

## Task 1: Disposable environment and a real remote reminder

Files: add `scripts/herdr_e2e/` support, `scripts/test-herdr-e2e.py`, and a usage/evidence document.

1. Prove isolated key authentication and native lifecycle/session reports against temporary real Herdr sessions.
2. Build an input-recording foreground agent without production dependencies.
3. Start separate controller/participant namespaces and install only a temporary plugin bundle. Register a temporary saved SSH profile and use production setup/activation.
4. Drive Start/Join through production forms, send a room message, and require exactly one reminder in the original agent's recorded PTY input after grace. Check its original cursor is unchanged and its attempt is accepted.
5. Require real SSH logs and actual helper subprocess execution; fail the scenario if the transport is bypassed.

## Task 2: Complete behavioral matrix

1. Cover same-host, local-room/remote-agent, and remote-room/local-agent routing, message and binary artifact transfer, listener coexistence, file-only events, own/lifecycle filtering, focused pane, grace reset, batching and outstanding suppression.
2. Exercise native identity removal/replacement/duplication, original conversation relocation, identical pane IDs on different hosts, legacy enrollment, disconnect/reconnect, coordinator restart, and remote-process restart with restored shell/offline original participant.
3. Exercise explicit retry and durable ambiguous-intent recovery, shared retry coverage, pending registration recovery, leave/stop/disable/shutdown and cursor regression. Use controlled fault injection where a public boundary permits it; pair otherwise unforceable persistence boundaries with existing Go regressions and state that distinction.
4. Demonstrate the harness fails if identity protection or prompt delivery is deliberately broken, then restore and rerun.

## Task 3: Reproducibility, review and handoff

1. Add an opt-in CI job or documented CI provisioning path that installs a pinned Herdr version and OpenSSH; run the same command locally.
2. Record the full scenario output, versions, coverage and remaining limits. Run build, lint, race, installer, and harness checks appropriate to changed files.
3. Request one fresh independent review of the harness, especially process isolation, false-positive assertions, SSH forwarding, restart simulation and cleanup. Fix material findings and rerun affected scenarios.
4. Commit and push only the existing feature branch. No deployment to user sessions.

## Stop condition

Every fallback acceptance condition has executable evidence or an explicitly identified deterministic fault test; real SSH and Herdr delivery works in both directions, failures are detected, cleanup leaves no owned processes, and the coverage document makes simulated versus native boundaries clear.

## Completion evidence

Completed on 2026-10-21 (Australia/Sydney). The combined real Herdr/OpenSSH suite passed all 27 named checks in 28 minutes, including shared-state interactions between relocation and subsequent disconnect/restart cases. Both SSH artifact directions verified the receiver's own mirror and byte-for-byte downloads. A deliberately injected real prompt demonstrated the negative assertion detects stray input; identical pane/native references on different hosts stayed isolated.

The first real run found the participant adapter's buffered notification channel could let a background waiter steal a concurrent send acknowledgement. A failing concurrent-wait regression preceded the broadcast fix. Five independent existing tests now run in parallel so the unchanged 30-second race gate passes with margin; assertions and deadlines remain intact.

Build, lint, race, installer, Python syntax and YAML checks pass. Independent review findings were resolved; completed and interrupted test environments left no matching owned processes. See the [coverage and retained execution evidence](../../herdr-e2e.md). Linux CI and physical/kernel reboot are explicitly unverified; no existing plugin or user session was deployed or restarted.
