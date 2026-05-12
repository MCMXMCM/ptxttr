#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: reapply-cfn-artifact.sh --stack-name NAME --domain-name DOMAIN --artifact-bucket BUCKET --artifact-key KEY [options]

Uploads are assumed to be in S3 already. This script finds the stack's instance
and reapplies the deployment artifact over SSM without changing CloudFormation.
EOF
}

STACK_NAME=""
DOMAIN_NAME="example.com"
ORIGIN_DOMAIN_NAME=""
ARTIFACT_BUCKET=""
ARTIFACT_KEY=""
ARTIFACT_VERSION=""
APP_USER="ptxt"
CADDY_VERSION="2.9.1"
GO_MEMORY_LIMIT="1GiB"
EVENT_RETENTION="20000"
DEBUG_ENABLED="false"
COMPACT_ON_START="false"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --stack-name)
      STACK_NAME="$2"
      shift 2
      ;;
    --domain-name)
      DOMAIN_NAME="$2"
      shift 2
      ;;
    --origin-domain-name)
      ORIGIN_DOMAIN_NAME="$2"
      shift 2
      ;;
    --artifact-bucket)
      ARTIFACT_BUCKET="$2"
      shift 2
      ;;
    --artifact-key)
      ARTIFACT_KEY="$2"
      shift 2
      ;;
    --artifact-version)
      ARTIFACT_VERSION="$2"
      shift 2
      ;;
    --app-user)
      APP_USER="$2"
      shift 2
      ;;
    --caddy-version)
      CADDY_VERSION="$2"
      shift 2
      ;;
    --go-memory-limit)
      GO_MEMORY_LIMIT="$2"
      shift 2
      ;;
    --event-retention)
      EVENT_RETENTION="$2"
      shift 2
      ;;
    --debug-enabled)
      DEBUG_ENABLED="$2"
      shift 2
      ;;
    --compact-on-start)
      COMPACT_ON_START="$2"
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

if [[ -z "${STACK_NAME}" || -z "${ARTIFACT_BUCKET}" || -z "${ARTIFACT_KEY}" ]]; then
  usage >&2
  exit 1
fi

INSTANCE_ID="$(aws cloudformation describe-stacks \
  --stack-name "${STACK_NAME}" \
  --query "Stacks[0].Outputs[?OutputKey=='InstanceId'].OutputValue | [0]" \
  --output text)"

if [[ -z "${INSTANCE_ID}" || "${INSTANCE_ID}" == "None" ]]; then
  echo "failed to resolve InstanceId from stack ${STACK_NAME}" >&2
  exit 1
fi

DATA_VOLUME_ID="$(aws cloudformation describe-stacks \
  --stack-name "${STACK_NAME}" \
  --query "Stacks[0].Outputs[?OutputKey=='DataVolumeId'].OutputValue | [0]" \
  --output text)"

if [[ -z "${DATA_VOLUME_ID}" || "${DATA_VOLUME_ID}" == "None" ]]; then
  echo "failed to resolve DataVolumeId from stack ${STACK_NAME}" >&2
  exit 1
fi

echo "Waiting for SSM on ${INSTANCE_ID}..."
for _ in $(seq 1 40); do
  PING_STATUS="$(aws ssm describe-instance-information \
    --filters "Key=InstanceIds,Values=${INSTANCE_ID}" \
    --query "InstanceInformationList[0].PingStatus" \
    --output text 2>/dev/null || true)"
  if [[ "${PING_STATUS}" == "Online" ]]; then
    break
  fi
  sleep 15
done

if [[ "${PING_STATUS:-}" != "Online" ]]; then
  echo "instance ${INSTANCE_ID} did not become SSM-online in time" >&2
  exit 1
fi

SSM_COMMANDS=(
  "set -euo pipefail"
  "tmpdir=\$(mktemp -d)"
  "trap 'rm -rf \"\$tmpdir\"' EXIT"
)

if [[ -n "${ARTIFACT_VERSION}" ]]; then
  SSM_COMMANDS+=("aws s3api get-object --bucket '${ARTIFACT_BUCKET}' --key '${ARTIFACT_KEY}' --version-id '${ARTIFACT_VERSION}' \"\$tmpdir/ptxt-nstr-artifact.tar.gz\"")
else
  SSM_COMMANDS+=("aws s3api get-object --bucket '${ARTIFACT_BUCKET}' --key '${ARTIFACT_KEY}' \"\$tmpdir/ptxt-nstr-artifact.tar.gz\"")
fi

SSM_COMMANDS+=(
  "tar -xzf \"\$tmpdir/ptxt-nstr-artifact.tar.gz\" -C \"\$tmpdir\""
  "bash \"\$tmpdir/install.sh\" --artifact-dir \"\$tmpdir\" --domain '${DOMAIN_NAME}' --origin-domain '${ORIGIN_DOMAIN_NAME}' --app-user '${APP_USER}' --caddy-version '${CADDY_VERSION}' --gomemlimit '${GO_MEMORY_LIMIT}' --ptxt-addr '127.0.0.1:8080' --ptxt-db '/var/lib/ptxt-nstr/ptxt-nstr.sqlite' --ptxt-debug '${DEBUG_ENABLED}' --ptxt-event-retention '${EVENT_RETENTION}' --ptxt-compact-on-start '${COMPACT_ON_START}' --data-volume-id '${DATA_VOLUME_ID}'"
)

SSM_COMMANDS_JSON="$(
  python3 - "${SSM_COMMANDS[@]}" <<'PY'
import json
import sys

print(json.dumps(sys.argv[1:]))
PY
)"

COMMAND_ID="$(aws ssm send-command \
  --instance-ids "${INSTANCE_ID}" \
  --document-name AWS-RunShellScript \
  --comment "Reapply ptxt-nstr artifact on live instance" \
  --parameters "commands=${SSM_COMMANDS_JSON}" \
  --query "Command.CommandId" \
  --output text)"

aws ssm wait command-executed --command-id "${COMMAND_ID}" --instance-id "${INSTANCE_ID}"

echo "Reapplied artifact on ${INSTANCE_ID}."
