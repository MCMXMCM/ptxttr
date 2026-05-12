#!/usr/bin/env bash
# After an app artifact deploy, purge CloudFront edge cache for /static/* so
# new JS/CSS from the origin is served immediately. CachingOptimized ignores
# query strings on that path, so bumping ?v= in HTML alone does not bust POPs.
#
# Requires AWS CLI credentials. Resolves the distribution id from
# CLOUDFRONT_DISTRIBUTION_ID, else from CloudFormation stack CLOUDFRONT_STACK_NAME
# (same region as the CloudFront stack, default us-east-1).
#
# Set SKIP_CLOUDFRONT_INVALIDATION=1 to no-op (e.g. local dry runs).

set -euo pipefail

if [[ "${SKIP_CLOUDFRONT_INVALIDATION:-0}" == "1" ]]; then
  echo "Skipping CloudFront static invalidation (SKIP_CLOUDFRONT_INVALIDATION=1)." >&2
  exit 0
fi

DIST_ID="${CLOUDFRONT_DISTRIBUTION_ID:-}"
REGION="${CLOUDFRONT_REGION:-us-east-1}"
cf_stack="${CLOUDFRONT_STACK_NAME:-}"

if [[ -z "${DIST_ID}" && -n "${cf_stack}" ]]; then
  if ! DIST_ID="$(
    aws cloudformation describe-stacks \
      --region "${REGION}" \
      --stack-name "${cf_stack}" \
      --query "Stacks[0].Outputs[?OutputKey=='DistributionId'].OutputValue | [0]" \
      --output text
  )"; then
    echo "error: could not read CloudFront stack outputs (stack=${cf_stack} region=${REGION})" >&2
    exit 1
  fi
fi

# AWS CLI prints the literal "None" when the JMESPath result is null.
if [[ -z "${DIST_ID}" || "${DIST_ID}" == "None" ]]; then
  if [[ -n "${cf_stack}" ]]; then
    echo "error: stack ${cf_stack} has no DistributionId output (wrong template or region?)" >&2
    exit 1
  fi
  echo "Skipping CloudFront static invalidation (set CLOUDFRONT_DISTRIBUTION_ID or CLOUDFRONT_STACK_NAME)." >&2
  exit 0
fi

echo "Creating CloudFront invalidation for /static/* (distribution ${DIST_ID})..." >&2
INV_ID="$(
  aws cloudfront create-invalidation \
    --distribution-id "${DIST_ID}" \
    --paths "/static/*" \
    --output text \
    --query 'Invalidation.Id'
)"
echo "CloudFront invalidation ${INV_ID} submitted (paths: /static/*)." >&2
