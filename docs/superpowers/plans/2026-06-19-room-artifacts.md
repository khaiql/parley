# Room Artifacts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add room-owned file artifacts so agents can send files with messages and fetch them explicitly by artifact id.

**Architecture:** Keep the existing TCP/NDJSON room protocol as the transcript authority and add a second HTTP listener in the same room server process for artifact bytes. Participant adapters remain the client-side transport owner: `parley send --file` uploads staged artifacts over HTTP, then commits a normal message event with artifact metadata; `parley artifact fetch` asks the adapter to download artifact bytes to a safe local path.

**Tech Stack:** Go, Cobra, TCP with line-delimited JSON, HTTP streaming, JSONL event logs, Unix domain sockets.

---

## Source Documents

- Design spec: `docs/superpowers/specs/2026-06-19-room-artifacts-design.md`
- ADR: `docs/adr/0001-room-owned-artifacts.md`
- Glossary: `CONTEXT.md`

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/model/event.go` | Modify | Add artifact metadata to message payloads |
| `internal/model/event_test.go` | Modify | Cover artifact metadata on message payloads |
| `internal/protocol/protocol.go` | Modify | Allow send requests to reference staged artifact ids |
| `internal/protocol/protocol_test.go` | Modify | Wire-format tests for artifact send requests/responses |
| `internal/artifact/artifact.go` | Create | Artifact metadata, limits, validation constants, path helpers |
| `internal/artifact/store.go` | Create | Room artifact storage, staging, commit, cleanup, fetch lookup |
| `internal/artifact/store_test.go` | Create | Storage, validation, cleanup, basename, collision tests |
| `internal/server/server.go` | Modify | Own artifact store, expose artifact metadata in room metadata, validate staged ids on send |
| `internal/server/artifact_http.go` | Create | HTTP upload/fetch handlers for artifact bytes |
| `internal/server/artifact_http_test.go` | Create | Upload/fetch, limits, not-found, room mismatch tests |
| `internal/runtime/runtime.go` | Modify | Persist artifact port/path and expose limits in invite/info surfaces |
| `internal/runtime/runtime_test.go` | Modify | Runtime and invite metadata tests |
| `internal/adapter/adapter.go` | Modify | Persist artifact endpoint metadata in participant meta |
| `internal/adapter/control.go` | Modify | Extend control request/response for file send and artifact fetch |
| `cmd/parley/daemon.go` | Modify | Start artifact HTTP listener, derive endpoint for participants, upload before send, fetch artifacts |
| `cmd/parley/participant_commands.go` | Modify | Add `send --file`, optional message, artifact-aware errors, fuller `info` |
| `cmd/parley/artifact_commands.go` | Create | Add `parley artifact fetch` command group |
| `cmd/parley/main.go` | Modify | Register artifact command group |
| `cmd/parley/sessions.go` | Modify | Include key artifact endpoint metadata |
| `cmd/parley/main_test.go` | Modify | CLI JSON shape and validation tests |
| `internal/e2e/headless_test.go` | Modify | End-to-end send-with-file and fetch flow |
| `README.md` | Modify | Document artifact send/fetch, lifecycle, limits, two-port remote guidance |
| `skills/parley/SKILL.md` | Modify | Document artifact send/fetch workflow and two tunnel requirement |

## Behavior Checklist

- `parley send --file file "message"` uploads artifacts and commits a message with metadata.
- `parley send --file file` is valid.
- `parley send` with no text and no files is invalid.
- Multiple files are uploaded with preserved CLI order in message metadata.
- Directories and symlinks are rejected before upload.
- Hidden and empty files are allowed.
- Failed multi-file sends do not commit a message and do not expose staged artifact ids.
- `parley artifact fetch art_1 art_2` fetches by artifact id only and returns per-artifact results.
- Fetch never overwrites existing files and has no `--force` in v1.
- Artifact bytes are unavailable after room stop, even if disk cleanup fails.
- `info` is the canonical recovery command for artifact endpoint metadata and limits.
- Remote guidance always mentions both room protocol and artifact HTTP ports.

## Wire Contracts

### Message Payload

Committed room messages keep the existing text shape and add artifact metadata:

```json
{
  "text": "please inspect",
  "mentions": ["alice"],
  "artifacts": [
    {
      "id": "art_123",
      "name": "trace.json",
      "size": 12345,
      "sha256": "abc..."
    }
  ]
}
```

Artifact bytes must never appear in `payload`, `inbox`, `history`, or room event JSON.

### Room Protocol

`protocol.SendRequest` carries staged artifact ids:

```json
{
  "type": "send",
  "send": {
    "text": "please inspect",
    "artifact_ids": ["art_123"]
  }
}
```

The server commits a `message` event only when all artifact ids are staged for the sending participant and have been promoted to room artifacts. If commit fails, no message event is appended.

### Artifact HTTP

The artifact HTTP listener is owned by the same room server process.

Upload one file:

```http
POST /rooms/<room-id>/artifacts/staged?participant=<participant-name>
Content-Type: multipart/form-data

field "file": file bytes with sanitized basename
```

Success:

```json
{
  "id": "art_123",
  "name": "trace.json",
  "size": 12345,
  "sha256": "abc..."
}
```

Fetch one committed artifact:

```http
GET /rooms/<room-id>/artifacts/<artifact-id>
```

Success returns `200`, `Content-Type: application/octet-stream`, `Content-Disposition` with the sanitized basename, and the raw artifact bytes.

Cleanup staged uploads for a participant:

```http
DELETE /rooms/<room-id>/artifacts/staged?participant=<participant-name>
```

Success:

```json
{ "status": "cleaned" }
```

HTTP errors use:

```json
{
  "error": {
    "code": "artifact_unavailable",
    "message": "artifact is not available: art_123"
  }
}
```

### CLI JSON

`start` includes hostless artifact metadata and limits:

```json
{
  "status": "started",
  "room_id": "room-1",
  "descriptor": "parley://127.0.0.1:49231/room-1",
  "local_host": "127.0.0.1",
  "local_port": 49231,
  "artifact_local_port": 49232,
  "artifact_path": "/rooms/room-1/artifacts",
  "artifact_limits": {
    "max_file_bytes": 104857600,
    "max_files_per_message": 10,
    "max_total_bytes_per_message": 262144000
  }
}
```

`invite` mirrors the same room endpoint fields and keeps `agent_instruction` as the human/agent pasteable text:

```json
{
  "status": "invite",
  "room_id": "room-1",
  "descriptor": "parley://127.0.0.1:49231/room-1",
  "local_host": "127.0.0.1",
  "local_port": 49231,
  "artifact_local_port": 49232,
  "artifact_path": "/rooms/room-1/artifacts",
  "artifact_limits": {
    "max_file_bytes": 104857600,
    "max_files_per_message": 10,
    "max_total_bytes_per_message": 262144000
  },
  "join_command_template": "parley join \"parley://127.0.0.1:49231/room-1\" --role <participant-role>",
  "agent_instruction": "Use your Parley skill to join this room: parley://127.0.0.1:49231/room-1. Remote setup needs two reachable endpoints: room protocol port 49231 and artifact HTTP port 49232."
}
```

`info` is the canonical recovery command:

```json
{
  "status": "info",
  "room_id": "room-1",
  "descriptor": "parley://127.0.0.1:49231/room-1",
  "local_host": "127.0.0.1",
  "local_port": 49231,
  "artifact_local_port": 49232,
  "artifact_path": "/rooms/room-1/artifacts",
  "artifact_limits": {
    "max_file_bytes": 104857600,
    "max_files_per_message": 10,
    "max_total_bytes_per_message": 262144000
  },
  "participant": {
    "artifact_endpoint": "http://127.0.0.1:49232/rooms/room-1/artifacts"
  }
}
```

`sessions` includes key recovery fields per session:

```json
{
  "status": "sessions",
  "sessions": [
    {
      "session_id": "psn_...",
      "room_id": "room-1",
      "descriptor": "parley://127.0.0.1:49231/room-1",
      "artifact_endpoint": "http://127.0.0.1:49232/rooms/room-1/artifacts",
      "artifact_local_port": 49232,
      "artifact_path": "/rooms/room-1/artifacts",
      "command_args": "--session psn_..."
    }
  ]
}
```

`send` success stays status-first:

```json
{
  "status": "sent",
  "events": [
    {
      "type": "message",
      "payload": {
        "text": "please inspect",
        "artifacts": [{ "id": "art_123", "name": "trace.json", "size": 12345, "sha256": "abc..." }]
      }
    }
  ]
}
```

Failed artifact sends must not expose staged artifact ids:

```json
{
  "status": "error",
  "error": {
    "code": "artifact_upload_failed",
    "message": "one or more artifacts failed to upload; no message was sent"
  },
  "files": [
    { "path": "trace.json", "status": "error", "error": { "code": "artifact_too_large" } }
  ],
  "message_committed": false
}
```

`artifact fetch` success:

```json
{
  "status": "downloaded",
  "results": [
    { "id": "art_123", "status": "downloaded", "path": "/absolute/path/trace.json" }
  ]
}
```

Multiple-id fetch with mixed outcomes:

```json
{
  "status": "partial",
  "results": [
    { "id": "art_123", "status": "downloaded", "path": "/absolute/path/trace.json" },
    { "id": "art_missing", "status": "error", "error": { "code": "artifact_unavailable", "message": "artifact is not available: art_missing" } }
  ]
}
```

All fetch failures use `status: "error"` with per-artifact `results`.

---

### Task 1: Artifact Model And Store

**Files:**
- Modify: `internal/model/event.go`
- Modify: `internal/model/event_test.go`
- Create: `internal/artifact/artifact.go`
- Create: `internal/artifact/store.go`
- Create: `internal/artifact/store_test.go`

- [ ] **Step 1: Write failing model/store tests**

Add these failing tests:

- `TestMessagePayloadArtifactsPreserveOrder`: `model.MessagePayload` carries ordered `[]ArtifactMetadata`.
- `TestArtifactSanitizeNameKeepsBasenameOnly`: `logs/app.log`, `/tmp/app.log`, and `../app.log` all display as `app.log`.
- `TestArtifactValidateRejectsDirectoryAndSymlink`: directory returns `artifact_must_be_file`; symlink returns `artifact_must_be_regular_file`.
- `TestArtifactValidateAllowsEmptyRegularFile`: empty regular file passes with size `0`.
- `TestArtifactValidateReportsAllLocalErrors`: validation helper returns every invalid input path before upload starts.
- `TestArtifactStoreStagesCommitsAndOpensByID`: staged artifacts commit and can be opened by id.
- `TestArtifactStoreCleanupStagedForParticipant`: cleanup removes only the requested participant's staged artifacts.

Run:

```sh
go test ./internal/model ./internal/artifact -run 'TestMessagePayload|Test.*Artifact' -v
```

Expected: FAIL because the artifact package and fields do not exist.

- [ ] **Step 2: Implement artifact metadata and store**

Create an `internal/artifact` package with:

- fixed limits: `MaxFileBytes = 100 * 1024 * 1024`, `MaxFilesPerMessage = 10`, `MaxTotalBytesPerMessage = 250 * 1024 * 1024`;
- `Metadata{ID, Name, Size, SHA256}`;
- `Limits`;
- validation helpers for regular files only;
- room-local paths under `<room-dir>/artifacts/`;
- staged files keyed by participant and artifact id;
- committed files keyed by artifact id;
- cleanup helpers for staged and committed artifacts.

Update `model.MessagePayload`:

```go
type MessagePayload struct {
	Text      string             `json:"text"`
	Mentions  []string           `json:"mentions,omitempty"`
	Artifacts []ArtifactMetadata `json:"artifacts,omitempty"`
}

type ArtifactMetadata struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
```

- [ ] **Step 3: Verify model/store tests pass**

Run:

```sh
go test ./internal/model ./internal/artifact -run 'TestMessagePayload|Test.*Artifact' -v
```

Expected: PASS.

### Task 2: Server Artifact HTTP Listener And Message Commit

**Files:**
- Modify: `internal/protocol/protocol.go`
- Modify: `internal/protocol/protocol_test.go`
- Modify: `internal/server/server.go`
- Create: `internal/server/artifact_http.go`
- Create: `internal/server/artifact_http_test.go`
- Modify: `internal/server/server_test.go`
- Modify: `internal/server/server_internal_test.go`

- [ ] **Step 1: Write failing server/protocol tests**

Add these failing tests:

- `TestArtifactSendRequestRoundTripsStagedIDs`: `SendRequest` carries `artifact_ids`.
- `TestServerHandleSendRejectsUnknownStagedArtifact`: unknown staged id returns `artifact_unavailable` and appends no message.
- `TestServerHandleSendCommitsParticipantArtifacts`: participant-owned staged ids become ordered message metadata.
- `TestServerHandleSendRejectsOtherParticipantStagedArtifact`: Alice cannot commit Bob's staged id.
- `TestServerHandleSendCommitFailureLeavesNoFetchableArtifact`: if promotion/event append fails after staging, staged artifacts are cleaned up or rolled back, no message event exists, and the artifact id cannot be fetched.
- `TestServerHandleSendPreservesMentionsFromTextOnly`: `--file @bob.log` does not add a mention unless text contains `@bob`.
- `TestArtifactHTTPUploadStagesArtifact`: upload stores a staged artifact and returns exact metadata JSON.
- `TestArtifactHTTPFetchReturnsCommittedBytes`: fetch returns committed bytes, `Content-Disposition`, and no JSON wrapper.
- `TestArtifactHTTPFetchMissingReturnsNotFound`: missing/uncommitted artifact returns `404` with JSON error code `artifact_unavailable`.
- `TestArtifactHTTPCleanupStaged`: cleanup endpoint removes staged artifacts for that participant.
- `TestArtifactHTTPWrongRoomReturnsNotFound`: wrong room id cannot access artifacts.

Run:

```sh
go test ./internal/protocol ./internal/server -run 'Test.*Artifact|TestServerHandleSend' -v
```

Expected: FAIL because protocol and server artifact support do not exist.

- [ ] **Step 2: Implement server-side artifact support**

Add artifact store ownership to `server.Config`/`Server`.

Extend `protocol.SendRequest`:

```go
type SendRequest struct {
	Text        string   `json:"text"`
	ArtifactIDs []string `json:"artifact_ids,omitempty"`
}
```

On send, while holding server authority:

- verify sender is online;
- verify each staged artifact id belongs to the sender;
- validate and prepare staged artifacts in request order;
- append `model.MessagePayload{Text, Mentions, Artifacts}`;
- make artifacts fetchable only after the message event append succeeds;
- if event append fails, roll back or clean up the staged artifacts so no fetchable orphan artifact remains.

Add an HTTP handler owned by the same server process with these routes:

- `POST /rooms/<room-id>/artifacts/staged?participant=<name>` for one file upload;
- `GET /rooms/<room-id>/artifacts/<artifact-id>` for committed file fetch.
- `DELETE /rooms/<room-id>/artifacts/staged?participant=<name>` for staged cleanup after failed sends.

Keep auth consistent with current v1 trust model; do not add bearer tokens.

- [ ] **Step 3: Verify server tests pass**

Run:

```sh
go test ./internal/protocol ./internal/server -run 'Test.*Artifact|TestServerHandleSend' -v
```

Expected: PASS.

### Task 3: Runtime Metadata, Daemons, And Adapter Control

**Files:**
- Modify: `internal/runtime/runtime.go`
- Modify: `internal/runtime/runtime_test.go`
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/adapter/control.go`
- Modify: `cmd/parley/daemon.go`

- [ ] **Step 1: Write failing runtime/adapter tests**

Cover:

- `TestArtifactRoomRuntimeRoundTrip`: room runtime persists `artifact_local_port`, `artifact_path`, and artifact limits.
- `TestInviteIncludesArtifactEndpointMetadataAndLimits`: invite reports artifact endpoint metadata and two-port remote instruction.
- `TestStartIncludesArtifactEndpointMetadataAndLimits`: start response includes `artifact_local_port`, `artifact_path`, and `artifact_limits`.
- `TestArtifactEndpointDerivedFromDescriptorHost`: participant derives `http://<descriptor-host>:<artifact-port><artifact-path>`, not `127.0.0.1` from host runtime.
- `TestArtifactListenerUsesRoomBindPosture`: artifact listener binds to the same interface posture as the room listener.
- `TestInfoAndSessionsExposeArtifactEndpointAndLimits`: `info` is canonical; `sessions` includes key endpoint fields.
- `TestArtifactControlRequestJSON`: adapter control supports send file paths and artifact fetch requests.

Run:

```sh
go test ./internal/runtime ./internal/adapter ./cmd/parley -run 'Test.*Artifact|TestInvite|TestInfo' -v
```

Expected: FAIL.

- [ ] **Step 2: Implement runtime and daemon wiring**

In `runRoomDaemon`, start the artifact HTTP listener in the same process before saving room runtime. Save both ports.

In `runParticipantAdapter`, derive the artifact endpoint from the room descriptor host plus room-advertised artifact port/path. Persist it in participant metadata.

Extend adapter control:

- send request: `Type: "send"`, `Text`, `Files []string`;
- fetch request: `Type: "artifact_fetch"`, `ArtifactIDs []string`, `Out string`.

Keep short CLI commands talking to the local control socket; the adapter performs HTTP upload/fetch.

Adapter upload behavior:

- preflight all files before upload and report all local validation errors;
- enforce count and aggregate byte limits before upload;
- upload files in bounded parallelism while preserving CLI order in `artifact_ids`;
- stream file bytes into multipart upload instead of reading the entire file into memory;
- detect obvious source mutation by comparing initial and final stat data;
- if any upload or message commit fails, call staged cleanup and return no staged ids to the CLI.
- expose artifact limits and endpoint fields in `start`, `invite`, `info`, and `sessions` using the Wire Contracts JSON field names.

- [ ] **Step 3: Verify runtime/adapter tests pass**

Run:

```sh
go test ./internal/runtime ./internal/adapter ./cmd/parley -run 'Test.*Artifact|TestInvite|TestInfo' -v
```

Expected: PASS.

### Task 4: CLI Send And Artifact Fetch

**Files:**
- Modify: `cmd/parley/main.go`
- Modify: `cmd/parley/participant_commands.go`
- Create: `cmd/parley/artifact_commands.go`
- Modify: `cmd/parley/sessions.go`
- Modify: `cmd/parley/main_test.go`

- [ ] **Step 1: Write failing CLI tests**

Cover:

- `TestSendWithFilePassesFilesAndMessageToAdapter`: `send --file path "text"` accepts one message arg.
- `TestSendFileOnlyIsValid`: `send --file path` accepts no message arg.
- `TestSendRequiresMessageOrFile`: `send` with no files and no message fails.
- `TestSendFileValidationReportsAllErrorsAndSkipsAdapter`: multiple invalid files return a structured `invalid_artifacts` response and do not call adapter control.
- `TestSendRejectsBatchLimit`: too many files and too many total bytes fail before upload.
- `TestSendFileRejectsDirectoryAndSymlink`: directory and symlink return expected codes.
- `TestArtifactFetchPassesMultipleIDsToAdapter`: `artifact fetch` accepts multiple ids and participation flags.
- `TestArtifactFetchMultipleIDsRejectsFileOut`: `artifact fetch --out file` with multiple ids fails.
- `TestArtifactFetchDefaultDirUsesFreshCollisionSafePath`: default managed download dir never overwrites.
- `TestArtifactFetchExplicitFileRefusesOverwrite`: single-id explicit file output refuses existing file.
- `TestArtifactFetchSingleIDExistingDirectoryWritesInsideDirectory`: single-id `--out existing-dir` writes a collision-safe file inside that directory.
- `TestArtifactFetchMissingParentFails`: single-id missing parent path fails.
- `TestArtifactFetchDirectoryOutputCreatesDirectory`: multiple ids create missing output directory.
- `TestArtifactFetchDuplicateBasenamesUseCollisionSafeNames`: fetching two artifacts named `trace.json` writes `trace.json` and `trace-1.json` or equivalent non-overwriting names.
- `TestArtifactFetchPartialResults`: mixed found/missing ids return public status `partial`.
- `TestArtifactFetchAllFailuresReturnsErrorStatus`: all failed artifact ids return public status `error` with per-id errors.
- `TestUploadFailureDoesNotAppendMessageOrExposeStagedIDs`: partial upload failure returns `message_committed: false`, no staged ids, and no transcript event.
- `TestChangedFileDuringUploadFails`: mutation between initial and final stat fails the send and commits no message.
- `TestSessionsIncludesArtifactEndpointFields`: `sessions` includes artifact endpoint fields.
- `TestInfoIncludesArtifactEndpointMetadataAndLimits`: `info` includes endpoint fields and limits.
- `TestHistoryIncludesArtifactMetadataWithoutBytes`: history includes metadata but never bytes.

Run:

```sh
go test ./cmd/parley -run 'TestSend|TestArtifact|TestSessions' -v
```

Expected: FAIL.

- [ ] **Step 2: Implement CLI surface**

Update `sendCmd`:

- add repeated `--file` flag;
- accept zero or one positional message;
- require at least one message or one file;
- preflight local files before calling adapter control;
- return structured JSON errors for validation failures.

Add `artifactCmd()` with `fetch` subcommand:

- same participation flags as other participant commands;
- one or more artifact ids;
- optional `--out`;
- calls adapter control and prints status-first JSON.

Register `artifactCmd()` in `newRootCmd`.

- [ ] **Step 3: Verify CLI tests pass**

Run:

```sh
go test ./cmd/parley -run 'TestSend|TestArtifact|TestSessions' -v
```

Expected: PASS.

### Task 5: End-To-End Flow, Docs, And Skill

**Files:**
- Modify: `internal/e2e/headless_test.go`
- Modify: `README.md`
- Modify: `skills/parley/SKILL.md`

- [ ] **Step 1: Write failing e2e test**

Add an end-to-end test:

- host starts a room;
- agent joins;
- host sends a message with one or more files;
- agent waits/inboxes and sees artifact metadata inline;
- agent fetches artifacts;
- fetched bytes match originals.
- fetching after `parley stop` fails with an unavailable/endpoint error;
- stop shuts down the artifact listener and attempts room-scoped cleanup;
- remote-style join derives artifact endpoint from descriptor host.

Run:

```sh
go test ./internal/e2e -run TestHeadlessRoomArtifacts -v
```

Expected: FAIL.

- [ ] **Step 2: Update e2e helpers and docs**

Update e2e helpers to drive `send --file` and `artifact fetch`.

Update README and skill docs with:

- `send --file` examples;
- `artifact fetch` examples;
- `info` as endpoint/limit recovery;
- two-port remote tunnel guidance;
- room-lifetime artifact cleanup behavior.

- [ ] **Step 3: Verify e2e and full suite**

Run:

```sh
go test ./internal/e2e -run TestHeadlessRoomArtifacts -v
go test ./... -timeout 30s
sh -n skills/parley/scripts/ensure-parley
```

Expected: PASS.

## Final Verification

Before claiming completion, run:

```sh
go test ./... -timeout 30s
sh -n skills/parley/scripts/ensure-parley
git diff --check
```

Then request a final code review against the design spec and this plan.
