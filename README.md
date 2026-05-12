# Plain Text Nostr

A small Go Nostr web app built around server-side relay aggregation, a SQLite event cache (including web-of-trust follow edges), Go HTML templates, vanilla JavaScript, and plain CSS.

The server fans out to a bounded set of relays, persists every raw event into SQLite, projects derived state (profiles, follow lists, relay hints, reply counts, trending, bookmarks, reads) into typed tables, expands web-of-trust reachability from the `follow_edges` projection inside SQLite, and wraps each resolved author set in a small bloom filter for cheap negative membership checks before exact lookups. Hot projection reads also use a bounded in-process LRU (relay hints, profiles, reply stats) with hit/miss counters on `/debug/metrics`. Pages render from local cache first; relay traffic warms the cache in the background.

**Try it in the browser:** open [plaintextnostr.com](https://plaintextnostr.com) for a quick look. The hosted site runs the same server-side aggregation and cache as this repo; you are **trusting that server** to connect to relays and aggregate notes on your behalf. If you want relay traffic and SQLite aggregation to stay **on your own machine**, use the **macOS desktop app** below (or run `go run ./cmd/server` locally).

**Operators:** hosted AWS / CloudFront deployment is optional and documented in [`deploy/README.md`](deploy/README.md).

## Try it (macOS desktop)

**Download a build:** open [GitHub Releases](https://github.com/MCMXMCM/ptxttr/releases) and download the latest **`ptxt-nstr-desktop-mac-*.dmg`**, then install from the disk image as usual.

**Build from source** on a Mac:

1. Install [Wails v2](https://wails.io/) (for example `go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0`) and Xcode Command Line Tools.
2. Clone this repository and from the repo root run:

```sh
make build-desktop
```

The app bundle is written to `cmd/desktop/build/bin/ptxt-nstr.app`. Open that folder in Finder and double-click the app, or run it from the terminal.

The desktop build embeds a short splash, then starts the same HTTP server as `cmd/server` on a random `127.0.0.1` port and opens it in a native window. Your database defaults to `~/Library/Application Support/ptxt-nstr/ptxt-nstr.sqlite` (override with `PTXT_DB`). The desktop preset also sets `PTXT_DESKTOP_MODE=1` (external links open in the default browser). First-launch compaction is off by default (`PTXT_COMPACT_ON_START=false`) so the window stays responsive; optional `pprof` on `127.0.0.1:6060` is off by default in the desktop preset (`PTXT_PPROF_ADDR=off`) to avoid clashing with a dev server.

**Desktop vs hosted URLs:** OpenGraph and canonical links use the request host. Behind CloudFront, `X-Forwarded-Host` supplies your public domain; in the desktop app the host is `127.0.0.1:<port>`, which is fine for local use.

**WebKit note:** the desktop shell uses the system webview. Browser extensions (NIP-07) may behave differently than in Chrome or Safari; read-only and remote-signer flows are the most reliable in embedded WebKit.

**Links:** `http`/`https` links are opened in your **default browser** (not inside the app). Same-origin app links on `127.0.0.1` / `localhost` stay in the window.

**Navigation:** use the **ptxt → Back / Forward** menu (or ⌘[ / ⌘]) for history. Native **two-finger swipe to go back** is not enabled by Wails’ macOS WKWebView wrapper today; that would need an upstream Wails change (`allowsBackForwardNavigationGestures`) or a custom fork.

## Run from source (CLI server)

```sh
go run ./cmd/server
```

Open `http://127.0.0.1:8080` (or the address in `PTXT_ADDR`). Use the same environment variables as production; defaults keep the database under `data/ptxt-nstr.sqlite` relative to the process working directory.

## Architecture

### Storage layout

One durable database file lives under `data/` by default:

- `data/ptxt-nstr.sqlite` — the canonical event cache and projection database. WAL mode, `synchronous=NORMAL`, 32 MiB page cache, 128 MiB mmap, 5 second busy timeout by default (all tunable via env). Holds raw events, tag rows, per-relay sightings, relay status, profile/follow projections (`follow_edges` for WoT expansion), reply counts, trending cache, hydration targets, bookmarks, and reads.



### Top-level packages

- `cmd/server` — CLI entrypoint. Applies the runtime memory limit, opens SQLite, builds the `nostrx.Client`, wires the HTTP server, runs startup compaction, and shuts down gracefully on SIGINT/SIGTERM.
- `cmd/desktop` — macOS Wails wrapper around the same `internal/httpx` stack: loopback HTTP server + native window (see **Try it (macOS desktop)** above).
- `internal/config` — environment loading, defaults, and the canonical relay list.
- `internal/nostrx` — typed Nostr events, NIP-19 helpers, NIP-27 mention parsing, websocket relay client with optional NIP-77 negentropy (when the SQLite cache is wired), per-relay metrics, and a backoff penalty box.
- `internal/store` — SQLite schema, migrations, projections, retention/compaction, hydration target queues, trending cache, WoT reachability over `follow_edges`, and bounded sidecar LRU caches.
- `internal/httpx` — HTTP server, handlers, render helpers, the warm queue, the hydration sweeper, the trending sweeper, and the outbox routing helpers.
- `internal/thread` — thread tree assembly from cached events.
- `internal/templates`, `web/static` — Go HTML templates, vanilla JS, plain CSS.
- `internal/bloom` — small bloom filter used for fast membership tests against resolved author universes.

## Data flow

```
                              +-------------------+
                              |   Browser (UI)    |
                              |  HTML + vanilla JS|
                              +---------+---------+
                                        | HTTP (cookie session, no relay traffic)
                                        v
+---------------------------------------+----------------------------------------+
|                                internal/httpx                                  |
|                                                                                |
|   handlers_feed / handlers_api / handlers_debug / handler_user / thread        |
|        |                |                  |                       |          |
|        v                v                  v                       v          |
|   feed pipeline   relay routing       hydrator / warmer       thread builder  |
|   (service.go)    (service_outbox)    (warmer.go,             (internal/      |
|        |                |              hydrator.go,            thread)        |
|        |                |              background_jobs.go)                    |
|        |                |                                                     |
+--------+----------------+------------------+--------------------------+-------+
         |                |                  |                          |
         | reads          | route fan-out    | enqueue warm jobs        | reads
         v                v                  v                          v
+--------+----------------+------------------+-----------------------------------+
| internal/store (SQLite)                                                         |
|  events, tags, relay_events, relay_status, follow_edges (WoT),                 |
|  profiles_cache, follow_list_cache, relay_hints, reply_counts,                |
|  trending_cache, hydration_targets, bookmarks, reads, cache_events, fetch_log |
+--------+----------------+------------------+-----------------------------------+
         ^                ^                  ^
         | projections    | sightings        | ingest + LRU invalidation
         |                |                  |
+--------+----------------+------------------+----------------------------------+
|                            internal/nostrx (relay client)                      |
|   websocket fan-out, optional NIP-77 negentropy + REQ fallback, dedupe,      |
|   per-relay metrics (incl. negentropy counters), penalty backoff               |
+--------+-----------------------------------------------------------------------+
         |
         v
+--------+-----------------+
|     Nostr relays         |
|  primal / damus / nos.lol|
|  + user-added relays     |
+--------------------------+
```

### Read path

1. The browser issues an HTTP request (`/`, `/feed`, `/u/<id>`, `/thread/<id>`, `/reads`, `/bookmarks`, `/notifications`, `/trending`, `/api/...`). Sessions and the active pubkey live in a cookie; the browser never speaks websocket itself.
2. `internal/httpx` parses the request into a `feedRequest`, resolves the viewer, and clamps relay/wot parameters.
3. Authors are resolved through `resolveAuthorsAll`:
   - viewer's follow list is loaded from the SQLite `follow_list_cache` projection,
   - if web-of-trust is enabled, `Store.ReachablePubkeysWithin` walks `follow_edges` in a read-only transaction (depth-bounded, same `MaxDepth = 3` ceiling as the UI) and merges its result,
   - the union is deduped, capped by `WOTMaxAuthors`, and memoized in an in-process `resolvedAuthorsCache`.
4. The page renders directly from SQLite projections (`events`, `profiles_cache`, `reply_counts`, `trending_cache`, etc.). For the logged-out feed the trending cache is consulted first, then a curated whitelist fallback.
5. Cache misses or stale entries are pushed into the warm queue (`warmer.go`) and into `hydration_targets` so they're refreshed asynchronously instead of blocking the response.

### Write path (relay → cache)

1. A handler or warmer asks `nostrx.Client` for events. The client fans out to a bounded set of relays (`MaxRelays = 8`), enforcing per-request and per-relay timeouts and tracking per-relay metrics (`queries`, `relay_attempts`, `relay_failures`, `events_seen`, plus `negentropy` counters on `/debug/metrics`). When the server wires SQLite via `SetNegentropyCache`, each relay may first try **NIP-77 negentropy** (download-only via `fiatjaf.com/nostr/nip77`): we reconcile id sets only for filters we can mirror in SQL (event ids and/or authors and kinds with optional `since`/`until`, no `#e`/`#p` tag maps or search), only if `PTXT_NEGENTROPY` is `1`/`true`/`on` (opt-in; default is REQ-only because most relays reject NIP-77) and the local cache has at least one matching row but fewer than 50k (so there is overlap to reconcile without loading an enormous vector). Missing events are fetched on the same relay connection the library opens. **Any** negentropy failure or timeout falls back to the classic `REQ` / `EVENT` / `EOSE` path for that relay only; the negentropy phase uses at most one third of the per-relay timeout so the REQ round trip still has budget. NIP-42 `AUTH` handling on the REQ path may differ from what `nostr.Relay` does during negentropy—treat auth-gated relays as higher risk until aligned. Outbound WebSockets use the stack from `nostr.RelayConnect` (coder/websocket with compression where negotiated) for negentropy, and the same dial style as today for REQ.
2. Repeated relay failures push that relay into a backoff penalty box, so a flaky relay can't keep extending request latency.
3. Returned events are deduped by id and persisted via `internal/store.SaveEvents`. Relay `EVENT` frames are staged as raw wire events in `nostrx.Client`, then converted and checked with `nostrx.ValidateIngestEvent(IngestFromRelay, …)` as a batch before they reach SQLite (and the negentropy publisher path uses the same relay validation helper). The HTTP publish API uses `IngestFromHTTPAPI` so kind limits, content size, and kind-specific shape rules stay aligned with `nostrx.ValidateSignedEvent`. When `PTXT_INGEST_VERIFY_PARALLEL` is greater than `1`, that staged relay batch validation runs with concurrent workers for every `FetchFrom` path. For each event the store:
   - writes the raw JSON, pubkey, kind, created_at, signature, and tag rows,
   - records a `relay_events` sighting per (event id, relay url),
   - calls `projectEventTx` to update typed projections — kind 0 → `profiles_cache`, kind 3 → `follow_list_cache` and `follow_edges`, kind 10002 → `relay_hints`, kind 10003 → `bookmarks`, replies → `reply_counts`, etc., and invalidates sidecar LRU keys for affected entities after batch ingest.
4. Retention runs FIFO by `inserted_at` once the per-process write counter trips `pruneEvery`. `Compact` (one-shot at startup if `PTXT_COMPACT_ON_START=1`) prunes plus vacuums. That FIFO cap can delete arbitrary rows by insertion time; it does not try to preserve one row per replaceable slot.

### Replaceable events (kind 0, 3, 10002)

- Reads pick the newest row per `(pubkey, kind)` using `ORDER BY created_at DESC, id DESC` (`LatestReplaceable`, feeds, etc.).
- By default (`PTXT_REPLACEABLE_HISTORY=true`), every distinct event id remains in `events` so you keep a full revision history until global retention deletes old rows.
- Set `PTXT_REPLACEABLE_HISTORY=false` to delete older same-slot rows immediately after a successful insert (same ordering as “newest”): only kinds **0**, **3**, and **10002** are pruned; other kinds are unchanged. Relay hint rows for removed ids are cleared via `DELETE` on `relay_events` first so orphaned relay rows do not accumulate.

### Tag indexing (for contributors)

- Normalized tag rows live in `tags` with `PRIMARY KEY(event_id, idx)` and index `idx_tags_name_value` on `(name, value, event_id)` for `#e` / `#p` / `#t` style lookups (`internal/store/migrate.go`).
- When adding a query that filters on a new tag name or a new composite shape, add an explicit migration index (or a projection fed from `projectEventTx`) rather than relying on full table scans.

### Denormalization and query planning

Hot paths today include feed timelines (`RecentByAuthors`, `RecentByKinds`, outbox-grouped refresh), `LatestReplaceable*` for profiles and follows, `SearchNoteSummaries` (FTS5 + kind filters), and thread assembly. Projections (`profiles_cache`, `follow_list_cache`, `reply_counts`, `trending_cache`, etc.) already denormalize what those reads need.

When profiling shows a slow plan, prefer (in order): tighten the query, add a **partial** or composite index in `migrate.go`, or extend a projection column updated in `projectEventTx`. Avoid new storage engines unless requirements clearly outgrow SQLite.

### Web-of-trust filter

- Toggle and depth (1–3) are user preferences stored in the browser via `web/static/js/sort-prefs.js` and surfaced server-side as `wot=1&wot_depth=N` query parameters. `store.MaxDepth = 3` is the canonical ceiling and the JS client constant must agree with it (asserted by `wot_depth_sync_test.go`).
- When the filter is on, feed and profile-feed handlers expand the viewer's follow set using `follow_edges` up to the requested depth, then intersect SQLite event queries with that author universe.
- The follow universe is capped by `WOTMaxAuthors` (default 240) before SQLite filtering to keep `IN (...)` plans bounded.

### Background loops

`internal/httpx/server.go` spawns these on startup when `HydrationEnabled=true`:

- **Hydration sweeper** (`hydrator.go`, every `PTXT_HYDRATION_SWEEP_INTERVAL`, default 5 min). Pulls stale `hydration_targets` rows for `profile`, `noteReplies`, `followGraph`, and `relayHints` and warms against the metadata + default relay set. Logged-out seed note hydration uses the separate `seedContact` crawler ([`seed_crawler.go`](internal/httpx/seed_crawler.go)); tune it with `PTXT_SEED_CRAWLER_*` in the table below.
- **Trending sweeper** (`trending.go`, every `PTXT_TRENDING_SWEEP_INTERVAL`, default 5 min). Recomputes the 24h and 1w trending tables from `reply_counts` and persists them into `trending_cache`, but only if the existing cache is older than `PTXT_TRENDING_MIN_RECOMPUTE` (default 20 min).
- **Projection rebuild** (one-shot, only if `PTXT_REBUILD_PROJECTIONS=1`). Rewrites every projection table from raw events. Useful after schema changes.
- **Warm queue** (`warmer.go`, 2 workers). Coalesces warm requests by key so simultaneous viewers asking for the same author/thread/event only spawn one relay round trip. Each job has a wall-clock timeout and per-kind work caps (`PTXT_WARM_*`); oversized jobs re-enqueue the tail.

### Outbox-style routing

`service_outbox.go` groups authors into route groups before fan-out:

- per-author write-relay hints from `relay_hints` (kind 10002),
- contact-list relay hints from followers-of-followers (capped by `OutboxFoFSeeds`, default 40),
- observed relays from `relay_events` for that author's notes/profiles/relay lists,
- the viewer-supplied / default relay set as a fallback.

Each author is bounded by `OutboxMaxRelaysPerAuthor` (default `MaxRelays = 8`), and the total number of grouped requests is bounded by `OutboxMaxRouteGroups` (default 6).

## Login modes

Private keys never leave the browser:

- read-only pubkey,
- NIP-07 (browser extension),
- NIP-46 connection boundary — saves a bunker / nostrconnect string and pubkey but does not yet open a remote-signer transport,
- session-only private key,
- ephemeral key.

The logged-out feed is seeded from a curated whitelist in `data/curated_pubkeys.json`, not the raw firehose.


## Configuration

The **desktop** preset (`cmd/desktop`) sets `PTXT_DB` under the per-user config directory, `PTXT_DESKTOP_MODE=1`, `PTXT_COMPACT_ON_START=false`, `PTXT_ACTIVE_VIEWER_TRENDING=false`, and `PTXT_PPROF_ADDR=off` when those keys are unset so first launch stays snappy and does not collide with a dev `pprof` listener.

Useful environment variables (all optional):

| Variable | Default | Purpose |
|---|---|---|
| `PTXT_ADDR` | `:8080` | Listen address. |
| `PTXT_DESKTOP_MODE` | `false` | Set automatically by `cmd/desktop` for the Wails build. Registers the loopback-only `/__ptxt/desktop/open-external` helper so external links open in the system browser. Do not enable on a publicly reachable `cmd/server` unless you understand the tradeoffs. |
| `PTXT_DB` | `data/ptxt-nstr.sqlite` | SQLite path. |
| `PTXT_RELAYS` | `wss://relay.primal.net,wss://relay.damus.io,wss://nos.lol` | Default relay set. |
| `PTXT_METADATA_RELAYS` | `PTXT_RELAYS` | Preferred relays for profile / follow / relay-list hydration. |
| `PTXT_CURATED_PUBKEYS` | — | Comma-separated hex pubkeys for the logged-out feed. |
| `PTXT_REQUEST_TIMEOUT_MS` | `3500` | Per-relay timeout in `nostrx.Client`. The outer HTTP handler context uses `max(5s, relayTimeout+2s)` (see `internal/httpx/middleware.go`). |
| `PTXT_RELAY_MAX_OUTBOUND_CONNS` | `48` | Process-wide cap on concurrent relay WebSocket operations (`nostrx` acquire/release around each dial). Set to `0` for unlimited (tests only). |
| `PTXT_WARM_JOB_TIMEOUT_MS` | `45000` | Hard wall-clock cap per warm-queue job. |
| `PTXT_WARM_MAX_AUTHORS_PER_JOB` | `16` | Max authors processed per warm `authors` job; remainder is re-enqueued. |
| `PTXT_WARM_MAX_NOTE_IDS_PER_JOB` | `12` | Max note IDs per warm `noteReplies` / `noteReactions` job; remainder is re-enqueued. |
| `PTXT_HEALTH_PROBE_ENABLED` | `false` | When `1`/`true`, periodically `GET` the app over loopback (see path/timeout below) and set `degraded` in `/healthz` after repeated failures. |
| `PTXT_HEALTH_PROBE_INTERVAL` | `30s` | Delay between self-probes. |
| `PTXT_HEALTH_PROBE_PATH` | `/healthz` | URL path for the self-probe. Keep this cheap; `/healthz` only pings SQLite and does not call relays. |
| `PTXT_HEALTH_PROBE_TIMEOUT_MS` | `12000` | Per-probe HTTP client timeout. |
| `PTXT_HEALTH_PROBE_DEGRADED_THRESHOLD` | `3` | Consecutive probe failures before `/healthz` reports `"degraded": true`. |
| `PTXT_NEGENTROPY` | off | Set to `1`/`true`/`on` to try NIP-77 before REQ on eligible fetches (see write path). Unset or `0`/`false`/`off` keeps REQ-only. |
| `PTXT_FEED_WINDOW` | `168h` | Logged-out firehose window. |
| `PTXT_EVENT_RETENTION` | `20000` | FIFO event ceiling for the SQLite cache. |
| `PTXT_REPLACEABLE_HISTORY` | `true` | When `false`, delete superseded kind 0 / 3 / 10002 rows for the same pubkey after each insert (see “Replaceable events”). |
| `PTXT_INGEST_VERIFY_PARALLEL` | `0` | When set to an integer `2`–`32`, `nostrx.Client` validates relay `EVENT` batches concurrently after the read loop and before returning them to SQLite-backed paths. `0` or `1` keeps validation sequential. |
| `PTXT_HYDRATION_ENABLED` | `true` | Run the hydration + trending sweepers. |
| `PTXT_HYDRATION_SWEEP_INTERVAL` | `5m` | Delay between hydration sweeps. |
| `PTXT_TRENDING_SWEEP_INTERVAL` | `5m` | Delay between trending sweep passes. |
| `PTXT_TRENDING_MIN_RECOMPUTE` | `20m` | Minimum age before trending cache recomputes. |
| `PTXT_ACTIVE_VIEWER_TRENDING` | `false` | When `1`/`true`, runs the per-active-viewer trending warm loop (extra SQLite load). |
| `PTXT_SEED_CRAWLER_ENABLED` | `true` | Keep the WoT seed crawler running in the background. |
| `PTXT_SEED_CRAWLER_INTERVAL` | `20s` | Delay between crawler ticks. |
| `PTXT_SEED_CRAWLER_AUTHOR_BATCH` | `16` | Stale seed contacts processed per tick. |
| `PTXT_SEED_CRAWLER_FETCH_LIMIT` | `60` | Max notes fetched per author per seed tick. |
| `PTXT_SEED_CRAWLER_AUTHOR_NOTE_LOOKBACK` | `2880h` | Oldest `created_at` for those notes (`Since` on the relay filter; default 120 days). Set to `0` or `0s` to disable (count limit still applies). |
| `PTXT_SEED_CRAWLER_REPLY_WARM_LIMIT` | `24` | Reply threads warmed per author per tick. |
| `PTXT_SEED_CONTACT_FOLLOW_ENQUEUE_PER_TICK` | `120` | Follow pubkeys enqueued per contact when expanding the seed frontier. |
| `PTXT_SQLITE_MAX_OPEN_CONNS` | `max(10, runtime.NumCPU())` | SQLite pool max-open when unset (WAL read concurrency). |
| `PTXT_SQLITE_MAX_IDLE_CONNS` | `max(4, runtime.NumCPU()/2)` | SQLite pool max-idle when unset. |
| `PTXT_SQLITE_CACHE_KIB` | `32768` | SQLite page-cache target in KiB (`PRAGMA cache_size=-N`). |
| `PTXT_SQLITE_MMAP_BYTES` | `134217728` | SQLite mmap target bytes (`PRAGMA mmap_size`). |
| `PTXT_SQLITE_BUSY_TIMEOUT_MS` | `5000` | SQLite busy timeout (ms) for lock retries (`PRAGMA busy_timeout`). |
| `PTXT_SQLITE_WAL_AUTOCHECKPOINT_PAGES` | `800` | SQLite WAL autocheckpoint page threshold. |
| `PTXT_SIDECAR_LRU_SIZE` | `2048` | Max entries per sidecar domain (relay hints, profiles, reply stats) for the in-process read-through LRU. |
| `PTXT_WOT_MAX_AUTHORS` | `240` | Cap on the WoT-expanded author universe per request. |
| `PTXT_OUTBOX_MAX_RELAYS_PER_AUTHOR` | `8` | Per-author outbox cap. |
| `PTXT_OUTBOX_MAX_ROUTE_GROUPS` | `6` | Max simultaneous route groups per request. |
| `PTXT_OUTBOX_FOF_SEEDS` | `40` | Followers-of-followers seed cap for outbox routing. |
| `PTXT_REBUILD_PROJECTIONS` | `false` | Rewrite all SQLite projections at startup. |
| `PTXT_COMPACT_ON_START` | `false` | One-shot prune + vacuum at startup. |
| `PTXT_DEBUG` | `false` | Enable `/debug/*` endpoints. |
| `PTXT_MEMORY_LIMIT_BYTES` | `1073741824` (1 GiB) | Soft heap ceiling. `GOMEMLIMIT` takes precedence. |

### Health and capacity notes

- **`GET /healthz`** (always on): SQLite `Ping` only — use for **liveness** (process + DB). It does not call relays. JSON includes `degraded` when the optional self-probe has failed repeatedly (see `PTXT_HEALTH_PROBE_*`). **`systemd` `MemoryCurrent` can stay high while RSS is moderate** (e.g. page cache / mmap); treat `/healthz` + probe state as separate from RSS-only signals.
- **Readiness vs liveness**: `/healthz` may return `200` with `"degraded": true` if the probe path is slow or wedged while SQLite still answers — wire your load balancer to `degraded` for **readiness** if you want to drain traffic before full failure.
- **`/debug/metrics`** (when `PTXT_DEBUG=1`): includes `relay_queries.outbound` (slot cap, wait counts), `health` (last probe, consecutive failures), and sidecar counters `sidecar.relay_hint.{hit,miss}`, `sidecar.profile.{hit,miss}`, `sidecar.reply_stat.{hit,miss}` (same keys appear in `/debug/cache` under `sidecar_lru` as snapshots).

## Local checks

```sh
go test ./...
PTXT_DEBUG=1 go run ./cmd/server
```

After `make build-desktop` on macOS, open `cmd/desktop/build/bin/ptxt-nstr.app` once to confirm the splash hands off to the loopback UI (or run `cd cmd/desktop && wails dev` while iterating on the shell).

With debug enabled:

- `/debug/cache` — event, tag, relay-sighting, relay-status, and projection counts.
- `/debug/metrics` — relay query attempts/failures/event counts (including outbound slot contention), app counters, and health probe snapshot.
- `/debug/runtime` — Go runtime stats (goroutines, heap, GC).
- `/debug/event?id=<eventid>` — fetches and returns one cached or relay-backed event with relay sources.
- `/debug/profile?pubkey=<npub-or-hex>` — refreshes and returns one profile summary.
- `/debug/firehose` — recent relay activity sample.
- `/debug/pprof/*` — standard pprof handlers.

## MVP scope

- Server-rendered home, login, relays, user, event, thread, reads, bookmarks, notifications, trending, search, and settings pages.
- SQLite WAL cache for raw events, tags, relay sightings, relay status, profile / follow / relay-hint / reply-count / trending projections, hydration targets, bookmarks, and reads.
- SQLite-backed web-of-trust expansion over projected follow edges for opt-in feed filtering.
- Relay fan-out, event dedupe, profile / contact / recent-note fetches, outbox-style routing, and thread assembly on the Go server.
- Login modes that keep private keys in the browser (see above).
- Curated logged-out feed (whitelist), bounded relay selection (defaults + up to 8 user-added), NIP-65 suggestions, cursor-and-fragment pagination.
- Idle-gated background hydration and trending sweepers, in-flight warm queue with key-level dedupe, request-scoped timeouts, persisted NIP-11 relay status, and per-relay query counters.

## Known limitations

- NIP-46 is intentionally a UI/session boundary in this PoC. It saves the connection string and pubkey but does not open a remote-signer transport or request signatures.
- Publishing exists for a small set of kinds (notes, reactions, bookmarks, reads); zaps, media proxying, and richer notifications are deferred.
- The curated logged-out feed seed should be reviewed before production use.
- Feed pagination uses both event timestamp and event id as a cursor tie-breaker.
- Thread pages render the currently-cached tree with collapse, parent/select, hash focus, and keyboard navigation. Deep branch navigation uses the `continue thread` link rather than a streaming reply loader.
- WoT depth is capped at 3 and the author universe is capped at `WOTMaxAuthors` before SQLite filtering.
- Debug metrics are in-memory counters and reset on restart. Relay status is persisted in SQLite.

## Small-server sanity pass

For a constrained local profile, run with a temp database and debug enabled:

```sh
PTXT_DB="$(mktemp -t ptxt-nstr.XXXXXX.sqlite)" PTXT_DEBUG=1 PTXT_REQUEST_TIMEOUT_MS=2500 go run ./cmd/server
```

Then repeatedly load `/`, `/feed`, `/relays`, a known `/u/<npub-or-hex>`, and a known `/thread/<eventid>` while watching structured logs plus `/debug/cache` and `/debug/metrics`. Relay fan-out should stay bounded by `MaxRelays`, profile/feed pagination should advance with timestamp + id cursors, repeated requests should grow cache hits, the hydration and trending sweepers should tick on idle gates, and relay failures should not block the whole page.

## Hosted deployment (advanced)

For a single-instance AWS deployment, TLS, optional CloudFront in front of the origin, and operational runbooks, see [`deploy/README.md`](deploy/README.md). This path is aimed at operators who want a public site, not at someone who only wants to try the app locally.

## License

[MIT License](LICENSE): 
