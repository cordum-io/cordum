#!/usr/bin/env sh
set -eu

CONFIG_PATH="/usr/share/nginx/html/runtime-config.js"

API_KEY="${CORETEX_DASHBOARD_API_KEY:-${CORETEX_SUPER_SECRET_API_TOKEN:-${CORETEX_API_KEY:-${API_KEY:-}}}}"
API_BASE="${CORETEX_API_BASE:-}"
WS_BASE="${CORETEX_WS_BASE:-}"

js_escape() {
  printf "%s" "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

cat >"$CONFIG_PATH" <<EOF
// Generated at container start (Docker/K8s).
window.__CORETEXOS_STUDIO_CONFIG__ = {
  apiKey: "$(js_escape "$API_KEY")",
  apiBase: "$(js_escape "$API_BASE")",
  wsBase: "$(js_escape "$WS_BASE")",
};
EOF

