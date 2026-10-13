# Herdr reactive transport proof

Verified 2026-10-13 against Herdr **0.9.3**, socket protocol **22**, schema version **1**, and OpenSSH **10.2p1** (LibreSSL 3.3.6), on macOS. The reactive gate passes: a remote-selected plugin action commits a durable request, its native metadata event reaches a subscriber over locally initiated SSH, and a separate SSH read recovers every pending request after reconnect, restart, and real event-history loss.

The initial transport proof below did not cover the production coordinator, ownership locks, automatic routing, native agent attachment, Linux support, or the artifact CLI override. The later production plugin acceptance section records the installed macOS/Linux Start/Join workflow and exact artifact CLI bytes. The previous 2026-10-12 fixture passed 13 primitive checks using isolated macOS XDG roots. It did not exercise subscriptions, shared-root session isolation, Linux, real user hosts, orchestration, or the CLI override. None of those prior results substitutes for this fresh run.

## Fixture and reproducibility

The fresh fixture uses two named servers, `alpha` and `beta`, for the same OS user and the **same XDG config/state roots**. It creates private temporary SSH host/client keys and a loopback-only sshd. No real Herdr registry, plugin, user SSH configuration, or unrelated master is modified. `StrictHostKeyChecking=yes`, a fixture-only known-host file, `IdentitiesOnly=yes`, public-key-only authentication, and sshd `StrictModes=yes` remain enabled. All advertised and forwarded TCP listeners bind `127.0.0.1`.

The temporary standard-library Python harness lives in the ignored plan workspace at `.superpowers/sdd/2026-10-12-herdr-plugin/transport-fixture.py`. It is not shipped as production tooling. Run it from the repository with installed absolute executable paths:

```sh
python3 .superpowers/sdd/2026-10-12-herdr-plugin/transport-fixture.py \
  --herdr /absolute/path/to/herdr \
  --parley /absolute/path/to/parley \
  --output /absolute/path/to/transport-evidence.json
```

The harness creates an owned private directory beneath the user's home; `/tmp` was rejected by sshd strict authorization because `/private/tmp` is world-writable. The script generates credentials at runtime, removes them in `finally`, stops owned Parley rooms, unlinks the throwaway plugin, reaps every owned child, removes temporary Parley state roots, and removes its fixture root. The resulting JSON replaces the fixture directory with `<fixture>`. The original schema and full sanitized result live in the ignored plan workspace as `transport-evidence.json`. Selected native envelopes are committed in [transport-observed.json](../tests/fixtures/herdr/transport-observed.json). UUIDs, hashes, ephemeral ports, terminal IDs, and PIDs are fixture observations, not stable contract values.

The proof ran these CLI surfaces, with the explicit saved profile ID rather than an inferred label/default session:

```text
herdr --version
ssh -V
herdr --session alpha server
herdr --session beta server
herdr --session alpha session list --json
herdr --session alpha api schema --json
herdr machine list --json
herdr --session alpha agent list
herdr --machine 22222222222222222222222222222222 agent list
herdr --machine 22222222222222222222222222222222 plugin list --json
herdr --machine 22222222222222222222222222222222 plugin action invoke parley-reactive-proof.enqueue
```

The saved fixture profile is enabled, targets `proof-host`, and explicitly names remote session `alpha`. The catalog and discovered-executable cache were seeded solely in the fixture XDG root. This proves saved-profile API dispatch, not interactive `machine add` onboarding or executable installation. A trusted fixture wrapper pins those XDG roots; its SSH invocation of `--version` separately returned `herdr 0.9.3`. Both server endpoints use that same binary/schema.

## Socket and installed-helper resolution

`herdr --session alpha session list --json` returns the native session inventory without a request/result wrapper:

```json
{"sessions":[
  {"default":true,"name":"default","running":false,"session_dir":"<fixture>/config/herdr","socket_path":"<fixture>/config/herdr/herdr.sock"},
  {"default":false,"name":"alpha","running":true,"session_dir":"<fixture>/config/herdr/sessions/alpha","socket_path":"<fixture>/config/herdr/sessions/alpha/herdr.sock"},
  {"default":false,"name":"beta","running":true,"session_dir":"<fixture>/config/herdr/sessions/beta","socket_path":"<fixture>/config/herdr/sessions/beta/herdr.sock"}
]}
```

The subscriber pins the selected inventory socket and canonicalizes it while reachable. The default socket is deliberately absent. The profile's `session:"alpha"`, explicit session inventory, action's injected `HERDR_SOCKET_PATH`, and helper probe canonical path/key agree. A helper must persist this verified path, compare its probe key before reading state, and reject a mismatch. It must not let the remote login shell select a default session.

Remote `plugin list --json` returns `result.type:"plugin_list"`, `plugin_root:"<fixture>/plugin"`, and `manifest_path:"<fixture>/plugin/herdr-plugin.toml"`. The prototype helper is `<fixture>/helper.py`, outside the observed `<fixture>/plugin` install root; it accepts fixture-only socket/state-root arguments. Plugin-root discovery is observed, but installed-helper lookup is **unverified**. Production must resolve its fixed installed executable from trusted registry/probe information and derive plugin roots internally, never expose that prototype's arbitrary path arguments.

Both action invocations receive identical `HERDR_PLUGIN_STATE_DIR` and `HERDR_PLUGIN_CONFIG_DIR`:

```text
<fixture>/state/herdr/plugins/parley-reactive-proof
<fixture>/config/herdr/plugins/config/parley-reactive-proof
```

The action, startup hook, and helper derive the session namespace as lowercase SHA-256 of the actual canonical socket path. Alpha and beta keys differ even though both have pane `w1:p1` and workspace `w1`. The committed fixture retains observed hashes; replacing the private directory with `<fixture>` means those hashes cannot be recalculated from sanitized display paths.

Each session contains separate request/room records and separate config/controller records. The fixture seeds distinctive per-session role/owner values, then actually restarts **both** servers. Startup reads only its own role/owner records; separate SSH probes return alpha's three requests and beta's single request and their own room records. The keys remain stable through restart. This proves the namespace rule, not production authorization/locking for controller claims.

Linking the plugin to already-running servers did not invoke its startup hook. Actual restart invoked it with `invocation_source:"startup"` and `correlation_id:"plugin.startup"`. Immediate activation after linking/enabling therefore needs an explicit setup path.

## Remote action and native event envelopes

The throwaway pane action runs through the selected remote Herdr server. Its injected context includes:

```json
{"workspace_id":"w1","workspace_label":"alpha","tab_id":"w1:t1","focused_pane_id":"w1:p1","focused_pane_status":"unknown","invocation_source":"cli","correlation_id":"cli:plugin"}
```

The action writes a random Join request under its socket-derived `sessions/<key>/requests/`, using exclusive temporary creation, mode `0600`, flush/fsync, and atomic rename. Only afterward it reports a fresh random `parley_request` token. The raw metadata request and response are:

```json
{"id":"<random-marker>","method":"workspace.report_metadata","params":{"workspace_id":"w1","source":"plugin:parley-reactive-proof","tokens":{"parley_request":"<random-marker>"}}}
{"id":"<random-marker>","result":{"type":"ok"}}
```

The fixed subscriber sends one NDJSON request to that pinned Unix socket and drains stdout through SSH initiated on the local machine:

```json
{"id":"join-events-1","method":"events.subscribe","params":{"subscriptions":[{"type":"workspace.metadata_updated"}]}}
```

Observed native acknowledgement:

```json
{"id":"join-events-1","result":{"type":"subscription_started"}}
```

Observed event (fixture marker retained):

```json
{"data":{"type":"workspace_metadata_updated","workspace":{"active_tab_id":"w1:t1","agent_status":"unknown","focused":true,"label":"alpha","number":1,"pane_count":1,"tab_count":1,"tokens":{"parley_request":"<random-marker>"},"workspace_id":"w1"}},"event":"workspace_metadata_updated"}
```

The subscription filter uses **dot notation**; emitted `event` and `data.type` use **underscore notation**. Events have no request ID and are not nested in `result`. Manifest metadata hooks do not receive these native API events. The long-lived subscriber is plugin code; no production dependency on Herdr's internal `remote-api-bridge` is needed.

An invalid subscription receives an `error` envelope with code `invalid_request` and a message beginning `invalid request: unknown variant \`invalid.event\``. The full diagnostic is tied to the installed schema; do not parse its prose to identify errors.

## Subscribe-first recovery and real history loss

The fresh run performs this order:

1. Invoke the remote action once **before** subscribing. Its request survives without a live listener.
2. Start the outbound SSH subscriber and verify `subscription_started` before any queue read.
3. Invoke the remote action twice more, changing the marker twice before the first read. Both metadata events arrive locally.
4. Fetch pending state through a **separate outbound SSH process** while the subscriber remains running. The helper returns all three request files, including the pre-subscription request.
5. Disconnect the subscriber, restart alpha, resubscribe, and read the same three files. `workspace.list` no longer contains metadata tokens; durable requests and the room record remain accessible.
6. Restart beta. Its helper still returns only its one request and own room record; both startup hooks read the correct config/controller namespaces.
7. Open a second subscriber, verify its acknowledgement, stop draining it, and issue **2,400 real metadata updates** on alpha. Native retained history overflows and the slow stream receives:

```json
{"id":"slow-events","error":{"code":"events_lost","message":"event subscription fell behind retained history; resubscribe and resync with session.snapshot"}}
```

8. Establish a fresh outbound subscription and reread alpha's pending queue. All three durable requests remain available.

The original harness reused an append-only output file, so old content could satisfy recovery readiness. That evidence did not establish fresh acknowledgement order. The corrected fresh run creates an exclusive, empty output file and a distinct request ID for **each** connection. It parses and requires that connection's `subscription_started` response before starting the separate queue-read SSH process. A controlled stale-content test fails against the old readiness check and passes against the corrected parser.

The sanitized fixture now captures the explicit order (monotonic timestamps are retained in JSON):

| Reconciliation | Fresh stream | Fresh acknowledgement ID | Ack order | Read start | Read complete | Pending requests |
| --- | --- | --- | --- | --- | --- | --- |
| Initial | `alpha-stream-1.jsonl` | `join-events-1` | 2 | 3 | 4 | 3 |
| After restart | `alpha-stream-2.jsonl` | `join-events-2` | 6 | 7 | 8 | 3 |
| After history loss | `alpha-stream-3.jsonl` | `join-events-3` | 10 | 11 | 12 | 3 |

All three reads return identical alpha request IDs. Each acknowledgement is a native response from its newly started SSH relay; file existence or stale output alone cannot satisfy readiness. This trace establishes fresh subscribe-before-read ordering for initial reconciliation, restart/reconnect, and history-loss recovery.

`events_lost` is **observed on the real binary**, not a synthetic frame. Frame counts before the error vary with socket buffers and scheduling. Herdr 0.9.3's [event hub source](https://github.com/herdrdev/herdr/blob/v0.9.3/src/api/event_hub.rs) retains 512 events; the bounded test exceeds that history after acknowledgement. [Subscription source](https://github.com/herdrdev/herdr/blob/v0.9.3/src/api/subscriptions.rs) confirms the error and recovery instruction. The error closes that stream; production must reconnect, subscribe first, and reconcile authoritative/durable state. Treat events as invalidations rather than an authoritative request queue. Task 4 should still regress this exact error frame synthetically, including an event arriving while a queue read is in flight.

## Native pane and conversation identity

The real pane envelope includes `terminal_id`, `pane_id:"w1:p1"`, `workspace_id:"w1"`, `tab_id:"w1:t1"`, and `agent_status:"unknown"`. Both local and selected-remote `agent list` return:

```json
{"id":"cli:agent:list","result":{"agents":[],"type":"agent_list"}}
```

`agent.get` takes `params:{"target":"w1:p1"}`, not `pane_id`. The actual shell fixture returns:

```json
{"id":"proof","error":{"code":"agent_not_found","message":"agent target w1:p1 not found"}}
```

The bundled schema defines `AgentSessionInfo` with required `source`, `agent`, `kind`, and `value`; `agent_session` is optional/nullable on native pane/agent information. There is **no observed `agent_session.value` in these shell panes**. Production must obtain native conversation identity using the existing Herdr harness integration. Never infer it from labels, titles, pane IDs, or fabricated session reports. This proof does not validate attachment to a real running coding agent.

## Owned OpenSSH master and two independent forwards

The listener subscription itself opens no callback/socket/room forwards. A separate plugin-owned master starts with no forwards:

```text
ssh -F <fixture-ssh-config> -M -S <private-control-path> -N
  -o BatchMode=yes -o ExitOnForwardFailure=yes -o ControlPersist=no
  -o ServerAliveInterval=30 -o ServerAliveCountMax=3 proof-host
```

A separate protocol and HTTP artifact forward is added for each route:

```text
ssh -F <fixture-ssh-config> -S <private-control-path> -O forward
  -R 127.0.0.1:0:127.0.0.1:54394 proof-host
# stdout: 54396
ssh -F <fixture-ssh-config> -S <private-control-path> -O forward
  -R 127.0.0.1:0:127.0.0.1:54393 proof-host
# stdout: 54397
```

Assigned port output is a decimal port plus newline on stdout; parse it only after successful exit. Cancellation with the **allocated** port succeeds:

```text
ssh -F <fixture-ssh-config> -S <private-control-path> -O cancel
  -R 127.0.0.1:54396:127.0.0.1:54394 proof-host
```

The fixture also verified cancellation using the original zero-port request in the partial-failure test. Production should retain exact forward tuples and assigned ports.

For `-L`, the caller allocates a candidate local port; OpenSSH does not return dynamic local port allocation:

```text
ssh -F <fixture-ssh-config> -S <private-control-path> -O forward
  -L 127.0.0.1:54406:127.0.0.1:54404 proof-host
ssh -F <fixture-ssh-config> -S <private-control-path> -O cancel
  -L 127.0.0.1:54406:127.0.0.1:54404 proof-host
```

For **both `-R` and `-L`**, real Parley rooms join through the protocol forward, exchange a uniquely tagged message, and fetch exact artifact bytes through the separate forwarded HTTP port. The macOS processes use separate private Parley state roots. Both endpoints are loopback sshd on one computer; this does not represent different physical hosts.

The participant CLI's artifact endpoint override is not used in this proof: artifact bytes are fetched directly with HTTP through the second forward. Automatic routes, CLI remapping, and production artifact positive/negative probing require later tasks.

Partial two-port failure is tested independently for both directions. The first forward succeeds; an owned listening socket occupies the requested second port; the second `-O forward` exits **255**. The newly added first route is cancelled. For `-L`, a connection to the cancelled listener is refused, proving no remaining listener. For `-R`, cancellation returns success. No participant is joined during either partial-failure test. SSH emits `mux_client_forward: forwarding request failed` and `muxclient: master forward request failed`; production should use exit status and normalized errors rather than depend on stderr wording.

## UI close, cleanup, and limits

A private Herdr UI process is attached under a PTY with explicit terminal dimensions and a controlling terminal. Closing that client with SIGKILL leaves the independently owned SSH subscriber and master alive. `ssh -O check` still returns `Master running`; a subsequent **remote-selected action** produces another metadata event over the surviving relay. This is real UI-process-close survival of the primitives.

Default detach keystrokes did not exit the automated PTY client, so **graceful keyboard detach remains unverified**. This fixture does not contain the production coordinator or installed helper. Task 7 must prove their survival when the actual UI closes/detaches, including tunnels with live participant bindings.

Cleanup stops owned Parley rooms, cancels/exits the owned master, unlinks the throwaway plugin, stops both named servers, reaps children, removes generated SSH credentials and all private fixture roots. It never stops existing agents or unrelated SSH masters.

Linux remains unverified. Docker reports a Linux daemon, but it had no images; a bounded attempt to run `alpine:3.22` stalled at image acquisition and the owned client was terminated. No Linux Herdr process or Linux SSH policy was tested. Real saved user hosts and their SSH policies were not used. The macOS reactive transport gate can proceed; later platform and production end-to-end acceptance remain separate.

Repository validation after the proof: `go test ./... -timeout 30s` passed. This task changes only documentation and sanitized observed fixtures. No production behavior is implemented.

## Production plugin acceptance (macOS controller / Linux remote)

On 2026-10-13, the shipped manifest and production CLI were development-built and natively linked on both Herdr 0.9.3 hosts. The remote Linux ARM64 binary was built from the same source and transferred only into the owned development checkout. This verifies native development build/link, not GitHub installation or a remote Go toolchain build. Both hosts used private, short, immutable runtime-root pins before any action. SSH remained strict and loopback-only; controlled native idle agents used official session hooks and made no paid harness calls.

The tested entrypoints were `parley herdr setup`, `parley herdr startup` twice, and native `plugin.action.invoke` for `parley.start` / `parley.join`. Actions opened the declared managed popup; input was submitted through owned Herdr UI PTYs. No manual participant setup, room-specific port command, adapter launch, or `agent.prompt` call supplied these results. The local coordinator had been restarted after the request-array and terminal-identity adapter fixes before either cross-host Join.

| Native workflow | Source TCP / HTTP | Automatically routed TCP / HTTP | Result |
| --- | --- | --- | --- |
| Linux Start, macOS Join | 45593 / 34407 | macOS 61602 / 61603, two `-L` forwards | Room `room-6cd8dd5ac1053e7f75c6`; host `psn_fde40ff7055fdc7e57b9`; participant `psn_5bea75e04d5a804706ea` |
| macOS Start, Linux Join | 61922 / 61921 | Linux 34147 / 42571, two `-R` forwards | Room `room-f250f158314d21092826`; host `psn_36e004c2d4ff704dfa5f`; participant `psn_b550620c557a6916dfed` |

The macOS Join request `db1bfeda5625407b5faa071aa78ecb5a` and Linux Join request `22cb1ce4a5807c2c30c1cc2aa8c7dc38` remained immutable with host-local `self` targets. Their original-host outcomes became `complete` under the same unique local controller identity. The Linux request was processed by the existing locally initiated event listener after its native workspace marker; the form only polled its own durable outcome. No caller issued a remote mutation or a manual coordinator drain. The local room had no usable metadata tokens during this run, so the reverse Join also exercised authoritative durable room fallback.

Both endpoints sent existing Parley messages and files. The receiving command results contained the exact opposite-host message texts. `parley artifact fetch` returned `downloaded`; independently reading each fetched file matched all bytes, including NUL and non-UTF-8 data:

| Route | Receiving host | Bytes | SHA-256 |
| --- | --- | --- | --- |
| `-L` | macOS | 29 | `6a237d61449c9295d0a3c5aaedccc0d4bd2512ebe5abc4bb1085739e06198a04` |
| `-L` | Linux | 28 | `c941ed097bcba395d08648df37be501f36ef5cbfba4aec94c2302899eee8214a` |
| `-R` | macOS | 29 | `d7d0fc4b9b6373f10f0652621d254c3fed7de7ec3a73579bcb82c1f475b78572` |
| `-R` | Linux | 28 | `469503abe64f870468964c0effadbf694f1c967d9ab4309ff923ceb09ad20ba9` |

A second native Linux Join with distinct request `1ab327bf9027c084a1dad789af96181e` returned the original participant session and both original routed ports. A direct native same-host Join (`ea9d0b6c4557e6c8bd0dabc3ffcbdf37`) returned the original Linux host session and source endpoints, without acquiring another route. The macOS Start action captured `w1:p2`; focusing underlying `w1:p1` before submission still bound the room to the original terminal and native session. Native DEL/backspace produced the exact intended topic. Native Escape left room and request counts unchanged.

The initial native run exposed three adapter assumptions. Empty durable requests are an array, while error envelopes are objects; the decoder now enforces that shape. Native `agent.get` accepts pane/name, so captured terminal identity is resolved through `agent.list`, followed by `agent.get` and identity revalidation. `pane.report_metadata` requires `pane_id`. Focused RED/GREEN regressions cover each correction. The rebuilt action/form subsequently created `room-c2c9d435c48b14c6ded0` and native `pane.get` reported its exact room/TCP63791/HTTP63790 tokens, with no metadata warning. That final Start also verified visible input feedback. Earlier warning/error attempts are not counted as passing metadata or mutation evidence.

The source contracts are pinned to [native terminal resolution](https://github.com/herdrdev/herdr/blob/v0.9.3/src/app/terminal_targets.rs), [plugin environment and panes](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/plugins.mdx), and [socket schemas](https://github.com/herdrdev/herdr/blob/v0.9.3/docs/next/website/src/content/docs/socket-api.mdx). A fixture UI onboarding modal initially obscured the popup. Its [native input handler](https://github.com/herdrdev/herdr/blob/v0.9.3/src/client/shell/overlay_input.rs) accepts Enter to open settings; Escape then closes settings. This only updated isolated Herdr onboarding state and installed nothing.

### Coverage boundaries

Current native actions prove both automatic directions, native identity capture, durable fallback, distinct-request reuse, same-host Join, real messages, and exact artifact CLI bytes. Production tests cover fresh acknowledgement after event loss, coalesced durable queues, lost results, concurrent direct/helper host locks, original cursor preservation, shared-root namespace isolation, strict artifact probes, partial forwarding, occupied-port remap, ambiguous cancellation, and unavailable routes without participant recreation. The earlier forced native `events_lost`, server restart, SSH allocation/cancellation, and artifact probe experiments remain primitive evidence; they were not repeated through every final popup scenario. No notification, readiness prompt, unread replay, resume, automatic adapter reconnection, or real paid harness conversation was tested or shipped.

### Final lifecycle and cleanup

After both owned macOS UI clients and the Linux UI detached, both original participants still sent messages through their existing routes, and each opposite host received them. All four original controlled native terminal/session identities remained present. Only the owned participant sessions were left and the three owned room servers stopped through existing CLI commands; native servers and agents stayed alive.

The coordinator was then disabled through the native plugin API, rebuilt action/form binaries were already installed, and the final amended coordinator was restored through startup. Startup repeated successfully without another coordinator or new adapter. Disabling the remote plugin made the final coordinator cancel its exact remote subscription and persist that peer as disabled. Disabling the local plugin removed its private ready socket. All ten recorded source/routed TCP and HTTP ports were closed. This final lifecycle check used the later metadata, display, request-shape, and saved-peer-deactivation amendments; it does not claim the earlier running daemon hot-reloaded code.

The fixture's named native servers and four idle agents were deliberately retained for review. Owned core room/participant processes, event readers, and SSH masters were closed. No user root, unrelated process, shared SSH master, or harness configuration was cleaned. Build, exact lint, and the full race gate were rerun only after concrete native producer/lifecycle amendments; focused checks were used while iterating.
