#!/usr/bin/env bash
# Sign + notarize + staple the built .app. Idempotent: safe to re-run on the
# same .app. No-ops gracefully if DEVELOPER_ID_APPLICATION is unset so local
# development builds still work without an Apple cert.
#
# Required env (when DEVELOPER_ID_APPLICATION is set):
#   DEVELOPER_ID_APPLICATION   Common Name of the Developer ID Application
#                              certificate (e.g. "Developer ID Application:
#                              Jane Doe (TEAM12345)").
# Either:
#   APPLE_ID                   Apple ID email
#   APPLE_TEAM_ID              Team identifier (10-char alphanumeric)
#   APPLE_APP_SPECIFIC_PASSWORD App-specific password from appleid.apple.com
# Or:
#   APPLE_API_KEY_PATH         Path to AuthKey_*.p8 from App Store Connect
#   APPLE_API_KEY_ID           Key ID
#   APPLE_API_ISSUER           Issuer UUID
#
# Optional local file (gitignored): copy scripts/desktop/signing.env.example to
# scripts/desktop/signing.env and fill in values so you do not need to export
# them in every shell session. If you already exported a variable before
# invoking make, that value is kept (signing.env does not override it).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
signing_env="${repo_root}/scripts/desktop/signing.env"

# Remember signing-related vars already set in the environment (e.g. CI or
# DEVELOPER_ID_APPLICATION=… make desktop-sign) before sourcing signing.env.
_save_signing_env() {
  if [[ -n "${DEVELOPER_ID_APPLICATION+x}" ]]; then
    _ptxt_saved_DEVELOPER_ID_APPLICATION="${DEVELOPER_ID_APPLICATION}"
  fi
  if [[ -n "${APPLE_ID+x}" ]]; then
    _ptxt_saved_APPLE_ID="${APPLE_ID}"
  fi
  if [[ -n "${APPLE_TEAM_ID+x}" ]]; then
    _ptxt_saved_APPLE_TEAM_ID="${APPLE_TEAM_ID}"
  fi
  if [[ -n "${APPLE_APP_SPECIFIC_PASSWORD+x}" ]]; then
    _ptxt_saved_APPLE_APP_SPECIFIC_PASSWORD="${APPLE_APP_SPECIFIC_PASSWORD}"
  fi
  if [[ -n "${APPLE_API_KEY_PATH+x}" ]]; then
    _ptxt_saved_APPLE_API_KEY_PATH="${APPLE_API_KEY_PATH}"
  fi
  if [[ -n "${APPLE_API_KEY_ID+x}" ]]; then
    _ptxt_saved_APPLE_API_KEY_ID="${APPLE_API_KEY_ID}"
  fi
  if [[ -n "${APPLE_API_ISSUER+x}" ]]; then
    _ptxt_saved_APPLE_API_ISSUER="${APPLE_API_ISSUER}"
  fi
}

_restore_signing_env() {
  if [[ -n "${_ptxt_saved_DEVELOPER_ID_APPLICATION+x}" ]]; then
    export DEVELOPER_ID_APPLICATION="${_ptxt_saved_DEVELOPER_ID_APPLICATION}"
  fi
  if [[ -n "${_ptxt_saved_APPLE_ID+x}" ]]; then
    export APPLE_ID="${_ptxt_saved_APPLE_ID}"
  fi
  if [[ -n "${_ptxt_saved_APPLE_TEAM_ID+x}" ]]; then
    export APPLE_TEAM_ID="${_ptxt_saved_APPLE_TEAM_ID}"
  fi
  if [[ -n "${_ptxt_saved_APPLE_APP_SPECIFIC_PASSWORD+x}" ]]; then
    export APPLE_APP_SPECIFIC_PASSWORD="${_ptxt_saved_APPLE_APP_SPECIFIC_PASSWORD}"
  fi
  if [[ -n "${_ptxt_saved_APPLE_API_KEY_PATH+x}" ]]; then
    export APPLE_API_KEY_PATH="${_ptxt_saved_APPLE_API_KEY_PATH}"
  fi
  if [[ -n "${_ptxt_saved_APPLE_API_KEY_ID+x}" ]]; then
    export APPLE_API_KEY_ID="${_ptxt_saved_APPLE_API_KEY_ID}"
  fi
  if [[ -n "${_ptxt_saved_APPLE_API_ISSUER+x}" ]]; then
    export APPLE_API_ISSUER="${_ptxt_saved_APPLE_API_ISSUER}"
  fi
}

_save_signing_env
if [[ -f "${signing_env}" ]]; then
  set -a
  # shellcheck source=/dev/null
  source "${signing_env}"
  set +a
  _restore_signing_env
fi

app_path="${repo_root}/cmd/desktop/build/bin/ptxt-nstr.app"
entitlements="${repo_root}/cmd/desktop/build/darwin/entitlements.plist"

if [[ ! -d "${app_path}" ]]; then
  echo "no .app at ${app_path}; run scripts/desktop/build-mac.sh first" >&2
  exit 1
fi

if [[ -z "${DEVELOPER_ID_APPLICATION:-}" ]]; then
  echo "DEVELOPER_ID_APPLICATION unset; skipping codesign + notarize." >&2
  echo "(The build is usable locally after right-click -> Open.)" >&2
  exit 0
fi

if [[ ! -f "${entitlements}" ]]; then
  echo "missing entitlements at ${entitlements}" >&2
  exit 1
fi

echo "==> codesign --deep --options runtime"
codesign \
  --force \
  --deep \
  --options runtime \
  --timestamp \
  --entitlements "${entitlements}" \
  --sign "${DEVELOPER_ID_APPLICATION}" \
  "${app_path}"

echo "==> codesign verify"
codesign --verify --deep --strict "${app_path}"

if [[ -n "${APPLE_API_KEY_PATH:-}" && -n "${APPLE_API_KEY_ID:-}" && -n "${APPLE_API_ISSUER:-}" ]]; then
  echo "==> notarytool submit (API key)"
  zip_base="$(mktemp -t ptxt-desktop)"
  rm -f "${zip_base}"
  zip_path="${zip_base}.zip"
  ditto -c -k --keepParent "${app_path}" "${zip_path}"
  xcrun notarytool submit "${zip_path}" \
    --key "${APPLE_API_KEY_PATH}" \
    --key-id "${APPLE_API_KEY_ID}" \
    --issuer "${APPLE_API_ISSUER}" \
    --wait
  rm -f "${zip_path}"
elif [[ -n "${APPLE_ID:-}" && -n "${APPLE_TEAM_ID:-}" && -n "${APPLE_APP_SPECIFIC_PASSWORD:-}" ]]; then
  echo "==> notarytool submit (Apple ID)"
  zip_base="$(mktemp -t ptxt-desktop)"
  rm -f "${zip_base}"
  zip_path="${zip_base}.zip"
  ditto -c -k --keepParent "${app_path}" "${zip_path}"
  xcrun notarytool submit "${zip_path}" \
    --apple-id "${APPLE_ID}" \
    --team-id "${APPLE_TEAM_ID}" \
    --password "${APPLE_APP_SPECIFIC_PASSWORD}" \
    --wait
  rm -f "${zip_path}"
else
  echo "Apple notarization credentials not set; skipping notarytool (codesign only)." >&2
  exit 0
fi

echo "==> stapler staple"
xcrun stapler staple "${app_path}"
