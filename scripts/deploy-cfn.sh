#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: deploy-cfn.sh --stack-name NAME --hosted-zone-id ZONE --vpc-id VPC --subnet-id SUBNET --artifact-bucket BUCKET --artifact-key KEY [options]

Deploys the single-instance CloudFormation stack and then reapplies the artifact
through SSM so updates work on an existing instance.
EOF
}

STACK_NAME=""
DOMAIN_NAME="example.com"
ORIGIN_DOMAIN_NAME=""
VIEWER_DNS_MODE="DirectToEip"
CLOUDFRONT_DISTRIBUTION_DOMAIN_NAME=""
HOSTED_ZONE_ID=""
VPC_ID=""
SUBNET_ID=""
AVAILABILITY_ZONE=""
INSTANCE_TYPE="t4g.small"
ROOT_VOLUME_SIZE_GIB="30"
DATA_VOLUME_SIZE_GIB="20"
DATA_VOLUME_TYPE="gp3"
EXISTING_DATA_VOLUME_ID=""
ARTIFACT_BUCKET=""
ARTIFACT_PREFIX="ptxt-nstr/deploy"
ARTIFACT_KEY=""
ARTIFACT_VERSION=""
EXISTING_EIP_ALLOCATION_ID=""
EXISTING_EIP_ADDRESS=""
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
    --viewer-dns-mode)
      VIEWER_DNS_MODE="$2"
      shift 2
      ;;
    --cloudfront-distribution-domain-name)
      CLOUDFRONT_DISTRIBUTION_DOMAIN_NAME="$2"
      shift 2
      ;;
    --hosted-zone-id)
      HOSTED_ZONE_ID="$2"
      shift 2
      ;;
    --vpc-id)
      VPC_ID="$2"
      shift 2
      ;;
    --subnet-id)
      SUBNET_ID="$2"
      shift 2
      ;;
    --availability-zone)
      AVAILABILITY_ZONE="$2"
      shift 2
      ;;
    --instance-type)
      INSTANCE_TYPE="$2"
      shift 2
      ;;
    --root-volume-size-gib)
      ROOT_VOLUME_SIZE_GIB="$2"
      shift 2
      ;;
    --data-volume-size-gib)
      DATA_VOLUME_SIZE_GIB="$2"
      shift 2
      ;;
    --data-volume-type)
      DATA_VOLUME_TYPE="$2"
      shift 2
      ;;
    --existing-data-volume-id)
      EXISTING_DATA_VOLUME_ID="$2"
      shift 2
      ;;
    --artifact-bucket)
      ARTIFACT_BUCKET="$2"
      shift 2
      ;;
    --artifact-prefix)
      ARTIFACT_PREFIX="$2"
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
    --existing-eip-allocation-id)
      EXISTING_EIP_ALLOCATION_ID="$2"
      shift 2
      ;;
    --existing-eip-address)
      EXISTING_EIP_ADDRESS="$2"
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

if [[ -z "${STACK_NAME}" || -z "${HOSTED_ZONE_ID}" || -z "${VPC_ID}" || -z "${SUBNET_ID}" || -z "${ARTIFACT_BUCKET}" || -z "${ARTIFACT_KEY}" ]]; then
  usage >&2
  exit 1
fi

if [[ -n "${EXISTING_EIP_ALLOCATION_ID}" && -z "${EXISTING_EIP_ADDRESS}" ]]; then
  echo "--existing-eip-address is required when reusing an Elastic IP" >&2
  exit 1
fi

if [[ -z "${AVAILABILITY_ZONE}" ]]; then
  AVAILABILITY_ZONE="$(aws ec2 describe-subnets \
    --subnet-ids "${SUBNET_ID}" \
    --query "Subnets[0].AvailabilityZone" \
    --output text)"
  if [[ -z "${AVAILABILITY_ZONE}" || "${AVAILABILITY_ZONE}" == "None" ]]; then
    echo "failed to resolve AvailabilityZone for ${SUBNET_ID}" >&2
    exit 1
  fi
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TEMPLATE_PATH="${REPO_ROOT}/deploy/cloudformation/ptxt-nstr-single-instance.yaml"

aws cloudformation deploy \
  --stack-name "${STACK_NAME}" \
  --template-file "${TEMPLATE_PATH}" \
  --capabilities CAPABILITY_NAMED_IAM \
  --parameter-overrides \
    DomainName="${DOMAIN_NAME}" \
    OriginDomainName="${ORIGIN_DOMAIN_NAME}" \
    ViewerDnsMode="${VIEWER_DNS_MODE}" \
    CloudFrontDistributionDomainName="${CLOUDFRONT_DISTRIBUTION_DOMAIN_NAME}" \
    HostedZoneId="${HOSTED_ZONE_ID}" \
    VpcId="${VPC_ID}" \
    SubnetId="${SUBNET_ID}" \
    AvailabilityZone="${AVAILABILITY_ZONE}" \
    InstanceType="${INSTANCE_TYPE}" \
    RootVolumeSizeGiB="${ROOT_VOLUME_SIZE_GIB}" \
    DataVolumeSizeGiB="${DATA_VOLUME_SIZE_GIB}" \
    DataVolumeType="${DATA_VOLUME_TYPE}" \
    ExistingDataVolumeId="${EXISTING_DATA_VOLUME_ID}" \
    ArtifactBucket="${ARTIFACT_BUCKET}" \
    ArtifactPrefix="${ARTIFACT_PREFIX}" \
    ArtifactKey="${ARTIFACT_KEY}" \
    ArtifactVersion="${ARTIFACT_VERSION}" \
    ExistingElasticIpAllocationId="${EXISTING_EIP_ALLOCATION_ID}" \
    ExistingElasticIpAddress="${EXISTING_EIP_ADDRESS}" \
    AppUser="${APP_USER}" \
    CaddyVersion="${CADDY_VERSION}" \
    GoMemoryLimit="${GO_MEMORY_LIMIT}" \
    EventRetention="${EVENT_RETENTION}" \
    DebugEnabled="${DEBUG_ENABLED}" \
    CompactOnStart="${COMPACT_ON_START}"

REAPPLY_ARGS=(
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
  REAPPLY_ARGS+=(--artifact-version "${ARTIFACT_VERSION}")
fi

"${SCRIPT_DIR}/reapply-cfn-artifact.sh" "${REAPPLY_ARGS[@]}"

echo "Deployed stack ${STACK_NAME} and reapplied the current artifact."
