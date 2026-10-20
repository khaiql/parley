# Herdr terminal participation and automatic handoff

This records the revised plan approved in the conversation. It supersedes the native-conversation requirement and no-prompt restriction in the October 12 plan.

## Accepted behavior

- Target the original Herdr socket, terminal, and detected harness kind; conversation identity is optional.
- Start creates a room; Join offers a room picker and creates or reuses one participant for the terminal.
- Preserve old binding keys, participant IDs, runtime pins, ownership, and receive cursors.
- Confirmed picker hosts remain selectable when another enabled peer is unavailable. Revalidate the selected authoritative host before mutation.
- After confirmed Start/Join, use Herdr `agent.prompt` on the participant host. Include the room/topic, participant/session, bundled skill, binary, and pinned root. Never ask the agent to join again.
- Harnesses control their normal prompt behavior. No Codex queue/API, daemon flags, hook changes, agent launch, or restart is required.
- Persist submitted/failed/uncertain receipts; replay never duplicates a submitted or uncertain prompt. Explicit retry acts only on failed handoff and preserves the participant.
- Late queued completion still submits instructions after the original popup closes. Show durable handoff status.
- Gate helper protocol compatibility before remote participant mutation.

## Tasks and checks

1. Terminal targeting and legacy reuse: native-identity absence, changed kind/terminal rejection, completed legacy reuse, ambiguity rejection, no duplicate adapter/cursor reset.
2. Scoped selection: unavailable unrelated peer, exact host revalidation, saved profile/socket/helper changes, same-ID different hosts.
3. Handoff: fixed native prompt target, session/skill/root payload, working and blocked harnesses, delayed completion, submitted replay, concurrent replay, failed-only retry, unknown acknowledgement/corrupt receipt.
4. Rollout and docs: protocol 2 probe, old coordinator diagnostics, skill session reuse, honest local/SSH/harness test procedure.
5. Whole-branch review and final gates: build, repository lint, full race suite; native API/real-agent delivery evidence; actual shortcut UI, SSH both directions, and two harness kinds required before claiming full end-to-end acceptance.

## Rulings

An old coordinator has no private verified PID or shutdown API. Automatic replacement could interrupt active SSH routes. Reject the old coordinator with explicit plugin-only restart instructions rather than signal an unverified process or silently drop routes. This restart does not restart agents or Herdr servers; existing participants remain stored.

Native API/PTY tests are separate from actual shortcut UI proof. Two actual Codex agents read the skill and attempted sends, but sandbox approvals prevented message exchange. Do not approve those dialogs through automation. SSH authentication and non-Codex harness availability are environmental acceptance limits, not reasons to add harness-specific behavior.
