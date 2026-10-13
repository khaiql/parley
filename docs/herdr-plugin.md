# Herdr Start and Join

Install Parley on every participating host. Herdr 0.9.3 or newer, Go, and OpenSSH are required. The first manifest builds the CLI during installation.

```sh
herdr plugin install khaiql/parley
parley herdr setup
```

Run setup on the local controller, with the intended Herdr session selected. Setup records a unique controller identity for that server socket and starts its detached coordinator immediately, including when the server is already running. Later Herdr server startups restore that coordinator. Linking or enabling a plugin does not run Herdr startup hooks; run setup after first activation. Unconfigured and participant sessions do not launch a coordinator.

For development, build before linking the absolute checkout; link does not run builds:

```sh
go build -o bin/parley ./cmd/parley
herdr plugin link /absolute/path/to/parley
parley herdr setup
```

Enable the intended saved SSH profiles in Herdr. Each remote profile must identify its named Herdr session, or select one uniquely verified running session. Install/link the same plugin on those hosts normally. The coordinator verifies the installed helper and claims its session before subscribing. It does not copy a helper to a remote host. A different remote controller owner, missing helper, disabled plugin, incompatible capability, authentication failure, or host-key refusal remains visible in the coordinator log. Configure SSH permission and trust normally; Parley never bypasses either.

Invoke **Parley: Start room** from an existing agent pane. Enter a nonempty topic and press Enter. The popup captures the original terminal and native agent identity before opening. It creates a local room and shows its room ID, session ID, and existing Parley command instructions. Escape, EOF, empty input, control characters, and topics over 512 bytes do not submit work.

Invoke **Parley: Join room** from another existing agent pane. Enter the room ID. A same-host room joins directly without SSH. Cross-host Join persists a request, reports its marker through the native socket, and waits for the local coordinator. The coordinator acquires both the message and HTTP artifact routes before invoking the fixed host-local Join helper. Both initiating-host directions work: local room with remote participant uses two reverse forwards; remote room with local participant uses two local forwards. Joining between two different remote hosts is unsupported. A remote-only UI needs the local controller's saved SSH profile active; the remote popup never starts another controller.

The form shows a request ID for pending or uncertain work. Inspect the original host's durable status before retrying:

```sh
parley herdr status
```

Pending requests remain saved if the popup times out. Lost events trigger a fresh acknowledged subscription and durable queue read. Duplicate delivery reuses the original binding/session; an unavailable route or disconnected adapter remains unavailable. Parley does not blindly recreate a participant or reset its cursor. Direct Join publishes its outcome before reporting completion. Completed receipts survive a later controller assignment; incomplete ownership conflicts require explicit recovery.

Plugin state and config use the full canonical Herdr socket namespace, including when named sessions share plugin roots. A private immutable runtime-root pin keeps native actions and SSH helpers on the same trusted Parley runtime. Requests cannot select a runtime root, executable, or socket. For an isolated fixture, set a short trusted `PARLEY_STATE_DIR` before activation and native actions; long runtime paths can exceed Unix socket limits.

Detaching the Herdr UI leaves the owned coordinator, event readers, and SSH masters running. Disabling the local plugin closes only those owned readers and masters. Disabling a remote plugin or profile closes that session's owned reader and master; its existing participant stays alive, with unavailable routes. Profile refresh runs every 30 seconds; plugin status checks run every two seconds. Empty queues do not trigger room discovery or forwarding.

Returned session instructions use existing Parley messaging and artifacts. This phase adds no automatic receive hooks, readiness prompts, harness changes, notification workers, participant resume/replay, adapter reconnection, or agent launch.

Native API contracts follow the [Herdr 0.9.3 plugin documentation](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/plugins.mdx) and [socket API](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/socket-api.mdx).
