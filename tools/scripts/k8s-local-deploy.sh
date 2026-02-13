#!/usr/bin/env bash
# Deploy Cordum to a local K8s cluster (Docker Desktop / kind / minikube).
# Reads config from .env, applies the local kustomize overlay, creates secrets,
# and waits for all pods to be ready.
#
# Usage: ./tools/scripts/k8s-local-deploy.sh [--no-port-forward]
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OVERLAY_DIR="$REPO_ROOT/deploy/k8s/local"
ENV_FILE="$REPO_ROOT/.env"
NAMESPACE="cordum"
PORT_FORWARD=true

for arg in "$@"; do
  case "$arg" in
    --no-port-forward) PORT_FORWARD=false ;;
  esac
done

# ---- helpers ----
info()  { printf '\033[0;34m[INFO]\033[0m  %s\n' "$*"; }
warn()  { printf '\033[0;33m[WARN]\033[0m  %s\n' "$*"; }
error() { printf '\033[0;31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }

read_env() {
  local key="$1" default="${2:-}"
  if [ -f "$ENV_FILE" ]; then
    local val
    val=$(grep "^${key}=" "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2-)
    val="${val//\"/}"   # strip quotes
    val="${val//\'/}"
    if [ -n "$val" ]; then
      echo "$val"
      return
    fi
  fi
  echo "$default"
}

# ---- read config from .env ----
CORDUM_API_KEY=$(read_env CORDUM_API_KEY "")
CORDUM_ADMIN_USERNAME=$(read_env CORDUM_ADMIN_USERNAME "admin")
CORDUM_ADMIN_PASSWORD=$(read_env CORDUM_ADMIN_PASSWORD "")
CORDUM_ADMIN_EMAIL=$(read_env CORDUM_ADMIN_EMAIL "")

if [ -z "$CORDUM_API_KEY" ]; then
  warn "CORDUM_API_KEY not set in .env — generating a random key"
  CORDUM_API_KEY=$(openssl rand -hex 32)
  info "Generated API key: $CORDUM_API_KEY"
fi

# ---- pre-flight checks ----
command -v kubectl >/dev/null 2>&1 || error "kubectl not found in PATH"
kubectl cluster-info >/dev/null 2>&1 || error "Cannot connect to K8s cluster — is Docker Desktop / kind / minikube running?"

# ---- ensure namespace exists ----
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

# ---- apply kustomize overlay ----
info "Applying local kustomize overlay..."
kubectl kustomize "$OVERLAY_DIR" --load-restrictor LoadRestrictionsNone | kubectl apply -f -

# ---- create/update API key secret ----
info "Creating cordum-api-key secret..."
kubectl create secret generic cordum-api-key \
  --namespace="$NAMESPACE" \
  --from-literal="API_KEY=$CORDUM_API_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -

# ---- create/update admin credentials secret (if user auth enabled) ----
if [ -n "$CORDUM_ADMIN_PASSWORD" ]; then
  info "Creating cordum-admin-creds secret..."
  kubectl create secret generic cordum-admin-creds \
    --namespace="$NAMESPACE" \
    --from-literal="CORDUM_ADMIN_USERNAME=$CORDUM_ADMIN_USERNAME" \
    --from-literal="CORDUM_ADMIN_PASSWORD=$CORDUM_ADMIN_PASSWORD" \
    --from-literal="CORDUM_ADMIN_EMAIL=$CORDUM_ADMIN_EMAIL" \
    --dry-run=client -o yaml | kubectl apply -f -
fi

# ---- restart gateway to pick up new secrets ----
info "Restarting gateway to pick up secrets..."
kubectl rollout restart deployment/cordum-api-gateway -n "$NAMESPACE" 2>/dev/null || true

# ---- wait for pods ----
info "Waiting for all deployments to be ready (timeout 5m)..."
DEPLOYMENTS=$(kubectl get deployments -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}')
for dep in $DEPLOYMENTS; do
  info "  Waiting for $dep..."
  kubectl rollout status deployment/"$dep" -n "$NAMESPACE" --timeout=300s || warn "Timeout waiting for $dep"
done

info "All deployments ready!"
echo ""
kubectl get pods -n "$NAMESPACE"
echo ""

# ---- port forwarding ----
if [ "$PORT_FORWARD" = true ]; then
  info "Setting up port forwarding..."
  # Kill any existing port-forwards for these ports
  pkill -f "kubectl port-forward.*$NAMESPACE.*8081" 2>/dev/null || true
  pkill -f "kubectl port-forward.*$NAMESPACE.*8082" 2>/dev/null || true

  kubectl port-forward -n "$NAMESPACE" svc/cordum-api-gateway 8081:8081 &
  kubectl port-forward -n "$NAMESPACE" svc/cordum-dashboard 8082:8080 &

  sleep 2
  echo ""
  info "Access URLs:"
  info "  API Gateway:  http://localhost:8081"
  info "  Dashboard:    http://localhost:8082"
  info "  API Key:      $CORDUM_API_KEY"
  echo ""
  info "Port forwarding running in background. Press Ctrl+C or run k8s-local-teardown.sh to stop."
  wait
else
  echo ""
  info "Skipping port forwarding (--no-port-forward)."
  info "To forward manually:"
  info "  kubectl port-forward -n $NAMESPACE svc/cordum-api-gateway 8081:8081"
  info "  kubectl port-forward -n $NAMESPACE svc/cordum-dashboard 8082:8080"
fi
