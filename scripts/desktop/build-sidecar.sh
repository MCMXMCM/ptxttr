#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
output_dir="$repo_root/.tmp/desktop/bin"
cd "$repo_root"
mkdir -p "$output_dir"

GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$output_dir/ptxt-nstr-server-arm64" ./cmd/desktop-server
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$output_dir/ptxt-nstr-server-amd64" ./cmd/desktop-server
lipo -create -output "$output_dir/ptxt-nstr-server" "$output_dir/ptxt-nstr-server-arm64" "$output_dir/ptxt-nstr-server-amd64"
chmod 0755 "$output_dir/ptxt-nstr-server"
rm "$output_dir/ptxt-nstr-server-arm64" "$output_dir/ptxt-nstr-server-amd64"
