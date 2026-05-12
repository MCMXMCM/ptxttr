#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: deploy-cloudfront-cfn.sh --stack-name NAME --viewer-domain-name DOMAIN --origin-domain-name DOMAIN --hosted-zone-id ZONE [options]

Deploys (or updates) the CloudFront CloudFormation stack in us-east-1. The
stack owns its own ACM certificate (DNS validated against the supplied hosted
zone) and a CloudFront Function that sets X-Forwarded-Host from the viewer
Host header so the origin can render canonical URLs with the viewer hostname
(CloudFront does not allow functions to set X-Forwarded-Proto).

Uses create-stack / update-stack instead of cloudformation deploy because
changeset-based deploy can trip account-level EarlyValidation hooks that do
not surface actionable errors for this template.

After this script returns, the stack's DistributionDomainName output is what
viewer DNS should alias once you are ready to cut over.

Options:
  --stack-name NAME                CloudFormation stack name (e.g. ptxt-nstr-cloudfront-prod).
  --viewer-domain-name DOMAIN      Public hostname (e.g. example.com).
  --origin-domain-name DOMAIN      Dedicated origin hostname (e.g. origin.example.com).
  --hosted-zone-id ZONE            Route 53 hosted zone for DNS-validated ACM.
  --price-class CLASS              CloudFront price class (default PriceClass_100).
EOF
}

STACK_NAME=""
VIEWER_DOMAIN_NAME=""
ORIGIN_DOMAIN_NAME=""
HOSTED_ZONE_ID=""
PRICE_CLASS="PriceClass_100"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --stack-name)
      STACK_NAME="$2"
      shift 2
      ;;
    --viewer-domain-name)
      VIEWER_DOMAIN_NAME="$2"
      shift 2
      ;;
    --origin-domain-name)
      ORIGIN_DOMAIN_NAME="$2"
      shift 2
      ;;
    --hosted-zone-id)
      HOSTED_ZONE_ID="$2"
      shift 2
      ;;
    --price-class)
      PRICE_CLASS="$2"
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

if [[ -z "${STACK_NAME}" || -z "${VIEWER_DOMAIN_NAME}" || -z "${ORIGIN_DOMAIN_NAME}" || -z "${HOSTED_ZONE_ID}" ]]; then
  usage >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
TEMPLATE_PATH="${REPO_ROOT}/deploy/cloudformation/ptxt-nstr-cloudfront.yaml"
REGION="us-east-1"

PARAMS=(
  "ParameterKey=ViewerDomainName,ParameterValue=${VIEWER_DOMAIN_NAME}"
  "ParameterKey=OriginDomainName,ParameterValue=${ORIGIN_DOMAIN_NAME}"
  "ParameterKey=HostedZoneId,ParameterValue=${HOSTED_ZONE_ID}"
  "ParameterKey=PriceClass,ParameterValue=${PRICE_CLASS}"
)

STACK_STATUS="$(
  aws cloudformation describe-stacks \
    --region "${REGION}" \
    --stack-name "${STACK_NAME}" \
    --query 'Stacks[0].StackStatus' \
    --output text 2>/dev/null || echo DOES_NOT_EXIST
)"

if [[ "${STACK_STATUS}" == "DOES_NOT_EXIST" ]]; then
  echo "Creating stack ${STACK_NAME}..."
  aws cloudformation create-stack \
    --region "${REGION}" \
    --stack-name "${STACK_NAME}" \
    --template-body "file://${TEMPLATE_PATH}" \
    --capabilities CAPABILITY_IAM \
    --parameters "${PARAMS[@]}"
  aws cloudformation wait stack-create-complete --region "${REGION}" --stack-name "${STACK_NAME}"
elif [[ "${STACK_STATUS}" == "ROLLBACK_COMPLETE" ]] || [[ "${STACK_STATUS}" == "DELETE_FAILED" ]]; then
  echo "stack ${STACK_NAME} is ${STACK_STATUS}; delete it before redeploying:" >&2
  echo "  aws cloudformation delete-stack --region ${REGION} --stack-name ${STACK_NAME}" >&2
  exit 1
else
  echo "Updating stack ${STACK_NAME}..."
  set +e
  UPDATE_ERR="$(
    aws cloudformation update-stack \
      --region "${REGION}" \
      --stack-name "${STACK_NAME}" \
      --template-body "file://${TEMPLATE_PATH}" \
      --capabilities CAPABILITY_IAM \
      --parameters "${PARAMS[@]}" 2>&1
  )"
  UPDATE_EXIT=$?
  set -e
  if [[ "${UPDATE_EXIT}" -ne 0 ]]; then
    if [[ "${UPDATE_ERR}" == *"No updates are to be performed"* ]]; then
      echo "No CloudFormation updates (template unchanged)."
    else
      echo "${UPDATE_ERR}" >&2
      exit "${UPDATE_EXIT}"
    fi
  else
    aws cloudformation wait stack-update-complete --region "${REGION}" --stack-name "${STACK_NAME}"
  fi
fi

DISTRIBUTION_DOMAIN_NAME="$(aws cloudformation describe-stacks \
  --region "${REGION}" \
  --stack-name "${STACK_NAME}" \
  --query "Stacks[0].Outputs[?OutputKey=='DistributionDomainName'].OutputValue | [0]" \
  --output text)"

echo
echo "Deployed CloudFront stack ${STACK_NAME}."
echo "DistributionDomainName=${DISTRIBUTION_DOMAIN_NAME}"
echo
echo "Next step: set VIEWER_DNS_MODE=CloudFrontAlias and"
echo "CLOUDFRONT_DISTRIBUTION_DOMAIN_NAME=${DISTRIBUTION_DOMAIN_NAME} in your prod.env"
echo "then run 'make deploy-infra' to alias viewer DNS at the distribution."
