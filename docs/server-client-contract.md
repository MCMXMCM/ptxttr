# Server and Client Contract

This app is SSR-first for the surfaces that need fast, robust first paint, then progressively enhanced by vanilla JS. The server should make every primary route useful without client relay work; the client should improve freshness, personalization, interactivity, and resilience after the document exists.

## Goals

- First navigation should paint meaningful HTML on slow mobile devices.
- Canonical URLs should stay clean and cacheable.
- Viewer identity and preferences should not leak into shareable URLs.
- Client relay work should be an enhancement, not the only way to read a page.
- Fragment hydration should never repaint the wrong route after navigation.

## Server Owns

- Full document HTML for public route loads: `/`, `/feed`, `/thread/*`, `/u/*`, `/reads`, `/tag/*`, and static/error shells.
- Route fragments used by the document router, including thread hydrate/focus/replies fragments and feed/profile pagination.
- Cache policy and ETags for anonymous SSR documents and immutable preview surfaces.
- Open Graph, oEmbed, Telegram Instant View, share pages, and crawler-friendly fallbacks.
- Store-backed projections needed for SSR: profiles, follows, reply counts, reactions, trending, tags, reads, notifications, and thread context.
- Bounded relay warming when the server is configured as a relay backend.
- API mutation surfaces where server validation or persistence is required, such as publish/reaction/share APIs.

## Client Owns

- Session state, signing keys, selected relays, Web-of-Trust preferences, sort preferences, media preferences, and other local-only UI state.
- Sending viewer identity and preferences as `X-Ptxt-*` headers through `fetchWithSession`, not as canonical URL query params.
- Same-document route orchestration: render a skeleton only when SSR has not already supplied a usable shell, fetch the current fragment, ignore stale route responses, and restore scroll.
- Progressive enhancement of server HTML: buttons, menus, media viewers, reply composers, reactions, lazy tabs, polling, and relative timestamps.
- IndexedDB caches for relay-native reads, route snapshots, events, profiles, avatars, and first-paint handoffs.
- Direct relay reads/writes when enabled, with server fragments and APIs treated as fallback or first-paint support depending on deployment mode.

## Shared Boundary

- `web/static/js/session.js` is the client transport boundary. All same-origin client fetches that can be personalized should go through `fetchWithSession`.
- `internal/httpx/viewer_request.go` is the server transport boundary. It reads the same `X-Ptxt-*` headers and only falls back to legacy query params for old links.
- `web/static/js/viewer-pref-url.js` keeps viewer prefs and relay selections out of canonical URLs.
- `internal/httpx/cache_headers.go` owns cache semantics; route handlers should call its helpers rather than writing cache headers ad hoc.
- `web/static/js/app/document-router.js` owns browser route lifecycle. Server handlers should return full documents or fragments; they should not rely on hidden client-only state to make those responses coherent.

## Request Flow

1. Browser requests a canonical route.
2. Server renders useful HTML from SQLite projections and, when allowed, bounded relay fetches or warmers.
3. Server marks anonymous responses cacheable only when the response shape does not depend on viewer-specific state.
4. Client bootstraps the document router, session links, route polling, and route-specific controls.
5. Client fetches fragments with `fetchWithSession`, carrying viewer/pref headers.
6. Client applies a fragment only if it still matches the active route and selected note/profile context.
7. Client may refresh from relays and IndexedDB, but SSR remains the durable fallback for hard loads, mobile first paint, bots, and failed relay reads.

## Decision Rules

- Put work on the server when it is needed for first paint, crawlers, shareability, cacheable public HTML, or no-JS fallback.
- Put work on the client when it needs private keys, browser storage, local relay choices, optimistic interaction, media/device APIs, or fine-grained UI state.
- Keep route data in URLs only when it identifies public content or pagination. Put viewer state in headers or browser storage.
- Prefer document/fragment HTML for route transitions that need stable mobile layout. Prefer JSON only for small data patches that update an already-mounted component.
- Treat direct relay mode as a performance and autonomy enhancement, not as a requirement for reading a shared URL.

## Things To Avoid

- Adding `pubkey`, `relays`, `sort`, `wot`, or similar viewer params to canonical links.
- Letting fragment responses depend on localStorage-only state without sending the matching `X-Ptxt-*` header.
- Returning a personalized full document with shared cache headers.
- Rebuilding core route layout only on the client when the server can cheaply render the initial shape.
- Starting background relay work from request paths that should be cache-only or store-first pagination.
