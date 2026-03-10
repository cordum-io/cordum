#!/usr/bin/env bash
# validate_deploy_manifests.sh — Lint deployment manifests for known
# misconfiguration classes that break production security posture.
#
# Usage: ./tools/scripts/validate_deploy_manifests.sh [--strict]
#
# Exit 0 = all checks pass, Exit 1 = one or more failures found.
# --strict  Treat warnings as errors.

set -euo pipefail
cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"

STRICT=false
[[ "${1:-}" == "--strict" ]] && STRICT=true

FAIL=0
WARN=0

fail() {
  echo "  FAIL: $*" >&2
  FAIL=$((FAIL + 1))
}

warn() {
  echo "  WARN: $*" >&2
  WARN=$((WARN + 1))
  if $STRICT; then
    FAIL=$((FAIL + 1))
  fi
}

info() {
  echo "  INFO: $*"
}

# ─────────────────────────────────────────────────────────
# Gate 1: YAML syntax validation
# ─────────────────────────────────────────────────────────
echo "=== Gate 1: YAML syntax validation ==="
YAML_FILES=(
  docker-compose.yml
  docker-compose.release.yml
  docker-compose.ha.yaml
  docker-compose.enterprise.override.yml
  deploy/k8s/base.yaml
  deploy/k8s/ingress.yaml
  deploy/k8s/production/kustomization.yaml
  deploy/k8s/production/networkpolicy.yaml
  deploy/k8s/production/ha.yaml
  deploy/k8s/production/monitoring.yaml
  deploy/k8s/production/ingress.yaml
  deploy/k8s/production/patches/tls-env.yaml
  deploy/k8s/production/nats.yaml
  deploy/k8s/production/redis.yaml
  deploy/k8s/production/backup.yaml
)
for f in "${YAML_FILES[@]}"; do
  if [ -f "$f" ]; then
    if python -c "import yaml; list(yaml.safe_load_all(open('$f')))" 2>/dev/null; then
      info "$f — valid YAML"
    else
      fail "$f — invalid YAML syntax"
    fi
  else
    warn "$f — file not found"
  fi
done

# ─────────────────────────────────────────────────────────
# Gate 2: Production k8s TLS patch must preserve Redis auth
# ─────────────────────────────────────────────────────────
echo ""
echo "=== Gate 2: Redis URL must include auth in TLS patches ==="
TLS_PATCH="deploy/k8s/production/patches/tls-env.yaml"
if [ -f "$TLS_PATCH" ]; then
  # Check that every REDIS_URL in the TLS patch includes password interpolation
  REDIS_URLS=$(grep -A1 'name: REDIS_URL' "$TLS_PATCH" | grep 'value:' || true)
  if echo "$REDIS_URLS" | grep -q 'rediss://[^@]*@'; then
    info "TLS patch REDIS_URL includes auth credentials"
  elif echo "$REDIS_URLS" | grep -q 'REDIS_PASSWORD'; then
    info "TLS patch REDIS_URL references REDIS_PASSWORD variable"
  else
    fail "TLS patch REDIS_URL is missing password (auth will fail): $REDIS_URLS"
  fi
else
  warn "TLS patch file not found: $TLS_PATCH"
fi

# ─────────────────────────────────────────────────────────
# Gate 3: Gateway probes must use HTTPS when TLS is enabled
# ─────────────────────────────────────────────────────────
echo ""
echo "=== Gate 3: Gateway probes must match TLS mode ==="
if [ -f "$TLS_PATCH" ]; then
  if grep -q 'GATEWAY_HTTP_TLS_CERT' "$TLS_PATCH" 2>/dev/null; then
    # TLS is enabled for gateway — probes in base.yaml must either:
    # 1. Use scheme: HTTPS, or
    # 2. Be patched to use the metrics port, or
    # 3. The TLS patch must also patch the probes
    if grep -qE 'livenessProbe|readinessProbe|scheme:' "$TLS_PATCH" 2>/dev/null; then
      info "TLS patch includes probe adjustments"
    else
      fail "Gateway TLS is enabled in TLS patch but probes are NOT patched to use HTTPS scheme — pods will crash-loop"
    fi
  else
    info "No gateway TLS in patch — HTTP probes are correct"
  fi
fi

# ─────────────────────────────────────────────────────────
# Gate 4: All containers must drop ALL capabilities
# ─────────────────────────────────────────────────────────
echo ""
echo "=== Gate 4: Security context — capabilities drop ==="
BASE_YAML="deploy/k8s/base.yaml"
if [ -f "$BASE_YAML" ]; then
  CAP_RESULTS=$(python -c "
import yaml, sys
docs = list(yaml.safe_load_all(open('$BASE_YAML')))
for doc in docs:
    if not doc or doc.get('kind') != 'Deployment':
        continue
    name = doc['metadata']['name']
    spec = doc['spec']['template']['spec']
    containers = spec.get('containers', [])
    for c in containers:
        sc = c.get('securityContext', {})
        caps = sc.get('capabilities', {})
        drop = caps.get('drop', [])
        if 'ALL' not in drop:
            print(f'FAIL:{name}/{c[\"name\"]}:missing capabilities drop ALL')
        else:
            print(f'OK:{name}/{c[\"name\"]}')
" 2>/dev/null)
  while IFS=: read -r status resource msg; do
    if [ "$status" = "FAIL" ]; then
      fail "Container $resource: $msg"
    else
      info "Container $resource: capabilities drop ALL present"
    fi
  done <<< "$CAP_RESULTS"
fi

# ─────────────────────────────────────────────────────────
# Gate 5: Redis persistence must be enabled in Helm defaults
# ─────────────────────────────────────────────────────────
echo ""
echo "=== Gate 5: Helm Redis persistence default ==="
HELM_VALUES="cordum-helm/values.yaml"
if [ -f "$HELM_VALUES" ]; then
  REDIS_PERSIST=$(python -c "
import yaml
v = yaml.safe_load(open('$HELM_VALUES'))
print(v.get('redis', {}).get('persistence', {}).get('enabled', 'missing'))
" 2>/dev/null)
  if [ "$REDIS_PERSIST" = "True" ]; then
    info "Helm Redis persistence enabled by default"
  else
    warn "Helm Redis persistence DISABLED by default (redis.persistence.enabled=$REDIS_PERSIST) — data loss on pod restart"
  fi
fi

# ─────────────────────────────────────────────────────────
# Gate 6: Backup CronJob must include Redis auth
# ─────────────────────────────────────────────────────────
echo ""
echo "=== Gate 6: Backup CronJob Redis auth ==="
BACKUP_YAML="deploy/k8s/production/backup.yaml"
if [ -f "$BACKUP_YAML" ]; then
  BACKUP_RESULTS=$(python -c "
import yaml
docs = list(yaml.safe_load_all(open('$BACKUP_YAML')))
for doc in docs:
    if not doc or doc.get('kind') != 'CronJob':
        continue
    name = doc['metadata']['name']
    if 'redis' not in name:
        continue
    spec = doc['spec']['jobTemplate']['spec']['template']['spec']
    containers = spec.get('containers', [])
    for c in containers:
        envs = {e['name'] for e in c.get('env', [])}
        if 'REDIS_PASSWORD' in envs:
            print(f'OK:{name}:has REDIS_PASSWORD env')
        else:
            print(f'FAIL:{name}:missing REDIS_PASSWORD env -- backup will fail NOAUTH')
" 2>/dev/null)
  while IFS=: read -r status resource msg; do
    if [ "$status" = "FAIL" ]; then
      fail "CronJob $resource: $msg"
    else
      info "CronJob $resource: $msg"
    fi
  done <<< "$BACKUP_RESULTS"
fi

# ─────────────────────────────────────────────────────────
# Gate 7: Helm templates — TLS-dependent services must have TLS client env vars
# ─────────────────────────────────────────────────────────
echo ""
echo "=== Gate 7: Helm TLS client env vars ==="
HELM_DEPLOY="cordum-helm/templates/deployment-control-plane.yaml"
if [ -f "$HELM_DEPLOY" ]; then
  # Check if scheduler has SAFETY_KERNEL_TLS_CA when tls.enabled
  if grep -q 'SAFETY_KERNEL_TLS_CA' "$HELM_DEPLOY" 2>/dev/null; then
    info "Helm scheduler has SAFETY_KERNEL_TLS_CA"
  else
    warn "Helm scheduler missing SAFETY_KERNEL_TLS_CA env var — gRPC TLS to safety kernel will fail when tls.enabled=true"
  fi

  # Check if gateway has CONTEXT_ENGINE_TLS_CA
  if grep -q 'CONTEXT_ENGINE_TLS_CA' "$HELM_DEPLOY" 2>/dev/null; then
    info "Helm gateway has CONTEXT_ENGINE_TLS_CA"
  else
    warn "Helm gateway missing CONTEXT_ENGINE_TLS_CA env var — gRPC TLS to context-engine will fail when tls.enabled=true"
  fi

  # Check NATS TLS vars
  if grep -q 'NATS_TLS_CA' "$HELM_DEPLOY" 2>/dev/null; then
    info "Helm templates have NATS_TLS_CA"
  else
    warn "Helm templates missing NATS_TLS_CA — NATS client TLS not configurable"
  fi
fi

# ─────────────────────────────────────────────────────────
# Gate 8: Release compose must require critical env vars
# ─────────────────────────────────────────────────────────
echo ""
echo "=== Gate 8: Release compose required env vars ==="
RELEASE_COMPOSE="docker-compose.release.yml"
if [ -f "$RELEASE_COMPOSE" ]; then
  REQUIRED_VARS=(
    "CORDUM_API_KEY:?error"
    "REDIS_PASSWORD:?error"
    "CORDUM_TLS_DIR:?error"
  )
  for var in "${REQUIRED_VARS[@]}"; do
    VARNAME="${var%%:*}"
    if grep -q "$var" "$RELEASE_COMPOSE" 2>/dev/null; then
      info "Release compose requires $VARNAME"
    else
      fail "Release compose missing required-or-fail for $VARNAME"
    fi
  done
fi

# ─────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────
echo ""
echo "=== Summary ==="
echo "  Failures: $FAIL"
echo "  Warnings: $WARN"

if [ "$FAIL" -gt 0 ]; then
  echo ""
  echo "DEPLOYMENT VALIDATION FAILED — $FAIL issue(s) must be fixed before deploy."
  exit 1
fi

echo ""
echo "DEPLOYMENT VALIDATION PASSED."
exit 0
