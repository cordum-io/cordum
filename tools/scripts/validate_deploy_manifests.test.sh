#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
VALIDATOR="$ROOT/tools/scripts/validate_deploy_manifests.sh"
TRUST_VALIDATOR="$ROOT/tools/scripts/validate_worker_trust_manifests.py"
HELM_CHART="$ROOT/cordum-helm"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  local haystack="$1" needle="$2"
  [[ "$haystack" == *"$needle"* ]] || fail "missing expected text: $needle"
}

render_default_chart() {
  helm template trust-default "$HELM_CHART" \
    --set secrets.apiKey=test-api-key \
    --set redis.auth.password=test-redis-password
}

render_active_chart() {
  local values_file="$1"
  shift
  helm template trust-active "$HELM_CHART" \
    --set secrets.apiKey=test-api-key \
    --set redis.auth.password=test-redis-password \
    -f "$values_file" "$@"
}

write_active_values() {
  local path="$1"
  cat >"$path" <<'YAML'
workerTrust:
  mode: enforce
  heartbeatMode: telemetry
  schedulerId: cordum-scheduler
  schedulerKeyId: scheduler_key_v1
  schedulerProof:
    privateKeySecret:
      name: scheduler-proof
      key: private.pem
    publicKeySecret:
      name: scheduler-proof
      key: public.pem
  sessionSigning:
    keyId: session_v1
    privateKeySecret:
      name: session-signing
      key: private.pem
    publicKeySecret:
      name: session-signing
      key: public.pem
YAML
}

test_default_chart_is_explicitly_legacy_safe() {
  local rendered
  rendered="$(render_default_chart)"
  [[ "$(grep -c 'name: CORDUM_SDK_HANDSHAKE' <<<"$rendered")" -eq 2 ]] || \
    fail "scheduler and gateway must both render CORDUM_SDK_HANDSHAKE"
  [[ "$(grep -c 'value: \"off\"' <<<"$rendered")" -ge 2 ]] || \
    fail "default handshake mode must render off twice"
  [[ "$(grep -c 'name: CORDUM_HEARTBEAT_MODE' <<<"$rendered")" -eq 2 ]] || \
    fail "scheduler and gateway must both render CORDUM_HEARTBEAT_MODE"
  [[ "$(grep -c 'value: \"authority\"' <<<"$rendered")" -ge 2 ]] || \
    fail "default heartbeat mode must render authority twice"
}

test_active_chart_requires_and_renders_all_authorities() {
  local tmp values rendered output
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  values="$tmp/active.yaml"
  write_active_values "$values"
  rendered="$(render_active_chart "$values")"
  for required in \
    CORDUM_HANDSHAKE_PRIVATE_KEY_FILE CORDUM_HANDSHAKE_PUBLIC_KEY_FILE \
    CORDUM_POLICY_SIGNING_KEY CORDUM_POLICY_SIGNING_KEY_ID \
    CORDUM_POLICY_PUBLIC_KEY_SESSION_V1; do
    assert_contains "$rendered" "name: $required"
  done

  python - "$values" <<'PY'
from pathlib import Path
import sys
import yaml

path = Path(sys.argv[1])
data = yaml.safe_load(path.read_text())
data["workerTrust"]["sessionSigning"]["privateKeySecret"]["name"] = ""
path.write_text(yaml.safe_dump(data, sort_keys=False))
PY
  if output="$(render_active_chart "$values" 2>&1)"; then
    fail "active chart rendered without a session-signing private secret"
  fi
  assert_contains "$output" "workerTrust.sessionSigning.privateKeySecret.name"
}

test_chart_enforces_mode_classes() {
  local tmp values handshake heartbeat output
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  values="$tmp/active.yaml"
  write_active_values "$values"
  for pair in "warn warn" "warn telemetry" "enforce warn" "enforce telemetry"; do
    read -r handshake heartbeat <<<"$pair"
    render_active_chart "$values" \
      --set "workerTrust.mode=$handshake" \
      --set "workerTrust.heartbeatMode=$heartbeat" >/dev/null
  done
  for pair in "off warn" "off telemetry" "warn authority" "enforce authority"; do
    read -r handshake heartbeat <<<"$pair"
    if output="$(render_active_chart "$values" \
        --set "workerTrust.mode=$handshake" \
        --set "workerTrust.heartbeatMode=$heartbeat" 2>&1)"; then
      fail "chart accepted contradictory modes $handshake+$heartbeat"
    fi
    assert_contains "$output" "FATAL:"
  done
  if output="$(render_active_chart "$values" \
      --set scheduler.env.workerAttestation=warn 2>&1)"; then
    fail "chart accepted active handshake with legacy worker attestation"
  fi
  assert_contains "$output" "scheduler.env.workerAttestation=off"
}

copy_validation_fixture() {
  local target="$1" path
  while IFS= read -r path; do
    mkdir -p "$target/$(dirname "$path")"
    cp "$ROOT/$path" "$target/$path"
  done < <(printf '%s\n' \
    docker-compose.yml docker-compose.release.yml docker-compose.ha.yaml \
    docker-compose.enterprise.override.yml deploy/k8s/base.yaml deploy/k8s/ingress.yaml \
    deploy/k8s/production/kustomization.yaml deploy/k8s/production/networkpolicy.yaml \
    deploy/k8s/production/ha.yaml deploy/k8s/production/monitoring.yaml \
    deploy/k8s/production/ingress.yaml deploy/k8s/production/patches/tls-env.yaml \
    deploy/k8s/production/nats.yaml deploy/k8s/production/redis.yaml \
    deploy/k8s/production/backup.yaml cordum-helm/values.yaml \
    cordum-helm/templates/deployment-control-plane.yaml)
}

test_strict_validator_rejects_trust_default_drift() {
  local tmp checker_output output
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  copy_validation_fixture "$tmp"
  python - "$tmp/docker-compose.yml" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
text = path.read_text()
needle = "      - CORDUM_SDK_HANDSHAKE=${CORDUM_SDK_HANDSHAKE:-off}\n"
if needle not in text:
    raise SystemExit("fixture did not contain scheduler handshake default")
path.write_text(text.replace(needle, "", 1))
PY
  if checker_output="$(python "$TRUST_VALIDATOR" "$tmp" 2>&1)"; then
    fail "worker-trust checker accepted a missing scheduler handshake default"
  fi
  assert_contains "$checker_output" "docker-compose.yml scheduler"
  assert_contains "$checker_output" "CORDUM_SDK_HANDSHAKE=off"
  if output="$(CORDUM_DEPLOY_ROOT="$tmp" bash "$VALIDATOR" --strict 2>&1)"; then
    fail "strict validator accepted a missing scheduler handshake default"
  fi
  assert_contains "$output" "worker trust manifest checker failed"
}

test_strict_validator_rejects_checker_crash() {
  local tmp output
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  copy_validation_fixture "$tmp"
  python - "$tmp/docker-compose.ha.yaml" <<'PY'
from pathlib import Path
import sys
import yaml

path = Path(sys.argv[1])
data = yaml.safe_load(path.read_text())
data["services"]["scheduler-2"] = None
path.write_text(yaml.safe_dump(data, sort_keys=False))
PY
  if output="$(CORDUM_DEPLOY_ROOT="$tmp" bash "$VALIDATOR" --strict 2>&1)"; then
    fail "strict validator accepted a crashed worker-trust checker"
  fi
  assert_contains "$output" "worker trust manifest checker failed"
}

test_manifest_checker_rejects_env_on_wrong_container() {
  local tmp output
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  copy_validation_fixture "$tmp"
  python - "$tmp/deploy/k8s/base.yaml" <<'PY'
from pathlib import Path
import sys
import yaml

path = Path(sys.argv[1])
documents = list(yaml.safe_load_all(path.read_text()))
for document in documents:
    if (not document or document.get("kind") != "Deployment" or
            document.get("metadata", {}).get("name") != "cordum-scheduler"):
        continue
    containers = document["spec"]["template"]["spec"]["containers"]
    scheduler = next(item for item in containers if item.get("name") == "scheduler")
    trust = {"CORDUM_SDK_HANDSHAKE", "CORDUM_HEARTBEAT_MODE"}
    decoy_env = [entry for entry in scheduler.get("env", []) if entry.get("name") in trust]
    scheduler["env"] = [entry for entry in scheduler.get("env", []) if entry.get("name") not in trust]
    containers.insert(0, {"name": "trust-decoy", "env": decoy_env})
path.write_text(yaml.safe_dump_all(documents, sort_keys=False))
PY
  if output="$(python "$TRUST_VALIDATOR" "$tmp" 2>&1)"; then
    fail "worker-trust checker accepted env on a decoy sidecar"
  fi
  assert_contains "$output" "cordum-scheduler"
}

test_default_chart_is_explicitly_legacy_safe
test_active_chart_requires_and_renders_all_authorities
test_chart_enforces_mode_classes
test_strict_validator_rejects_trust_default_drift
test_strict_validator_rejects_checker_crash
test_manifest_checker_rejects_env_on_wrong_container
echo "PASS: deployment trust defaults and Helm authority refs"
