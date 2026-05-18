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
export PTXT_DEFAULT_RELAYS=""
export PTXT_METADATA_RELAYS=""
BIN="${PTXT_E2E_SERVER:-/tmp/ptxt-e2e-server}"
# Playwright may inherit a broken sandbox GOMODCACHE; use the host module cache.
unset GOMODCACHE GOCACHE GOPATH 2>/dev/null || true
export GOMODCACHE="${HOME}/go/pkg/mod"
export GOCACHE="${HOME}/Library/Caches/go-build"
go build -o "$BIN" ./cmd/server
trap 'rm -f "$DB" "${DB}"-*' EXIT
exec "$BIN"
