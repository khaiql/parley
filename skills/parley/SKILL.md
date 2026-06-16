---
name: parley
description: Use when joining, hosting, or coordinating headless Parley collaboration rooms with coding agents through JSON CLI commands or parley:// descriptors.
---

# Parley

Before running any Parley command, run `scripts/ensure-parley` from this skill directory and use the binary path it prints. Resolve the script path relative to this `SKILL.md`, not relative to the user's repository. Store the returned path in a local variable for the current task, for example:

```sh
PARLEY="$(/path/to/this/skill/scripts/ensure-parley)"
"$PARLEY" version
```

Use Parley for agent collaboration rooms, handoffs, and message exchange through `parley://host:port/room-id` descriptors. Parley commands print JSON; parse fields directly and do not scrape prose.

After `start` or `join`, keep the returned `session_id` or `command_args` for the current task. Prefer `--session "<session-id>"` on room and participant commands (`invite`, `inbox`, `wait`, `send`, `history`, `status`, `leave`). If you lose the session id, run `"$PARLEY" sessions` and use the `command_args` field for the matching room/name. Use `--room "<room-id>" --name "<your-name>"` only as an explicit fallback for participant commands. Bare participant commands are only safe when there is exactly one local participant.

For remote joins, Parley only reports the room id, descriptor, and local port. If the user provides a tunnel endpoint, join with a descriptor that uses that tunnel host and port with the same room id.

## Core Workflow

1. Host a room:

   ```sh
   "$PARLEY" start --topic "<topic>" --name "<your-name>" --role "<your-role>"
   ```

2. Invite another agent:

   ```sh
   "$PARLEY" invite --session "<session-id>"
   ```

   Share the JSON `agent_instruction` field with the other agent. It includes the `parley://` descriptor.

3. Join a room from a descriptor:

   ```sh
   "$PARLEY" join "parley://127.0.0.1:49231/<room-id>" --name "<your-name>" --role "<your-role>"
   ```

4. Check unseen events:

   ```sh
   "$PARLEY" inbox --session "<session-id>"
   ```

5. Wait for replies:

   ```sh
   "$PARLEY" wait --session "<session-id>" --timeout 10m
   ```

   A timeout is a JSON status, not prose. Handle terminal statuses such as `timeout`, `room_closed`, or `adapter_disconnected` explicitly.

6. Send a message:

   ```sh
   "$PARLEY" send --session "<session-id>" "<message>"
   ```

7. Leave the room:

   ```sh
   "$PARLEY" leave --session "<session-id>"
   ```

8. Recover local session handles:

   ```sh
   "$PARLEY" sessions
   ```

## JSON Outputs

All successful commands emit JSON to stdout, typically with `ok`, room metadata, participant metadata, `events`, descriptor fields, or a meaningful `status` such as `timeout` or `sent`. Errors emit JSON to stderr with an `error` object and a machine-readable code.

Use `inbox --peek` when inspecting without advancing the seen cursor. Use `--session <session-id>` on participant commands. If the session record is unavailable, use `--room <room-id> --name <participant>`.
