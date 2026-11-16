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

assert_not_contains() {
  local haystack="$1" needle="$2"
  [[ "$haystack" != *"$needle"* ]] || fail "unexpected text: $needle"
}

# Reads rendered manifests on stdin and prints "<deployment> <container> <env>
# <secret ref or ->" per env entry. Values are never printed.
env_refs() {
  python -c '
import sys, yaml
sys.stdout.reconfigure(newline="")
for doc in yaml.safe_load_all(sys.stdin.buffer.read().decode("utf-8")):
    if not doc or doc.get("kind") != "Deployment":
        continue
    for container in doc["spec"]["template"]["spec"]["containers"]:
        for entry in container.get("env") or []:
            ref = (entry.get("valueFrom") or {}).get("secretKeyRef")
            source = "-" if ref is None else "secret:%s/%s optional=%s" % (
                ref.get("name"), ref.get("key"), str(ref.get("optional", False)).lower())
            print(doc["metadata"]["name"], container["name"], entry["name"], source)
'
}

render_default_chart() {
  helm template trust-default "$HELM_CHART" \
    --set secrets.apiKey=test-api-key \
    --set redis.auth.password=test-redis-password "$@"
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
  [[ "$(grep -c 'name: CORDUM_SDK_HANDSHAKE' <<<"$rendered")" -eq 3 ]] || \
    fail "scheduler, gateway, and workflow engine must render CORDUM_SDK_HANDSHAKE"
  [[ "$(grep -c 'value: \"off\"' <<<"$rendered")" -ge 3 ]] || \
    fail "default handshake mode must render off three times"
  [[ "$(grep -c 'name: CORDUM_HEARTBEAT_MODE' <<<"$rendered")" -eq 2 ]] || \
    fail "scheduler and gateway must both render CORDUM_HEARTBEAT_MODE"
  [[ "$(grep -c 'value: \"authority\"' <<<"$rendered")" -ge 2 ]] || \
    fail "default heartbeat mode must render authority twice"
  rendered="$(env_refs <<<"$rendered")"
  for absent in CORDUM_AUDIT_SIGNING_KEY CORDUM_AUDIT_PUBLIC_KEY_ \
    CORDUM_POLICY_SIGNING_KEY CORDUM_POLICY_PUBLIC_KEY_; do
    assert_not_contains "$rendered" " $absent"
  done
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

test_audit_nats_chart_requires_control_plane_key() {
  local tmp values rendered output workload
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  values="$tmp/active.yaml"
  write_active_values "$values"
  local nats=(--set workerTrust.mode=off --set workerTrust.heartbeatMode=authority \
    --set audit.transport=nats)
  rendered="$(render_active_chart "$values" "${nats[@]}" | env_refs)"
  for workload in scheduler api-gateway; do
    assert_contains "$rendered" \
      "trust-active-cordum-$workload $workload CORDUM_POLICY_SIGNING_KEY secret:session-signing/private.pem"
    assert_contains "$rendered" \
      "trust-active-cordum-$workload $workload CORDUM_POLICY_PUBLIC_KEY_SESSION_V1 secret:session-signing/public.pem"
  done
  if output="$(render_active_chart "$values" "${nats[@]}" \
      --set workerTrust.sessionSigning.privateKeySecret.name= 2>&1)"; then
    fail "audit.transport=nats rendered without a session-signing private secret"
  fi
  assert_contains "$output" \
    "workerTrust.sessionSigning.privateKeySecret.name is required when audit.transport=nats"
}

test_production_chart_requires_kernel_audit_key() {
  local rendered output
  local kernel=trust-default-cordum-safety-kernel gateway=trust-default-cordum-api-gateway
  local prod=(--set global.production=true --set global.tls.enabled=true)
  if output="$(render_default_chart "${prod[@]}" 2>&1)"; then
    fail "production chart rendered without the safety kernel's audit-publisher secret"
  fi
  assert_contains "$output" "audit.publisherKeys.safetyKernel.privateKeySecret.name"
  rendered="$(render_default_chart "${prod[@]}" \
    --set audit.publisherKeys.keyId=audit_v1 \
    --set audit.publisherKeys.safetyKernel.privateKeySecret.name=audit-signing \
    --set audit.publisherKeys.safetyKernel.privateKeySecret.key=signing-key \
    --set audit.publisherKeys.safetyKernel.publicKeySecret.name=audit-trust \
    --set audit.publisherKeys.safetyKernel.publicKeySecret.key=cordum-safety-kernel \
    --set audit.publisherKeys.mcp.publicKeySecret.name=audit-trust \
    --set audit.publisherKeys.mcp.publicKeySecret.key=cordum-mcp | env_refs)"
  assert_contains "$rendered" \
    "$kernel safety-kernel CORDUM_AUDIT_SIGNING_KEY secret:audit-signing/signing-key optional=false"
  assert_contains "$rendered" "$kernel safety-kernel CORDUM_AUDIT_SIGNING_KEY_ID -"
  assert_contains "$rendered" \
    "$gateway api-gateway CORDUM_AUDIT_PUBLIC_KEY_CORDUM_SAFETY_KERNEL secret:audit-trust/cordum-safety-kernel"
  assert_contains "$rendered" \
    "$gateway api-gateway CORDUM_AUDIT_PUBLIC_KEY_CORDUM_MCP secret:audit-trust/cordum-mcp"
}

test_production_overlay_provides_audit_key_refs() {
  local kustomize=() rendered expected
  if command -v kubectl >/dev/null 2>&1; then
    kustomize=(kubectl kustomize)
  elif command -v kustomize >/dev/null 2>&1; then
    kustomize=(kustomize build)
  else
    echo "SKIP: kubectl/kustomize not installed; production overlay key refs not checked"
    return 0
  fi
  rendered=$'\n'"$("${kustomize[@]}" "$ROOT/deploy/k8s/production" | env_refs)"$'\n'
  for expected in \
    "cordum-safety-kernel safety CORDUM_AUDIT_SIGNING_KEY secret:cordum-audit-signing/signing-key optional=false" \
    "cordum-safety-kernel safety CORDUM_AUDIT_SIGNING_KEY_ID secret:cordum-audit-signing/key-id optional=true" \
    "cordum-scheduler scheduler CORDUM_POLICY_SIGNING_KEY secret:cordum-policy-signing/signing-key optional=true" \
    "cordum-scheduler scheduler CORDUM_POLICY_SIGNING_KEY_ID secret:cordum-policy-signing/key-id optional=true" \
    "cordum-api-gateway gateway CORDUM_AUDIT_PUBLIC_KEY_CORDUM_SAFETY_KERNEL secret:cordum-audit-trust/cordum-safety-kernel optional=true" \
    "cordum-api-gateway gateway CORDUM_AUDIT_PUBLIC_KEY_CORDUM_MCP secret:cordum-audit-trust/cordum-mcp optional=true"; do
    assert_contains "$rendered" $'\n'"$expected"$'\n'
  done
  assert_not_contains "$rendered" " AUDIT_TRANSPORT "
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

test_strict_validator_rejects_missing_workflow_handshake_default() {
  local tmp output
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  copy_validation_fixture "$tmp"
  python - "$tmp/docker-compose.release.yml" <<'PY'
from pathlib import Path
import sys
import yaml

path = Path(sys.argv[1])
data = yaml.safe_load(path.read_text())
data["services"]["workflow-engine"]["environment"].pop("CORDUM_SDK_HANDSHAKE")
path.write_text(yaml.safe_dump(data, sort_keys=False))
PY
  if output="$(python "$TRUST_VALIDATOR" "$tmp" 2>&1)"; then
    fail "worker-trust checker accepted workflow engine without handshake default"
  fi
  assert_contains "$output" "docker-compose.release.yml workflow-engine"
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

test_strict_validator_rejects_broken_kustomization() {
  local tmp output
  if ! command -v kubectl >/dev/null 2>&1 && ! command -v kustomize >/dev/null 2>&1; then
    echo "SKIP: kubectl/kustomize not installed; kustomize build gate not exercised"
    return 0
  fi
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  copy_validation_fixture "$tmp"
  cp -R "$ROOT/deploy/k8s/." "$tmp/deploy/k8s/"
  output="$(CORDUM_DEPLOY_ROOT="$tmp" bash "$VALIDATOR" --strict 2>&1 || true)"
  assert_contains "$output" "INFO: deploy/k8s/production — builds"
  # A second cordum-nats-config resource is the duplicate id that broke the overlay.
  { echo "---"; cat "$tmp/deploy/k8s/production/patches/nats-config.yaml"; } \
    >>"$tmp/deploy/k8s/production/nats.yaml"
  printf '# drift\n' >>"$tmp/deploy/k8s/base/base.yaml"
  if output="$(CORDUM_DEPLOY_ROOT="$tmp" bash "$VALIDATOR" --strict 2>&1)"; then
    fail "strict validator accepted a production overlay that does not build"
  fi
  assert_contains "$output" "deploy/k8s/production — kustomize build failed"
  assert_contains "$output" "already registered id"
  assert_contains "$output" "deploy/k8s/base/base.yaml differs"
}

test_default_chart_is_explicitly_legacy_safe
test_active_chart_requires_and_renders_all_authorities
test_chart_enforces_mode_classes
test_audit_nats_chart_requires_control_plane_key
test_production_chart_requires_kernel_audit_key
test_production_overlay_provides_audit_key_refs
test_strict_validator_rejects_trust_default_drift
test_strict_validator_rejects_missing_workflow_handshake_default
test_strict_validator_rejects_checker_crash
test_manifest_checker_rejects_env_on_wrong_container
test_strict_validator_rejects_broken_kustomization
echo "PASS: deployment trust defaults and Helm authority refs"
