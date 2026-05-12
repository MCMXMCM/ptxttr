#!/usr/bin/env bash
set -euo pipefail

# grow-prod-data-volume.sh grows the XFS filesystem on /var/lib/ptxt-nstr after
# the backing EBS data volume has been enlarged (typically by increasing
# DATA_VOLUME_SIZE_GIB and re-running deploy-prod-infra.sh, or by modifying
# the volume size in the EC2 console).
#
# Usage:
#   ./scripts/grow-prod-data-volume.sh
#   DEPLOY_ENV_FILE=other.env ./scripts/grow-prod-data-volume.sh
#
# The script:
#   1. Resolves DataVolumeId from the CloudFormation stack outputs.
#   2. Waits for any in-flight EBS volume modification to finish optimizing.
#   3. Sends an SSM RunCommand that runs xfs_growfs on /var/lib/ptxt-nstr.
#   4. Streams the command output back to stdout.
#
# Requires: aws CLI v2 with permission to read CFN, EC2, and run SSM commands
# against the instance. The data volume must be mounted as XFS at
# /var/lib/ptxt-nstr on the instance.

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
AWS_REGION="${AWS_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}"

echo "Stack: ${STACK_NAME}"
echo "Region: ${AWS_REGION}"

INSTANCE_ID="$(aws cloudformation describe-stacks \
  --region "${AWS_REGION}" \
  --stack-name "${STACK_NAME}" \
  --query "Stacks[0].Outputs[?OutputKey=='InstanceId'].OutputValue | [0]" \
  --output text)"

if [[ -z "${INSTANCE_ID}" || "${INSTANCE_ID}" == "None" ]]; then
  echo "could not resolve InstanceId from stack ${STACK_NAME}" >&2
  exit 1
fi

VOLUME_ID="$(aws cloudformation describe-stacks \
  --region "${AWS_REGION}" \
  --stack-name "${STACK_NAME}" \
  --query "Stacks[0].Outputs[?OutputKey=='DataVolumeId'].OutputValue | [0]" \
  --output text)"

if [[ -z "${VOLUME_ID}" || "${VOLUME_ID}" == "None" ]]; then
  echo "could not resolve DataVolumeId from stack ${STACK_NAME}" >&2
  exit 1
fi

echo "Instance: ${INSTANCE_ID}"
echo "Data volume: ${VOLUME_ID}"

while :; do
  STATE="$(aws ec2 describe-volumes-modifications \
    --region "${AWS_REGION}" \
    --volume-ids "${VOLUME_ID}" \
    --query "VolumesModifications[0].ModificationState" \
    --output text 2>/dev/null || echo "None")"
  if [[ "${STATE}" == "None" || "${STATE}" == "completed" || "${STATE}" == "optimizing" ]]; then
    echo "Volume modification state: ${STATE}"
    break
  fi
  echo "Waiting for volume modification (state=${STATE})..."
  sleep 10
done

REMOTE_SCRIPT='set -euo pipefail
MP="/var/lib/ptxt-nstr"
if ! mountpoint -q "${MP}"; then
  echo "${MP} is not a mountpoint" >&2
  exit 1
fi
FS_TYPE="$(findmnt -no FSTYPE "${MP}")"
if [[ "${FS_TYPE}" != "xfs" ]]; then
  echo "expected xfs on ${MP}, got ${FS_TYPE}" >&2
  exit 1
fi
echo "before:"
df -hT "${MP}"
echo
xfs_growfs -d "${MP}"
echo
echo "after:"
df -hT "${MP}"'

echo
echo "Sending SSM RunCommand..."

REMOTE_SCRIPT_B64="$(printf '%s' "${REMOTE_SCRIPT}" | base64 | tr -d '\n')"
SSM_PARAMS_FILE="$(mktemp)"
trap 'rm -f "${SSM_PARAMS_FILE}"' EXIT
cat >"${SSM_PARAMS_FILE}" <<JSON
{"commands":["echo ${REMOTE_SCRIPT_B64} | base64 -d | sudo bash"]}
JSON

COMMAND_ID="$(aws ssm send-command \
  --region "${AWS_REGION}" \
  --instance-ids "${INSTANCE_ID}" \
  --document-name AWS-RunShellScript \
  --comment "grow-prod-data-volume from $(whoami)@$(hostname)" \
  --parameters "file://${SSM_PARAMS_FILE}" \
  --query "Command.CommandId" \
  --output text)"

echo "CommandId: ${COMMAND_ID}"

aws ssm wait command-executed \
  --region "${AWS_REGION}" \
  --command-id "${COMMAND_ID}" \
  --instance-id "${INSTANCE_ID}" || true

INVOCATION="$(aws ssm get-command-invocation \
  --region "${AWS_REGION}" \
  --command-id "${COMMAND_ID}" \
  --instance-id "${INSTANCE_ID}")"

STATUS="$(jq -r '.Status' <<<"${INVOCATION}")"
STDOUT="$(jq -r '.StandardOutputContent' <<<"${INVOCATION}")"
STDERR="$(jq -r '.StandardErrorContent' <<<"${INVOCATION}")"

echo
echo "----- stdout -----"
printf '%s\n' "${STDOUT}"
if [[ -n "${STDERR}" && "${STDERR}" != "null" ]]; then
  echo "----- stderr -----"
  printf '%s\n' "${STDERR}"
fi
echo "------------------"
echo "Status: ${STATUS}"

if [[ "${STATUS}" != "Success" ]]; then
  exit 1
fi
