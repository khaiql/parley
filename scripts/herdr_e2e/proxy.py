"""Pass-through Unix socket transport with explicit, recorded fault boundaries."""

import json
import os
import select
import socket
import threading
from collections import Counter


class SocketProxy:
    def __init__(self, host):
        self.host = host
        self.path = host.socket
        self.backend = self.path + ".up"
        self.lock = threading.Lock()
        self.connections = set()
        self.threads = []
        self.stopped = threading.Event()
        self.drop_prompt = False
        self.reject_metadata = False
        self.dropped_prompts = 0
        self.counts = Counter()
        self.hold_prompt = False
        self.accepted_held = threading.Event()
        self.release_held = threading.Event()
        os.rename(self.path, self.backend)
        self.listener = socket.socket(socket.AF_UNIX)
        self.listener.bind(self.path)
        os.chmod(self.path, 0o600)
        self.listener.listen(128)
        self.listener.settimeout(.2)
        self.thread = threading.Thread(target=self.accept, daemon=True)
        self.thread.start()

    def accept(self):
        while not self.stopped.is_set():
            try:
                client, _ = self.listener.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            thread = threading.Thread(target=self.relay, args=(client,), daemon=True)
            self.threads.append(thread)
            thread.start()

    def relay(self, client):
        backend = socket.socket(socket.AF_UNIX)
        with self.lock:
            self.connections.update((client, backend))
        try:
            client.settimeout(5)
            frame = b""
            while b"\n" not in frame:
                part = client.recv(65536)
                if not part:
                    return
                frame += part
            request = json.loads(frame.split(b"\n", 1)[0])
            method = request.get("method")
            with self.lock:
                self.counts[(method, request.get("params", {}).get("target"))] += 1
                reject = self.reject_metadata and method == "workspace.report_metadata"
                if reject:
                    self.reject_metadata = False
                drop = self.drop_prompt and method == "agent.prompt"
                if drop:
                    self.drop_prompt = False
                hold = self.hold_prompt and method == "agent.prompt"
                if hold:
                    self.hold_prompt = False
            if reject:
                client.sendall((json.dumps({"id": request["id"], "error": {
                    "code": "unavailable", "message": "injected registration failure"}}) + "\n").encode())
                self.host.lab.record("metadata_rejected", host=self.host.name)
                return
            backend.connect(self.backend)
            backend.sendall(frame)
            client.settimeout(None)
            while not self.stopped.is_set():
                readable, _, _ = select.select([client, backend], [], [], .2)
                for source in readable:
                    data = source.recv(65536)
                    if not data:
                        return
                    if source is backend and (drop or hold):
                        # Read and validate the complete native acceptance before losing its reply.
                        while b"\n" not in data:
                            part = backend.recv(65536)
                            if not part:
                                return
                            data += part
                        result = json.loads(data.split(b"\n", 1)[0])
                        if result.get("result", {}).get("type") == "agent_prompted":
                            if hold:
                                self.accepted_held.set()
                                self.host.lab.record("accepted_reply_held", host=self.host.name, response=result)
                                while not self.release_held.wait(.1):
                                    if self.stopped.is_set():
                                        return
                                hold = False
                            if not drop:
                                client.sendall(data)
                                continue
                            with self.lock:
                                self.dropped_prompts += 1
                            self.host.lab.record("prompt_ack_dropped", host=self.host.name, response=result)
                            return
                    (client if source is backend else backend).sendall(data)
        except (OSError, ValueError):
            pass
        finally:
            client.close()
            backend.close()
            with self.lock:
                self.connections.difference_update((client, backend))

    def close(self):
        self.stopped.set()
        self.listener.close()
        with self.lock:
            connections = list(self.connections)
        for connection in connections:
            try:
                connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
        self.thread.join(timeout=5)
        for thread in self.threads:
            thread.join(timeout=5)
