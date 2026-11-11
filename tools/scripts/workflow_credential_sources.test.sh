#!/usr/bin/env bash
# Contract tests for credentials reached transitively from GitHub workflows: each case mutates a throwaway copy
# of the workflow, Makefile and script tree and asserts one exact guard code. Every fixture is synthetic (nothing
# reads, mints or prints a real credential), and SENTINEL proves guard reports and inventory lines stay value-free.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GUARD="${ROOT}/tools/scripts/workflow_credential_guard.py"
SOURCES="${ROOT}/tools/scripts/workflow_credential_sources.py"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT
SENTINEL="wcs_synthetic_${$}_${RANDOM}${RANDOM}"
PASS=0 FAIL=0 SKIP=0

record() {
  case "$1" in PASS) PASS=$((PASS + 1)) ;; SKIP) SKIP=$((SKIP + 1)) ;; *) FAIL=$((FAIL + 1)) ;; esac
  printf '%s %s\n' "$1" "$2"
}

# The scanned copy holds what workflow edges can reach: workflows, Makefile, tools/scripts, demo/ and tests/ shell.
BASE="${WORK}/pristine"
mkdir -p "${BASE}/.github" "${BASE}/tools"
cp -R "${ROOT}/.github/workflows" "${BASE}/.github/workflows"
cp -R "${ROOT}/tools/scripts" "${BASE}/tools/scripts"
cp "${ROOT}/Makefile" "${BASE}/Makefile"
(cd "${ROOT}" && find demo tests -type f \( -name '*.sh' -o -name Makefile -o -name '*.mk' \) -print0 \
  | tar --null -T - -cf -) | tar -xf - -C "${BASE}"

# build CASE copies BASE to WORK/CASE and applies its mutation; exit 3: this platform cannot create the fixture.
build() {
  cp -R "${BASE}" "${WORK}/$1"
  python - "${WORK}/$1" "$1" "${SENTINEL}" <<'PY'
import os
import pathlib
import sys

root, case, sentinel = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3]
PH = "credential-unavailable-for-redaction"
E2E, EDGE = ".github/workflows/e2e.yml", ".github/workflows/edge-fake-hook-e2e.yml"
CAPTURE = "      - name: Capture compose logs on failure\n"
LINK = (".github/workflows/docs-linkcheck.yml", "      - name: Run lychee (offline, gating)\n")
API = "          CORDUM_API_KEY: ${{ env.CORDUM_API_KEY || '" + PH + "' }}\n"
REG = 'gha_mask_value "${minted}"'
SAFE = '[[ -n "${minted}" ]] || exit 1\n' + REG
MINT_SH, RUN_MINT = "tools/scripts/zz_synthetic_mint.sh", "bash tools/scripts/zz_synthetic_mint.sh"
BODY = "\n".join((
    'base="${SYNTHETIC_GATEWAY:-https://127.0.0.1:9}"',
    'resp="$(curl -fsS -X POST "${base}/auth/keys" -d \'{"name":"@SENTINEL@"}\')"',
    'minted="$(printf \'%s\' "${resp}" | jq -r \'.secret // empty\')"',
    "@BEFORE@",
    'curl -fsS -H "X-Synthetic-Key: ${minted}" "${base}/api/v1/status" >/dev/null',
    "@AFTER@",
))
BINDS = {
    "bind_cross": API.replace("env.CORDUM_API_KEY", "env.REDIS_PASSWORD"),
    "bind_const": "          CORDUM_API_KEY: " + PH + "\n",
    "bind_foreign": API.replace("env.CORDUM_API_KEY", "secrets.SYNTHETIC_FOREIGN"),
    "bind_drop": "",
    "bind_rename": API.replace("CORDUM_API_KEY:", "CORDUM_API_KEY_RENAMED:"),
    "bind_spacing": "          CORDUM_API_KEY: ${{env.CORDUM_API_KEY||'" + PH + "'}}\n",
    "bind_literal_space": API.replace(PH + "'", PH + " '"),
}
MINTS = {  # (lines after the acquisition, lines after its first use) in a set -euo pipefail script
    "mint_unregistered": ("", ""), "mint_registered": (SAFE, ""), "mint_late": ("", REG),
    "mint_prefix": (REG + '\necho "synthetic prefix ${minted:0:6}"', ""), "mint_ignored": (REG + " || true", ""),
}
GEN = {
    "gen_openssl": "$(openssl rand -hex 16)", "gen_uuidgen": "$(uuidgen)",
    "gen_urandom": "$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \\n')", "gen_random": "syn-${RANDOM}${RANDOM}",
    "gen_date": "syn-$(date +%s%N)", "gen_python": "$(python3 -c 'import secrets; print(secrets.token_urlsafe(16))')",
    "gen_quoted_paren": "$(printf '(' \"(\")$(openssl rand -hex 8)",
    "gen_nested": '$(curl -fsS -d "$(jq -cn --arg n "syn-$(date +%s)" "{}")" https://127.0.0.1:9/)',
}
DYNAMIC = {
    "dyn_var": 'bash "${SYNTHETIC_DIR}/zz_synthetic_mint.sh"', "dyn_c": 'bash -c "${SYNTHETIC_CMD}"',
    "dyn_heredoc": "bash <<'SYNTHETIC'\ntrue\nSYNTHETIC", "dyn_pipe": "curl -fsS https://127.0.0.1:9/x | bash",
    "dyn_eval": 'eval "${SYNTHETIC_CMD}"', "dyn_make": 'make "${SYNTHETIC_TARGET}"',
    "dyn_source": 'source "${SYNTHETIC_DIR}/zz_synthetic_helper.sh"', "dyn_procsub": "source <(cat " + MINT_SH + ")",
    "dyn_wrapper": "timeout -s KILL 10m " + RUN_MINT, "dyn_cmdword": '"${SYNTHETIC_SHELL}" ' + MINT_SH,
}
SHELLS = {"shell_env_bash": "/usr/bin/env bash {0}", "shell_pwsh": "pwsh"}
def edit(rel, old, new):
    text = (root / rel).read_bytes().decode("utf-8")
    if text.count(old) != 1:
        raise SystemExit(case + ": fixture anchor is not unique in " + rel)
    (root / rel).write_bytes(text.replace(old, new).encode("utf-8"))
def write(rel, text):
    (root / rel).write_bytes(text.replace("@SENTINEL@", sentinel).encode("utf-8"))
def script(name, body):
    write("tools/scripts/" + name, "#!/usr/bin/env bash\nset -euo pipefail\n" + body + "\n")
def mint(before="", after="", rel=MINT_SH):
    body = BODY.replace("@BEFORE@", before).replace("@AFTER@", after)
    write(rel, "#!/usr/bin/env bash\nset -euo pipefail\nsource tools/scripts/github_actions_mask_env.sh\n" + body + "\n")
def step(body, wf=E2E, anchor=CAPTURE, shell=""):
    run = "".join("          " + line + "\n" for line in body.replace("@SENTINEL@", sentinel).split("\n"))
    edit(wf, anchor, "      - name: Synthetic fixture\n" + ("        shell: " + shell + "\n" if shell else "") + "        run: |\n" + run + "\n" + anchor)
if case == "bind_nightly":
    anchor = "      id: redact_full_gate_results\n"
    edit(".github/workflows/nightly.yml", anchor, anchor + "      env:\n        CORDUM_API_KEY: ${{ secrets.SYNTHETIC_FOREIGN }}\n")
elif case in BINDS:
    edit(E2E, API, BINDS[case])
elif case in MINTS:
    mint(*MINTS[case])
    step(RUN_MINT)
elif case == "mint_make":
    mint()
    makefile = root / "Makefile"
    makefile.write_bytes(makefile.read_bytes() + b"\nzz-synthetic-mint:\n\tbash tools/scripts/zz_synthetic_mint.sh\n")
    step("make zz-synthetic-mint")
elif case == "mint_untracked_job":
    mint()
    step(RUN_MINT, *LINK)
elif case == "mint_inline":
    step(BODY.replace("@BEFORE@", "").replace("@AFTER@", ""))
elif case in GEN:
    script("zz_synthetic_gen.sh", 'synthetic_password="' + GEN[case] + '"\ncurl -fsS -u "synthetic:${synthetic_password}" https://127.0.0.1:9/ >/dev/null')
    step("bash tools/scripts/zz_synthetic_gen.sh")
elif case == "nonce_bypass_removed":
    edit(EDGE, '          CORDUM_EDGE_E2E_BYPASS_HOOK: "1"\n', "")
elif case == "nonce_bypass_overridden":
    edit(EDGE, "run: bash tools/scripts/edge_fake_hook_e2e.sh", "run: CORDUM_EDGE_E2E_BYPASS_HOOK=0 bash tools/scripts/edge_fake_hook_e2e.sh")
elif case == "quickstart_unseeded":
    step("bash tools/scripts/quickstart.sh --skip-build", *LINK)
elif case in DYNAMIC:
    mint()
    step(DYNAMIC[case])
elif case == "dyn_script":
    script("zz_synthetic_dyn.sh", 'source "${SYNTHETIC_DIR}/zz_synthetic_helper.sh"')
    step("bash tools/scripts/zz_synthetic_dyn.sh")
elif case == "uses_local":
    edit(E2E, CAPTURE, "      - name: Synthetic local action\n        uses: ./.github/actions/zz-synthetic\n" + CAPTURE)
elif case in SHELLS:
    mint()
    step(RUN_MINT, shell=SHELLS[case])
elif case == "edge_cycle":
    script("zz_synthetic_cycle_a.sh", "bash tools/scripts/zz_synthetic_cycle_b.sh")
    script("zz_synthetic_cycle_b.sh", "bash tools/scripts/zz_synthetic_cycle_a.sh")
    step("bash tools/scripts/zz_synthetic_cycle_a.sh")
elif case == "edge_escape":
    mint(rel="../zz_synthetic_outside.sh")
    step("bash ../zz_synthetic_outside.sh")
elif case == "edge_symlink":
    mint()
    try:
        os.symlink("zz_synthetic_mint.sh", root / "tools/scripts/zz_synthetic_link.sh")
    except OSError:
        raise SystemExit(3)
    step("bash tools/scripts/zz_synthetic_link.sh")
elif case == "edge_oversize":
    script("zz_synthetic_big.sh", "# synthetic padding line\n" * 24000)
    step("bash tools/scripts/zz_synthetic_big.sh")
elif case == "edge_missing":
    step("bash tools/scripts/zz_synthetic_missing.sh")
elif case == "edge_depth":
    for depth in range(16):
        script("zz_synthetic_depth_%d.sh" % depth, "bash tools/scripts/zz_synthetic_depth_%d.sh" % (depth + 1) if depth < 15 else "true")
    step("bash tools/scripts/zz_synthetic_depth_0.sh")
elif case != "base":
    raise SystemExit("unknown fixture case: " + case)
PY
}

# has_issue REPORT WANT: WANT is workflow:job:CODE, optionally @path; the guard may append @location or :line.
has_issue() {
  local line
  while IFS= read -r line; do
    if [[ "${line}" == "$2" || "${line}" == "$2@"* || "${line}" == "$2:"* ]]; then return 0; fi
  done <<<"$1"
  return 1
}

check() {
  local name="$1" want="$2" out rc=0 built=0
  build "${name}" || built=$?
  if [[ "${built}" -ne 0 ]]; then
    if [[ "${built}" -eq 3 ]]; then record SKIP "${name}: platform cannot create this fixture"; else record FAIL "${name}: fixture build failed"; fi
    return 0
  fi
  out="$(python "${GUARD}" "${WORK}/${name}" 2>&1 | tr -d '\r')" || rc=$?
  rm -rf "${WORK:?}/${name}"
  if [[ "${out}" == *"${SENTINEL}"* ]]; then
    record FAIL "${name}: guard report echoed a synthetic fixture value"
  elif [[ "${want}" == clean && "${rc}" -eq 0 && -z "${out}" ]]; then
    record PASS "${name}: clean"
  elif [[ "${want}" == clean ]]; then
    record FAIL "${name}: expected clean, got rc=${rc}: ${out}"
  elif [[ "${rc}" -eq 1 ]] && has_issue "${out}" "${want}"; then
    record PASS "${name}: ${want}"
  else
    record FAIL "${name}: expected ${want}, got rc=${rc}: ${out}"
  fi
}

while read -r name want; do
  check "${name}" "${want}"
done <<'CASES'
base clean
bind_spacing clean
mint_registered clean
bind_literal_space e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_cross e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_const e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_foreign e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_drop e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_rename e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_nightly nightly.yml:full-production-gate:REDACTION_VALUE_BINDING
mint_unregistered e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
mint_late e2e.yml:e2e-tls:CREDENTIAL_USE_BEFORE_REGISTRATION@tools/scripts/zz_synthetic_mint.sh
mint_prefix e2e.yml:e2e-tls:CREDENTIAL_PREFIX_PRINT@tools/scripts/zz_synthetic_mint.sh
mint_ignored e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
mint_make e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
mint_untracked_job docs-linkcheck.yml:link-check:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
mint_inline e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED
gen_openssl e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_gen.sh
gen_uuidgen e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_gen.sh
gen_urandom e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_gen.sh
gen_random e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_gen.sh
gen_date e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_gen.sh
gen_python e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_gen.sh
gen_quoted_paren e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_gen.sh
gen_nested clean
nonce_bypass_removed edge-fake-hook-e2e.yml:edge-fake-hook-e2e:CREDENTIAL_UNREGISTERED@tools/scripts/edge_fake_hook_e2e.sh
nonce_bypass_overridden edge-fake-hook-e2e.yml:edge-fake-hook-e2e:CREDENTIAL_UNREGISTERED@tools/scripts/edge_fake_hook_e2e.sh
quickstart_unseeded docs-linkcheck.yml:link-check:CREDENTIAL_UNREGISTERED@tools/scripts/quickstart.sh
dyn_var e2e.yml:e2e-tls:SOURCE_DYNAMIC
dyn_c e2e.yml:e2e-tls:SOURCE_DYNAMIC
dyn_heredoc e2e.yml:e2e-tls:SOURCE_DYNAMIC
dyn_pipe e2e.yml:e2e-tls:SOURCE_DYNAMIC
dyn_eval e2e.yml:e2e-tls:SOURCE_DYNAMIC
dyn_make e2e.yml:e2e-tls:SOURCE_DYNAMIC
dyn_source e2e.yml:e2e-tls:SOURCE_DYNAMIC
dyn_script e2e.yml:e2e-tls:SOURCE_DYNAMIC@tools/scripts/zz_synthetic_dyn.sh
dyn_procsub e2e.yml:e2e-tls:SOURCE_DYNAMIC
dyn_wrapper e2e.yml:e2e-tls:SOURCE_DYNAMIC
dyn_cmdword e2e.yml:e2e-tls:SOURCE_DYNAMIC
uses_local e2e.yml:e2e-tls:SOURCE_DYNAMIC@.github/workflows/e2e.yml
shell_env_bash e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
shell_pwsh e2e.yml:e2e-tls:SOURCE_DYNAMIC@.github/workflows/e2e.yml
edge_cycle e2e.yml:e2e-tls:SOURCE_CYCLE
edge_escape e2e.yml:e2e-tls:SOURCE_ESCAPE
edge_symlink e2e.yml:e2e-tls:SOURCE_SYMLINK
edge_oversize e2e.yml:e2e-tls:SOURCE_OVERSIZE
edge_missing e2e.yml:e2e-tls:SOURCE_MISSING
edge_depth e2e.yml:e2e-tls:SOURCE_LIMIT
CASES

real_rc=0
real_out="$(python "${GUARD}" "${ROOT}" 2>&1 | tr -d '\r')" || real_rc=$?
if [[ "${real_rc}" -eq 0 && -z "${real_out}" ]]; then
  record PASS "real tree: every workflow credential path is registered or condition-exempt"
else
  record FAIL "real tree: rc=${real_rc}: ${real_out}"
fi

# --inventory is the value-free census: kind@path:line:variable:state.
inv_rc=0
build mint_registered || inv_rc=$?
inv="$(python "${SOURCES}" --inventory "${WORK}/mint_registered" 2>&1 | tr -d '\r')" || inv_rc=$?
if [[ "${inv_rc}" -eq 0 && "${inv}" != *"${SENTINEL}"* ]] \
  && grep -Eq '^e2e\.yml:e2e-tls:auth-response@tools/scripts/zz_synthetic_mint\.sh:[0-9]+:minted:registered$' <<<"${inv}" \
  && grep -Eq '^edge-fake-hook-e2e\.yml:edge-fake-hook-e2e:generated@tools/scripts/edge_fake_hook_e2e\.sh:[0-9]+:AGENTD_NONCE:exempt$' <<<"${inv}" \
  && grep -Eq '^nightly\.yml:soak-test:masked-env@\.github/workflows/nightly\.yml:[0-9]+:CORDUM_LICENSE_PUBLIC_KEY:registered$' <<<"${inv}" \
  && grep -Eq '^e2e\.yml:e2e-tls:masked-env@\.github/workflows/e2e\.yml:[0-9]+:CORDUM_ADMIN_PASSWORD:registered$' <<<"${inv}"; then
  record PASS "inventory lists registered and condition-exempt sources without values"
else
  record FAIL "inventory: rc=${inv_rc}: ${inv}"
fi

echo "SUMMARY: ${PASS} pass, ${FAIL} fail, ${SKIP} skip"
[[ "${FAIL}" -eq 0 ]]
