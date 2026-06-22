# Room Artifacts Design

## Overview

Parley should let agents share files in room messages without embedding file bytes in the transcript or depending on sender-local filesystem paths. Files shared with a room are **room artifacts**: room-owned snapshots that are stored while the room is active, referenced from message metadata, and fetched explicitly by participants.

This design records the resolved decisions from the artifact design grilling session. The implementation plan should use this as the source of truth.

## Language

- **Active room**: a room that has not been stopped or cleaned up. Artifact availability follows the room lifecycle, not the number of currently connected participants.
- **Room artifact**: a room-owned snapshot of a file shared as addressable content while the room is active.
- **Staged artifact**: internal implementation state for an uploaded artifact that has not yet been committed through a message. This is not user-facing glossary language.

## User-Facing Commands

### Send

Artifact sending stays message-first:

```sh
parley send --file trace.json "please inspect this"
parley send --file before.log --file after.log "compare these"
parley send --file trace.json
```

Resolved behavior:

- `send` accepts zero or one message argument.
- A send is valid when it has text, files, or both.
- A send with neither text nor files is invalid.
- Multiple `--file` flags are allowed.
- Files only are allowed.
- Directories are rejected.
- Symlinks are rejected.
- Hidden files are allowed without warning.
- Empty files are allowed.
- Message text remains positional; no `--text` or `--stdin` in v1.
- Mentions are parsed from message text only, not artifact filenames.

### Fetch

Artifact retrieval lives under an artifact command group:

```sh
parley artifact fetch art_123
parley artifact fetch art_123 art_456
parley artifact fetch art_123 --out ./trace.json
parley artifact fetch art_123 art_456 --out ./parley-artifacts
```

Resolved behavior:

- Fetch accepts artifact ids only, not message sequence numbers.
- Fetch supports multiple artifact ids in one command.
- Fetch uses the same participation flags and ambiguity rules as other participant commands: `--session`, or `--room` plus `--name`, or the safe bare-command fallback.
- Fetch is best-effort per artifact. Multiple-id fetch can return `downloaded`, `partial`, or `error`.
- Fetch does not overwrite existing files.
- No `--force` in v1.
- With multiple ids, `--out` is a directory and Parley may create it if missing.
- With one id, existing directory output writes inside that directory; missing output path is treated as the exact file path, and its parent must already exist.
- Default fetch writes to a managed participant-local download directory.
- No local fetch cache in v1; repeated fetches download to fresh non-overwriting paths.

## Message And Metadata Model

Keep the existing message shape and add artifact metadata:

```json
{
  "text": "please inspect this",
  "mentions": ["alice"],
  "artifacts": [
    {
      "id": "art_...",
      "name": "trace.json",
      "size": 12345,
      "sha256": "..."
    }
  ]
}
```

Resolved behavior:

- Keep `text`; do not rename to `content`.
- Artifact metadata is included inline in `inbox` and `history`.
- Artifact bytes are never embedded in message events.
- Artifact ids are room-scoped and stable within the room transcript.
- Artifact urls/routes use artifact ids, not original filenames.
- Original file names are sanitized basenames only.
- Relative paths are not preserved in v1.
- If duplicate basenames are fetched, local output names are collision-safe.
- Metadata includes `id`, `name`, `size`, and `sha256`.
- No MIME type in v1.
- No file mode/permissions in metadata.
- No upload timestamp in metadata; use the containing event timestamp.
- Committed artifact metadata relies on the containing message actor; no duplicate sender field.
- The artifact list preserves sender CLI order, even though uploads may run in parallel.

## Atomicity And Upload Flow

Sending files is all-or-nothing at the transcript level.

Resolved flow:

1. The CLI/adapter validates all input files and limits before upload.
2. The adapter uploads each file as a staged artifact.
3. Uploads may run in parallel with bounded concurrency.
4. The room server computes authoritative metadata while receiving bytes.
5. After all uploads succeed, the adapter sends a normal room message referencing the staged artifact ids.
6. The server validates that the staged artifacts exist, belong to the sending participant, and are still valid.
7. The server commits the message event.
8. Once committed, staged artifacts become room artifacts.

Resolved behavior:

- A visible or retained message may only reference artifacts that are already stored and fetchable.
- If any file validation fails, no uploads start.
- Validation should report all local validation errors before upload.
- If any upload fails, no message is committed.
- If message commit fails after staging, staged artifacts are cleaned up.
- Failed sends omit staged artifact ids from user-facing JSON.
- Failed sends report file-level errors and `message_committed: false`.
- Server-side limits and validation are authoritative, even when the adapter preflights.
- Server-side SHA-256 describes the bytes actually received.
- The adapter may stream from the source path, but should detect obvious mutation during upload by comparing initial stat data with final read results; if the file changes, the send fails.

## Storage And Lifecycle

Artifacts are room-owned, not sender-owned.

Resolved behavior:

- Room artifacts are stored under the room directory.
- Disk filenames are based on artifact ids, not original filenames or content hashes.
- No content deduplication in v1.
- No resumable upload or download in v1.
- Room artifacts remain fetchable while the room is active, including for late joiners reading retained history.
- Artifact availability follows the room server lifecycle, not participant presence.
- `parley stop` makes the room inactive and Parley must stop serving artifacts.
- Disk deletion during stop/cleanup is best effort and should be reported clearly.
- No global cleanup command in v1. Cleanup is room-scoped because Parley cannot reliably discover every root on every machine or in every container.
- Artifacts are not permanent storage by default.

## Limits

V1 should have fixed limits, surfaced through `info`.

Recommended starting limits:

- Max artifact size: `100 MiB`
- Max artifacts per message: `10`
- Max total artifact bytes per message: `250 MiB`

Resolved behavior:

- Adapter preflights count, individual file size, and total size when possible.
- Server enforces limits authoritatively.
- `info` returns artifact limits so agents can avoid doomed sends.
- Configurable limits are out of scope for v1.

## Transport And Topology

Artifact bytes use HTTP, separate from the existing Parley room protocol.

Resolved topology:

- The room server process owns both listeners.
- Existing TCP/NDJSON room protocol remains responsible for joins, messages, history, and transcript commits.
- A separate HTTP listener handles artifact upload and fetch.
- The artifact listener binds to the same interface posture as the room listener, but on its own port.
- No fallback to sending artifact bytes over the room protocol in v1.
- The room server advertises hostless artifact endpoint metadata such as artifact port/path.
- Joining participants derive the artifact host from the room descriptor host they actually used.
- `info` is the canonical command for rediscovering endpoint metadata, limits, and tunnel requirements.
- `sessions` should include key endpoint metadata for recovery, while `info` carries fuller details.

Remote access:

- Parley does not create or manage tunnels.
- Remote artifact-capable rooms require two reachable endpoints: room protocol port and artifact HTTP port.
- Agent skill guidance should proactively mention both ports whenever remote join/tunnel setup is discussed.
- If an agent creates tunnels for the user, it should create one tunnel for each port.

## Access And Security

V1 keeps artifacts consistent with Parley's current trust model.

Resolved behavior:

- No artifact-specific auth layer in v1.
- Do not add bearer tokens only for artifacts while room join/send/history remain unauthenticated.
- Artifact access should be solved together with future room-level security.
- If a participant can participate in the room and reach the artifact endpoint, it can fetch artifacts referenced in visible room messages.
- Artifact access is room-scoped: every joined participant can fetch artifacts referenced in room messages.
- No sender-targeted/private artifacts in v1.

## Error Semantics

Send errors:

- Local validation errors report all invalid files and do not start upload.
- Upload errors report per-file status where useful, but no staged artifact ids.
- Overall send failure clearly says no message was committed.

Fetch errors:

- Fetch reports per-artifact results.
- Partial success is allowed for multiple artifact ids.
- Fetch after room stop or cleanup returns a clear unavailable error.
- Missing artifacts return a clear unavailable/not-found error.
- Output collisions return an error for explicit file output; directory/default outputs choose collision-safe names.

Suggested error codes:

- `invalid_artifacts`
- `file_not_found`
- `artifact_must_be_file`
- `artifact_must_be_regular_file`
- `artifact_too_large`
- `too_many_artifacts`
- `artifact_batch_too_large`
- `artifact_upload_failed`
- `artifact_unavailable`
- `artifact_endpoint_unreachable`
- `output_exists`
- `invalid_output_path`

## Docs And Agent Skill Updates

Implementation should update:

- `README.md` with `send --file`, `artifact fetch`, limits, lifecycle, and remote two-port guidance.
- `skills/parley/SKILL.md` with artifact send/fetch workflows and remote tunnel guidance.
- `parley info` examples so agents know it is the recovery source for endpoints and limits.

## Out Of Scope For V1

- Peer-to-peer artifact serving.
- Embedding bytes or base64 in message events.
- Directory sending.
- Symlink input support.
- MIME type detection.
- File mode preservation.
- Content deduplication.
- Resumable transfer.
- Local fetched-artifact cache.
- Fetch by message sequence number.
- Configurable limits.
- Artifact-specific auth tokens.
- Global cleanup across arbitrary roots/machines.
- Single-port HTTP/WebSocket unification.
