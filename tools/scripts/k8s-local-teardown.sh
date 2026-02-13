#!/usr/bin/env bash
# Tear down Cordum from the local K8s cluster.
# Removes all resources in the cordum namespace.
#
# Usage: ./tools/scripts/k8s-local-teardown.sh [--delete-namespace]
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OVERLAY_DIR="$REPO_ROOT/deploy/k8s/local"
NAMESPACE="cordum"
DELETE_NS=false

for arg in "$@"; do
  case "$arg" in
    --delete-namespace) DELETE_NS=true ;;
  esac
done

info()  { printf '\033[0;34m[INFO]\033[0m  %s\n' "$*"; }
warn()  { printf '\033[0;33m[WARN]\033[0m  %s\n' "$*"; }

# Kill port-forward processes
info "Stopping port forwarding..."
pkill -f "kubectl port-forward.*$NAMESPACE" 2>/dev/null || true

# Delete resources from the overlay
info "Deleting Cordum resources..."
kubectl kustomize "$OVERLAY_DIR" --load-restrictor LoadRestrictionsNone 2>/dev/null \
  | kubectl delete -f - --ignore-not-found 2>/dev/null || true

# Delete secrets
kubectl delete secret cordum-api-key -n "$NAMESPACE" --ignore-not-found 2>/dev/null || true
kubectl delete secret cordum-admin-creds -n "$NAMESPACE" --ignore-not-found 2>/dev/null || true

if [ "$DELETE_NS" = true ]; then
  info "Deleting namespace $NAMESPACE..."
  kubectl delete namespace "$NAMESPACE" --ignore-not-found 2>/dev/null || true
fi

info "Teardown complete."
