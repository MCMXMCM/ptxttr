#!/usr/bin/env bash
# Package the built (and ideally signed) .app into a distributable .dmg.
# Uses create-dmg if available, otherwise falls back to hdiutil.
# Output goes to dist/ at the repo root (already gitignored).
#
# DMG filename uses PTXT_NSTR_DESKTOP when set (e.g. 0.1.0); otherwise the
# default is productVersion from cmd/desktop/wails.json. scripts/desktop/signing.env
# is sourced when present so you can set PTXT_NSTR_DESKTOP next to signing secrets.
# signing.env must contain only assignments/comments — not `make …` lines.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
signing_env="${repo_root}/scripts/desktop/signing.env"

# Keep a caller-provided PTXT_NSTR_DESKTOP if signing.env also defines it.
_explicit_ptxt=false
if [[ -n "${PTXT_NSTR_DESKTOP+x}" ]]; then
  _explicit_ptxt=true
  _saved_PTXT_NSTR_DESKTOP="${PTXT_NSTR_DESKTOP}"
fi
if [[ -f "${signing_env}" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "${signing_env}"
  set +a
fi
if [[ "${_explicit_ptxt}" == true ]]; then
  export PTXT_NSTR_DESKTOP="${_saved_PTXT_NSTR_DESKTOP}"
fi

app_path="${repo_root}/cmd/desktop/build/bin/ptxt-nstr.app"
dist_dir="${repo_root}/dist"
wails_json="${repo_root}/cmd/desktop/wails.json"

wails_product_version() {
  local v
  v="$(sed -n 's/^[[:space:]]*"productVersion"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "${wails_json}" 2>/dev/null | head -1)"
  if [[ -z "${v}" ]]; then
    v="0.1.0"
  fi
  printf '%s' "${v}"
}

version="${PTXT_NSTR_DESKTOP:-$(wails_product_version)}"
version="${version//\//-}"
dmg_path="${dist_dir}/ptxt-nstr-desktop-mac-${version}.dmg"

if [[ ! -d "${app_path}" ]]; then
  echo "no .app at ${app_path}; run scripts/desktop/build-mac.sh first" >&2
  exit 1
fi

mkdir -p "${dist_dir}"
rm -f "${dmg_path}"

if command -v create-dmg >/dev/null 2>&1; then
  echo "==> create-dmg ${dmg_path}"
  create-dmg \
    --volname "Plain Text Nostr" \
    --window-size 540 360 \
    --icon "ptxt-nstr.app" 130 180 \
    --hide-extension "ptxt-nstr.app" \
    --app-drop-link 410 180 \
    --no-internet-enable \
    "${dmg_path}" \
    "${app_path}"
else
  echo "==> create-dmg not found; falling back to hdiutil"
  staging="$(mktemp -d)"
  cp -R "${app_path}" "${staging}/"
  ln -s /Applications "${staging}/Applications"
  hdiutil create \
    -volname "Plain Text Nostr" \
    -srcfolder "${staging}" \
    -ov \
    -format UDZO \
    "${dmg_path}"
  rm -rf "${staging}"
fi

echo "==> packaged ${dmg_path}"
du -sh "${dmg_path}"
