#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
OUTPUT_DIR="${OUTPUT_DIR:-${REPO_ROOT}/dist}"
VERSION="${VERSION:-}"

if [[ -z "${VERSION}" ]]; then
  GIT_SHA="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || true)"
  TIMESTAMP="$(date -u +%Y%m%d%H%M%S)"
  if [[ -n "${GIT_SHA}" ]]; then
    VERSION="${TIMESTAMP}-${GIT_SHA}"
  else
    VERSION="${TIMESTAMP}"
  fi
fi

ARTIFACT_NAME="ptxt-nstr-deploy-linux-arm64-${VERSION}.tar.gz"
STAGING_DIR="$(mktemp -d)"
trap 'rm -rf "${STAGING_DIR}"' EXIT

mkdir -p "${OUTPUT_DIR}"

(cd "${REPO_ROOT}" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "${STAGING_DIR}/ptxt-nstr" ./cmd/server)

cp "${REPO_ROOT}/deploy/artifact/install.sh" "${STAGING_DIR}/install.sh"
cp "${REPO_ROOT}/deploy/artifact/ptxt-nstr.env.tmpl" "${STAGING_DIR}/ptxt-nstr.env.tmpl"
cp "${REPO_ROOT}/deploy/artifact/ptxt-nstr.service.tmpl" "${STAGING_DIR}/ptxt-nstr.service.tmpl"
cp "${REPO_ROOT}/deploy/artifact/Caddyfile.tmpl" "${STAGING_DIR}/Caddyfile.tmpl"
cp "${REPO_ROOT}/deploy/artifact/caddy.service.tmpl" "${STAGING_DIR}/caddy.service.tmpl"
cp "${REPO_ROOT}/deploy/artifact/amazon-cloudwatch-agent.json.tmpl" "${STAGING_DIR}/amazon-cloudwatch-agent.json.tmpl"

chmod 0755 "${STAGING_DIR}/install.sh" "${STAGING_DIR}/ptxt-nstr"

tar -C "${STAGING_DIR}" -czf "${OUTPUT_DIR}/${ARTIFACT_NAME}" .
(cd "${OUTPUT_DIR}" && shasum -a 256 "${ARTIFACT_NAME}" > "${ARTIFACT_NAME}.sha256")

echo "${OUTPUT_DIR}/${ARTIFACT_NAME}"
