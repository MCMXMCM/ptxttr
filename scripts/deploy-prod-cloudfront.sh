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

CLOUDFRONT_STACK_NAME="${CLOUDFRONT_STACK_NAME:-${STACK_NAME:-ptxt-nstr}-cloudfront}"
DOMAIN_NAME="${DOMAIN_NAME:?missing DOMAIN_NAME}"
ORIGIN_DOMAIN_NAME="${ORIGIN_DOMAIN_NAME:?missing ORIGIN_DOMAIN_NAME (required for the CloudFront stack)}"
HOSTED_ZONE_ID="${HOSTED_ZONE_ID:?missing HOSTED_ZONE_ID}"
CLOUDFRONT_PRICE_CLASS="${CLOUDFRONT_PRICE_CLASS:-PriceClass_100}"

"${SCRIPT_DIR}/deploy-cloudfront-cfn.sh" \
  --stack-name "${CLOUDFRONT_STACK_NAME}" \
  --viewer-domain-name "${DOMAIN_NAME}" \
  --origin-domain-name "${ORIGIN_DOMAIN_NAME}" \
  --hosted-zone-id "${HOSTED_ZONE_ID}" \
  --price-class "${CLOUDFRONT_PRICE_CLASS}"
