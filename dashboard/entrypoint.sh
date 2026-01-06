#!/bin/sh
set -e

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

API_BASE=$(json_escape "${CORETEX_API_BASE_URL:-}")
API_KEY=$(json_escape "${CORETEX_API_KEY:-}")
TENANT_ID=$(json_escape "${CORETEX_TENANT_ID:-default}")

cat > /usr/share/nginx/html/config.json <<CONFIGEOF
{
  "apiBaseUrl": "${API_BASE}",
  "apiKey": "${API_KEY}",
  "tenantId": "${TENANT_ID}"
}
CONFIGEOF

exec nginx -g "daemon off;"
