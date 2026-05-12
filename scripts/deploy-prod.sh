#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEPLOY_ENV_FILE="${DEPLOY_ENV_FILE:-${REPO_ROOT}/deploy/environments/prod.env}"

if [[ ! -f "${DEPLOY_ENV_FILE}" ]]; then
  echo "deployment env file not found: ${DEPLOY_ENV_FILE}" >&2
  exit 1
fi

set -a
source "${DEPLOY_ENV_FILE}"
set +a

STACK_NAME="${STACK_NAME:?missing STACK_NAME}"
DOMAIN_NAME="${DOMAIN_NAME:?missing DOMAIN_NAME}"
ORIGIN_DOMAIN_NAME="${ORIGIN_DOMAIN_NAME:-}"
ARTIFACT_BUCKET="${ARTIFACT_BUCKET:?missing ARTIFACT_BUCKET}"
ARTIFACT_PREFIX="${ARTIFACT_PREFIX:?missing ARTIFACT_PREFIX}"
APP_USER="${APP_USER:?missing APP_USER}"
CADDY_VERSION="${CADDY_VERSION:?missing CADDY_VERSION}"
GO_MEMORY_LIMIT="${GO_MEMORY_LIMIT:?missing GO_MEMORY_LIMIT}"
EVENT_RETENTION="${EVENT_RETENTION:?missing EVENT_RETENTION}"
DEBUG_ENABLED="${DEBUG_ENABLED:?missing DEBUG_ENABLED}"
COMPACT_ON_START="${COMPACT_ON_START:?missing COMPACT_ON_START}"
VERSION="${VERSION:-}"

unset TMPDIR TMP TEMP TEMPDIR

UPLOAD_ARGS=(
  --bucket "${ARTIFACT_BUCKET}"
  --prefix "${ARTIFACT_PREFIX}"
)

if [[ -n "${VERSION}" ]]; then
  UPLOAD_ARGS+=(--version "${VERSION}")
fi

UPLOAD_OUTPUT="$("${SCRIPT_DIR}/upload-cfn-artifact.sh" "${UPLOAD_ARGS[@]}")"

ARTIFACT_KEY=""
ARTIFACT_VERSION=""

while IFS='=' read -r key value; do
  case "${key}" in
    ArtifactKey)
      ARTIFACT_KEY="${value}"
      ;;
    ArtifactVersion)
      ARTIFACT_VERSION="${value}"
      ;;
  esac
done <<<"${UPLOAD_OUTPUT}"

if [[ -z "${ARTIFACT_KEY}" ]]; then
  echo "failed to parse ArtifactKey from upload output" >&2
  echo "${UPLOAD_OUTPUT}" >&2
  exit 1
fi

DEPLOY_ARGS=(
  --stack-name "${STACK_NAME}"
  --domain-name "${DOMAIN_NAME}"
  --origin-domain-name "${ORIGIN_DOMAIN_NAME}"
  --artifact-bucket "${ARTIFACT_BUCKET}"
  --artifact-key "${ARTIFACT_KEY}"
  --app-user "${APP_USER}"
  --caddy-version "${CADDY_VERSION}"
  --go-memory-limit "${GO_MEMORY_LIMIT}"
  --event-retention "${EVENT_RETENTION}"
  --debug-enabled "${DEBUG_ENABLED}"
  --compact-on-start "${COMPACT_ON_START}"
)

if [[ -n "${ARTIFACT_VERSION}" ]]; then
  DEPLOY_ARGS+=(--artifact-version "${ARTIFACT_VERSION}")
fi

"${SCRIPT_DIR}/reapply-cfn-artifact.sh" "${DEPLOY_ARGS[@]}"

"${SCRIPT_DIR}/cloudfront-invalidate-static.sh"

echo
echo "Deployed app on ${STACK_NAME} to https://${DOMAIN_NAME}/"
echo "Deploy env file: ${DEPLOY_ENV_FILE}"
echo "Artifact bucket: ${ARTIFACT_BUCKET}"
echo "Artifact key: ${ARTIFACT_KEY}"
if [[ -n "${ARTIFACT_VERSION}" ]]; then
  echo "Artifact version: ${ARTIFACT_VERSION}"
fi
