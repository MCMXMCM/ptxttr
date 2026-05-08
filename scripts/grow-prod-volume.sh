#!/usr/bin/env bash
set -euo pipefail

# grow-prod-volume.sh resizes the root partition and filesystem on the
# ptxt-nstr prod instance after the underlying EBS volume has been enlarged
# (typically by re-running deploy-prod-infra.sh with a larger
# ROOT_VOLUME_SIZE_GIB). It is idempotent: if the partition and filesystem
# are already at the volume's full size, growpart and the resize tools just
# report no-op and exit 0.
#
# Usage:
#   ./scripts/grow-prod-volume.sh                # uses deploy/environments/prod.env
#   DEPLOY_ENV_FILE=other.env ./scripts/grow-prod-volume.sh
#
# The script:
#   1. Resolves the instance id from the CloudFormation stack.
#   2. Waits for any in-flight EBS volume modification to finish optimizing.
#   3. Sends an SSM RunCommand that runs growpart + xfs_growfs/resize2fs.
#   4. Streams the command output back to stdout.
#
# Requires: aws CLI v2 with permission to read CFN, EC2, and run SSM
# commands against the instance role. The instance must have the SSM agent
# running (it does by default on AL2023 with the role we attach in CFN).

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

INSTANCE_ID="$(aws cloudformation describe-stack-resources \
  --region "${AWS_REGION}" \
  --stack-name "${STACK_NAME}" \
  --query "StackResources[?ResourceType=='AWS::EC2::Instance'].PhysicalResourceId | [0]" \
  --output text)"

if [[ -z "${INSTANCE_ID}" || "${INSTANCE_ID}" == "None" ]]; then
  echo "could not resolve EC2 instance from stack ${STACK_NAME}" >&2
  exit 1
fi

echo "Instance: ${INSTANCE_ID}"

ROOT_DEVICE_NAME="$(aws ec2 describe-instances \
  --region "${AWS_REGION}" \
  --instance-ids "${INSTANCE_ID}" \
  --query "Reservations[0].Instances[0].RootDeviceName" \
  --output text)"

if [[ -z "${ROOT_DEVICE_NAME}" || "${ROOT_DEVICE_NAME}" == "None" ]]; then
  echo "could not resolve root device name for ${INSTANCE_ID}" >&2
  exit 1
fi

VOLUME_ID="$(aws ec2 describe-instances \
  --region "${AWS_REGION}" \
  --instance-ids "${INSTANCE_ID}" \
  --query "Reservations[0].Instances[0].BlockDeviceMappings[?DeviceName=='${ROOT_DEVICE_NAME}'].Ebs.VolumeId | [0]" \
  --output text)"

if [[ -z "${VOLUME_ID}" || "${VOLUME_ID}" == "None" ]]; then
  echo "could not resolve root volume id for ${INSTANCE_ID}" >&2
  exit 1
fi

echo "Root device name: ${ROOT_DEVICE_NAME}"
echo "Root volume: ${VOLUME_ID}"

# Wait until any in-flight modification reaches optimizing/completed; AWS
# returns the new block-level size as soon as the modification leaves the
# 'modifying' state, which is what growpart needs.
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
ROOT_DEV="$(findmnt -no SOURCE /)"
ROOT_BASENAME="$(basename "${ROOT_DEV}")"
ROOT_SYSFS="/sys/class/block/${ROOT_BASENAME}"
echo "root device: ${ROOT_DEV}"
if [[ ! -r "${ROOT_SYSFS}/partition" ]]; then
  echo "could not determine partition number from ${ROOT_SYSFS}/partition" >&2
  exit 1
fi
PARENT_DEV="/dev/$(lsblk -no PKNAME "${ROOT_DEV}")"
PART_NUM="$(<"${ROOT_SYSFS}/partition")"
echo "parent device: ${PARENT_DEV}"
echo "partition number: ${PART_NUM}"
echo "before:"
df -hT /
echo
echo "growing partition..."
growpart "${PARENT_DEV}" "${PART_NUM}" || true
FS_TYPE="$(findmnt -no FSTYPE /)"
echo "filesystem: ${FS_TYPE}"
case "${FS_TYPE}" in
  xfs)
    xfs_growfs -d /
    ;;
  ext4|ext3|ext2)
    resize2fs "${ROOT_DEV}"
    ;;
  *)
    echo "unsupported filesystem: ${FS_TYPE}" >&2
    exit 1
    ;;
esac
echo
echo "after:"
df -hT /'

echo
echo "Sending SSM RunCommand..."

# Pass the remote script through base64 to avoid shell-quoting hazards in the
# SSM parameter JSON. The remote side decodes and execs it under sudo bash.
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
  --comment "grow-prod-volume from $(whoami)@$(hostname)" \
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
