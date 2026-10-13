#!/usr/bin/env python3
"""Check transport between two local sessions created through the Herdr UI."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import uuid


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("host_session", help="Session ID shown by the Start popup")
    parser.add_argument("joined_session", help="Session ID shown by the Join popup")
    args = parser.parse_args()
    binary = os.environ.get("PARLEY_BIN", str(Path(__file__).resolve().parents[1] / "bin" / "parley"))

    def run(*command):
        result = subprocess.run([binary, *command], capture_output=True, text=True, timeout=15)
        require(result.returncode == 0, f"{command[0]} failed: {result.stderr.strip()}")
        return json.loads(result.stdout)

    require(args.host_session != args.joined_session, "Use two distinct session IDs.")
    sessions = {item["session_id"]: item for item in run("sessions")["sessions"]}
    require(all(session in sessions for session in (args.host_session, args.joined_session)),
            "Session not found. Check IDs and use the same PARLEY_STATE_DIR as plugin setup.")
    host, joined = (sessions[session] for session in (args.host_session, args.joined_session))
    require(host["room_id"] == joined["room_id"] and host["room_id"], "Sessions belong to different rooms.")
    require(host["name"] != joined["name"], "Sessions identify the same participant.")
    require(host["adapter_running"] and joined["adapter_running"], "Both participant adapters must be running.")

    with tempfile.TemporaryDirectory(prefix="parley-local-check-") as temporary:
        root = Path(temporary)
        for sender, receiver, direction in (
            (args.host_session, args.joined_session, "host-to-joined"),
            (args.joined_session, args.host_session, "joined-to-host"),
        ):
            marker = "parley-local-check-" + uuid.uuid4().hex
            content = marker.encode() + b"\nartifact bytes\x00\xff\n"
            source = root / (direction + ".bin")
            source.write_bytes(content)
            run("send", "--session", sender, "--file", str(source), marker)
            deadline = time.monotonic() + 10
            message = None
            while time.monotonic() < deadline:
                message = next((event for event in run("history", "--session", receiver, "--all").get("events", [])
                                if event["type"] == "message" and event.get("payload", {}).get("text") == marker), None)
                if message is not None:
                    # Same-host history may read the server log; require the receiver's own receipt too.
                    if run("status", "--session", receiver)["state"].get("last_received_seq", 0) >= message["seq"]:
                        break
                    message = None
                time.sleep(0.1)
            require(message is not None, f"{direction}: message did not arrive within 10 seconds.")
            require(message["room_id"] == host["room_id"] and message["actor"] == sessions[sender]["name"],
                    f"{direction}: wrong room or sender.")
            artifacts = message["payload"].get("artifacts", [])
            require(len(artifacts) == 1, f"{direction}: expected one attached artifact.")
            metadata = artifacts[0]
            require(metadata["size"] == len(content) and metadata["sha256"] == hashlib.sha256(content).hexdigest(),
                    f"{direction}: artifact metadata differs from sent bytes.")
            destination = root / (direction + ".download")
            fetched = run("artifact", "fetch", "--session", receiver, "--out", str(destination), metadata["id"])
            require(len(fetched.get("results", [])) == 1 and fetched["results"][0]["status"] == "downloaded",
                    f"{direction}: artifact download failed.")
            require(destination.read_bytes() == content, f"{direction}: downloaded bytes differ.")
            print(f"PASS {direction}: message received; artifact bytes match.")

    print(f"PASS local transport: room {host['room_id']}, two distinct participants.")
    print("Record the manual shortcut/popup checks separately. Sessions remain running for inspection.")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.TimeoutExpired) as error:
        raise SystemExit(f"FAIL: {error}") from error
