#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: upload-cfn-artifact.sh --bucket BUCKET [--prefix PREFIX] [--version VERSION]

Builds the ARM64 deployment artifact and uploads it to S3.
EOF
}

BUCKET=""
PREFIX="ptxt-nstr/deploy"
VERSION=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bucket)
      BUCKET="$2"
      shift 2
      ;;
    --prefix)
      PREFIX="$2"
      shift 2
      ;;
    --version)
      VERSION="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "${BUCKET}" ]]; then
  usage >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

if [[ -n "${VERSION}" ]]; then
  ARTIFACT_PATH="$(cd "${REPO_ROOT}" && VERSION="${VERSION}" "${SCRIPT_DIR}/build-cfn-artifact.sh")"
else
  ARTIFACT_PATH="$(cd "${REPO_ROOT}" && "${SCRIPT_DIR}/build-cfn-artifact.sh")"
fi

ARTIFACT_NAME="$(basename "${ARTIFACT_PATH}")"
KEY="${PREFIX%/}/${ARTIFACT_NAME}"

VERSION_ID="$(aws s3api put-object \
  --bucket "${BUCKET}" \
  --key "${KEY}" \
  --body "${ARTIFACT_PATH}" \
  --query "VersionId" \
  --output text)"

aws s3api put-object \
  --bucket "${BUCKET}" \
  --key "${KEY}.sha256" \
  --body "${ARTIFACT_PATH}.sha256" >/dev/null

echo "ArtifactBucket=${BUCKET}"
echo "ArtifactKey=${KEY}"
if [[ "${VERSION_ID}" != "None" ]]; then
  echo "ArtifactVersion=${VERSION_ID}"
fi
