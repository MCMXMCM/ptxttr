#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
DB="$(mktemp -t ptxt-e2e.XXXXXX.sqlite)"
export PTXT_DB="$DB"
BIN="${PTXT_E2E_SERVER:-/tmp/ptxt-e2e-server}"
if [[ ! -x "$BIN" ]]; then
  # Playwright may inherit a broken sandbox GOMODCACHE; use the host module cache.
  unset GOMODCACHE GOCACHE GOPATH 2>/dev/null || true
  export GOMODCACHE="${HOME}/go/pkg/mod"
  export GOCACHE="${HOME}/Library/Caches/go-build"
  go build -o "$BIN" ./cmd/server
fi
trap 'rm -f "$DB" "${DB}"-*' EXIT
exec "$BIN"
