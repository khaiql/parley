# Herdr action usability evidence

Observed on 2026-10-13 (Australia/Sydney), against Parley parent revision `dc74dc4a8de4cbeba728d719391283b76e1d616d`, Herdr 0.9.3, Codex CLI 0.160.0, and the live managed app-server package 0.162.1.

## Missing native Codex identity

Read-only native checks found a real Codex pane classified as idle with a terminal ID but no `agent_session`. `agent read` showed its untouched welcome prompt, not a completed task. No prompt was sent to it. The installed official Codex integration was v8. A read-only `config/read` and `hooks/list` probe for the agent's actual working directory found the Herdr SessionStart hook enabled and trusted, with its current hash matching saved trust. The pane process and its managed app-server process inherited the correct Herdr pane/socket environment. These checks do not expose credentials or establish that a hook has run.

The responsible timing boundary is upstream: Codex queues SessionStart delivery and dispatches it in the turn path, before sampling. See [Codex 0.160.0 turn handling](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/session/turn.rs#L322), [0.162.1 turn handling](https://github.com/openai/codex/blob/rust-v0.162.1/codex-rs/core/src/session/turn.rs#L322), and [pending hook dispatch](https://github.com/openai/codex/blob/rust-v0.160.0/codex-rs/core/src/hook_runtime.rs#L128). Herdr's official v8 integration handles SessionStart only. Opening the Codex process and seeing an idle badge therefore does not imply that Herdr has received identity.

A disposable no-model probe used the actual installed Codex app-server and unmodified official v8 hook. Two reviewed hooks were trusted only in its temporary home: the official hook and a stopping SessionStart hook. A local HTTP trap replaced the model endpoint. No hook-trust bypass was used, and no user configuration changed.

```text
Isolated probe hook trust: ['trusted', 'trusted']
Real Codex thread/start: native reports 0 model HTTP requests 0
Real Codex first turn: native reports 1 model HTTP requests 0
official v8 hook report:
  method: pane.report_agent_session
  source: herdr:codex
  agent: codex
  session_matches_actual_thread: true
  session_start_source: startup
```

The receiver was a socket fixture, so this proves real Codex hook timing and genuine thread-ID propagation, not native Herdr acceptance or a successful Start/Join flow. Probe processes and files were removed. The real user's agents, hooks, trust settings, and plugin installation were left unchanged.

This timing explains the current untouched pane's missing identity. It does not prove what happens after the user's next ordinary task. Support for genuine pre-turn identities is tracked in [Parley #131](https://github.com/khaiql/parley/issues/131). [Herdr #3517](https://github.com/herdrdev/herdr/issues/3517) records the same timing in older Codex versions and is closed after upstream launch/resume work. [Herdr #4814](https://github.com/herdrdev/herdr/issues/4814) remains open for inherited/stale daemon environments; that is a separate risk, not the established cause of this untouched pane. Neither hook reinstallation nor daemon restart was tested or recommended as a fix.

## Regression coverage

CLI tests reproduced the missing notification before the patch, then passed with notification delivery. They exercise the actual CLI over isolated Unix sockets for shell targets, absent identity, a busy modal, unavailable Herdr, incompatible versions, missing native context, and undelivered notifications. Both Start and Join assert that the popup receives the original terminal ID, native session, pane, and socket. The fixture deliberately names its session differently from the local default, so notifications cannot use ordinary local CLI routing. Additional RED/GREEN tests cover failed form outcomes returning failure after dismissal and Escape/EOF cancellation producing no failure toast. Independent review identified the EOF case; its fix stays at the input boundary so genuine native socket EOFs still notify.

These are fixtures, not native shortcut evidence. Existing terminal-to-current-pane, popup target validation, lifecycle, receipt, route ordering, message/artifact forwarding, and cursor tests remain applicable; no transport or identity-validation logic was relaxed.

## Live negative action and repository gates

A separately built patched CLI ran `herdr action start` against the actual default Herdr server and its still-unbound Codex pane, using the verified invocation socket/pane environment. It returned exit code 1 and the original structured `herdr_failed` / `agent_identity_required` diagnostic. The notification API reported delivery: the patched notifier checks `shown` and would append `notification_failed` otherwise. This proves native error delivery was acknowledged, not that a human saw the toast. No popup opened and no room was created.

Final local gates passed:

```sh
go build ./...
go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./... --timeout=5m
go test ./... -timeout 30s -race
```

The first race run failed the existing `TestHerdrRPCEventRestart` at its six-second deadline and subsequently hit the command package's 30-second timeout during `TestArtifactEndpointOverrideRemappedBytes`. Repeating the same required race command passed; the command package completed in 26.785 seconds. No deadline or production transport behavior was changed to obtain a pass. This remains timing-sensitive evidence, not a claim that those existing timing limits are robust under load. CI targets `main`, so a child PR targeting the open parent may receive no automatic run.

## Native UI limits

Computer-use tools rejected both `com.apple.Terminal` and `com.mitchellh.ghostty` with “Computer Use is not allowed to use the app … for safety reasons.” No direct shortcut/popup UI test, successful native Start/Join recording, or screenshot of this patch was captured. No screen annotation is offered as execution proof. The actual user's pane did not have a usable native identity, so its successful room flow was not exercised.

Before declaring the live flow verified, use an existing supported agent with a real reported identity, reload the documented shortcuts, invoke Start/Join while that agent stays focused, and capture the popup/result and a visible failure. Remote native UI routing also needs that proof. Do not make paid prompt calls solely for transport validation.
