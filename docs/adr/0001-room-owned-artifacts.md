# Room-Owned Artifacts

Parley messages may reference files, but file bytes are stored as room-owned artifacts rather than embedded in the message event or served peer-to-peer from the sender. Artifacts are snapshots stored under the room while the room is active, fetched explicitly by artifact id, and cleaned up with the room; this keeps the transcript lightweight, lets late joiners fetch retained artifacts, and avoids making senders remain online or remotely reachable after sharing a file.

## Considered Options

- Embed file bytes in message events: rejected because large files would bloat the JSONL transcript and block normal room traffic.
- Serve files peer-to-peer from the sender: rejected because remote receivers may not be able to reach the sender and artifacts would disappear when the sender disconnects.
- Store artifacts permanently: rejected because Parley is a live collaboration room, not long-term file storage.

## Consequences

Artifact bytes move over a separate HTTP listener in the same room server process, while the existing room protocol remains responsible for message metadata and transcript commits. Remote artifact-capable rooms require both the room protocol port and artifact HTTP port to be reachable; agent guidance should treat `parley info` as the canonical way to rediscover endpoints, limits, and tunnel requirements.
