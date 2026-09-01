#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INTEGRATION="${ROOT}/.github/workflows/integration-nightly.yml"
NIGHTLY="${ROOT}/.github/workflows/nightly.yml"
SOAK="${ROOT}/tools/scripts/soak_test.sh"
CI="${ROOT}/.github/workflows/ci.yml"
HELPER="${ROOT}/tools/scripts/github_actions_mask_env.sh"
TEST_TMP="$(mktemp -d)"
PASS=0; FAIL=0
trap 'rm -rf "${TEST_TMP}"' EXIT
record() {
  local name="$1" status="$2"
  if [[ "${status}" -eq 0 ]]; then
    echo "PASS: ${name}"
    PASS=$((PASS + 1))
  else
    echo "FAIL: ${name}" >&2
    FAIL=$((FAIL + 1))
  fi
}
assert_count() {
  local name="$1" file="$2" pattern="$3" want="$4" got status=0
  got="$(grep -cF -- "${pattern}" "${file}" || true)"
  [[ "${got}" == "${want}" ]] || status=1
  record "${name}" "${status}"
}
new_value() {
  local tag="$1" suffix="${2:-}"
  VALUE="gha_test_${tag}_${$}_${RANDOM}_${RANDOM}${suffix}"
  printf '%s\n' "${VALUE}" >> "${CASE_SENTINELS}"
}
mask_count() {
  local value="$1" escaped; escaped="${value//%/%25}"
  grep -Fxc -- "::add-mask::${escaped}" "${HELPER_LOG}" 2>/dev/null || true
}
call_ok() { "$@" >> "${HELPER_LOG}" 2>&1; }
call_fails() {
  local status
  set +e
  "$@" >> "${HELPER_LOG}" 2>&1
  status=$?
  set -e
  [[ "${status}" -ne 0 && "${status}" -ne 127 ]]
}
setup_env_file() { export GITHUB_ENV="${CASE_DIR}/github-env"; : > "${GITHUB_ENV}"; }
valid_helper_case() {
  local -a names=(CORDUM_API_KEY CORDUM_APPROVER_API_KEY REDIS_PASSWORD CORDUM_ADMIN_PASSWORD CORDUM_API_KEYS) values=()
  local name value mask_only mode artifact_dir="${CASE_DIR}/artifacts" artifact="${CASE_DIR}/artifacts/nested/log.txt"
  mkdir -p "${artifact_dir}/nested"
  setup_env_file
  for name in "${names[@]}"; do new_value "${name}"; values+=("${VALUE}"); done
  new_value mask_only '%pct'; mask_only="${VALUE}"
  new_value license_token '=='; export TEST_LICENSE_TOKEN="${VALUE}"; names+=(CORDUM_LICENSE_TOKEN); values+=("${VALUE}")
  new_value license_public; export TEST_LICENSE_PUBLIC="${VALUE}"; names+=(CORDUM_LICENSE_PUBLIC_KEY); values+=("${VALUE}")
  call_ok gha_mask_value "${mask_only}" || return 1
  for ((i=0; i<5; i++)); do call_ok gha_mask_env "${names[i]}" "${values[i]}" || return 1; done
  call_ok gha_mask_env_from_command CORDUM_LICENSE_TOKEN CORDUM_LICENSE_PUBLIC_KEY -- \
    bash -c 'printf "CORDUM_LICENSE_TOKEN=%s\nCORDUM_LICENSE_PUBLIC_KEY=%s\n" "$TEST_LICENSE_TOKEN" "$TEST_LICENSE_PUBLIC"' || return 1
  for value in "${values[@]}"; do printf '%s\n' "${value}" >> "${artifact}"; done
  mode="$(stat -c '%a' "${artifact}")"
  call_ok gha_redact_paths "${names[@]}" -- "${artifact_dir}" || return 1
  call_ok gha_redact_paths "${names[@]}" -- "${artifact_dir}" || return 1
  [[ "$(stat -c '%a' "${artifact}")" == "${mode}" ]] || return 1
  [[ "$(wc -l < "${GITHUB_ENV}")" -eq 7 ]] || return 1
  for ((i=0; i<7; i++)); do
    name="${names[i]}"; value="${values[i]}"
    grep -Fxq -- "${name}=${value}" "${GITHUB_ENV}" 2>/dev/null || return 1
    [[ "${!name-}" == "${value}" ]] || return 1
    [[ "$(mask_count "${value}")" -eq 1 ]] || return 1
    ! grep -Fq -- "${value}" "${artifact}" 2>/dev/null || return 1
  done
  [[ "$(mask_count "${mask_only}")" -eq 1 ]] || return 1
  new_value fallback_redaction; unset CORDUM_API_KEY; export CORDUM_API_KEY="${VALUE}"; printf '%s\n' "${VALUE}" > "${artifact}"
  call_ok gha_redact_paths CORDUM_API_KEY -- "${artifact}" && ! grep -Fq -- "${VALUE}" "${artifact}" 2>/dev/null
}
batch_failure_case() {
  local kind="$1" command
  setup_env_file; new_value "batch_${kind}"; export TEST_BATCH_VALUE="${VALUE}"
  case "${kind}" in
    command) command='printf "CORDUM_LICENSE_TOKEN=%s\n" "$TEST_BATCH_VALUE"; printf "%s" "$TEST_BATCH_VALUE" >&2; exit 9' ;;
    malformed) command='printf "%s\n" "$TEST_BATCH_VALUE"' ;;
    missing) command='printf "CORDUM_LICENSE_TOKEN=%s\n" "$TEST_BATCH_VALUE"' ;;
    extra) command='printf "CORDUM_LICENSE_TOKEN=%s\nCORDUM_LICENSE_PUBLIC_KEY=%s\nEXTRA_NAME=%s\n" "$TEST_BATCH_VALUE" "$TEST_BATCH_VALUE" "$TEST_BATCH_VALUE"' ;;
    duplicate) command='printf "CORDUM_LICENSE_TOKEN=%s\nCORDUM_LICENSE_TOKEN=%s\nCORDUM_LICENSE_PUBLIC_KEY=%s\n" "$TEST_BATCH_VALUE" "$TEST_BATCH_VALUE" "$TEST_BATCH_VALUE"' ;;
  esac
  call_fails gha_mask_env_from_command CORDUM_LICENSE_TOKEN CORDUM_LICENSE_PUBLIC_KEY -- bash -c "${command}" || return 1
  [[ ! -s "${GITHUB_ENV}" && "$(mask_count "${VALUE}")" -eq 0 ]]
}
helper_case() {
  local kind="$1" bad path
  source "${HELPER}" >> "${HELPER_LOG}" 2>&1 || return 1
  for fn in gha_mask_value gha_mask_env gha_mask_env_from_command gha_redact_paths; do
    declare -F "${fn}" >/dev/null || return 1
  done
  case "${kind}" in
    valid) valid_helper_case ;;
    empty_name) setup_env_file; new_value empty_name; call_fails gha_mask_env '' "${VALUE}" ;;
    invalid_name) setup_env_file; new_value invalid_name; call_fails gha_mask_env 'BAD-NAME' "${VALUE}" ;;
    cr_name) setup_env_file; new_value cr_name; call_fails gha_mask_env $'BAD\rNAME' "${VALUE}" ;;
    lf_name) setup_env_file; new_value lf_name; call_fails gha_mask_env $'BAD\nNAME' "${VALUE}" ;;
    empty_value) setup_env_file; call_fails gha_mask_env CORDUM_API_KEY '' ;;
    extra_value) new_value extra_value; call_fails gha_mask_value "${VALUE}" unexpected ;;
    extra_env) setup_env_file; new_value extra_env; call_fails gha_mask_env CORDUM_API_KEY "${VALUE}" unexpected ;;
    cr_value) setup_env_file; new_value cr_value; bad="${VALUE}"$'\rtail'; call_fails gha_mask_env CORDUM_API_KEY "${bad}" ;;
    lf_value) setup_env_file; new_value lf_value; bad="${VALUE}"$'\ntail'; call_fails gha_mask_env CORDUM_API_KEY "${bad}" ;;
    unset_env) unset GITHUB_ENV; new_value unset_env; call_fails gha_mask_env CORDUM_API_KEY "${VALUE}" ;;
    bad_env) export GITHUB_ENV="${CASE_DIR}/missing/env"; new_value bad_env; call_fails gha_mask_env CORDUM_API_KEY "${VALUE}" ;;
    write_failure)
      export GITHUB_ENV=/dev/full; [[ -e "${GITHUB_ENV}" ]] || export GITHUB_ENV="${CASE_DIR}"
      new_value write_failure; unset CORDUM_API_KEY
      call_fails gha_mask_env CORDUM_API_KEY "${VALUE}" || return 1
      [[ "$(mask_count "${VALUE}")" -eq 1 && -z "${CORDUM_API_KEY+x}" ]]
      ;;
    command|malformed|missing|extra|duplicate) batch_failure_case "${kind}" ;;
    redact_missing)
      new_value redact_missing; export CORDUM_API_KEY="${VALUE}"; path="${CASE_DIR}/absent"
      call_fails gha_redact_paths CORDUM_API_KEY -- "${path}"
      ;;
    redact_bad_name)
      new_value redact_bad_name; export CORDUM_API_KEY="${VALUE}"; path="${CASE_DIR}/artifact"; : > "${path}"
      call_fails gha_redact_paths 'BAD-NAME' -- "${path}"
      ;;
    redact_binary) new_value redact_binary; export CORDUM_API_KEY="${VALUE}"; path="${CASE_DIR}/binary"; printf '\0%s' "${VALUE}" > "${path}"; call_fails gha_redact_paths CORDUM_API_KEY -- "${path}" ;;
    redact_symlink)
      new_value redact_symlink; export CORDUM_API_KEY="${VALUE}"; mkdir -p "${CASE_DIR}/artifacts"; printf '%s\n' "${VALUE}" > "${CASE_DIR}/external"
      if ln -s "${CASE_DIR}/external" "${CASE_DIR}/artifacts/link" 2>/dev/null && [[ -L "${CASE_DIR}/artifacts/link" ]]; then call_fails gha_redact_paths CORDUM_API_KEY -- "${CASE_DIR}/artifacts"; else true; fi
      ;;
  esac
}
logs_are_safe() {
  local value count
  [[ ! -s "${CASE_STDIO}" ]] || return 1
  while IFS= read -r value; do
    count="$(grep -Fc -- "${value}" "${HELPER_LOG}" 2>/dev/null || true)"
    [[ "${count}" -le 1 ]] || return 1
    [[ "${count}" -eq 0 ]] || grep -Fxq -- "::add-mask::${value}" "${HELPER_LOG}" 2>/dev/null || return 1
  done < "${CASE_SENTINELS}"
}
run_helper_case() {
  local name="$1" kind="$2" status=0
  CASE_DIR="${TEST_TMP}/${kind}"; mkdir -p "${CASE_DIR}"
  CASE_SENTINELS="${CASE_DIR}/sentinels"; HELPER_LOG="${CASE_DIR}/helper.log"; CASE_STDIO="${CASE_DIR}/stdio.log"
  : > "${CASE_SENTINELS}"; : > "${HELPER_LOG}"; : > "${CASE_STDIO}"
  (helper_case "${kind}") > "${CASE_STDIO}" 2>&1 || status=1
  logs_are_safe || status=1
  record "${name}" "${status}"
}
write_workflow_guard() {
  GUARD="${TEST_TMP}/workflow_guard.py"
  cat > "${GUARD}" <<'PY'
from collections import Counter; from copy import deepcopy
from pathlib import Path; from tempfile import TemporaryDirectory
import re, sys, yaml
CREDENTIAL_NAME = r"[A-Z0-9_]*(?:API_KEYS?|REDIS_PASSWORD|ADMIN_PASSWORD|LICENSE_(?:TOKEN|PUBLIC_KEY))"; CREDENTIAL = re.compile(rf"\b{CREDENTIAL_NAME}\b")
LICENSED = ["CORDUM_API_KEY", "REDIS_PASSWORD", "CORDUM_ADMIN_PASSWORD", "CORDUM_LICENSE_TOKEN", "CORDUM_LICENSE_PUBLIC_KEY"]
EXPECTED = {("integration-nightly.yml", "integration"): LICENSED, ("nightly.yml", "full-production-gate"): LICENSED, ("nightly.yml", "release-gate"): LICENSED, ("nightly.yml", "soak-test"): LICENSED,
    ("demo-mock-bank-e2e.yml", "demo-mock-bank-e2e"): ["CORDUM_API_KEY", "REDIS_PASSWORD", "CORDUM_LICENSE_TOKEN", "CORDUM_LICENSE_PUBLIC_KEY"], ("platform-smoke.yml", "platform-smoke"): ["CORDUM_API_KEY", "REDIS_PASSWORD"], ("edge-fake-hook-e2e.yml", "edge-fake-hook-e2e"): ["CORDUM_API_KEY", "CORDUM_APPROVER_API_KEY", "CORDUM_API_KEYS", "REDIS_PASSWORD"], ("e2e.yml", "e2e-tls"): ["CORDUM_API_KEY", "REDIS_PASSWORD", "CORDUM_ADMIN_PASSWORD"]}
FALLBACKS = {key: EXPECTED[key] for key in (("demo-mock-bank-e2e.yml", "demo-mock-bank-e2e"), ("e2e.yml", "e2e-tls"), ("edge-fake-hook-e2e.yml", "edge-fake-hook-e2e"), ("platform-smoke.yml", "platform-smoke"))}; FALLBACKS[("fixture.yml", "safe")] = ["CORDUM_API_KEY"]
PREPARED = {("edge-fake-hook-e2e.yml", "edge-fake-hook-e2e"): "touch edge-fake-hook-e2e.log edge-fake-hook-e2e-health.json", ("platform-smoke.yml", "platform-smoke"): "touch platform-smoke.log platform-smoke-health.json", ("fixture.yml", "safe"): "touch optional.log"}
OUTPUT = re.compile(r"^\s*(?:echo|printf|cat|tee)\b"); SOURCE = "tools/scripts/github_actions_mask_env.sh"; PLACEHOLDER = "credential-unavailable-for-redaction"
def names_in(text): return set(CREDENTIAL.findall(text))
def helper_calls(run):
    found = Counter(re.findall(r"(?m)^gha_mask_env\s+([A-Z][A-Z0-9_]*)\b", run))
    for args in re.findall(r"(?ms)^gha_mask_env_from_command\s+(.+?)\s+--", run):
        found.update(name for name in re.findall(r"\b[A-Z][A-Z0-9_]*\b", args) if CREDENTIAL.fullmatch(name))
    return found
def redact_names(run):
    matches = re.findall(r"(?ms)^gha_redact_paths\s+(.+?)\s+--", run)
    return {name for args in matches for name in re.findall(r"\b[A-Z][A-Z0-9_]*\b", args) if CREDENTIAL.fullmatch(name)}
def check_job(path, job_id, steps, wanted):
    issues, setup, credential_job, fallback_steps, key = [], Counter(), False, 0, (path, job_id)
    for index, step in enumerate(steps):
        if not isinstance(step, dict): continue
        raw_run, uses, env = str(step.get("run") or ""), str(step.get("uses") or ""), step.get("env") or {}
        run = "\n".join(line for line in raw_run.splitlines() if not line.lstrip().startswith("#")); commands = [line.strip() for line in run.splitlines() if line.strip()]
        refs, calls = names_in(run), helper_calls(run); output_aliases = set(re.findall(r'(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*["\']?\$\{?' + CREDENTIAL_NAME + r'\b', run))
        fallback_names = {name for name, value in env.items() if PLACEHOLDER in str(value)} if isinstance(env, dict) else set(); redacted = redact_names(run)
        if fallback_names:
            if fallback_names != set(FALLBACKS.get(key, [])) or redacted != fallback_names: issues.append("FALLBACK_SCOPE")
            else: fallback_steps += 1
        if redacted and key in PREPARED:
            prior_raw = str(steps[index - 1].get("run") or "") if index and isinstance(steps[index - 1], dict) else ""; prepared_commands = [line.strip() for line in prior_raw.splitlines() if line.strip() and not line.lstrip().startswith("#")]
            if not prepared_commands or prepared_commands[0] != PREPARED[key]: issues.append("OPTIONAL_ARTIFACT_PREP")
        if calls:
            credential_job = True; setup.update(calls)
            admin = ('if [[ -z "${admin_random}" ]]; then', 'echo "::error::admin credential generation returned empty output" >&2', 'exit 1', 'fi'); control = [command for command in commands if command in admin]; allowed = (re.escape(f"source {SOURCE}"), r'[a-z_][A-Za-z0-9_]*="\$\(openssl rand -hex [0-9]+\)" \|\| exit 1', r'gha_mask_env [A-Z][A-Z0-9_]* "(?:\$\{[a-z_][A-Za-z0-9_]*\}|CordumE2E-\$\{admin_random\}!1)"', r'gha_mask_env_from_command(?: [A-Z][A-Z0-9_]*)+ -- go run \./tools/cilicense', re.escape("payload=$(printf '[{\"key\":\"%s\",\"role\":\"admin\",\"tenant\":\"default\",\"principal_id\":\"edge-fake-hook-e2e-approver\"}]' \"${CORDUM_APPROVER_API_KEY}\")"), *(re.escape(command) for command in admin), r'echo "CORDUM_USER_AUTH_ENABLED=true" >> "\$GITHUB_ENV"', r'echo "CORDUM_ADMIN_USERNAME=admin" >> "\$GITHUB_ENV"'); issues.append("HELPER_ORDER") if not commands or commands[0] != f"source {SOURCE}" or commands.count(f"source {SOURCE}") != 1 or any(not any(re.fullmatch(pattern, command) for pattern in allowed) for command in commands) or (control and (control != list(admin) or "\n".join(admin) not in "\n".join(commands))) else None
            for call in re.finditer(r"(?m)^gha_mask_env\s+" + CREDENTIAL_NAME + r"\s+(.+)$", run):
                argument = call.group(1).strip(); aliases = re.findall(r"\$\{?([A-Za-z_][A-Za-z0-9_]*)", argument)
                if "$(" in argument: issues.append("COMMAND_SUBSTITUTION_ARGUMENT")
                if any(re.search(rf"\$\{{?{re.escape(alias)}\b", line) and not re.match(r"^\s*(?:if\s+)?\[\[", line) for alias in aliases for line in run[:call.start()].splitlines()): issues.append("PREMASK_ALIAS_USE")
        nonhelper = "\n".join(line for line in run.splitlines() if not line.startswith(("gha_mask_env ", "gha_mask_env_from_command ")))
        if "GITHUB_ENV" in nonhelper and names_in(nonhelper): credential_job = True; issues.append("DIRECT_ENV_WRITE")
        for line in run.splitlines():
            if re.match(r"^\s*(?:export\s+)?" + CREDENTIAL_NAME + r"=", line) and re.search(r"\$\(|openssl|uuidgen|/dev/urandom|\bRANDOM\b", line) and "gha_mask_env" not in line:
                credential_job = True; issues.append("DIRECT_GENERATION")
        if "cilicense" in run:
            credential_job = True
            if "gha_mask_env_from_command" not in run: issues.append("BARE_CILICENSE_REDIRECT")
        for line in run.splitlines():
            direct, alias_ref = names_in(line), any(re.search(rf"\$\{{?{re.escape(alias)}\b", line) for alias in output_aliases); output = OUTPUT.search(line)
            if (credential_job or (path, job_id) in EXPECTED) and ((output and (direct or alias_ref)) or ("sha256sum" in line and direct)): issues.append("UNSAFE_OUTPUT")
        if uses.lower().startswith("actions/upload-artifact@") and credential_job:
            prior_step = steps[index - 1] if index and isinstance(steps[index - 1], dict) else {}; prior = str(prior_step.get("run") or ""); prior_commands = [line.strip() for line in prior.splitlines() if line.strip() and not line.lstrip().startswith("#")]
            if len(prior_commands) != 2 or prior_commands[0] != f"source {SOURCE}" or not re.fullmatch(r"gha_redact_paths(?: [A-Z][A-Z0-9_]*)+ --(?: [-A-Za-z0-9_./*]+)+", prior_commands[1]): issues.append("UPLOAD_WITHOUT_REDACTION")
            elif redact_names(prior) != set(wanted): issues.append("REDACTION_SET_MISMATCH")
            prior_id, condition = str(prior_step.get("id") or ""), str(step.get("if") or "")
            if not prior_id or not re.fullmatch(rf"(?:always|failure)\(\) && steps\.{re.escape(prior_id)}\.outcome == 'success'", condition.strip()): issues.append("UPLOAD_NOT_REDACTION_GATED")
    if credential_job and setup != Counter(wanted): issues.append("INVENTORY_MISMATCH")
    if key in FALLBACKS and fallback_steps != 1: issues.append("FALLBACK_MISMATCH")
    return issues, credential_job
def check_quickstart_text(text):
    text = "\n".join(line.strip() for line in text.splitlines() if not line.lstrip().startswith("#"))
    variables = ("seeded_key", "seeded_redis"); protected = 'if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then\nsource "${REPO_ROOT}/tools/scripts/github_actions_mask_env.sh"\ngha_mask_value "${seeded_key}"\ngha_mask_value "${seeded_redis}"\nfi'; exact = lambda command: [match.start() for match in re.finditer(rf"(?m)^{re.escape(command)}$", text)]; dotenv, log_print = text.find("cat > .env"), text.find('cat "${quickstart_log}"')
    triples = [(text.find(f"{name}="), positions[0] if len(positions := exact(f'gha_mask_value "${{{name}}}"')) == 1 else -1, text.find(f'grep -Fq "${{{name}}}"')) for name in variables]
    return [] if protected in text and all(0 <= generated < mask < dotenv < leak < log_print for generated, mask, leak in triples) else ["QUICKSTART_MASK_OR_LOG_ORDER"]
def check_quickstart(root):
    path = root / "tools/scripts/quickstart_env_sharing_test.sh"; return ["QUICKSTART_MISSING"] if not path.is_file() else check_quickstart_text(path.read_text(encoding="utf-8"))
def scan(root):
    issues, seen = [], set()
    paths = sorted((root / ".github/workflows").glob("*.y*ml"))
    if len(paths) != 17: issues.append(("workflows", "-", "WORKFLOW_COUNT"))
    for path in paths:
        try: jobs = (yaml.safe_load(path.read_text(encoding="utf-8")) or {}).get("jobs") or {}
        except Exception: issues.append((path.name, "-", "YAML_PARSE")); continue
        for job_id, job in jobs.items():
            key = (path.name, job_id); wanted = EXPECTED.get(key, [])
            found, credential_job = check_job(path.name, job_id, (job or {}).get("steps") or [], wanted)
            if credential_job: seen.add(key)
            issues.extend((path.name, job_id, issue) for issue in found)
    for key in set(EXPECTED) - seen: issues.append((*key, "INVENTORY_MISSING"))
    for key in seen - set(EXPECTED): issues.append((*key, "INVENTORY_EXTRA"))
    issues.extend(("quickstart_env_sharing_test.sh", "ci-transitive", issue) for issue in check_quickstart(root))
    return issues
def self_test():
    safe = [{"run": 'source tools/scripts/github_actions_mask_env.sh\ngha_mask_env CORDUM_API_KEY "${generated}"'},
        {"run": "touch optional.log"}, {"id": "redact_artifacts", "env": {"CORDUM_API_KEY": "${{ env.CORDUM_API_KEY || 'credential-unavailable-for-redaction' }}"}, "run": "source tools/scripts/github_actions_mask_env.sh\ngha_redact_paths CORDUM_API_KEY -- optional.log"},
        {"if": "always() && steps.redact_artifacts.outcome == 'success'", "uses": "actions/upload-artifact@v4"}]
    mutations = []
    direct = deepcopy(safe); direct.insert(1, {"run": 'echo "FUTURE_API_KEY=${generated}" >> "$GITHUB_ENV"'}); mutations.append((direct, "DIRECT_ENV_WRITE"))
    bare = deepcopy(safe); bare[0] = {"run": 'go run ./tools/cilicense >> "$GITHUB_ENV"'}; mutations.append((bare, "BARE_CILICENSE_REDIRECT"))
    echoed = deepcopy(safe); echoed.insert(1, {"run": 'echo "$CORDUM_API_KEY" # no-secret-lint'}); mutations.append((echoed, "UNSAFE_OUTPUT"))
    upload = deepcopy(safe); upload[-1]["if"] = "always()"; mutations.append((upload, "UPLOAD_NOT_REDACTION_GATED"))
    commented = deepcopy(safe); commented[0]["run"] = '# source tools/scripts/github_actions_mask_env.sh\n# gha_mask_env CORDUM_API_KEY "$generated"\necho "CORDUM_API_KEY=${generated}" >> "$GITHUB_ENV"'; mutations.append((commented, "DIRECT_ENV_WRITE"))
    unreachable = deepcopy(safe); unreachable[0]["run"] = 'if false; then\nsource tools/scripts/github_actions_mask_env.sh\ngha_mask_env CORDUM_API_KEY "$generated"\nfi'; mutations.append((unreachable, "HELPER_ORDER"))
    late = deepcopy(safe); late[0]["run"] = 'generated="$(openssl rand -hex 32)"\necho "$generated"\nsource tools/scripts/github_actions_mask_env.sh\ngha_mask_env CORDUM_API_KEY "$generated"'; mutations.append((late, "PREMASK_ALIAS_USE")); simple = deepcopy(safe); simple[0]["run"] = 'source tools/scripts/github_actions_mask_env.sh\ngha_mask_env CORDUM_API_KEY "$(false)"'; mutations.append((simple, "COMMAND_SUBSTITUTION_ARGUMENT"))
    alias = deepcopy(safe); alias.insert(1, {"run": 'lower_alias="$CORDUM_API_KEY"\necho "$lower_alias"'}); mutations.append((alias, "UNSAFE_OUTPUT")); upper = deepcopy(safe); upper.insert(1, {"run": 'UPPER_ALIAS="$CORDUM_API_KEY"\necho "$UPPER_ALIAS" # no-secret-lint'}); mutations.append((upper, "UNSAFE_OUTPUT")); fallback = deepcopy(safe); fallback[2]["env"] = {}; mutations.append((fallback, "FALLBACK_MISMATCH")); prep = deepcopy(safe); prep[1]["run"] = "true"; mutations.append((prep, "OPTIONAL_ARTIFACT_PREP"))
    mixed = deepcopy(safe); mixed[0]["run"] += '\necho "CORDUM_API_KEY=${generated}" >> "$GITHUB_ENV"'; mutations.append((mixed, "DIRECT_ENV_WRITE"))
    redirected = deepcopy(safe); redirected[0]["run"] += " >/dev/null"; mutations.append((redirected, "HELPER_ORDER")); continued = deepcopy(safe); continued[0]["run"] += " \\\n  >/dev/null"; mutations.append((continued, "HELPER_ORDER")); piped = deepcopy(safe); piped[0]["run"] += " | cat >/dev/null"; mutations.append((piped, "HELPER_ORDER")); scrubbed = deepcopy(safe); scrubbed[2]["run"] += " || true"; mutations.append((scrubbed, "UPLOAD_WITHOUT_REDACTION")); permissive = deepcopy(safe); permissive[-1]["if"] += " || failure()"; mutations.append((permissive, "UPLOAD_NOT_REDACTION_GATED")); heredoc = deepcopy(safe); heredoc[0]["run"] = 'source tools/scripts/github_actions_mask_env.sh\ncat <<EOF\ngha_mask_env CORDUM_API_KEY "$generated"\nEOF'; mutations.append((heredoc, "HELPER_ORDER")); wrapped = deepcopy(safe); wrapped[0]["run"] = 'source tools/scripts/github_actions_mask_env.sh\nif false; then\ngha_mask_env CORDUM_API_KEY "$generated"\nfi\necho "$generated" # no-secret-lint'; mutations.append((wrapped, "HELPER_ORDER")); shadowed = deepcopy(safe); shadowed[0]["run"] = 'source tools/scripts/github_actions_mask_env.sh\ngha_mask_env(){ printf "CORDUM_API_KEY=%s\\n" "$2" >> "$GITHUB_ENV"; export "$1=$2"; }\ngha_mask_env CORDUM_API_KEY "$generated"'; mutations.append((shadowed, "HELPER_ORDER")); admin_wrapped = deepcopy(safe); admin_wrapped[0]["run"] = 'source tools/scripts/github_actions_mask_env.sh\nif [[ -z "${admin_random}" ]]; then\ngha_mask_env CORDUM_API_KEY "$generated"\nfi'; mutations.append((admin_wrapped, "HELPER_ORDER")); indirect = deepcopy(safe); indirect[0]["run"] = 'source tools/scripts/github_actions_mask_env.sh\nsecret_name=generated\necho "${!secret_name}" # no-secret-lint\ngha_mask_env CORDUM_API_KEY "${generated}"'; mutations.append((indirect, "HELPER_ORDER"))  # no-secret-lint
    with TemporaryDirectory() as tmp:
        path = Path(tmp) / "fixture.yml"
        def parsed(steps):
            path.write_text(yaml.safe_dump({"jobs": {"fixture": {"steps": steps}}}), encoding="utf-8")
            return yaml.safe_load(path.read_text(encoding="utf-8"))["jobs"]["fixture"]["steps"]
        if check_job(path.name, "safe", parsed(safe), ["CORDUM_API_KEY"])[0]: return 1
        quick = 'seeded_key=x\nseeded_redis=x\nif [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then\nsource "${REPO_ROOT}/tools/scripts/github_actions_mask_env.sh"\ngha_mask_value "${seeded_key}"\ngha_mask_value "${seeded_redis}"\nfi\ncat > .env\ngrep -Fq "${seeded_key}"\ngrep -Fq "${seeded_redis}"\ncat "${quickstart_log}"'
        quick_red = all(check_quickstart_text(item) for item in (quick.replace('gha_mask_value "${seeded_redis}"', ""), quick.replace('grep -Fq "${seeded_redis}"', ""), quick.replace("seeded_redis=x\n", 'seeded_redis=x\ngrep -Fq "${seeded_redis}"\n'), quick.replace('gha_mask_value "${seeded_key}"', 'gha_mask_value "${seeded_key}" >/dev/null').replace('gha_mask_value "${seeded_redis}"', 'gha_mask_value "${seeded_redis}" >/dev/null'), quick.replace('source "${REPO_ROOT}/tools/scripts/github_actions_mask_env.sh"\n', 'source "${REPO_ROOT}/tools/scripts/github_actions_mask_env.sh"\ngha_mask_value(){ :; }\n'), *(quick.replace(command, f"# {command}") for command in ('gha_mask_value "${seeded_key}"', 'gha_mask_value "${seeded_redis}"', 'grep -Fq "${seeded_key}"', 'grep -Fq "${seeded_redis}"'))))
        return 0 if not check_quickstart_text(quick) and quick_red and all(code in check_job(path.name, "safe", parsed(item), ["CORDUM_API_KEY"])[0] for item, code in mutations) else 1
if __name__ == "__main__":
    if len(sys.argv) == 2 and sys.argv[1] == "--self-test": raise SystemExit(self_test())
    found = scan(Path(sys.argv[1]))
    for path, job, code in found: print(f"{path}:{job}:{code}")
    raise SystemExit(bool(found))
PY
}
run_quiet() {
  local name="$1"; shift
  local output="${TEST_TMP}/quiet-${PASS}-${FAIL}.log" status=0
  "$@" > "${output}" 2>&1 || status=1
  record "${name}" "${status}"
}
assert_count "integration enables managed-key storage" "${INTEGRATION}" 'CORDUM_USER_AUTH_ENABLED=true' 1
assert_count "all nightly service jobs enable managed-key storage" "${NIGHTLY}" 'CORDUM_USER_AUTH_ENABLED=true' 3
assert_count "nightly labels the complete 21-gate suite accurately" "${NIGHTLY}" 'Full Production Gate (21 gates)' 1
assert_count "nightly labels the complete gate step accurately" "${NIGHTLY}" 'Run all production gates (1-21)' 1
assert_count "nightly avoids duplicate setup-go and actions/cache restores" "${NIGHTLY}" 'cache: false' 3
assert_count "integration tests isolate fixture license environment" "${INTEGRATION}" 'env -u CORDUM_LICENSE_TOKEN -u CORDUM_LICENSE_PUBLIC_KEY go test -v -tags=integration -timeout 10m ./...' 1
assert_count "nightly soak uses the TLS gateway URL" "${NIGHTLY}" 'CORDUM_API_BASE: https://localhost:8081/api/v1' 1
assert_count "nightly soak trusts the generated CA" "${NIGHTLY}" 'CORDUM_TLS_CA: certs/ca/ca.crt' 1
assert_count "soak requests apply resolved TLS options" "${SOAK}" '"${CURL_TLS_OPTS[@]}"' 2
assert_count "soak supports an explicit TLS CA" "${SOAK}" '--cacert "${TLS_CA}"' 1
assert_count "CI runs soak analysis regression tests" "${CI}" 'bash tools/scripts/soak_test_lib.test.sh' 1
assert_count "strict shared-runner gate excludes hardware-dependent gate 6" "${NIGHTLY}" '--skip-rebuild --strict --exclude-gate 6' 1
assert_count "nightly keeps gate 6 as a visible advisory probe" "${NIGHTLY}" 'RESULTS_FILE=performance_gate_results.json bash tools/scripts/production_gate.sh --gate 6 --skip-rebuild --strict' 1
assert_count "shared-runner performance probe is explicitly nonblocking" "${NIGHTLY}" 'continue-on-error: true' 1
assert_count "nightly does not launder the production p95 threshold" "${NIGHTLY}" 'PERF_P95_MS:' 0
assert_count "release artifacts retain the advisory performance result" "${NIGHTLY}" 'performance_gate_results.json' 2
if [[ ! -f "${HELPER}" ]]; then
  record "masking helper API exists" 1
else
  run_helper_case "helper masks, exports, imports, and redacts every credential class" valid
  for kind in empty_name invalid_name cr_name lf_name empty_value extra_value extra_env cr_value lf_value unset_env bad_env write_failure command malformed missing extra duplicate redact_missing redact_bad_name redact_binary redact_symlink; do
    run_helper_case "helper rejects ${kind//_/ } without leaking" "${kind}"
  done
fi
write_workflow_guard
run_quiet "workflow guard detects all unsafe mutations" python "${GUARD}" --self-test
run_quiet "all active workflows satisfy masking and redaction contract" python "${GUARD}" "${ROOT}"
echo "SUMMARY: ${PASS} pass, ${FAIL} fail"; [[ "${FAIL}" -eq 0 ]]
