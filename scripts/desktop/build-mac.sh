#!/usr/bin/env bash
# Build the macOS desktop binary via Wails. Produces a universal .app
# (arm64 + amd64) at cmd/desktop/build/bin/ptxt-nstr.app when run on macOS.
# Run signing via scripts/desktop/sign-mac.sh after this completes.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
project_dir="${repo_root}/cmd/desktop"

if ! command -v wails >/dev/null 2>&1; then
  echo "wails CLI not found. Install it with:" >&2
  echo "  go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0" >&2
  exit 1
fi

PLATFORM="${PLATFORM:-darwin/universal}"
LDFLAGS="${LDFLAGS:--s -w}"

cd "${project_dir}"

# Wails reads build/appicon.png when packaging the .app (Dock / Finder icon).
# Keep it aligned with web favicons: web/static/img/ascritch_icon_black.png.
src_icon="${repo_root}/web/static/img/ascritch_icon_black.png"
if [[ ! -f "${src_icon}" ]]; then
  echo "missing branding icon ${src_icon}" >&2
  exit 1
fi
mkdir -p "${project_dir}/build"
echo "==> appicon: ${src_icon} -> build/appicon.png"
cp "${src_icon}" "${project_dir}/build/appicon.png"

echo "==> wails build -platform ${PLATFORM}"
wails build \
  -platform "${PLATFORM}" \
  -ldflags "${LDFLAGS}" \
  -clean \
  -trimpath \
  -skipbindings

app_path="${project_dir}/build/bin/ptxt-nstr.app"
if [[ ! -d "${app_path}" ]]; then
  echo "expected ${app_path} after build, not found" >&2
  exit 1
fi

echo "==> built ${app_path}"
du -sh "${app_path}"
