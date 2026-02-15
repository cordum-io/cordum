#!/bin/sh
set -e

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

API_BASE=$(json_escape "${CORDUM_API_BASE_URL:-}")
API_KEY=""
if [ "${CORDUM_DASHBOARD_EMBED_API_KEY:-}" = "true" ] || [ "${CORDUM_DASHBOARD_EMBED_API_KEY:-}" = "1" ]; then
  API_KEY=$(json_escape "${CORDUM_API_KEY:-}")
fi
TENANT_ID=$(json_escape "${CORDUM_TENANT_ID:-default}")
PRINCIPAL_ID=$(json_escape "${CORDUM_PRINCIPAL_ID:-}")
PRINCIPAL_ROLE=$(json_escape "${CORDUM_PRINCIPAL_ROLE:-}")
TRACE_URL_TEMPLATE=$(json_escape "${CORDUM_TRACE_URL_TEMPLATE:-}")
CONFIG_PATH="/tmp/config.json"

cat > "${CONFIG_PATH}" <<CONFIGEOF
{
  "apiBaseUrl": "${API_BASE}",
  "apiKey": "${API_KEY}",
  "tenantId": "${TENANT_ID}",
  "principalId": "${PRINCIPAL_ID}",
  "principalRole": "${PRINCIPAL_ROLE}",
  "traceUrlTemplate": "${TRACE_URL_TEMPLATE}"
}
CONFIGEOF

# --- Resolve API upstream scheme (http for dev, https for prod) ---
export CORDUM_UPSTREAM_SCHEME="${CORDUM_API_UPSTREAM_SCHEME:-http}"

if [ "${CORDUM_UPSTREAM_SCHEME}" = "https" ] && [ -f "/etc/cordum/tls/ca/ca.crt" ]; then
  export CORDUM_PROXY_SSL_VERIFY="on"
  export CORDUM_PROXY_SSL_TRUSTED_CA="proxy_ssl_trusted_certificate /etc/cordum/tls/ca/ca.crt;"
else
  export CORDUM_PROXY_SSL_VERIFY="off"
  export CORDUM_PROXY_SSL_TRUSTED_CA=""
fi

envsubst '$CORDUM_UPSTREAM_SCHEME $CORDUM_PROXY_SSL_VERIFY $CORDUM_PROXY_SSL_TRUSTED_CA' \
  < /etc/nginx/templates/nginx.conf.template \
  > /etc/nginx/conf.d/default.conf

exec nginx -g "daemon off;"
