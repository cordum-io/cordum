#!/usr/bin/env python3
"""Fail-closed contract for generated credentials in GitHub workflows."""
from collections import Counter
from copy import deepcopy
from pathlib import Path, PurePosixPath
import re
import sys
import yaml
SOURCE = "tools/scripts/github_actions_mask_env.sh"
PLACEHOLDER = "credential-unavailable-for-redaction"
CREDENTIAL_NAME = r"[A-Z0-9_]*(?:API_KEYS?|REDIS_PASSWORD|ADMIN_PASSWORD|LICENSE_(?:TOKEN|PUBLIC_KEY))"
CREDENTIAL = re.compile(rf"\b{CREDENTIAL_NAME}\b")
EXPECTED_WORKFLOWS = set("binaries-pr-validation.yml ci.yml codeql.yml demo-mock-bank-e2e.yml docker-main.yml docker.yml docs-linkcheck.yml e2e.yml edge-fake-hook-e2e.yml integration-nightly.yml nightly.yml platform-smoke.yml release.yml sdk-conformance.yml sdk-python.yml sdk-typescript.yml star-tracker.yml".split())
LICENSED = ("CORDUM_API_KEY", "REDIS_PASSWORD", "CORDUM_ADMIN_PASSWORD", "CORDUM_LICENSE_TOKEN", "CORDUM_LICENSE_PUBLIC_KEY")
EXPECTED = {
    ("integration-nightly.yml", "integration"): LICENSED,
    ("nightly.yml", "full-production-gate"): LICENSED,
    ("nightly.yml", "release-gate"): LICENSED,
    ("nightly.yml", "soak-test"): LICENSED,
    ("demo-mock-bank-e2e.yml", "demo-mock-bank-e2e"): ("CORDUM_API_KEY", "REDIS_PASSWORD", "CORDUM_LICENSE_TOKEN", "CORDUM_LICENSE_PUBLIC_KEY"),
    ("platform-smoke.yml", "platform-smoke"): ("CORDUM_API_KEY", "REDIS_PASSWORD"),
    ("edge-fake-hook-e2e.yml", "edge-fake-hook-e2e"): ("CORDUM_API_KEY", "CORDUM_APPROVER_API_KEY", "CORDUM_API_KEYS", "REDIS_PASSWORD"),
    ("e2e.yml", "e2e-tls"): ("CORDUM_API_KEY", "REDIS_PASSWORD", "CORDUM_ADMIN_PASSWORD"),
}
FALLBACK_KEYS = {key for key in EXPECTED if key[0] in {"demo-mock-bank-e2e.yml", "e2e.yml", "edge-fake-hook-e2e.yml", "platform-smoke.yml"}}
PREPARED = {("nightly.yml", "soak-test"): "touch soak_results.json soak_metrics.log soak_http.log"}
UNSAFE_ENV = {"BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "BASH_XTRACEFD", "GITHUB_ACTIONS", "PATH", "PYTHONHOME", "PYTHONPATH", "PYTHONSTARTUP"}
OUTPUT = re.compile(r"^\s*(?:echo|printf|cat|tee|printenv|declare\s+-p|typeset\s+-p|export\s+-p)\b")
def commands(run: str) -> list[str]:
    return [line.strip() for line in run.splitlines() if line.strip() and not line.lstrip().startswith("#")]
def helper_calls(run: str) -> Counter[str]:
    found: Counter[str] = Counter()
    found.update(re.findall(rf"(?m)^gha_mask_env\s+({CREDENTIAL_NAME})\s+.+$", run))
    for args in re.findall(r"(?m)^gha_mask_env_from_command\s+(.+?)\s+--\s+.+$", run):
        found.update(name for name in re.findall(r"\b[A-Z][A-Z0-9_]*\b", args) if CREDENTIAL.fullmatch(name))
    return found
def context_is_unsafe(*nodes: object) -> bool:
    for node in nodes:
        if not isinstance(node, dict):
            continue
        env = node.get("env") or {}
        defaults = (node.get("defaults") or {}).get("run") or {}
        if isinstance(env, dict) and UNSAFE_ENV.intersection(env):
            return True
        if node.get("shell") or node.get("working-directory") or defaults.get("shell") or defaults.get("working-directory"):
            return True
    return False
def prior_context_is_unsafe(steps: list[object], stop: int) -> bool:
    names = "|".join(sorted(UNSAFE_ENV))
    for step in steps[:stop]:
        run = str(step.get("run") or "") if isinstance(step, dict) else ""
        if "GITHUB_PATH" in run or re.search(rf"\b(?:{names})\s*=.*GITHUB_ENV|GITHUB_ENV.*\b(?:{names})\s*=", run):
            return True
    return False
def setup_issues(run: str) -> tuple[list[str], Counter[str]]:
    calls = helper_calls(run)
    lines = commands(run)
    issues: list[str] = []
    admin = ('if [[ -z "${admin_random}" ]]; then', 'echo "::error::admin credential generation returned empty output" >&2', "exit 1", "fi")
    allowed = (
        re.escape(f"source {SOURCE}"), r'[a-z_][A-Za-z0-9_]*="\$\(openssl rand -hex [0-9]+\)" \|\| exit 1',
        r'gha_mask_env [A-Z][A-Z0-9_]* "(?:\$\{[a-z_][A-Za-z0-9_]*\}|CordumE2E-\$\{admin_random\}!1)"',
        r'gha_mask_env_from_command(?: [A-Z][A-Z0-9_]*)+ -- go run \./tools/cilicense',
        re.escape("payload=$(printf '[{\"key\":\"%s\",\"role\":\"admin\",\"tenant\":\"default\",\"principal_id\":\"edge-fake-hook-e2e-approver\"}]' \"${CORDUM_APPROVER_API_KEY}\")"),
        *(re.escape(line) for line in admin), r'echo "CORDUM_USER_AUTH_ENABLED=true" >> "\$GITHUB_ENV"', r'echo "CORDUM_ADMIN_USERNAME=admin" >> "\$GITHUB_ENV"',
    )
    control = [line for line in lines if line in admin]
    if not lines or lines[0] != f"source {SOURCE}" or lines.count(f"source {SOURCE}") != 1:
        issues.append("HELPER_ORDER")
    if any(not any(re.fullmatch(pattern, line) for pattern in allowed) for line in lines):
        issues.append("HELPER_ORDER")
    if control and (control != list(admin) or "\n".join(admin) not in "\n".join(lines)):
        issues.append("HELPER_ORDER")
    for match in re.finditer(rf"(?m)^gha_mask_env\s+{CREDENTIAL_NAME}\s+(.+)$", run):
        argument = match.group(1).strip()
        if "$(" in argument:
            issues.append("COMMAND_SUBSTITUTION_ARGUMENT")
        aliases = re.findall(r"\$\{?([A-Za-z_][A-Za-z0-9_]*)", argument)
        if any(re.search(rf"\$\{{?{re.escape(alias)}\b", line) and not re.match(r"^\s*(?:if\s+)?\[\[", line) for alias in aliases for line in run[:match.start()].splitlines()):
            issues.append("PREMASK_ALIAS_USE")
    return issues, calls
def output_issues(run: str, credential_job: bool) -> list[str]:
    if not credential_job:
        return []
    issues: list[str] = []
    aliases = set(re.findall(rf'(?m)^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*["\']?\$\{{?{CREDENTIAL_NAME}\b', run))
    for line in run.splitlines():
        direct = set(CREDENTIAL.findall(line))
        alias_ref = any(re.search(rf"\$\{{?{re.escape(alias)}\b", line) for alias in aliases)
        dump_all = bool(re.match(r"^\s*(?:env|set)\s*(?:\|.*)?$", line))
        traced = bool(re.search(r"(?:^|[;&|]\s*)(?:set\s+-x|set\s+-o\s+xtrace|bash\s+-x)\b", line))
        grep_output = bool(re.match(r"^\s*grep\b", line)) and " -q" not in line
        if "${!" in line or dump_all or traced or ((OUTPUT.search(line) or grep_output) and (direct or alias_ref)):
            issues.append("UNSAFE_OUTPUT")
    return issues
def redact_call(run: str) -> tuple[set[str], list[str]] | None:
    lines = commands(run)
    if len(lines) != 2 or lines[0] != f"source {SOURCE}":
        return None
    match = re.fullmatch(r"gha_redact_paths((?: [A-Z][A-Z0-9_]*)+) --((?: [-A-Za-z0-9_./*]+)+)", lines[1])
    if not match:
        return None
    return set(match.group(1).split()), match.group(2).split()
def normalized_paths(value: object) -> list[str]:
    raw = value if isinstance(value, str) else ""
    paths = [line.strip().replace("\\", "/") for line in raw.splitlines() if line.strip()]
    if any("${{" in path or ".." in Path(path).parts for path in paths):
        return []
    return [str(PurePosixPath(path)).rstrip("/") for path in paths]
def paths_match(redacted: list[str], uploaded: list[str]) -> bool:
    if not redacted or not uploaded:
        return False
    return Counter(redacted) == Counter(uploaded)
def upload_issues(step: dict, prior: dict, wanted: tuple[str, ...]) -> list[str]:
    issues: list[str] = []
    parsed = redact_call(str(prior.get("run") or ""))
    if parsed is None:
        return ["UPLOAD_WITHOUT_REDACTION"]
    names, paths = parsed
    if names != set(wanted):
        issues.append("REDACTION_SET_MISMATCH")
    config = step.get("with") or {}
    uploaded = normalized_paths(config.get("path")) if isinstance(config, dict) else []
    if not paths_match(normalized_paths("\n".join(paths)), uploaded):
        issues.append("UPLOAD_PATH_MISMATCH")
    prior_id = str(prior.get("id") or "")
    condition = str(step.get("if") or "").strip()
    if not prior_id or not re.fullmatch(rf"(?:always|failure)\(\) && steps\.{re.escape(prior_id)}\.outcome == 'success'", condition):
        issues.append("UPLOAD_NOT_REDACTION_GATED")
    return issues
def check_job(path: str, job_id: str, steps: list[object], wanted: tuple[str, ...], parents: tuple[object, ...] = ()) -> tuple[list[str], bool]:
    issues: list[str] = []
    setup: Counter[str] = Counter()
    credential_job, fallback_steps = False, 0
    for index, raw_step in enumerate(steps):
        if not isinstance(raw_step, dict):
            continue
        run = str(raw_step.get("run") or "")
        calls = helper_calls(run)
        redaction = redact_call(run)
        protected = bool(calls or redaction or "quickstart_env_sharing_test.sh" in run)
        if protected and (context_is_unsafe(*parents, raw_step) or prior_context_is_unsafe(steps, index)):
            issues.append("UNSAFE_SHELL_CONTEXT")
        if "quickstart_env_sharing_test.sh" in run and (commands(run) != ["bash tools/scripts/quickstart_env_sharing_test.sh"] or raw_step.get("env") != {"CORDUM_INTEGRATION": "1", "CORDUM_QUICKSTART_ENV_SHARING_MODE": "live"}):
            issues.append("QUICKSTART_CALLER_SHAPE")
        if calls:
            credential_job = True
            setup.update(calls)
            found, _ = setup_issues(run)
            issues.extend(found)
        nonhelper = "\n".join(line for line in run.splitlines() if not line.startswith(("gha_mask_env ", "gha_mask_env_from_command ")))
        if "GITHUB_ENV" in nonhelper and CREDENTIAL.search(nonhelper):
            credential_job = True
            issues.append("DIRECT_ENV_WRITE")
        if re.search(rf"(?m)^\s*(?:export\s+)?{CREDENTIAL_NAME}=.*(?:\$\(|openssl|uuidgen|/dev/urandom|\bRANDOM\b)", run):
            credential_job = True
            issues.append("DIRECT_GENERATION")
        if "cilicense" in run and "gha_mask_env_from_command" not in run:
            credential_job = True
            issues.append("BARE_CILICENSE_REDIRECT")
        step_env = raw_step.get("env") or {}
        fallback = {name for name, value in step_env.items() if PLACEHOLDER in str(value)} if isinstance(step_env, dict) else set()
        if fallback:
            fallback_steps += 1
            if fallback != set(wanted) or not redaction or redaction[0] != fallback:
                issues.append("FALLBACK_SCOPE")
        if redaction and (path, job_id) in PREPARED and "soak_results.json" in redaction[1]:
            prior = steps[index - 1] if index and isinstance(steps[index - 1], dict) else {}
            if not commands(str(prior.get("run") or "")) or commands(str(prior.get("run") or ""))[0] != PREPARED[(path, job_id)]:
                issues.append("OPTIONAL_ARTIFACT_PREP")
        issues.extend(output_issues(run, credential_job or (path, job_id) in EXPECTED))
        uses = str(raw_step.get("uses") or "").lower()
        if uses.startswith("actions/upload-artifact@") and credential_job:
            prior = steps[index - 1] if index and isinstance(steps[index - 1], dict) else {}
            issues.extend(upload_issues(raw_step, prior, wanted))
    if credential_job and setup != Counter(wanted):
        issues.append("INVENTORY_MISMATCH")
    if (path, job_id) in FALLBACK_KEYS and fallback_steps != 1:
        issues.append("FALLBACK_MISMATCH")
    return issues, credential_job
def heredoc_lines(lines: list[str]) -> set[int]:
    marked: set[int] = set()
    delimiter: str | None = None
    for index, line in enumerate(lines):
        if delimiter is not None:
            marked.add(index)
            if line.strip() == delimiter:
                delimiter = None
            continue
        match = re.search(r"<<-?\s*['\"]?([A-Za-z_][A-Za-z0-9_]*)['\"]?", line)
        if match:
            delimiter = match.group(1)
    return marked
def shell_depth(lines: list[str], marked: set[int], stop: int) -> int:
    depth = 0
    for index, line in enumerate(lines[:stop]):
        if index in marked or not line.strip() or line.lstrip().startswith("#"):
            continue
        text = line.strip()
        if text in {"}", "fi", "done", "esac"}:
            depth -= 1
        if re.match(r"^(?:(?:function\s+)?[A-Za-z_]\w*\s*\(\)|function\s+[A-Za-z_]\w*)\s*\{$", text) or re.match(r"^(?:if\b.*;\s*then|(?:for|while|until|select)\b.*;\s*do|case\b.*\bin)$", text):
            depth += 1
        if depth < 0:
            return -1
    return depth
def check_quickstart_text(text: str) -> list[str]:
    lines = text.splitlines()
    marked = heredoc_lines(lines)
    protected = ('if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then', 'source "${REPO_ROOT}/tools/scripts/github_actions_mask_env.sh"', 'gha_mask_value "${seeded_key}"', 'gha_mask_value "${seeded_redis}"', 'fi')
    leak = ('if grep -Fq "${seeded_key}" "${quickstart_log}" || grep -Fq "${seeded_redis}" "${quickstart_log}"; then', 'fail "quickstart output leaked a full seeded secret value"', 'fi')
    masks = [i for i in range(len(lines) - len(protected) + 1) if tuple(line.strip() for line in lines[i:i + len(protected)]) == protected]
    guards = [i for i in range(len(lines) - len(leak) + 1) if tuple(line.strip() for line in lines[i:i + len(leak)]) == leak]
    if len(masks) != 1 or len(guards) != 1:
        return ["QUICKSTART_MASK_OR_LOG_ORDER"]
    mask, guard = masks[0], guards[0]
    if any(index in marked for index in range(mask, mask + len(protected))) or any(index in marked for index in range(guard, guard + len(leak))):
        return ["QUICKSTART_EXECUTION_SHAPE"]
    if shell_depth(lines, marked, mask) != 1 or shell_depth(lines, marked, guard) != 1:
        return ["QUICKSTART_EXECUTION_SHAPE"]
    before = "\n".join(lines[:mask])
    if re.search(r"(?m)^\s*(?:(?:function\s+)?(?:source|gha_mask_value)\s*\(\)|function\s+(?:source|gha_mask_value))\s*\{|^\s*alias\s+(?:source|gha_mask_value)=|^\s*(?:(?:export|readonly|declare|typeset|local)\s+)?GITHUB_ACTIONS=|^\s*unset\s+GITHUB_ACTIONS\b", before):
        return ["QUICKSTART_EXECUTION_SHAPE"]
    generated = [[i for i, line in enumerate(lines) if line.lstrip().startswith(f"{name}=")] for name in ("seeded_key", "seeded_redis")]
    dotenv = next((i for i, line in enumerate(lines) if line.lstrip().startswith("cat > .env")), -1)
    log = [i for i, line in enumerate(lines) if line.strip() == 'cat "${quickstart_log}"']
    if mask == 0 or lines[mask - 1].strip().endswith(("|", "&&", "||")) or not all(len(items) == 1 and items[0] < mask < dotenv < guard for items in generated) or len(log) != 1 or log[0] != guard + len(leak):
        return ["QUICKSTART_MASK_OR_LOG_ORDER"]
    return []
def inventory_issues(names: set[str]) -> list[tuple[str, str, str]]:
    issues = [(name, "-", "WORKFLOW_MISSING") for name in sorted(EXPECTED_WORKFLOWS - names)]
    issues.extend((name, "-", "WORKFLOW_UNEXPECTED") for name in sorted(names - EXPECTED_WORKFLOWS))
    return issues
def scan(root: Path) -> list[tuple[str, str, str]]:
    issues: list[tuple[str, str, str]] = []
    paths = sorted((root / ".github/workflows").glob("*.y*ml"))
    issues.extend(inventory_issues({path.name for path in paths}))
    seen: set[tuple[str, str]] = set()
    for path in paths:
        try:
            workflow = yaml.safe_load(path.read_text(encoding="utf-8")) or {}
        except Exception:
            issues.append((path.name, "-", "YAML_PARSE"))
            continue
        for job_id, job in (workflow.get("jobs") or {}).items():
            key = (path.name, job_id)
            found, credential_job = check_job(path.name, job_id, (job or {}).get("steps") or [], EXPECTED.get(key, ()), (workflow, job or {}))
            if credential_job:
                seen.add(key)
            issues.extend((path.name, job_id, code) for code in found)
    issues.extend((*key, "INVENTORY_MISSING") for key in set(EXPECTED) - seen)
    issues.extend((*key, "INVENTORY_EXTRA") for key in seen - set(EXPECTED))
    quickstart = root / "tools/scripts/quickstart_env_sharing_test.sh"
    quick_codes = ["QUICKSTART_MISSING"] if not quickstart.is_file() else check_quickstart_text(quickstart.read_text(encoding="utf-8"))
    issues.extend((quickstart.name, "ci-transitive", code) for code in quick_codes)
    return issues
def self_test() -> int:
    safe = [{"run": f'source {SOURCE}\ngha_mask_env CORDUM_API_KEY "${{generated}}"'}, {"run": "touch optional.log"}, {"id": "redact", "env": {"CORDUM_API_KEY": f"${{{{ env.CORDUM_API_KEY || '{PLACEHOLDER}' }}}}"}, "run": f"source {SOURCE}\ngha_redact_paths CORDUM_API_KEY -- optional.log"}, {"if": "always() && steps.redact.outcome == 'success'", "uses": "actions/upload-artifact@v4", "with": {"path": "optional.log"}}]
    source_and_mask = f'source {SOURCE}\ngha_mask_env CORDUM_API_KEY "${{generated}}"'
    cases = [
        ('echo "CORDUM_API_KEY=x" >> "$GITHUB_ENV"', "DIRECT_ENV_WRITE"), ('go run ./tools/cilicense >> "$GITHUB_ENV"', "BARE_CILICENSE_REDIRECT"),
        ('source tools/scripts/github_actions_mask_env.sh\ngha_mask_env CORDUM_API_KEY "${generated}" >/dev/null', "HELPER_ORDER"),
        ('source tools/scripts/github_actions_mask_env.sh\nif false; then\ngha_mask_env CORDUM_API_KEY "${generated}"\nfi', "HELPER_ORDER"),
        ('source tools/scripts/github_actions_mask_env.sh\nsecret_name=generated\necho "${!secret_name}"\ngha_mask_env CORDUM_API_KEY "${generated}"', "UNSAFE_OUTPUT"),
    ]
    cases.extend((f'{source_and_mask}\n{sink}', "UNSAFE_OUTPUT") for sink in ("printenv CORDUM_API_KEY", "env", "declare -p CORDUM_API_KEY", "typeset -p CORDUM_API_KEY", "export -p CORDUM_API_KEY", "set", "set -x"))
    if check_job("fixture.yml", "safe", safe, ("CORDUM_API_KEY",))[0]:
        return 1
    for run, code in cases:
        item = deepcopy(safe)
        item[0] = {"run": run}
        if code not in check_job("fixture.yml", "safe", item, ("CORDUM_API_KEY",))[0]:
            return 1
    for setting in ({"env": {"SHELLOPTS": "xtrace"}}, {"shell": "bash -x {0}"}, {"working-directory": "unsafe"}):
        context = deepcopy(safe); context[0].update(setting)
        if "UNSAFE_SHELL_CONTEXT" not in check_job("fixture.yml", "safe", context, ("CORDUM_API_KEY",))[0]: return 1
    prior = deepcopy(safe); prior.insert(0, {"run": 'echo "BASH_ENV=/tmp/unsafe" >> "$GITHUB_ENV"'})
    prior_path = deepcopy(safe); prior_path.insert(0, {"run": 'echo "/tmp/unsafe" >> "$GITHUB_PATH"'})
    mismatch = deepcopy(safe); mismatch[-1]["with"]["path"] = "unredacted.log"
    permissive = deepcopy(safe); permissive[-1]["if"] = "always()"
    parents = (({"defaults": {"run": {"working-directory": "unsafe"}}},), ({"env": {"BASH_ENV": "/tmp/unsafe"}},))
    caller = [{"run": "GITHUB_ACTIONS=false bash tools/scripts/quickstart_env_sharing_test.sh"}]; caller_env = [{"run": "bash tools/scripts/quickstart_env_sharing_test.sh", "env": {"CORDUM_INTEGRATION": "1", "CORDUM_QUICKSTART_ENV_SHARING_MODE": "disabled"}}]
    if any(code not in check_job("fixture.yml", "safe", item, ("CORDUM_API_KEY",), context)[0] for item, code, context in ((prior, "UNSAFE_SHELL_CONTEXT", ()), (prior_path, "UNSAFE_SHELL_CONTEXT", ()), (mismatch, "UPLOAD_PATH_MISMATCH", ()), (permissive, "UPLOAD_NOT_REDACTION_GATED", ()), *((safe, "UNSAFE_SHELL_CONTEXT", parent) for parent in parents), (caller, "QUICKSTART_CALLER_SHAPE", ()), (caller_env, "QUICKSTART_CALLER_SHAPE", ()))):
        return 1
    unprepared = [{"run": "bash tools/scripts/soak_test.sh"}, {"run": f"source {SOURCE}\ngha_redact_paths CORDUM_API_KEY -- soak_results.json"}]
    if "OPTIONAL_ARTIFACT_PREP" not in check_job("nightly.yml", "soak-test", unprepared, ("CORDUM_API_KEY",))[0]: return 1
    good = 'test_case() {\nseeded_key=x\nseeded_redis=x\nif [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then\nsource "${REPO_ROOT}/tools/scripts/github_actions_mask_env.sh"\ngha_mask_value "${seeded_key}"\ngha_mask_value "${seeded_redis}"\nfi\ncat > .env\nif grep -Fq "${seeded_key}" "${quickstart_log}" || grep -Fq "${seeded_redis}" "${quickstart_log}"; then\nfail "quickstart output leaked a full seeded secret value"\nfi\ncat "${quickstart_log}"\n}'
    bad = (good.replace('gha_mask_value "${seeded_redis}"', ''), "cat <<BLOCK\n" + good.replace("cat > .env", "BLOCK\ncat > .env"), good.replace('"${quickstart_log}" || grep', '/dev/null || grep'), good.replace("test_case() {", "test_case() {\nif false; then") + "\nfi", good.replace("seeded_key=x", "seeded_key=x\ngha_mask_value(){ :; }"), good.replace("seeded_key=x", "seeded_key=x\nfunction source { :; }"), good.replace("seeded_key=x", "seeded_key=x\nexport GITHUB_ACTIONS=false"), good.replace("cat > .env", "seeded_key=changed\ncat > .env"), good.replace('gha_mask_value "${seeded_key}"', 'gha_mask_value "${seeded_key}" >/dev/null'), good.replace('; then\nfail', ' || true; then\nfail'), good.replace('cat "${quickstart_log}"', 'quickstart_log=other\ncat "${quickstart_log}"'))
    return int(bool(check_quickstart_text(good)) or not all(check_quickstart_text(item) for item in bad) or paths_match(["a*"], ["a?"]) or not inventory_issues({"unexpected.yml"}))
def main(argv: list[str]) -> int:
    if argv == ["--self-test"]:
        return self_test()
    if len(argv) != 1:
        return 2
    found = scan(Path(argv[0]))
    for path, job, code in found:
        print(f"{path}:{job}:{code}")
    return int(bool(found))
if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
