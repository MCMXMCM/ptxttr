#!/usr/bin/env bash
# Snapshot Caddy request volume (CloudWatch Logs Insights) and optional EC2 CPU credits.
# Run before/after deploying Caddyfile changes to compare origin load.
#
# Usage:
#   LOG_GROUP=/ptxt-nstr/example.com/caddy-access ./scripts/caddy-traffic-snapshot.sh
#   LOG_GROUP=... INSTANCE_ID=i-0123456789abcdef0 ./scripts/caddy-traffic-snapshot.sh
#
set -euo pipefail

unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy ALL_PROXY all_proxy 2>/dev/null || true

LOG_GROUP="${LOG_GROUP:?set LOG_GROUP to your CloudWatch log group e.g. /ptxt-nstr/example.com/caddy-access}"
WINDOW_HOURS="${WINDOW_HOURS:-24}"

END_MS=$(( $(date +%s) * 1000 ))
START_MS=$(( END_MS - WINDOW_HOURS * 3600 * 1000 ))

echo "=== Caddy hourly requests (${WINDOW_HOURS}h window) ==="
echo "log_group=${LOG_GROUP}"
echo ""

QUERY='stats count() as requests by bin(1h) as hour | sort hour asc'

QID=$(aws logs start-query \
  --log-group-name "${LOG_GROUP}" \
  --start-time "${START_MS}" \
  --end-time "${END_MS}" \
  --query-string "${QUERY}" \
  --query 'queryId' \
  --output text)

for _ in $(seq 1 30); do
  STATUS=$(aws logs get-query-results --query-id "${QID}" --query 'status' --output text)
  if [[ "${STATUS}" == "Complete" ]] || [[ "${STATUS}" == "Failed" ]] || [[ "${STATUS}" == "Cancelled" ]]; then
    break
  fi
  sleep 1
done

if [[ "${STATUS}" != "Complete" ]]; then
  echo "query status=${STATUS}" >&2
  aws logs get-query-results --query-id "${QID}" --output json >&2 || true
  exit 1
fi

QUERY_JSON=$(aws logs get-query-results --query-id "${QID}" --output json)
echo "${QUERY_JSON}" | jq -r '
  .results[] | map({(.field): .value}) | add
  | "\(.hour)\t\(.requests)"
'
TOTAL=$(echo "${QUERY_JSON}" | jq '[.results[] | map({(.field): .value}) | add | .requests | tonumber] | add // 0')
echo ""
echo "total_requests=${TOTAL}"

if [[ -n "${INSTANCE_ID:-}" ]]; then
  echo ""
  echo "=== EC2 CPUCreditBalance (hourly avg, same window, UTC) ==="
  echo "instance=${INSTANCE_ID}"
  START_SEC=$(( END_MS / 1000 - WINDOW_HOURS * 3600 ))
  END_SEC=$(( END_MS / 1000 ))
  START_ISO=$(python3 -c "import datetime; print(datetime.datetime.fromtimestamp(${START_SEC}, datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'))")
  END_ISO=$(python3 -c "import datetime; print(datetime.datetime.fromtimestamp(${END_SEC}, datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'))")
  aws cloudwatch get-metric-statistics \
    --namespace AWS/EC2 \
    --metric-name CPUCreditBalance \
    --dimensions "Name=InstanceId,Value=${INSTANCE_ID}" \
    --start-time "${START_ISO}" \
    --end-time "${END_ISO}" \
    --period 3600 \
    --statistics Average \
    --output table 2>/dev/null || echo "(cloudwatch get-metric-statistics failed; check INSTANCE_ID and IAM)"
fi
