"""Production-path acceptance scenarios; timings use the unchanged 30s grace."""

import hashlib
import json
import os
from pathlib import Path
import signal
import time
import threading
from .environment import Agent, REMINDER, eventually, private_write, require, terminate


def require_delivery_window(reminders, count, ready_at_ns, within=None):
    # Recorder timestamps measure delivery even when observing pending state took longer than grace.
    elapsed = (reminders[count - 1]["at"] - ready_at_ns) / 1_000_000_000
    require(elapsed >= 29, f"production grace was bypassed: input after {elapsed:.3f}s")
    if within is not None:
        require(elapsed <= within, f"delivery exceeded {within}s from original readiness: {elapsed:.3f}s")


def fresh_recipient_missing(observation, after_ns, resolution_calls, baseline_calls):
    return bool(observation and observation["status"] == "suspended" and
                "recipient_missing" in observation.get("message", "") and
                observation["observed_file_mtime_ns"] > after_ns and
                resolution_calls > baseline_calls)


class Fixture:
    def __init__(self, lab):
        self.lab = lab
        self.checked_attempts = set()
        self.c, self.r = lab.host("local"), lab.host("remote")
        lab.ssh_setup(self.r)
        self.c.link()
        self.r.link()
        self.c.command([lab.herdr, "machine", "add", lab.target, "--label", "E2E",
                        "--remote-session", "t"], timeout=60)
        self.c.begin_coordinator()
        eventually(lambda: self.c.status()["peers"], "remote helper activation", timeout=40)
        self.a, self.b = Agent(self.c, "host"), Agent(self.r, "receiver")
        self.host = self.c.form(self.a, "start", "e2e-local-room")
        self.joined = self.r.form(self.b, "join", self.host["room_id"], manual=True)
        self.bindings = [(self.c, self.host), (self.r, self.joined)]
        self.drain()

    def drain(self):
        for host, binding in self.bindings:
            host.parley("inbox", "--session", binding["session_id"])
        # Pending inboxes reconcile themselves; do not manufacture topology reconnects after each read.
        for _, binding in self.bindings:
            eventually(lambda b=binding: self.c.notification(b) and
                       self.c.notification(b)["through_seq"] == 0 and
                       self.c.notification(b)["status"] == "listening",
                       f"consumption observed for {binding['session_id']}", timeout=75)

    def send(self, host=None, binding=None, text="e2e room message", files=()):
        host, binding = host or self.c, binding or self.host
        args = ["send", "--session", binding["session_id"]]
        for file in files:
            args += ["--file", str(file)]
        if text:
            args += [text]
        return host.parley(*args)

    def pending(self, binding=None):
        binding = binding or self.joined
        return eventually(lambda: self.c.notification(binding) and
                          self.c.notification(binding)["through_seq"] > self.c.notification(binding)["seen_seq"],
                          f"unread observation {binding['session_id']}", timeout=75)

    def accepted(self, agent, binding, count, ready_at_ns=None, within=None):
        eventually(lambda: len(agent.reminders()) == count, f"exactly {count} reminders at {agent.name}", timeout=95)
        if ready_at_ns is not None:
            require_delivery_window(agent.reminders(), count, ready_at_ns, within)
        attempt = eventually(lambda: next((a for a in self.c.attempts() if a["id"] not in self.checked_attempts and a["outcome"] == "accepted" and
                          a["conversation"]["value"] == agent.native and
                          any(c["session_id"] == binding["session_id"] for c in a["coverage"])), None),
                          "durable accepted coverage")
        self.checked_attempts.add(attempt["id"])
        return attempt

    def quiet(self, seconds, agents=None, counts=None):
        agents = agents or [self.b]
        before = counts if counts is not None else [len(a.reminders()) for a in agents]
        require(len(before) == len(agents), "negative assertion needs one pre-action count per agent")
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            require(self.c.broker.poll() is None, "coordinator died during negative assertion")
            require(all(p.process.poll() is None for p in (self.c, self.r)), "Herdr died during negative assertion")
            for agent, count in zip(agents, before):
                require(len(agent.reminders()) == count, f"unexpected input at {agent.name}")
            time.sleep(.2)

    def passed(self, name, **details):
        self.lab.record("pass", scenario=name, **details)
        print(f"PASS {name}", flush=True)


def remote_reminder(f):
    before = f.r.parley("status", "--session", f.joined["session_id"])["state"]["last_seen_seq"]
    count = len(f.b.reminders())
    sent, sent_ns = time.monotonic(), time.time_ns()
    f.send(text="actual SSH fallback message")
    f.pending()
    f.quiet(max(0, 28 - (time.monotonic() - sent)), counts=[count])
    f.accepted(f.b, f.joined, count + 1, ready_at_ns=sent_ns)
    after = f.r.parley("status", "--session", f.joined["session_id"])["state"]["last_seen_seq"]
    require(before == after, "fallback consumed original participant cursor")
    require("herdr rpc" in (f.lab.root / "remote-commands.log").read_text(), "SSH helper was bypassed")
    recorded = "\n".join(json.loads(line)["text"] for line in f.b.log.read_text().splitlines())
    require("actual SSH fallback message" not in recorded, "room message body was injected as trusted input")
    require(f.joined["session_id"] in recorded and f.joined["state_dir"] in recorded,
            "reminder omitted the original participant's host-local inbox context")
    require(len(f.canary.reminders()) == 1, "remote reminder reached the colliding local pane/native reference")
    f.passed("remote-reminder", seen_before=before, seen_after=after)


def verify_assertions(f):
    canary = Agent(f.c, "assertion-canary", native=f.b.native)
    f.canary = canary
    require(canary.pane == f.b.pane, "fixture did not create the intended identical pane IDs on both hosts")
    errors = []
    def inject():
        try:
            f.c.api("agent.prompt", {"target": canary.pane, "text": REMINDER + " injected test input"})
        except Exception as error:
            errors.append(error)
    thread = threading.Timer(.3, inject)
    thread.start()
    caught = False
    try:
        f.quiet(3, [canary])
    except AssertionError as error:
        require("unexpected input at assertion-canary" in str(error), f"wrong canary failure: {error}")
        caught = True
    finally:
        thread.join(timeout=10)
    require(caught and not errors and len(canary.reminders()) == 1,
            f"negative assertion did not detect deliberate real PTY input: {errors}")
    f.passed("negative-assertion-detects-real-stray-input")
    f.passed("identical-pane-and-native-reference-on-two-hosts", pane=canary.pane, native=canary.native)


def transport_and_batching(f):
    f.drain()
    local = Agent(f.c, "same-host")
    same = f.c.form(local, "join", f.host["room_id"], manual=True)
    remote_room = f.r.form(f.b, "start", "e2e-remote-room")
    reverse = f.c.form(f.a, "join", remote_room["room_id"], manual=True)
    f.bindings += [(f.c, same), (f.r, remote_room), (f.c, reverse)]
    f.same_agent, f.same, f.remote_room, f.reverse = local, same, remote_room, reverse
    f.drain()
    local.state("working")
    # Both bindings belong to the same original local conversation and should be batched.
    f.a.state("working")
    count = len(f.a.reminders())
    f.send(f.r, f.joined, "shared batch from the remote participant")
    f.send(f.r, remote_room, "shared batch from the remote host")
    f.pending(f.host)
    f.pending(reverse)
    f.a.state("idle")
    attempt = f.accepted(f.a, f.host, count + 1)
    require({f.host["session_id"], reverse["session_id"]} <=
            {c["session_id"] for c in attempt["coverage"]}, "same-conversation rooms were not batched")
    f.send(f.r, remote_room, "later message while original batch remains unread")
    f.quiet(32, [f.a])
    f.passed("shared-batch-and-outstanding-suppression", attempt=attempt["id"])
    f.drain()
    f.a.state("working")
    local.state("idle")
    count = len(local.reminders())
    f.send(text="same-host fallback")
    f.accepted(local, same, count + 1)
    f.passed("same-host-reminder")
    f.drain()
    # Artifact checks require the receiver's mirror and byte-for-byte fetch over the routed endpoint.
    for sender, sb, receiver, rb, label in [(f.c, f.host, f.r, f.joined, "reverse-forward"),
                                           (f.r, remote_room, f.c, reverse, "local-forward")]:
        content = b"e2e artifact\x00\xff" + os.urandom(32)
        source, destination = sender.root / (label + ".bin"), receiver.root / (label + ".download")
        source.write_bytes(content)
        result = f.send(sender, sb, "binary artifact " + label, [source])
        event = next(e for e in result["events"] if e["type"] == "message" and
                     e.get("payload", {}).get("text") == "binary artifact " + label)
        artifact = event["payload"]["artifacts"][0]
        eventually(lambda: receiver.parley("status", "--session", rb["session_id"])["state"]["last_received_seq"]
                   >= event["seq"], "receiver's own mirror receipt")
        fetched = receiver.parley("artifact", "fetch", "--session", rb["session_id"],
                                  "--out", str(destination), artifact["id"])
        require(fetched["results"][0]["status"] == "downloaded" and destination.read_bytes() == content,
                "routed artifact bytes differ")
        require(artifact["sha256"] == hashlib.sha256(content).hexdigest(), "artifact digest differs")
        require(sb["routed"] != rb["routed"], "SSH forwarding endpoints were bypassed")
        f.passed("artifact-" + label, sequence=event["seq"])
    f.drain()
    local.state("working")


def eligibility(f):
    f.a.state("working")
    f.b.state("working")
    blocked = Agent(f.r, "blocked", state="blocked")
    blocked_binding = f.r.form(blocked, "join", f.host["room_id"], manual=True)
    unknown = Agent(f.r, "unknown", kind="codex", state="unknown")
    unknown_binding = f.r.form(unknown, "join", f.host["room_id"], manual=True)
    f.bindings += [(f.r, blocked_binding), (f.r, unknown_binding)]
    f.drain()

    f.send(text="must wait through busy, blocked and unknown")
    f.pending()
    f.quiet(32, [f.b, blocked, unknown])
    f.passed("working-blocked-unknown-never-prompt")
    count = len(f.b.reminders())
    f.b.state("idle")
    f.quiet(12, counts=[count])
    f.b.state("working")
    ready_at_ns = time.time_ns()
    f.b.state("idle")
    f.quiet(18, counts=[count])
    f.r.api("agent.focus", {"target": f.b.pane})
    f.quiet(10, counts=[count])
    f.accepted(f.b, f.joined, count + 1, ready_at_ns=ready_at_ns, within=43)
    f.passed("busy-resets-grace-focus-does-not")
    f.drain()
    # Own messages and lifecycle events do not independently trigger a reminder.
    f.send(f.r, f.joined, "own message is not a pending incoming message")
    visitor = f.c.parley("join", f.host["routed"]["descriptor"], "--name", "lifecycle-visitor")
    f.c.parley("leave", "--session", visitor["session_id"])
    gets_before = f.r.proxy.counts[("agent.get", f.b.pane)]
    f.quiet(32)
    require(f.r.proxy.counts[("agent.get", f.b.pane)] - gets_before <= 12,
            "empty inbox is being polled in a busy loop")
    f.passed("own-and-lifecycle-filtering")
    f.drain()
    # Normal agent-owned wait and inbox coexist with the non-consuming plugin listener.
    f.send(text="consumed by the normal listener")
    ready = f.r.parley("wait", "--session", f.joined["session_id"], "--timeout", "1s")
    require(ready.get("events"), "normal listener did not observe the message")
    f.r.parley("inbox", "--session", f.joined["session_id"])
    eventually(lambda: f.c.notification(f.joined)["through_seq"] == 0, "listener consumption observed")
    f.quiet(32)
    f.passed("agent-listener-coexistence")
    f.drain()
    count = len(f.b.reminders())
    source = f.c.root / "file-only.bin"
    source.write_bytes(b"file-only\x00\xff")
    sent, sent_ns = time.monotonic(), time.time_ns()
    f.send(text="", files=[source])
    f.pending()
    f.quiet(max(0, 10 - (time.monotonic() - sent)), counts=[count])
    f.send(text="burst message one")
    f.quiet(10, counts=[count])
    f.send(text="burst message two")
    f.quiet(max(0, 28 - (time.monotonic() - sent)), counts=[count])
    f.accepted(f.b, f.joined, count + 1, ready_at_ns=sent_ns, within=43)
    f.passed("file-only-message-and-new-batch")
    f.drain()



def registration(f):
    f.a.state("working")
    f.drain()
    # A legacy binding needs explicit verified enrollment; it never inherits pane identity alone.
    enrollment = next(p for p in (f.r.store / "notifications/enrollments").glob("*.json")
                      if json.loads(p.read_text())["binding"]["session_id"] == f.joined["session_id"])
    enrollment.unlink()
    f.r.signal(f.b)
    eventually(lambda: f.c.notification(f.joined) is None, "legacy watcher removed", timeout=75)
    f.send(text="legacy requires verified explicit enrollment")
    f.quiet(32)
    count = len(f.b.reminders())
    f.r.parley("herdr", "notification-enroll", "--participant-session", f.joined["session_id"])
    f.accepted(f.b, f.joined, count + 1)
    f.passed("explicit-legacy-enrollment")
    f.drain()
    pending = Agent(f.c, "pending-registration")
    f.c.proxy.reject_metadata = True
    room = f.c.form(pending, "start", "e2e-registration-recovery")
    require("Room message fallback: registration_pending" in f.c.last_form,
            "failed registration was reported as enrolled")
    sender = f.c.parley("join", room["routed"]["descriptor"], "--name", "registration-sender")
    f.c.parley("inbox", "--session", room["session_id"])
    f.c.parley("send", "--session", sender["session_id"], "durable registration survives failed invalidation")
    f.c.signal(pending)
    f.accepted(pending, room, 1)
    f.bindings.append((f.c, room))
    pending.state("working")
    f.passed("failed-registration-and-coalesced-invalidation-recovery")
    f.drain()
    # Inject a rollback only into this disposable participant's metadata.
    meta_path = Path(f.joined["state_dir"]) / "rooms" / f.joined["room_id"] / "participants" / (f.joined["participant_name"] + ".json")
    metadata = json.loads(meta_path.read_text())
    seen = metadata["last_seen_seq"]
    require(seen > 0, "cursor regression precondition missing")
    metadata["last_seen_seq"] = seen - 1
    stage = meta_path.with_suffix(".fault")
    private_write(stage, json.dumps(metadata))
    stage.replace(meta_path)
    f.r.signal(f.b)
    f.send(text="a cursor rollback must suspend fallback")
    eventually(lambda: "notification_cursor_regression" in f.c.notification(f.joined).get("message", ""),
               "durable cursor regression suspended", timeout=75)
    f.quiet(32)
    metadata = json.loads(meta_path.read_text())
    metadata["last_seen_seq"] = seen
    private_write(stage, json.dumps(metadata))
    stage.replace(meta_path)
    f.r.signal(f.b)
    count = len(f.b.reminders())
    f.accepted(f.b, f.joined, count + 1)
    f.passed("durable-cursor-regression-suspends")
    f.drain()

def identity(f):
    f.a.state("working")
    f.drain()
    original = f.b
    native, count = original.native, len(original.reminders())
    original.report_identity("replacement-conversation")
    sent = f.send(text="never send to a replacement in the original pane")
    sequence = max(e["seq"] for e in sent["events"])
    eventually(lambda: f.r.parley("status", "--session", f.joined["session_id"])["state"]["last_received_seq"]
               >= sequence, "original adapter buffered the message before identity rejection")
    eventually(lambda: f.c.notification(f.joined)["status"] == "suspended", "replacement suspended", timeout=75)
    f.quiet(32, [original])
    require(len(original.reminders()) == count, "replacement received original room input")
    f.passed("same-pane-replacement-rejected")
    moved = Agent(f.r, "moved-original", native=native)
    original.report_identity(native)
    require(sum(a.get("agent_session", {}).get("value") == native
                for a in f.r.api("agent.list")["agents"]) == 2, "native duplicate fixture is missing")
    eventually(lambda: "recipient_ambiguous" in f.c.notification(f.joined).get("message", ""),
               "duplicate identity suspended", timeout=75)
    f.quiet(32, [original, moved])
    f.passed("duplicate-native-reference-rejected")
    original.report_identity("replacement-conversation")
    f.r.api("pane.close", {"pane_id": original.pane})
    f.b = moved
    moved.move()
    f.accepted(moved, f.joined, 1)
    status = f.r.status()
    require(any(b["session_id"] == f.joined["session_id"] and b["status"] == "online"
                for b in status["bindings"].values()), "old-terminal cleanup removed relocated original")
    require(len(original.reminders()) == count, "old pane received relocated delivery")
    f.passed("original-relocation-and-old-terminal-cleanup")
    f.drain()


def recovery(f):
    f.a.state("working")
    f.drain()
    count = len(f.b.reminders())
    f.send(text="a helper connection interruption must restart grace")
    f.pending()
    f.quiet(12, counts=[count])
    f.r.proxy.subscription_held.clear()
    f.r.proxy.release_subscription.clear()
    f.r.proxy.hold_subscription = True
    f.lab.interrupt_remote_helpers()
    eventually(lambda: f.c.notification(f.joined)["status"] == "unavailable", "lost helper generation")
    eventually(f.r.proxy.subscription_held.is_set, "new helper notification subscription held", timeout=75)
    # Hold past the old grace so reconnect delay cannot conceal a missing generation reset.
    f.quiet(32, counts=[count])
    reconnect_at, reconnect_at_ns = time.monotonic(), time.time_ns()
    f.r.proxy.release_subscription.set()
    eventually(lambda: (o := f.c.notification(f.joined)) and o["status"] == "waiting" and
               o["observed_file_mtime_ns"] > reconnect_at_ns, "fresh helper generation", timeout=75)
    f.quiet(max(0, 28 - (time.monotonic() - reconnect_at)), counts=[count])
    f.accepted(f.b, f.joined, count + 1, ready_at_ns=reconnect_at_ns)
    f.passed("real-ssh-helper-disconnect-and-fresh-grace")
    f.drain()
    count = len(f.b.reminders())
    f.r.proxy.drop_prompt = True
    f.send(text="the native acceptance reply will be lost")
    eventually(lambda: f.r.proxy.dropped_prompts == 1, "native acceptance acknowledged then dropped", timeout=45)
    uncertain = eventually(lambda: next((a for a in f.c.attempts() if a["outcome"] == "uncertain" and
                            a["conversation"]["value"] == f.b.native), None), "uncertain durable attempt")
    require(len(f.b.reminders()) == count + 1, "fault did not follow actual terminal delivery")
    f.quiet(32)
    # Retry uses the actual CLI and must wait through another grace period.
    retry_at, retry_at_ns = time.monotonic(), time.time_ns()
    f.c.parley("herdr", "notification-retry", "--attempt", uncertain["id"])
    f.quiet(max(0, 28 - (time.monotonic() - retry_at)), counts=[count + 1])
    f.accepted(f.b, f.joined, count + 2, ready_at_ns=retry_at_ns)
    f.passed("lost-ack-suppression-and-explicit-retry", original_attempt=uncertain["id"])
    f.drain()
    # Coordinator restart affects SSH routes, so use an original same-host participant here.
    local = Agent(f.c, "restart-local")
    same = f.c.form(local, "join", f.host["room_id"], manual=True)
    f.c.parley("inbox", "--session", same["session_id"])
    f.send(text="pending before coordinator process restart")
    f.pending(same)
    f.quiet(12, [local], counts=[0])
    f.c.proxy.subscription_held.clear()
    f.c.proxy.release_subscription.clear()
    f.c.proxy.hold_subscription = True
    f.c.restart_coordinator()
    eventually(f.c.proxy.subscription_held.is_set, "restarted coordinator notification subscription held", timeout=75)
    restart_at, restart_at_ns = time.monotonic(), time.time_ns()
    f.c.proxy.release_subscription.set()
    eventually(lambda: (o := f.c.notification(same)) and o["status"] == "waiting" and
               o["observed_file_mtime_ns"] > restart_at_ns, "post-restart observation", timeout=75)
    f.quiet(max(0, 28 - (time.monotonic() - restart_at)), [local], counts=[0])
    f.accepted(local, same, 1, ready_at_ns=restart_at_ns)
    f.c.parley("inbox", "--session", same["session_id"])
    f.c.proxy.hold_prompt = True
    f.send(text="crash after native acceptance but before journal completion")
    eventually(lambda: f.c.proxy.accepted_held.is_set(), "native input accepted before held reply", timeout=95)
    eventually(lambda: len(local.reminders()) == 2, "held reply followed actual terminal input")
    prepared = next(a for a in f.c.attempts() if a["outcome"] == "prepared" and
                    a["conversation"]["value"] == local.native)
    f.c.broker.kill()
    f.c.broker.wait(timeout=5)
    f.c.proxy.release_held.set()
    f.c.restart_coordinator()
    f.quiet(32, [local])
    require(len(local.reminders()) == 2, "prepared ambiguous reminder repeated after crash")
    retry_at, retry_at_ns = time.monotonic(), time.time_ns()
    f.c.parley("herdr", "notification-retry", "--attempt", prepared["id"])
    f.quiet(max(0, 28 - (time.monotonic() - retry_at)), [local], counts=[2])
    f.accepted(local, same, 3, ready_at_ns=retry_at_ns)
    f.c.restart_coordinator()
    f.quiet(32, [local])
    f.passed("coordinator-restart-grace-prepared-crash-and-durable-suppression", prepared_attempt=prepared["id"])
    f.restart_agent, f.restart_binding = local, same


def shutdown(f):
    # Disconnect every actual SSH connection, restart the endpoint, and retain original adapter identity.
    metadata_path = Path(f.joined["state_dir"]) / "rooms" / f.joined["room_id"] / "participants" / (f.joined["participant_name"] + ".json")
    before = json.loads(metadata_path.read_text())
    unread = f.r.parley("inbox", "--session", f.joined["session_id"], "--peek").get("events", [])
    if not any(e["type"] == "message" and e["actor"] != f.joined["participant_name"] for e in unread):
        f.send(text="buffered incoming message before endpoint loss")
        f.pending()
        unread = f.r.parley("inbox", "--session", f.joined["session_id"], "--peek").get("events", [])
    require(any(e["type"] == "message" and e["actor"] != f.joined["participant_name"] for e in unread),
            "offline/reboot test needs buffered incoming messages")
    sessions_before = {p.name for p in (Path(f.r.env["PARLEY_STATE_DIR"]) / "sessions").glob("*.json")}
    count = len(f.b.reminders())
    prompt_count = f.r.proxy.calls("agent.prompt")
    disconnect_at_ns = time.time_ns()
    f.lab.disconnect()
    f.lab.ssh_start()
    eventually(lambda: (o := f.c.notification(f.joined)) and
               o["observed_file_mtime_ns"] > disconnect_at_ns and
               "participant_disconnected" in o.get("message", ""),
               "reconnected helper observes offline original participant", timeout=75)
    f.quiet(32, counts=[count])
    require(f.r.proxy.calls("agent.prompt") == prompt_count,
            "fallback attempted native input while original participant was offline")
    f.passed("ssh-endpoint-loss-and-reconnect-offline-adapter")
    # Stop only the disposable remote Herdr server; its persisted layout restores shell panes.
    old_pane = f.b.pane
    f.lab.disconnect()
    f.lab.stop_host_adapters(f.r)
    terminate(f.r.process)
    f.r.proxy.close()
    require(f.r.proxy.calls("agent.prompt") == prompt_count,
            "fallback attempted native input while original participant was offline")
    f.r.proxy = None
    f.r.start()
    f.lab.ssh_start()
    snapshot = f.r.api("session.snapshot")["snapshot"]
    agents = f.r.api("agent.list").get("agents", [])
    require(not any(a.get("agent_session", {}).get("value") == f.b.native for a in agents),
            "test did not restore an agent-free layout")
    # The new proxy belongs to the restarted endpoint; exclude this test's inventory calls.
    resolution_calls = f.r.proxy.calls("agent.list")
    observed_after_ns = time.time_ns()
    eventually(lambda: fresh_recipient_missing(f.c.notification(f.joined), observed_after_ns,
                             f.r.proxy.calls("agent.list"), resolution_calls),
                             "restarted endpoint freshly observes missing recipient", timeout=75)
    f.lab.record("restored_shell_observed", session_id=f.joined["session_id"],
                 after_ns=observed_after_ns, resolution_calls=f.r.proxy.calls("agent.list"))
    f.quiet(32, counts=[count])
    require(f.r.proxy.calls("agent.prompt") == 0,
            "fallback attempted native input into the restored shell layout")
    f.passed("remote-process-restart-restored-shell", previous_pane=old_pane,
             snapshot_workspaces=len(snapshot.get("workspaces", [])))
    # Resume the original native conversation without reviving its disconnected Parley adapter.
    resume_at_ns = time.time_ns()
    resumed = Agent(f.r, "resumed-offline", native=f.b.native)
    eventually(lambda: (o := f.c.notification(f.joined)) and
               o["observed_file_mtime_ns"] > resume_at_ns and
               "participant_disconnected" in o.get("message", ""),
               "resumed identity rejected because original adapter is offline", timeout=75)
    f.quiet(32, [resumed], counts=[0])
    require(f.r.proxy.calls("agent.prompt") == 0,
            "offline original participant reached native input submission")
    after = json.loads(metadata_path.read_text())
    require(after["last_seen_seq"] == before["last_seen_seq"], "recovery changed the original inbox cursor")
    require(sessions_before == {p.name for p in (Path(f.r.env["PARLEY_STATE_DIR"]) / "sessions").glob("*.json")},
            "recovery created a replacement participant session")
    f.passed("resumed-conversation-offline-original-participant")
    # Local leave and plugin disable cancel delivery; no user plugin registry is selected.
    agent = Agent(f.c, "leave-local")
    binding = f.c.form(agent, "join", f.host["room_id"], manual=True)
    f.c.parley("leave", "--session", binding["session_id"])
    f.send(text="left participants cannot receive a fallback")
    f.quiet(32, [agent])
    f.passed("leave-cancels-delivery")
    stop_agent = Agent(f.c, "stop-local")
    stop_binding = f.c.form(stop_agent, "join", f.host["room_id"], manual=True)
    f.c.parley("inbox", "--session", stop_binding["session_id"])
    f.send(text="stopping the room cancels pending fallback")
    f.pending(stop_binding)
    f.c.parley("stop", "--session", f.host["session_id"])
    f.quiet(32, [stop_agent])
    f.passed("room-stop-cancels-pending-delivery")
    f.c.command([f.lab.herdr, "plugin", "disable", "parley"])
    eventually(lambda: f.c.broker.poll() is not None, "disable stops owned coordinator", timeout=20)
    f.passed("plugin-disable-stops-coordinator")


def run_scenarios(lab, selected):
    groups = dict(transport=transport_and_batching, eligibility=eligibility, registration=registration, identity=identity,
                  recovery=recovery, shutdown=shutdown)
    require(selected in {"all", "remote-reminder", *groups}, f"Unknown scenario: {selected}")
    f = Fixture(lab)
    verify_assertions(f)
    if selected in {"all", "remote-reminder"}:
        remote_reminder(f)
    if selected == "all":
        for group in groups.values():
            group(f)
    elif selected in groups:
        groups[selected](f)
    require(len(f.canary.reminders()) == 1, "a reminder crossed into the colliding local host identity")
    print(f"PASS suite {selected}; evidence retained at {lab.root}", flush=True)
