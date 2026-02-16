#!/usr/bin/env bash
# =============================================================================
# Cordum Helm Soak Test Wrapper
# Thin wrapper around soak_test.sh for Kubernetes/Helm deployments.
# Sets up port-forwards, resolves secrets, and delegates to the main script.
#
# Usage:
#   bash tools/scripts/soak_test_helm.sh
#
# Environment:
#   CORDUM_NAMESPACE          K8s namespace (default: cordum)
#   CORDUM_GATEWAY_SVC        Gateway service name (default: cordum-api-gateway)
#   CORDUM_GATEWAY_PORT       Gateway service port (default: 8081)
#   CORDUM_SECRET_NAME        K8s secret name for API key (default: cordum-api-key)
#   CORDUM_API_KEY            Override API key (skips secret lookup if set)
#   SOAK_DURATION_MINUTES     Passed through to soak_test.sh (default: 60)
#   SOAK_LOCAL_PORT           Local port for port-forward (default: 18081)
#
# All other SOAK_* env vars are passed through to soak_test.sh.
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
NAMESPACE="${CORDUM_NAMESPACE:-cordum}"
GATEWAY_SVC="${CORDUM_GATEWAY_SVC:-cordum-api-gateway}"
GATEWAY_PORT="${CORDUM_GATEWAY_PORT:-8081}"
SECRET_NAME="${CORDUM_SECRET_NAME:-cordum-api-key}"
LOCAL_PORT="${SOAK_LOCAL_PORT:-18081}"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
log() { echo "[soak-helm] $(date +%H:%M:%S) $*"; }
die() { echo "[soak-helm] ERROR: $*" >&2; exit 1; }

# ---------------------------------------------------------------------------
# Preflight
# ---------------------------------------------------------------------------
command -v kubectl >/dev/null 2>&1 || die "kubectl not found in PATH"
kubectl cluster-info >/dev/null 2>&1 || die "Cannot connect to K8s cluster"

# Verify namespace exists
kubectl get namespace "${NAMESPACE}" >/dev/null 2>&1 || die "Namespace '${NAMESPACE}' not found"

# Verify gateway pods are running
READY_PODS=$(kubectl get pods -n "${NAMESPACE}" -l "app.kubernetes.io/name=${GATEWAY_SVC}" \
  --field-selector=status.phase=Running -o name 2>/dev/null | wc -l || echo "0")
if [[ "${READY_PODS}" -eq 0 ]]; then
  # Fallback: try matching by deployment name
  READY_PODS=$(kubectl get pods -n "${NAMESPACE}" -l "app=${GATEWAY_SVC}" \
    --field-selector=status.phase=Running -o name 2>/dev/null | wc -l || echo "0")
fi
if [[ "${READY_PODS}" -eq 0 ]]; then
  log "Warning: Could not verify running gateway pods (label selectors may differ)"
fi

# ---------------------------------------------------------------------------
# Resolve API key from K8s secret
# ---------------------------------------------------------------------------
if [[ -z "${CORDUM_API_KEY:-}" ]]; then
  log "Resolving API key from secret '${SECRET_NAME}' in namespace '${NAMESPACE}'..."
  CORDUM_API_KEY=$(kubectl get secret "${SECRET_NAME}" -n "${NAMESPACE}" \
    -o jsonpath='{.data.api-key}' 2>/dev/null | base64 -d 2>/dev/null || true)
  if [[ -z "${CORDUM_API_KEY}" ]]; then
    # Try alternate key name
    CORDUM_API_KEY=$(kubectl get secret "${SECRET_NAME}" -n "${NAMESPACE}" \
      -o jsonpath='{.data.CORDUM_API_KEY}' 2>/dev/null | base64 -d 2>/dev/null || true)
  fi
  if [[ -z "${CORDUM_API_KEY}" ]]; then
    die "Could not resolve API key from secret '${SECRET_NAME}'. Set CORDUM_API_KEY manually."
  fi
  export CORDUM_API_KEY
  log "API key resolved from secret"
fi

# ---------------------------------------------------------------------------
# Set up port-forward
# ---------------------------------------------------------------------------
log "Setting up port-forward: localhost:${LOCAL_PORT} -> ${GATEWAY_SVC}:${GATEWAY_PORT}..."
kubectl port-forward -n "${NAMESPACE}" "svc/${GATEWAY_SVC}" "${LOCAL_PORT}:${GATEWAY_PORT}" &
PF_PID=$!

cleanup() {
  log "Stopping port-forward (pid ${PF_PID})..."
  kill "${PF_PID}" 2>/dev/null || true
  wait "${PF_PID}" 2>/dev/null || true

  # Collect K8s resource metrics and logs as post-soak artifacts
  log "Collecting final K8s metrics..."
  kubectl top pods -n "${NAMESPACE}" > soak_k8s_metrics.log 2>/dev/null || true
  kubectl logs -n "${NAMESPACE}" -l "app.kubernetes.io/name=${GATEWAY_SVC}" \
    --tail=500 --since=5m > soak_k8s_gateway_logs.log 2>/dev/null || true
}
trap cleanup EXIT

# Wait for port-forward to be ready
sleep 3
if ! kill -0 "${PF_PID}" 2>/dev/null; then
  die "Port-forward failed to start"
fi

# Verify connectivity through port-forward
HEALTH_STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  "http://localhost:${LOCAL_PORT}/api/v1/health" 2>/dev/null || echo "000")
if [[ "${HEALTH_STATUS}" != "200" ]]; then
  die "Gateway not reachable through port-forward (status=${HEALTH_STATUS})"
fi
log "Gateway reachable through port-forward"

# ---------------------------------------------------------------------------
# Run soak test
# ---------------------------------------------------------------------------
export CORDUM_API_BASE="http://localhost:${LOCAL_PORT}/api/v1"
export CORDUM_API_KEY

log "Delegating to soak_test.sh..."
exec bash "${SCRIPT_DIR}/soak_test.sh"
