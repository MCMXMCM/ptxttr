# Plain Text Nostr

Plain Text Nostr is a local-first Nostr reader and writer. The macOS app runs the Go web application on loopback and presents it in a secure Electron shell, so relay traffic, accounts, preferences, and the SQLite cache stay on the user’s machine while the UI keeps normal browser navigation.

This repository contains local application and desktop release workflows only. It intentionally does not contain hosted infrastructure or production operator data.

## macOS desktop app

Requirements for development are macOS 12 or newer, Go, Node.js/npm, Xcode Command Line Tools, and `lipo`.

```sh
npm install
make desktop-dev
```

Useful targets:

- `make desktop-dev` builds the universal Go sidecar and starts Electron Forge.
- `make desktop-build` creates an ad-hoc-signed universal application bundle for local testing.
- `make desktop-package` creates universal macOS distributables.
- `make desktop-release` requires signing and notarization environment variables, then builds the release DMG and ZIP.

The packaged sidecar is `resources/bin/ptxt-nstr-server`. Electron starts exactly one copy on `127.0.0.1:24787`, waits for `/healthz`, and shuts it down when the last window closes. A port collision or startup failure stays on a diagnostic screen with Retry, Open Logs, and Quit actions.

The app uses bundle ID `com.ptxttr.desktop` and stores fresh state under:

```text
~/Library/Application Support/Plain Text Nostr/
├── Chromium cache, accounts, and preferences (no desktop event database)
└── local/ptxt-nstr.sqlite
```

There is no Wails data migration. Older browser state, keys, accounts, IndexedDB, and SQLite files are not read or copied.

### Navigation

- ⌘T creates a native macOS tab at Home; ⌘W closes the active tab.
- ⌘-click, middle-click, `target="_blank"`, and same-origin `window.open()` create native tabs.
- Tabs share the persistent Chromium session and Go cache, but keep independent history and scroll state.
- ⌘[, ⌘], Reload, and macOS two-finger Back/Forward swipes use Chromium’s navigation history. The app opts fresh installs into fluid swipe tracking and retains a precise-trackpad fallback for configurations where macOS does not deliver the native swipe event.
- The macOS title bar uses native hidden-inset, translucent window chrome; its traffic lights, tabbing, and inactive-window appearance remain system-native.
- Two-finger click on selected text or a link opens a native macOS context menu. Text supports copy, edit commands, Dictionary Look Up, spelling suggestions, and macOS Services; local links can open in a foreground or background native tab.
- External HTTP(S) links open in the system browser. Non-loopback main-frame navigation and unsupported schemes are blocked.

Renderers use Chromium sandboxing with Node integration disabled, context isolation enabled, no preload bridge, strict window/navigation handlers, and a deny-by-default permission policy.

## Local-first behavior

Desktop mode is selected by an explicit bootstrap capability contract; the UI does not infer Electron from its user agent. Chromium signs events and renders the UI, while the authenticated Go sidecar owns relay reads, relay writes, and durable SQLite state. Desktop also enables local signing accounts, relay selection, web-of-trust depth 1–3, storage reporting, and scoped cache clearing.

Hosted traffic shielding, guest admission, anonymous cache-only behavior, CDN policies, and per-IP cost throttles are bypassed in desktop mode. Input validation, body limits, signature verification, relay timeouts, fan-out limits, backoff, CSP, same-origin checks, and the process-wide relay concurrency bound remain active.

Desktop defaults are balanced for a modern local machine:

- SQLite cache: user-configurable in Settings, defaulting to 2 GiB with least-recently-used pruning toward 90%.
- No renderer event cache; Chromium keeps only normal HTTP assets and UI/account preferences.
- Process-wide relay operations: 12.
- Maintenance warm workers: 1; queue capacity: 64. A separate one-worker interactive intent lane is reserved for hover, focus, and pointer navigation.
- Go memory limit: 1 GiB.

SQLite preserves pinned dependencies and the newest metadata, follow-list, and relay-list events ahead of cold notes. Desktop enables the signed-in viewer crawler and hydration sweeper while keeping seed, guest, global hot-feed, and trending crawlers off. Direct follows are checked every 30 seconds in the foreground, degree-two and bounded degree-three cohorts use slower durable schedules, and minimized windows retain a reduced five-minute direct-follow pass. Feed rendering queues likely threads for durable server-side materialization; coherent stored graphs render immediately with stale-while-revalidate relay work after the response.

Settings shows SQLite and Chromium usage, lets the user change the persistent SQLite cache limit, and provides scoped clearing for note data, metadata, user data, or all cache without clearing signing accounts and app preferences.

### Advanced overrides

The desktop sidecar honors the normal `PTXT_*` environment variables. Useful local overrides include:

| Variable | Desktop default | Purpose |
| --- | ---: | --- |
| `PTXT_DB_MAX_BYTES` | `2147483648` | Initial SQLite + WAL/SHM byte budget. The desktop Settings preference takes precedence after the user saves a limit. |
| `PTXT_DB_PRUNE_TARGET_BYTES` | `1932735283` | Target after byte-budget pruning. |
| `PTXT_EVENT_RETENTION` | `0` | Optional event-count ceiling; `0` disables the count limit. |
| `PTXT_RELAY_MAX_OUTBOUND_CONNS` | `12` | Process-wide outbound relay operations. |
| `PTXT_WARM_WORKERS` | `1` | On-demand warm worker count. Desktop currently clamps this to one. |
| `PTXT_WARM_QUEUE_CAPACITY` | `64` | Bounded on-demand warm queue capacity. |
| `PTXT_HYDRATION_ENABLED` | `true` | Durable projection hydration. Retained as an emergency kill switch. |
| `PTXT_VIEWER_CRAWLER_ENABLED` | `true` | Signed-in follow-graph synchronization. Retained as an emergency kill switch. |
| `PTXT_VIEWER_CRAWLER_INTERVAL` | `30s` | Foreground direct-follow crawler tick. |
| `PTXT_VIEWER_CRAWLER_DEGREE2_INTERVAL` | `5m` | Degree-two note and graph refresh interval. |
| `PTXT_VIEWER_CRAWLER_DEGREE3_INTERVAL` | `30m` | Bounded degree-three candidate refresh interval. |
| `PTXT_VIEWER_CRAWLER_REDUCED_INTERVAL` | `5m` | Minimized-window direct-follow refresh interval. |
| `PTXT_VIEWER_CRAWLER_DIRECT_METADATA_INTERVAL` | `15m` | Direct-follow kind-3 and relay-list metadata refresh interval. |
| `PTXT_VIEWER_CRAWLER_DEGREE2_METADATA_INTERVAL` | `6h` | Degree-two metadata refresh interval. |
| `PTXT_VIEWER_CRAWLER_DEGREE3_METADATA_INTERVAL` | `24h` | Degree-three metadata refresh interval. |
| `PTXT_VIEWER_CRAWLER_PROFILE_INTERVAL` | `6h` | Missing or stale profile refresh interval. |
| `GOMEMLIMIT` | `1GiB` | Go runtime memory limit override. |
| `PTXT_MEMORY_LIMIT_BYTES` | `1073741824` | Decimal-byte override when `GOMEMLIMIT` is unset. |
| `PTXT_DESKTOP_DATA_DIR` | `…/Plain Text Nostr/local` | Test/development-only local SQLite directory override. |

`PTXT_DESKTOP_MODE` and `PTXT_DESKTOP_SESSION_TOKEN` are shell-owned. The desktop entry point refuses to start without the per-launch session token. Do not enable desktop mode on a publicly reachable server, and never expose that token to page JavaScript.

## CLI server

The same application can run without Electron:

```sh
npm run build:web
go run ./cmd/server
```

Open `http://127.0.0.1:8080` unless `PTXT_ADDR` is changed. CLI defaults keep SQLite at `data/ptxt-nstr.sqlite`.

## Architecture

- `desktop/` — Electron main process, native-window policy, startup diagnostics, and shell tests.
- `cmd/desktop-server` — packaged universal sidecar entrypoint and desktop defaults.
- `cmd/server` — local CLI entrypoint.
- `internal/apprun` — reusable Go server lifecycle shared by both entrypoints.
- `internal/httpx` — routes, templates, capability contract, security middleware, warmers, and relay-backed services.
- `internal/store` — SQLite events, projections, web-of-trust graph, retention, and scoped storage controls.
- `internal/nostrx` — validated Nostr events and bounded websocket relay fan-out.
- `internal/templates` and `web/static` — the shared web experience.

In desktop mode, Chromium talks only to the authenticated loopback origin. The Go sidecar fetches from selected Nostr relays, validates events, persists SQLite projections, and serves renderer reads from that single durable authority. The reusable web build retains its browser relay adapter, but desktop calls are routed through the bounded sidecar bridge.

## Testing

```sh
make test
make test-e2e
node --test desktop/*.test.mjs
```

Before a release, also run the upstream parity and tracked-tree checks:

```sh
npm run check:upstream
npm run check:public-tree
```

The release workflow must additionally verify both architectures, codesigning, notarization, stapling, Gatekeeper acceptance, DMG installation, and sidecar cleanup on a signing-capable macOS runner.

See [docs/upstream-sync.md](docs/upstream-sync.md) for the pinned private-source boundary and synchronization policy.
