# Parley

Parley is a headless, JSON-first room server for coordinating coding agents through short CLI commands.

[Install / upgrade](#install-and-upgrade) · [Herdr plugin](#herdr-plugin) · [Agent skill](#agent-skill)

## What It Does

Parley starts a local room, writes a JSONL event log, and gives each participant a local adapter with a Unix control socket. Agent skills can call small commands such as `wait`, `inbox`, `send`, and `history` without owning a long-running terminal UI.

## Features

- **Headless rooms** - TCP room server with line-delimited JSON protocol
- **Participant adapters** - local mirrors and control sockets per agent
- **JSON command output** - scriptable responses for agent skills and automation
- **Event log first** - append-only room history with transcript filtering
- **Descriptors** - `parley://host:port/room-id` invite strings for handoff
- **Local runtime metadata** - session-scoped room and participant state in the first writable runtime root Parley can find

## Install and upgrade

### Homebrew (macOS)

Install the tap explicitly, then install the cask:

```sh
brew tap khaiql/parley
brew install --cask khaiql/parley/parley
parley version
```

Already installed? Refresh the tap before upgrading:

```sh
brew update
brew upgrade --cask khaiql/parley/parley
"$(brew --prefix)/bin/parley" version
```

If Homebrew still shows an older release, compare `brew info --cask khaiql/parley/parley` with the [tap's current cask](https://github.com/khaiql/homebrew-parley/blob/main/Casks/parley.rb). `brew update` refreshes that local definition. A GitHub tag alone is not proof that tap publishing succeeded; check the [Release workflow](https://github.com/khaiql/parley/actions/workflows/release.yml).

If Homebrew reports the new version but `parley` still runs an old one:

```sh
type -a parley
"$(brew --prefix)/bin/parley" version
```

An earlier `~/.local/bin/parley`, `~/.parley/bin/parley`, or Go installation can take precedence over Homebrew. Remove or rename only the old binary you recognize, or put Homebrew's `bin` directory first on `PATH`. Open a new shell after changing your shell profile. Parley runtime data in `~/.parley` is separate; do not delete that directory to fix a binary conflict.

### Script (macOS or Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/khaiql/parley/main/install.sh | sh
"$HOME/.local/bin/parley" version
```

Run the same command to upgrade. The installer downloads the latest release, verifies its SHA-256 checksum, and atomically installs `~/.local/bin/parley`. It reports an older binary taking precedence on `PATH`. It does not edit your shell profile. If `~/.local/bin` is absent from `PATH`, use the printed absolute path or add `export PATH="$HOME/.local/bin:$PATH"` to your shell profile once. Homebrew handles `PATH` through its normal shell setup on macOS.

Older script installs used `~/.parley/bin`. New installs leave that binary and all runtime data alone; check `type -a parley` when migrating.

### Go (contributors)

```sh
mkdir -p "$HOME/.local/bin"
GOBIN="$HOME/.local/bin" go install github.com/khaiql/parley/cmd/parley@latest
"$HOME/.local/bin/parley" version
```

Go builds report version `dev`; use release binaries when matching versions across machines.

## Herdr plugin

Start and join rooms from existing agent panes. Parley manages both room and artifact SSH tunnels. Install on **every participating laptop/host**; choose **one controller** for cross-machine routing.

**Release availability:** `parley herdr install` is new and is not in v0.3.0. Until the next release, v0.3.0 users must use the [prebuilt bundle instructions](docs/herdr-plugin.md#install-a-prebuilt-release). A source build of this revision supports the command below.

### Install Parley, then add the plugin

Requirements: [Herdr](https://herdr.dev) 0.9.3+ and Parley on each host. Use [Homebrew or the script above](#install-and-upgrade) to install Parley first. Then add the plugin with one command:

```sh
parley herdr install
```

This links and enables Parley in Herdr using **the binary you just installed**, plus its embedded manifest and agent skill. No second binary download, repository clone, Go installation, or manual file copy is needed. Registration works before starting Herdr. Repeating the command for the same installation is safe. It keeps a hard link to that binary (a copy across filesystems), so Homebrew cleanup does not break a registered plugin.

Verify registration:

```sh
herdr plugin list --json
```

Look for plugin `parley`, `enabled: true`, and the expected version. Once Herdr is running, `herdr plugin action list --plugin parley` lists `parley.start` and `parley.join`.

### Choose the controller

On the controller laptop, open the intended Herdr session and run:

```sh
parley herdr setup
```

Run setup **only on the controller**, not the other laptop. This starts its coordinator immediately; later Herdr startups restore it. Add the other host as an enabled saved SSH machine in Herdr. SSH must work without an interactive password prompt (`ssh -o BatchMode=yes <saved-target> true`). Install and enable the plugin on that host too, under the same OS account Herdr uses.

### Add Start / Join shortcuts

On **each host**, add these blocks once to `~/.config/herdr/config.toml`, preserving your other settings:

```toml
[[keys.command]]
key = "prefix+shift+s"
type = "plugin_action"
command = "parley.start"
description = "Parley: Start room"

[[keys.command]]
key = "prefix+shift+i"
type = "plugin_action"
command = "parley.join"
description = "Parley: Join room"
```

Choose different keys if yours are already assigned. In Herdr's global menu, choose **reload config** on each connected host. With the default prefix, focus an existing agent pane, press **Ctrl+B**, release, then **Shift+S** to start a room. Enter a topic. In another agent pane, use **Ctrl+B**, then **Shift+I**, and choose the room. Parley gives each agent its session and communication instructions automatically.

For SSH machines, configure the remote host's shortcuts too. See [remote keybindings and diagnostics](docs/herdr-plugin.md#set-up-focused-pane-shortcuts) if actions do not appear.

### Upgrade the plugin

Upgrade the CLI using its original install method. Existing plugin registrations keep their previous binary until you explicitly update them.

Finish active routed work first. Disable Parley on the controller and wait for its old `parley herdr broker` processes to exit (normally after its two-second polling cycle; check `ps -axo pid,command`). Then disable/update each participant, followed by the controller:

```sh
herdr plugin disable parley
parley herdr install
```

Run `parley herdr setup` again on the controller after every host has the same release. Do not restart agents or delete their state. The install command refuses to replace a different enabled plugin, with instructions to disable it first. Keep previous plugin directories until old rooms/agents no longer reference them. See [rollback and multi-session precautions](docs/herdr-plugin.md#update-and-rollback).

The [full Herdr guide](docs/herdr-plugin.md) covers pinned v0.3.0 bundles, remote setup, diagnostics, and rollback. `install-herdr.sh` is the legacy **bundle staging** installer; `install.sh` installs the **CLI**. Neither script chooses a controller or modifies Herdr shortcuts.

## Quick start

```sh
PARLEY=parley
```

### Use

```bash
# Start a room as the host participant
"$PARLEY" start --topic "debug parser" --role host
SESSION_ARGS="--session psn_..."

# Emit the descriptor for another participant
"$PARLEY" invite $SESSION_ARGS

# Join from another agent shell
"$PARLEY" join "parley://127.0.0.1:49231/01j..." --role "auth reviewer"

# Save the command_args returned by start or join, then use it for room and participant commands
SESSION_ARGS="--session psn_..."
"$PARLEY" wait $SESSION_ARGS --timeout 10m
"$PARLEY" send $SESSION_ARGS "I found the issue"
"$PARLEY" send $SESSION_ARGS --file trace.json "please inspect this"
"$PARLEY" send $SESSION_ARGS --file trace.json

# Recover local session handles if needed
"$PARLEY" sessions
```

`start` and `join` generate a participant name when `--name` is omitted, using `adjective_noun_number` format. Prefer `--session` for room and participant commands. Use `sessions` to list local session handles on the machine. Use `--room` and `--name` as an explicit fallback for participant commands. Bare participant commands only work when exactly one local participation exists.

`wait` is non-consuming: it blocks until unread message events are available and returns them with `status: "ready"`, but only `inbox` advances the seen cursor. Use `inbox --peek` to inspect unread events without acknowledging them.

Parley stores runtime state in `~/.parley` by default. Set `PARLEY_STATE_DIR` to force a shared state directory. Without an override, Parley probes common agent-friendly roots in order: `~/.parley`, `$XDG_RUNTIME_DIR/parley`, `$XDG_STATE_HOME/parley`, the user cache directory, the OS temp directory, and finally `.parley` in the current working directory.

### Room Artifacts

Share files as room-owned artifacts with `send --file`. The file bytes are uploaded to the room server first, then the message is committed with artifact metadata (`id`, `name`, `size`, and `sha256`) visible in `inbox` and `history`.

```bash
"$PARLEY" send $SESSION_ARGS --file trace.json "please inspect this"
"$PARLEY" send $SESSION_ARGS --file before.log --file after.log "compare these"
"$PARLEY" send $SESSION_ARGS --file trace.json
```

Fetch artifacts explicitly by id:

```bash
"$PARLEY" artifact fetch $SESSION_ARGS art_123
"$PARLEY" artifact fetch $SESSION_ARGS art_123 art_456 --out ./parley-artifacts
```

Artifact limits are fixed in v1: 100 MiB per file, 10 files per message, and 250 MiB total per message. Directories and symlinks are rejected; hidden and empty regular files are allowed. Artifact bytes remain available only while the room server is active. `parley stop` makes the room inactive and artifact cleanup is room-scoped best effort.

Use `"$PARLEY" info $SESSION_ARGS` to rediscover artifact endpoint metadata and limits for an active session. `sessions` includes the key artifact endpoint fields for local recovery.

For remote participants, create your own tunnels to both ports returned by `start`, `invite`, `info`, or `sessions`: the room protocol `local_port` and the artifact HTTP `artifact_local_port`. Share a descriptor that uses the room protocol tunnel host and port with the same room id. Parley does not create or manage tunnels. If the artifact tunnel uses a different local port, pass its HTTP URL when joining:

```bash
"$PARLEY" join parley://127.0.0.1:49001/room-1 --name reviewer \
  --artifact-endpoint http://127.0.0.1:49002/rooms/room-1/artifacts
```

The override must use `http`, an explicit port from 1 to 65535, and the exact `/rooms/<room-id>/artifacts` path. Credentials, query strings, and fragments are rejected. Without this flag, Parley uses the descriptor host and the room's advertised artifact port. The override applies only to the joining participant; room metadata remains unchanged.

## Agent Skill

Parley ships an agent skill at `skills/parley/SKILL.md` for coding agents that
support `SKILL.md`-style skills. The skill tells agents how to bootstrap the
`parley` binary, start or join rooms, handle `parley://` descriptors, and parse
command JSON by `status`.

### Install With `npx skills`

The easiest install path is the open
[`vercel-labs/skills`](https://github.com/vercel-labs/skills) CLI.

Install the Parley skill globally:

```bash
npx skills add khaiql/parley --skill parley --global
```

Install it into the current project instead of globally:

```bash
npx skills add khaiql/parley --skill parley
```

For unattended setup, add `--yes`:

```bash
npx skills add khaiql/parley --skill parley --global --yes
```

To target a specific agent, pass the agent id supported by `skills`:

```bash
npx skills add khaiql/parley --skill parley --agent <agent-id>
```

After installing, restart the agent or reload its skills. The installed skill
runs its bundled `scripts/ensure-parley` helper before Parley workflows; that
helper uses `PARLEY_BIN`, `PATH`, `~/.local/bin/parley`, legacy
`~/.parley/bin/parley`, Homebrew, the
installer script, or `go install` to locate or install the binary.

## Architecture

```
cmd/parley/         CLI commands and JSON error handling
internal/
  model/            Event envelope, payloads, participant and room metadata
  descriptor/       parley:// descriptor parse/format helpers
  paths/            Per-user room paths and file permissions
  jsonout/          JSON success and error response helpers
  eventlog/         Append/read/query JSONL event logs
  protocol/         V1 request/response/event wire types over NDJSON
  server/           Headless TCP room server and local control socket
  adapter/          Participant TCP adapter, mirror log, and control API
  runtime/          Active room, invite, and participant runtime metadata
  e2e/              Headless room integration coverage
```

### How It Works

```
parley start -> room server -> event log
      |             ^             |
      v             |             v
 host adapter <- NDJSON -> participant adapter
      |                           |
      v                           v
 parley send/wait         parley send/wait
```

The server assigns sequence numbers, appends room events, and broadcasts them to connected adapters. Each adapter keeps a local event mirror and exposes a Unix control socket so short-lived CLI commands can inspect state, wait for unseen messages, or send responses.

## Development

```bash
go test ./... -timeout 30s
go test ./... -timeout 30s -race
go test ./internal/e2e -run TestHeadlessRoomTwoParticipants -count=100
```

## Release

Push a SemVer tag such as `v0.1.0` to run the GoReleaser workflow. The workflow publishes standalone CLI archives, versioned Herdr plugin bundles (manifest, `bin/parley`, and agent skills) for Linux/macOS amd64/arm64, a plugin installer, and SHA-256 checksums. The [plugin guide](docs/herdr-plugin.md) covers installation, updates, and rollback. It also updates the Homebrew cask in `khaiql/homebrew-parley`; it requires a `HOMEBREW_TAP_GITHUB_TOKEN` secret with contents write access to that tap repository.

See [CLAUDE.md](CLAUDE.md) for detailed architecture docs, package dependencies, and contributor conventions.
