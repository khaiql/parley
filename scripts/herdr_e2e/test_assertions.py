"""Fast regression checks for production-path acceptance assertions."""

import unittest
from collections import Counter
import threading
import json
from pathlib import Path
import tempfile
import socket
from types import SimpleNamespace
from unittest.mock import patch

from .proxy import SocketProxy

from .environment import Agent, REMINDER
from .scenarios import Fixture, remote_reminder, recovery, require_delivery_window, fresh_recipient_missing


class DeliveryWindowTests(unittest.TestCase):
    def test_early_input_is_rejected_even_when_observed_after_grace(self):
        # Observation at 75s must not turn input recorded at 1s into a valid delivery.
        with self.assertRaisesRegex(AssertionError, "production grace was bypassed"):
            require_delivery_window([{"at": 101_000_000_000}], 1, 100_000_000_000)

    def test_focus_reset_delivery_is_too_late_for_original_readiness(self):
        with self.assertRaisesRegex(AssertionError, "delivery exceeded"):
            require_delivery_window([{"at": 148_000_000_000}], 1, 100_000_000_000, within=43)

    def test_busy_transition_must_start_a_new_grace(self):
        with self.assertRaisesRegex(AssertionError, "production grace was bypassed"):
            require_delivery_window([{"at": 118_000_000_000}], 1, 100_000_000_000, within=43)

    def test_delivery_uses_new_input_after_pre_action_count(self):
        require_delivery_window([{"at": 1}, {"at": 132_000_000_000}], 2,
                                100_000_000_000, within=43)


class RemoteScenarioTests(unittest.TestCase):
    def run_delayed_observation(self, delivery_seconds):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fixture = Fixture.__new__(Fixture)
            fixture.checked_attempts = set()
            fixture.joined = {"session_id": "session-original", "state_dir": "/original/state"}
            agent = Agent.__new__(Agent)
            agent.name, agent.native, agent.log = "receiver", "native-original", root / "input.jsonl"
            # An existing accepted reminder must not become the new delivery's baseline.
            agent.log.write_text(json.dumps({"at": 1, "text": REMINDER}) + "\n")
            fixture.b = agent
            fixture.canary = SimpleNamespace(reminders=lambda: [{}])
            fixture.c = SimpleNamespace(attempts=lambda: [{"id": "new-attempt", "outcome": "accepted",
                "conversation": {"value": agent.native}, "coverage": [{"session_id": "session-original"}]}])
            fixture.r = SimpleNamespace(parley=lambda *args: {"state": {"last_seen_seq": 7}})
            fixture.lab = SimpleNamespace(root=root, record=lambda *args, **kwargs: None)
            (root / "remote-commands.log").write_text("herdr rpc")
            clock = {"now": 100}

            def send(**kwargs):
                with agent.log.open("a") as stream:
                    stream.write(json.dumps({"at": int((100 + delivery_seconds) * 1_000_000_000),
                        "text": REMINDER + " session-original /original/state"}) + "\n")

            def pending():
                clock["now"] = 175

            fixture.send, fixture.pending = send, pending
            with patch("scripts.herdr_e2e.scenarios.time.monotonic", side_effect=lambda: clock["now"]), \
                 patch("scripts.herdr_e2e.scenarios.time.time_ns", return_value=100_000_000_000), \
                 patch("builtins.print"):
                remote_reminder(fixture)

    def test_remote_scenario_rejects_early_input_after_delayed_pending(self):
        with self.assertRaisesRegex(AssertionError, "production grace was bypassed"):
            self.run_delayed_observation(1)

    def test_remote_scenario_accepts_on_time_input_after_delayed_pending(self):
        self.run_delayed_observation(30)

    def test_negative_window_retains_pre_action_count(self):
        alive = SimpleNamespace(poll=lambda: None)
        fixture = Fixture.__new__(Fixture)
        fixture.b = SimpleNamespace(name="recipient", reminders=lambda: [{"at": 1}])
        fixture.c = SimpleNamespace(broker=alive, process=alive)
        fixture.r = SimpleNamespace(process=alive)
        with self.assertRaisesRegex(AssertionError, "unexpected input at recipient"):
            fixture.quiet(.01, counts=[0])


class RecoveryScenarioTests(unittest.TestCase):
    def run_delayed_recovery(self, target, delay, delivery_seconds):
        fixture = Fixture.__new__(Fixture)
        clock = {"now": 100, "active": False, "records": [], "delivery_at": None,
                 "observations": 0, "held": False, "released": False}
        alive = SimpleNamespace(poll=lambda: None)
        fixture.joined = {"session_id": "session-original"}
        fixture.checked_attempts = set()

        def reminders():
            records = list(clock["records"])
            if clock["delivery_at"] is not None and clock["now"] >= clock["delivery_at"]:
                records.append({"at": int(clock["delivery_at"] * 1_000_000_000)})
            return records

        fixture.a = SimpleNamespace(state=lambda state: None)
        fixture.b = SimpleNamespace(name="receiver", native="native-original", reminders=reminders)
        def begin_delivery():
            clock["active"] = True
            clock["delivery_at"] = clock["now"] + delivery_seconds

        def subscription_held():
            if target == "helper" and not clock["held"]:
                clock["now"] += 35
                clock["held"] = True
            return True

        def release_subscription():
            if target == "helper":
                clock["released"] = True
                begin_delivery()

        fixture.r = SimpleNamespace(process=alive, proxy=SimpleNamespace(dropped_prompts=1,
            subscription_held=SimpleNamespace(is_set=subscription_held, clear=lambda: None),
            release_subscription=SimpleNamespace(set=release_subscription, clear=lambda: None)))

        def notification(binding):
            clock["observations"] += 1
            if clock["observations"] == 1:
                return {"status": "unavailable"}
            if target == "helper":
                if not clock["released"]:
                    # Without the ACK gate, reconnect takes 35s before a new generation can deliver.
                    clock["now"] += 35
                    begin_delivery()
                clock["now"] += delay
            return {"status": "waiting", "observed_file_mtime_ns": int(clock["now"] * 1_000_000_000) + 1}

        def retry(*args):
            begin_delivery()
            clock["now"] += delay

        attempts = [{"id": "accepted", "outcome": "accepted", "conversation": {"value": "native-original"},
                     "coverage": [{"session_id": "session-original"}]},
                    {"id": "uncertain", "outcome": "uncertain", "conversation": {"value": "native-original"}}]
        fixture.c = SimpleNamespace(broker=alive, process=alive, notification=notification,
                                    attempts=lambda: attempts, parley=retry)
        fixture.lab = SimpleNamespace(interrupt_remote_helpers=lambda: None)
        fixture.drain = lambda: None
        fixture.pending = lambda: None

        def send(**kwargs):
            if "acceptance reply" in kwargs["text"]:
                clock["records"].append({"at": int(clock["now"] * 1_000_000_000)})

        fixture.send = send

        def quiet(seconds, agents=None, counts=None):
            if clock["active"]:
                Fixture.quiet(fixture, seconds, agents, counts)
            else:
                clock["now"] += seconds

        def accepted(agent, binding, count, **kwargs):
            if clock["active"]:
                return Fixture.accepted(fixture, agent, binding, count, **kwargs)
            clock["records"].append({"at": int(clock["now"] * 1_000_000_000)})

        class Finished(Exception):
            pass

        def passed(name, **details):
            if name == ("real-ssh-helper-disconnect-and-fresh-grace" if target == "helper" else
                        "lost-ack-suppression-and-explicit-retry"):
                raise Finished

        fixture.quiet, fixture.accepted, fixture.passed = quiet, accepted, passed
        with patch("scripts.herdr_e2e.scenarios.time.monotonic", side_effect=lambda: clock["now"]), \
             patch("scripts.herdr_e2e.scenarios.time.time_ns", side_effect=lambda: int(clock["now"] * 1_000_000_000)), \
             patch("scripts.herdr_e2e.scenarios.time.sleep", side_effect=lambda seconds: clock.update(now=clock["now"] + seconds)):
            try:
                recovery(fixture)
            except Finished:
                return
            self.fail("recovery did not reach its acceptance assertion")

    def test_delayed_observation_and_retry_cli_accept_on_time_input(self):
        for target in ("helper", "retry"):
            for delay in (5, 40):
                with self.subTest(target=target, delay=delay):
                    self.run_delayed_recovery(target, delay, 30)

    def test_delayed_observation_and_retry_cli_still_reject_early_input(self):
        for target in ("helper", "retry"):
            for delay in (5, 40):
                with self.subTest(target=target, delay=delay):
                    with self.assertRaisesRegex(AssertionError, "unexpected input|production grace was bypassed"):
                        self.run_delayed_recovery(target, delay, 1)


class FreshObservationTests(unittest.TestCase):
    def test_persisted_unavailable_status_is_not_restarted_endpoint_evidence(self):
        self.assertFalse(fresh_recipient_missing({"status": "unavailable", "message": "closed",
                                                "observed_file_mtime_ns": 120}, 100, 2, 1))

    def test_missing_recipient_requires_both_fresh_status_and_endpoint_resolution(self):
        status = {"status": "suspended", "message": "recipient_missing",
                  "observed_file_mtime_ns": 99}
        self.assertFalse(fresh_recipient_missing(status, 100, 2, 1))
        status["observed_file_mtime_ns"] = 120
        self.assertFalse(fresh_recipient_missing(status, 100, 1, 1))
        self.assertTrue(fresh_recipient_missing(status, 100, 2, 1))


class ProxyEvidenceTests(unittest.TestCase):
    def test_prompt_count_includes_attempts_at_every_target(self):
        proxy = SocketProxy.__new__(SocketProxy)
        proxy.lock = threading.Lock()
        proxy.counts = Counter({("agent.prompt", "pane-a"): 1,
                                ("agent.prompt", "pane-b"): 2,
                                ("agent.get", "pane-a"): 9})
        self.assertEqual(proxy.calls("agent.prompt"), 3)


class ProxySubscriptionTests(unittest.TestCase):
    def test_gate_holds_only_notification_ack_and_close_unblocks_it(self):
        with tempfile.TemporaryDirectory(prefix="ph-p-", dir="/tmp") as directory:
            path = str(Path(directory) / "native")
            backend = socket.socket(socket.AF_UNIX)
            backend.bind(path)
            backend.listen(5)
            backend.settimeout(.2)
            stopped = threading.Event()
            ack = b'{"id":"events","result":{"type":"subscription_started"}}\n'

            def serve():
                while not stopped.is_set():
                    try:
                        connection, _ = backend.accept()
                    except socket.timeout:
                        continue
                    except OSError:
                        return
                    with connection:
                        request = b""
                        while b"\n" not in request:
                            request += connection.recv(65536)
                        connection.sendall(ack[:20])
                        connection.sendall(ack[20:])
                        stopped.wait(.3)

            thread = threading.Thread(target=serve, daemon=True)
            thread.start()
            events = []
            host = SimpleNamespace(socket=path, name="remote", lab=SimpleNamespace(
                record=lambda event, **kwargs: events.append(event)))
            proxy = SocketProxy(host)
            # Assign explicitly so the regression fails on ignored gate behavior, not a missing attribute.
            proxy.hold_subscription = True
            proxy.subscription_held = threading.Event()
            proxy.release_subscription = threading.Event()

            def subscribe(subscription):
                client = socket.socket(socket.AF_UNIX)
                client.settimeout(2)
                client.connect(path)
                client.sendall((json.dumps({"id": "events", "method": "events.subscribe", "params": {
                    "subscriptions": [{"type": subscription}]}}) + "\n").encode())
                return client

            try:
                with subscribe("workspace.metadata_updated") as watch:
                    data = b""
                    while b"\n" not in data:
                        data += watch.recv(65536)
                    self.assertEqual(data, ack)
                    self.assertTrue(proxy.hold_subscription, "WatchRequests consumed notification gate")
                with subscribe("pane.agent_status_changed") as notification:
                    self.assertTrue(proxy.subscription_held.wait(2), "notification acknowledgement bypassed gate")
                    notification.settimeout(.05)
                    with self.assertRaises(socket.timeout):
                        notification.recv(65536)
                    proxy.release_subscription.set()
                    notification.settimeout(2)
                    self.assertEqual(notification.recv(65536), ack)
                self.assertIn("subscription_ack_held", events)
                proxy.subscription_held.clear()
                proxy.release_subscription.clear()
                proxy.hold_subscription = True
                with subscribe("pane.agent_status_changed"):
                    self.assertTrue(proxy.subscription_held.wait(2))
                    proxy.close()
                    self.assertTrue(all(not worker.is_alive() for worker in proxy.threads))
            finally:
                proxy.close()
                stopped.set()
                backend.close()
                thread.join(timeout=2)


if __name__ == "__main__":
    unittest.main()
