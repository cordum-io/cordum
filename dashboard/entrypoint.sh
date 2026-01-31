#!/bin/sh
set -e

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

API_BASE=$(json_escape "${CORDUM_API_BASE_URL:-}")
API_KEY=""
if [ -n "${CORDUM_DASHBOARD_EMBED_API_KEY:-}" ]; then
  API_KEY=$(json_escape "${CORDUM_API_KEY:-}")
fi
TENANT_ID=$(json_escape "${CORDUM_TENANT_ID:-default}")
PRINCIPAL_ID=$(json_escape "${CORDUM_PRINCIPAL_ID:-}")
PRINCIPAL_ROLE=$(json_escape "${CORDUM_PRINCIPAL_ROLE:-}")
TRACE_URL_TEMPLATE=$(json_escape "${CORDUM_TRACE_URL_TEMPLATE:-}")

cat > /usr/share/nginx/html/config.json <<CONFIGEOF
{
  "apiBaseUrl": "${API_BASE}",
  "apiKey": "${API_KEY}",
  "tenantId": "${TENANT_ID}",
  "principalId": "${PRINCIPAL_ID}",
  "principalRole": "${PRINCIPAL_ROLE}",
  "traceUrlTemplate": "${TRACE_URL_TEMPLATE}"
}
CONFIGEOF

exec nginx -g "daemon off;"
