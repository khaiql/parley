# Herdr handoff verification — October 14, 2026

The revised flow uses the terminal and harness kind, not `agent_session`. Historical identity/error evidence in the other proof documents describes the earlier implementation.

## Regression evidence

Tests first reproduced missing-conversation rejection, duplicate legacy participation, global picker blocking, missing handoff after room creation, and invalid acknowledgement acceptance. The implementation now passes those focused checks. Native API fixtures cover Codex, Claude, Gemini, and OpenCode kinds without special branches; these fixtures do not prove each real harness.

Final build, repository lint, and full `go test ./... -timeout 30s -race` passed. The Herdr package completed in 27.976 seconds and the command package in 26.807 seconds.

Handoff checks include submitted replay and concurrent replay, known blocked rejection with explicit retry, EOF and malformed acknowledgement without retry, changed terminal rejection, wrong acknowledgement type/terminal, missing/invalid skill, corrupt receipt, and a completed request showing the latest handoff receipt. Review also found a mixed legacy/new request lock race and a cross-host joined-badge leak. Regression tests failed first; terminal-only transaction locks, locked legacy lookup, and host-scoped badges fixed them. Selected room references now pin the original host participant session across delayed requests. Integration uses a freshly built production CLI and real room/participant adapters, with a private native API fixture.

## Actual local API and agent evidence

A separately built binary and copied bundled skills under `/private/tmp/parley-handoff-ei0xdkb8` ran Start and numbered Join in PTYs against the actual default Herdr server. Both existing Codex panes shared their daemon; the second had no `agent_session`. Neither agent was restarted or given daemon flags. Targets were captured terminal IDs, with empty native conversation references.

Start created `room-ea1a9ac6f72c9dd5bc58`, session `psn_3c197aa0696cba2f1f4c`. Numbered Join created distinct session `psn_853631b0633f47288616`. Both reported `Agent instructions: submitted`. The enabled, unactivated `ubuntu-server` profile produced an incomplete warning without blocking the local room.

Read-only `herdr agent read` showed both original agents read the supplied skill, used the supplied executable/root/session, and attempted their presence sends. Both then displayed sandbox approval dialogs for participant socket access under `/Users/sle/.parley`. No approvals were automated. The test therefore confirmed room setup and actual agent awareness, but failed its requirement that both agents successfully send presence. The test left the joined participant and stopped only its disposable room; pending approval commands reference that now-closed test room. The temporary binary/skills were retained so delivered paths were not removed underneath the agents.

## Acceptance limits

Actual keyboard shortcut/popup GUI operation was not automated: computer-use access to Terminal and Ghostty was rejected. The PTY form run does not substitute for that proof.

The saved remote profile had no Parley plugin. Fresh outbound OpenSSH authentication failed with `Permission denied (publickey,password)`. Neither native SSH direction was exercised for this change. No second real harness kind was available. No existing coordinator or user's plugin binary was replaced during verification.

Use the [local procedure](herdr-local-test.md) for final GUI, successful agent sends, transport/artifacts, SSH, and cross-harness acceptance. A submitted native prompt is not proof that the agent completed its work.

The prompt acknowledgement contract follows the [Herdr 0.9.3 response schema](https://github.com/herdrdev/herdr/blob/v0.9.3/src/api/schema/response.rs#L100): require `agent_prompted` and matching terminal/harness. Fixture responses use that shape; an empty or unrelated response remains uncertain.
