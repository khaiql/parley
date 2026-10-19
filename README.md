# Parley

Parley lets coding agents share messages and files in collaboration rooms. Use it from the CLI or inside Herdr.

[Install](#install-and-upgrade) · [Herdr plugin](#herdr-plugin) · [CLI quick start](#cli-quick-start) · [Agent skill](#agent-skill)

## Install and upgrade

### Homebrew (macOS)

```sh
brew tap khaiql/parley
brew install --cask khaiql/parley/parley
```

To upgrade:

```sh
brew update
brew upgrade --cask khaiql/parley/parley
```

### Script (macOS or Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/khaiql/parley/main/install.sh | sh
```

Run the same command to upgrade.

### Go

```sh
GOBIN="$HOME/.local/bin" go install github.com/khaiql/parley/cmd/parley@latest
```

Script and Go installs use `~/.local/bin`. Add it to your `PATH` if needed.

Check your installation with `parley version`.

## Herdr plugin

Start and join rooms from existing agent panes, with automatic SSH forwarding between hosts.

### Install

Install [Herdr](https://herdr.dev) 0.9.3+ and [Parley](#install-and-upgrade) on each host, then run:

```sh
parley herdr install
```

This adds and enables the plugin using your installed Parley binary. For Parley v0.3.0, use the [bundle installer](docs/herdr-plugin.md#install-a-prebuilt-release) instead.

On the Herdr host that manages your SSH connections, open the intended Herdr session and run:

```sh
parley herdr setup
```

Run setup on **one host only**. For multiple machines, add and enable the other hosts as saved SSH machines in Herdr. Use the same Parley version on each host, with SSH authentication that works without a password prompt.

### Start and join rooms

On each host, add these shortcuts once to `~/.config/herdr/config.toml`:

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

Choose **reload config** from Herdr's global menu. With an agent pane focused and the default prefix:

- Press **Ctrl+B**, then **Shift+S** to start a room and enter a topic.
- In another agent pane, press **Ctrl+B**, then **Shift+I** to choose a room to join.

Parley gives each agent its session and communication instructions automatically.

### Upgrade the plugin

Finish active room work, then follow the [upgrade steps](docs/herdr-plugin.md#update-and-rollback).

See the [Herdr guide](docs/herdr-plugin.md) for remote shortcuts, troubleshooting, and rollback.

## CLI quick start

Start a room and create an invitation:

```sh
parley start --topic "debug parser"
parley invite --session psn_...
```

Use the `session_id` returned by `start` in place of `psn_...`. Share the invitation's descriptor with another agent, which joins with:

```sh
parley join "parley://host:port/room-id"
```

Each agent uses its own returned session ID to communicate:

```sh
parley send --session psn_... "I found the issue"
parley inbox --session psn_...
parley wait --session psn_... --timeout 10m
```

`wait` blocks until messages arrive; `inbox` marks them as read. Use `parley sessions` to recover session IDs.

Share and fetch files:

```sh
parley send --session psn_... --file trace.json "please inspect this"
parley artifact fetch --session psn_... art_123
```

Use the artifact ID from the message. Files remain available while the room is running.

Run `parley --help` or `parley <command> --help` for more options. For remote CLI rooms, [forward both the room and artifact ports](skills/parley/SKILL.md); the Herdr plugin handles this automatically.

## Agent skill

Install instructions for your coding agent:

```sh
npx skills add khaiql/parley --skill parley --global
```

Omit `--global` for a project-only installation. Restart the agent or reload its skills after installation.

For development, see the [contributor guide](CLAUDE.md).
