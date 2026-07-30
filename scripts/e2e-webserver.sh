#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
DB="$(mktemp -t ptxt-e2e.XXXXXX.sqlite)"
export PTXT_DB="$DB"
export PTXT_DEBUG=true
E2E_PORT="${PTXT_E2E_PORT:-18080}"
export PTXT_ADDR=":${E2E_PORT}"
export PTXT_SEED_CRAWLER_ENABLED=false
export PTXT_VIEWER_CRAWLER_ENABLED=false
export PTXT_HYDRATION_ENABLED=false
export PTXT_HOT_FEED_CRAWLER_ENABLED=false
# Parallel browser workers share one loopback IP, so production traffic-shield
# defaults can make unrelated e2e cases throttle one another.
export PTXT_ANON_RATE_BURST=10000
export PTXT_ANON_RATE_PER_SEC=10000
export PTXT_BOT_RATE_BURST=10000
export PTXT_BOT_RATE_PER_SEC=10000
export PTXT_VIEWER_RATE_BURST=10000
export PTXT_VIEWER_RATE_PER_SEC=10000
# Keep the server from crawling real relays during e2e (empty env falls back to defaults).
export PTXT_RELAYS="wss://127.0.0.1:1"
export PTXT_METADATA_RELAYS="wss://127.0.0.1:1"
export PTXT_INDEXER_RELAYS="wss://127.0.0.1:1"
export PTXT_INDEXER_NIP50_RELAYS="wss://127.0.0.1:1"
BIN="${PTXT_E2E_SERVER:-/tmp/ptxt-e2e-server}"
# Playwright may inherit a broken sandbox GOMODCACHE; use the host module cache.
unset GOMODCACHE GOCACHE GOPATH 2>/dev/null || true
export GOMODCACHE="${HOME}/go/pkg/mod"
export GOCACHE="${HOME}/Library/Caches/go-build"
npm run build:web
go build -o "$BIN" ./cmd/server
trap 'rm -f "$DB" "${DB}"-*' EXIT
exec "$BIN"
