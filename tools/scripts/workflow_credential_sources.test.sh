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
record() { case "$1" in PASS) PASS=$((PASS + 1)) ;; SKIP) SKIP=$((SKIP + 1)) ;; *) FAIL=$((FAIL + 1)) ;; esac; printf '%s %s\n' "$1" "$2"; }

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
import os, pathlib, sys  # noqa: E401
root, case, sentinel = pathlib.Path(sys.argv[1]), sys.argv[2], sys.argv[3]
PH = "credential-unavailable-for-redaction"
E2E, EDGE = ".github/workflows/e2e.yml", ".github/workflows/edge-fake-hook-e2e.yml"
CAPTURE = "      - name: Capture compose logs on failure\n"
LINK = (".github/workflows/docs-linkcheck.yml", "      - name: Run lychee (offline, gating)\n")
API = "          CORDUM_API_KEY: ${{ env.CORDUM_API_KEY || '" + PH + "' }}\n"
REG, STRICT = 'gha_mask_value "${minted}"', "set -euo pipefail\n"
SAFE = '[[ -n "${minted}" ]] || exit 1\n' + REG
MINT_SH, RUN_MINT, INC = "tools/scripts/zz_synthetic_mint.sh", "bash tools/scripts/zz_synthetic_mint.sh", "tools/scripts/zz_synthetic_mint.inc"
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
    "mint_unregistered": ("", ""), "mint_registered": (SAFE, ""), "mint_late": ("", REG), "mint_echo_n": ('echo -n "${minted}"\n' + SAFE, ""),  # -n is a test only in [, [[ or test
    "mint_prefix": (REG + '\necho "synthetic prefix ${minted:0:6}"', ""), "mint_ignored": (REG + " || true", ""),
    "mint_errexit_off": ("set +e\n" + SAFE + "\nset -e", ""), "mint_xtrace": ("set -x\n" + SAFE, ""),
}
OPTS = {  # mint() keywords, before defaulting to SAFE: a set applies in its own scope; one that may run later or again applies throughout its subshell
    "mint_errexit_func": dict(head="main() {\nset -euo pipefail\n", tail="}\nmain\n"), "errexit_window": dict(head=STRICT + "probe() {\nset +e\nfalse\nset -e\n}\nprobe\n"),
    "errexit_scoped": dict(head="set -uo pipefail\nunused() { set -e; }\n( set -e )\nif false; then set -e; fi\n"), "errexit_leak": dict(head=STRICT + "relax() { set +e; }\nrelax\n"),
    "errexit_subshell": dict(head="set -uo pipefail\n(\nset -e\nrelax() { set +e; }\nrelax\n", tail=")\n"), "subshell_confined": dict(head=STRICT + "( relax() { set +e; }; relax; set -x )\nprobe=\"$(set +e; set -x; printf syn)\"\n"),
    "trace_unused": dict(head=STRICT + "set -x\nunused() { set +x; }\n"), "trace_scoped": dict(head=STRICT + "set -x\n( set +x )\nif false; then set +x; fi\n"),
    "trace_call_order": dict(head=STRICT + "main() {\n", tail="}\nset -x\nmain\n"), "trace_loop": dict(head=STRICT + "for round in 1 2; do\n", tail="set -x\ndone\n"),
    "trace_skipped": dict(head=STRICT + "set -x\nfalse && set +x\n"), "trace_piped": dict(head=STRICT + "set -x\nset +x | cat\nset +x &\nwait\n"),  # skipped, or run in another shell
    "errexit_skipped": dict(head="set -uo pipefail\nfalse && set -e\n"), "errexit_piped": dict(head="set -uo pipefail\nset -e | cat\nset -e &\nwait\n"),
    "errexit_tested": dict(head=STRICT + "main() {\n", tail="}\nmain || true\n"), "errexit_tested_body": dict(head="main() {\nset -euo pipefail\n", tail="}\nif true && main; then :; fi\n"), "errexit_tested_nested": dict(head=STRICT + "main() {\n", tail="}\nouter() { main; }\n! outer\n"),  # a tested status: bash ignores errexit in the call
    "errexit_group_tested": dict(head=STRICT + "main() {\n", tail="}\n{ main; } || true\n"), "errexit_block_tested": dict(head=STRICT + "{\n", tail="} || true\n"), "errexit_if_tested": dict(head=STRICT + "if true; then\n", tail="fi || true\n"),  # bash ignores errexit throughout a compound command
    "errexit_paren_tested": dict(head=STRICT + "(\n", tail=") && true\n"), "errexit_loop_tested": dict(head=STRICT + "for round in 1; do\n", tail="done || true\n"), "errexit_negated": dict(head=STRICT + "main() {\n", tail="}\n! { main; }\n"),  # that is negated, a condition, or not last in an && or || list
    "errexit_condition": dict(head=STRICT + "main() {\n", tail="}\nif { main; }; then :; fi\n"), "errexit_then_tested": dict(head=STRICT + "main() {\n", tail="}\nif true; then { main; } || true; fi\n"), "errexit_group_last": dict(head=STRICT + "main() {\n", tail="}\ntrue && { main; }\n"),  # last: errexit holds
    "errexit_pipe_tested": dict(head=STRICT + "main() {\n", tail="}\nmain | cat || true\n"), "errexit_group_piped": dict(head=STRICT + "main() {\n", tail="}\n{ main; } | cat || true\n"), "errexit_negated_if": dict(head=STRICT + "! if true; then\n", tail="fi\n"), "errexit_pipe_plain": dict(head=STRICT + "main() {\n", tail="}\nmain | cat\n"),  # the whole pipeline
    "errexit_condition_list": dict(head=STRICT + "main() {\n", tail="}\nif true; main; true; then :; fi\n"), "errexit_condition_group": dict(head=STRICT + "main() {\n", tail="}\nif true && { main; }; then :; fi\n"), "errexit_then_group": dict(head=STRICT + "main() {\n", tail="}\nif true; then { main; }; fi\n"),  # any command of a condition list
    "errexit_cold": dict(head=STRICT + "main() {\n", tail="}\nset +e\nmain\nset -e\n"), "errexit_last": dict(head=STRICT + "main() {\n", tail="}\ntrue && main\n"), "mask_captured_func": dict(before=SAFE + " || exit 1", head=STRICT + "main() {\n", tail='}\nout="$(main)"\n'),
    "return_global": dict(before=SAFE + " || return 1", head=STRICT + "main() {\n", tail="}\nmain || true\n"),  # the caller ignores the failure; minted outlives it
    "return_local": dict(before=SAFE + " || return 1", head=STRICT + "main() {\nlocal minted\n", tail="}\nmain || true\n"), "exit_block": dict(before=SAFE + ' || { echo "synthetic mask failure" >&2; exit 1; }'),
    "return_unset": dict(before=SAFE + " || { unset minted; return 1; }", head=STRICT + "main() {\n", tail="}\nmain || true\n"),  # a local or a clear counts only if it runs first:
    "return_and_local": dict(before=SAFE + " || return 1", head=STRICT + "main() {\nfalse && local minted\n", tail="}\nmain || true\n"),
    "return_and_unset": dict(before=SAFE + " || { false && unset minted; return 1; }", head=STRICT + "main() {\n", tail="}\nmain || true\n"),  # so does the exit or return itself:
    "exit_skipped": dict(before=SAFE + " || { true || exit 1; :; }"), "exit_captured": dict(before=SAFE + ' || { echo "$(exit 1)"; }'), "return_skipped": dict(before=SAFE + " || { true || return 1; :; }", head=STRICT + "main() {\nlocal minted\n", tail="}\nmain\n"),
    "return_nested": dict(before=SAFE + " || { if true; then return 1; fi; unset minted; return 1; }", head=STRICT + "main() {\n", tail="}\nmain || true\n")}
OPTS.update({  # a redirection before the command name is not the name: bash runs main as a tested call (errexit ignored); an untested one holds errexit
    "errexit_redirect_tested": dict(head=STRICT + "main() {\n", tail="}\n</dev/null main || true\n"), "errexit_redirect_assign": dict(head=STRICT + "main() {\n", tail="}\nFOO=1 </dev/null main || true\n"),
    "errexit_redirect_spaced": dict(head=STRICT + "main() {\n", tail="}\n< /dev/null main || true\n"), "errexit_redirect_plain": dict(head=STRICT + "main() {\n", tail="}\n</dev/null main\n")})
GEN = {
    "gen_openssl": "$(openssl rand -hex 16)", "gen_uuidgen": "$(uuidgen)",
    "gen_urandom": "$(head -c 16 /dev/urandom | od -An -tx1 | tr -d ' \\n')", "gen_random": "syn-${RANDOM}${RANDOM}",
    "gen_date": "syn-$(date +%s%N)", "gen_python": "$(python3 -c 'import secrets; print(secrets.token_urlsafe(16))')",
    "gen_quoted_paren": "$(printf '(' \"(\")$(openssl rand -hex 8)", "gen_nested": '$(curl -fsS -d "$(jq -cn --arg n "syn-$(date +%s)" "{}")" https://127.0.0.1:9/)',
}
CAPTURED = 'out="$(synthetic_key="$(openssl rand -hex 16)"; @MASK@ "${synthetic_key}" || exit 1; printf %s "${synthetic_key}")"'  # a $(...) captures the mask; pg_register_secret writes to PG_MASK_FD
SUBST = {"errexit_substitution": 'out="$(synthetic_key="$(openssl rand -hex 16)"; gha_mask_value "${synthetic_key}"; printf %s "${synthetic_key}")"',  # bash clears errexit in $(...)
         "trace_substitution": CAPTURED.replace("$(synthetic", "$(set -x; synthetic", 1).replace("@MASK@", "pg_register_secret"), "mask_captured": CAPTURED.replace("@MASK@", "gha_mask_value"),
         "mask_fd_captured": CAPTURED.replace("@MASK@", "pg_register_secret"), "mask_env_captured": 'synthetic_key="$(openssl rand -hex 16)"\ngha_mask_value "${synthetic_key}" || exit 1\nout="$(gha_mask_env SYNTHETIC_KEY "${synthetic_key}")"'}
DYNAMIC = {
    "dyn_var": 'bash "${SYNTHETIC_DIR}/zz_synthetic_mint.sh"', "dyn_c": 'bash -c "${SYNTHETIC_CMD}"', "dyn_c_prefixed": "bash -c 'SYNTHETIC_MODE=1 \"${SYNTHETIC_SHELL}\" synthetic_arg'",
    "dyn_heredoc": "bash <<'SYNTHETIC'\ntrue\nSYNTHETIC", "dyn_pipe": "curl -fsS https://127.0.0.1:9/x | bash",
    "dyn_eval": 'eval "${SYNTHETIC_CMD}"', "dyn_make": 'make "${SYNTHETIC_TARGET}"',
    "dyn_source": 'source "${SYNTHETIC_DIR}/zz_synthetic_helper.sh"', "dyn_procsub": "source <(cat " + MINT_SH + ")",
    "dyn_wrapper": "timeout -s KILL 10m " + RUN_MINT, "dyn_cmdword": '"${SYNTHETIC_SHELL}" ' + MINT_SH, "dyn_stdin_s": "bash -s synthetic_arg <<'SYNTHETIC'\ntrue\nSYNTHETIC",
}
SHELLS, PROG = {"shell_env_bash": "/usr/bin/env bash {0}", "shell_pwsh": "pwsh"}, ' \'synthetic_key="$(openssl rand -hex 16)"; curl -fsS -u "synthetic:${synthetic_key}" https://127.0.0.1:9/ >/dev/null\''
INLINE = {"shell_c_stdin": "bash -s -c" + PROG, "shell_c_option": "bash -c -e" + PROG, "shell_c_dashdash": "bash -c --" + PROG, "inline_redirect_first": "bash -c '> /dev/null " + RUN_MINT + "'", "inline_redirect": "bash -c 'exec >\"${SYNTHETIC_LOG:-/dev/null}\" 2>&1; true'"}  # bash runs the -c program after any later option, over -s; a redirection is not its command word
INLINE.update({"shell_c_rcfile": "bash --rcfile /dev/null -c" + PROG, "shell_c_initfile": "bash --init-file /dev/null -c" + PROG, "shell_c_norc": "bash --norc -c" + PROG})  # --rcfile and --init-file take a file operand, never the program; --norc takes none
INLINE.update({"shell_ic_rcfile": "bash --rcfile " + MINT_SH + " -ic" + PROG, "shell_i_initfile": "bash --init-file " + MINT_SH + " -i -c" + PROG, "shell_i_rcfile_script": "bash --rcfile " + MINT_SH + " -i tools/scripts/github_actions_mask_env.sh", "shell_c_rcfile_unread": "bash --rcfile " + MINT_SH + " -c true", "shell_i_rcfile_shadowed": "SYNTH_RC=" + MINT_SH + " bash -c 'SYNTH_RC=tools/scripts/github_actions_mask_env.sh bash --rcfile ${SYNTH_RC} -ic true'", "shell_i_rcfile_unknown": "SYNTH_RC=tools/scripts/github_actions_mask_env.sh bash --rcfile ${SYNTH_RC} -ic true"})  # -i: bash runs the rcfile first, then the program or script; without -i it never reads it; the caller, not the prefix, names it
RCTRACE = {"shell_i_rcfile_traced": "set -x\n", "shell_i_rcfile_untraced": "set -x\nset +x\n"}  # bash runs the -i rcfile in the shell that then runs the program: tracing the rcfile leaves on traces it
X, XE, XG = "tools/scripts/zz_synthetic_set.sh", "tools/scripts/zz_synthetic_set_e.sh", "tools/scripts/zz_synthetic_outer.sh"
SOURCED = {  # (run, {sourced file: text}, mint() keywords[, workflow, anchor]): source and . leave options in the caller, as a set at that line would; a subshell keeps them
    "sourced_trace": ("source " + X + "\n" + RUN_MINT, {X: "set -x\n"}, {}), "sourced_dot_trace": (". " + X + "\n" + RUN_MINT, {X: "set -x\n"}, {}),
    "sourced_nested_trace": ("source " + XG + "\n" + RUN_MINT, {XG: "source " + X + "\n", X: "set -x\n"}, {}), "sourced_function_trace": ("source " + X + "\n" + RUN_MINT, {X: "trace_on() { set -x; }\n"}, {}),
    "sourced_in_function": ("load() { source " + X + "; }\nload\n" + RUN_MINT, {X: "set -x\n"}, {}), "sourced_unparsable": ("source " + X + "\n" + RUN_MINT, {X: "echo 'unterminated\n"}, {}),
    "sourced_own_trace": (RUN_MINT, {X: "set -x\n"}, {"head": STRICT + "source " + X + "\n"}), "sourced_own_errexit": (RUN_MINT, {XE: "set +e\n"}, {"head": STRICT + "source " + XE + "\n"}),
    "rc_sources_trace": ("bash --rcfile " + XG + " -ic '" + RUN_MINT + "'", {XG: "source " + X + "\n", X: "set -x\n"}, {}), "rc_trace_script": ("bash --rcfile " + X + " -ic '" + RUN_MINT + "'", {X: "set -x\n"}, {}),
    "sourced_trace_off": ("source " + X + "\n" + RUN_MINT, {X: "set -x\nset +x\n"}, {}), "sourced_subshell": ("( source " + X + " )\n" + RUN_MINT, {X: "set -x\n"}, {}),
    "sourced_after_registration": (RUN_MINT, {X: "set -x\n"}, {"before": SAFE + "\nsource " + X}), "sourced_errexit_restored": (RUN_MINT, {XE: "set +e\n"}, {"head": STRICT + "source " + XE + "\nset -e\n"}),
    "sourced_errexit_window": (RUN_MINT, {XE: "probe() {\nset +e\nfalse\nset -e\n}\n"}, {"head": STRICT + "source " + XE + "\n"}), "sourced_caller_untraces": ("source " + X + "\nset +x\n" + RUN_MINT, {X: "set -x\n"}, {}, *LINK)}
TRACE = {  # (shell, run[, job]): xtrace on at a fail-closed registration; run-text tracing in a job the guard leaves open
    "trace_shell": ("bash -x {0}", "source " + MINT_SH), "trace_env": ("", "SHELLOPTS=xtrace " + RUN_MINT),
    "trace_bash_x": ("", "bash -x " + MINT_SH, *LINK), "trace_set_x": ("", "set -x\nsource " + MINT_SH, *LINK),
    "trace_off": ("", "set -x\nset +x\nsource " + MINT_SH, *LINK), "trace_bang": ("", "./" + MINT_SH)}
NOEXT = {  # (#! line, run, mode) for a credential file without a shell suffix
    "noext_source": ("", "source " + INC, 0o644), "noext_exec": ("", "./" + INC, 0o755), "noext_data": ("", "./" + INC, 0o644),
    "noext_python": ("#!/usr/bin/env python3\n", "./" + INC, 0o755)}
def edit(rel, old, new):
    text = (root / rel).read_bytes().decode("utf-8")
    if text.count(old) != 1: raise SystemExit(case + ": fixture anchor is not unique in " + rel)
    (root / rel).write_bytes(text.replace(old, new).encode("utf-8"))
def write(rel, text):
    (root / rel).write_bytes(text.replace("@SENTINEL@", sentinel).encode("utf-8"))
def script(name, body):
    write("tools/scripts/" + name, "#!/usr/bin/env bash\nset -euo pipefail\n" + body + "\n")
def mint(before="", after="", rel=MINT_SH, head=STRICT, tail="", bang="#!/usr/bin/env bash\n"):
    body = BODY.replace("@BEFORE@", before).replace("@AFTER@", after)
    write(rel, bang + head + "source tools/scripts/github_actions_mask_env.sh\n" + body + "\n" + tail)
def step(body, wf=E2E, anchor=CAPTURE, shell=""):
    run = "".join("          " + line + "\n" for line in body.replace("@SENTINEL@", sentinel).split("\n"))
    edit(wf, anchor, "      - name: Synthetic fixture\n" + ("        shell: " + shell + "\n" if shell else "") + "        run: |\n" + run + "\n" + anchor)
if case == "bind_nightly":
    anchor = "      id: redact_full_gate_results\n"
    edit(".github/workflows/nightly.yml", anchor, anchor + "      env:\n        CORDUM_API_KEY: ${{ secrets.SYNTHETIC_FOREIGN }}\n")
elif case in BINDS:
    edit(E2E, API, BINDS[case])
elif case in MINTS or case in OPTS:
    mint(*MINTS[case]) if case in MINTS else mint(**{"before": SAFE, **OPTS[case]})
    step(RUN_MINT)
elif case in ("mint_make", "make_errexit", "make_line_errexit", "make_oneshell"):  # make runs each logical recipe line in its own sh -c, errexit off; .ONESHELL runs a recipe in one
    mint()
    recipe = RUN_MINT if case == "mint_make" else ("" if case == "make_errexit" else "set -e\n\t") + 'synthetic_key="$$(openssl rand -hex 16)"; gha_mask_value "$$synthetic_key"; echo "$$synthetic_key"'
    makefile = root / "Makefile"
    makefile.write_bytes(makefile.read_bytes() + (("\n.ONESHELL:" if case == "make_oneshell" else "") + "\nzz-synthetic-mint:\n\t" + recipe + "\n").encode("utf-8"))
    step("make zz-synthetic-mint")
elif case == "mint_untracked_job":
    mint()
    step(RUN_MINT, *LINK)
elif case == "mint_inline":
    step(BODY.replace("@BEFORE@", "").replace("@AFTER@", ""))
elif case in GEN or case in SUBST:
    script("zz_synthetic_gen.sh", SUBST.get(case) or 'synthetic_password="' + GEN[case] + '"\ncurl -fsS -u "synthetic:${synthetic_password}" https://127.0.0.1:9/ >/dev/null')
    step("bash tools/scripts/zz_synthetic_gen.sh")
elif case == "nonce_bypass_removed":
    edit(EDGE, '          CORDUM_EDGE_E2E_BYPASS_HOOK: "1"\n', "")
elif case == "nonce_bypass_overridden":
    edit(EDGE, "run: bash tools/scripts/edge_fake_hook_e2e.sh", "run: CORDUM_EDGE_E2E_BYPASS_HOOK=0 bash tools/scripts/edge_fake_hook_e2e.sh")
elif case == "quickstart_unseeded":
    step("bash tools/scripts/quickstart.sh --skip-build", *LINK)
elif case in SOURCED:
    mint(**{"before": SAFE, **SOURCED[case][2]}); [write(rel, body) for rel, body in SOURCED[case][1].items()]  # noqa: E702
    step(SOURCED[case][0], *SOURCED[case][3:])
elif case in RCTRACE:
    write("tools/scripts/zz_synthetic_trace_rc.sh", RCTRACE[case])
    step("bash --rcfile tools/scripts/zz_synthetic_trace_rc.sh -ic" + PROG.replace('"; curl', '"; gha_mask_value "${synthetic_key}" || exit 1; curl', 1))
elif case in DYNAMIC or case in INLINE:
    mint()
    step(DYNAMIC.get(case) or INLINE[case])
elif case == "dyn_script":
    script("zz_synthetic_dyn.sh", 'source "${SYNTHETIC_DIR}/zz_synthetic_helper.sh"')
    step("bash tools/scripts/zz_synthetic_dyn.sh")
elif case == "uses_local":
    edit(E2E, CAPTURE, "      - name: Synthetic local action\n        uses: ./.github/actions/zz-synthetic\n" + CAPTURE)
elif case in SHELLS:
    mint()
    step(RUN_MINT, shell=SHELLS[case])
elif case in TRACE:  # trace_bang: an exec'd script whose #! line turns xtrace on
    mint(SAFE, bang="#!/bin/bash -x\n" if case == "trace_bang" else "#!/usr/bin/env bash\n")
    step(TRACE[case][1], *TRACE[case][2:], shell=TRACE[case][0])
elif case in NOEXT:  # source and bash read any text as shell; exec only a sh #!, or an executable without #! (ENOEXEC)
    write(INC, NOEXT[case][0] + BODY.replace("@BEFORE@", "").replace("@AFTER@", "") + "\n")
    os.chmod(root / INC, NOEXT[case][2])
    if case == "noext_exec" and not os.stat(root / INC).st_mode & 0o100: raise SystemExit(3)
    step(NOEXT[case][1])
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
  if [[ "${built}" -ne 0 ]]; then if [[ "${built}" -eq 3 ]]; then record SKIP "${name}: platform cannot create this fixture"; else record FAIL "${name}: fixture build failed"; fi; return 0; fi
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
# Each CASES row names one or more cases, then the code every one of them must report (or clean).
while read -r -a row; do
  for name in "${row[@]:0:${#row[@]}-1}"; do check "${name}" "${row[-1]}"; done
done <<'CASES'
base clean
bind_spacing clean
mint_registered clean
mint_errexit_func clean
errexit_window subshell_confined errexit_last errexit_group_last errexit_pipe_plain errexit_then_group clean
return_local return_unset exit_block clean
trace_off clean
noext_data clean
noext_python clean
make_oneshell clean
inline_redirect clean
bind_literal_space bind_cross e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_const e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_foreign e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_drop e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_rename e2e.yml:e2e-tls:REDACTION_VALUE_BINDING
bind_nightly nightly.yml:full-production-gate:REDACTION_VALUE_BINDING
mint_unregistered e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
mint_late mint_echo_n mint_xtrace e2e.yml:e2e-tls:CREDENTIAL_USE_BEFORE_REGISTRATION@tools/scripts/zz_synthetic_mint.sh
mint_prefix e2e.yml:e2e-tls:CREDENTIAL_PREFIX_PRINT@tools/scripts/zz_synthetic_mint.sh
mint_ignored e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
mint_errexit_off e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
errexit_scoped e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
errexit_leak errexit_subshell errexit_skipped errexit_piped errexit_tested errexit_tested_body errexit_tested_nested errexit_cold mask_captured_func errexit_group_tested errexit_block_tested errexit_if_tested errexit_paren_tested errexit_loop_tested errexit_negated errexit_condition errexit_then_tested errexit_pipe_tested errexit_group_piped errexit_negated_if errexit_condition_list errexit_condition_group e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
return_global return_and_local return_and_unset return_nested return_skipped exit_skipped exit_captured e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
trace_unused trace_skipped trace_piped trace_scoped trace_call_order trace_loop e2e.yml:e2e-tls:CREDENTIAL_USE_BEFORE_REGISTRATION@tools/scripts/zz_synthetic_mint.sh
trace_substitution e2e.yml:e2e-tls:CREDENTIAL_USE_BEFORE_REGISTRATION@tools/scripts/zz_synthetic_gen.sh
trace_shell trace_env e2e.yml:e2e-tls:CREDENTIAL_USE_BEFORE_REGISTRATION@tools/scripts/zz_synthetic_mint.sh
trace_bash_x trace_set_x docs-linkcheck.yml:link-check:CREDENTIAL_USE_BEFORE_REGISTRATION@tools/scripts/zz_synthetic_mint.sh
trace_bang e2e.yml:e2e-tls:CREDENTIAL_USE_BEFORE_REGISTRATION@tools/scripts/zz_synthetic_mint.sh
noext_source noext_exec e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.inc
mint_make e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
make_errexit make_line_errexit e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@Makefile
inline_redirect_first e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
mint_untracked_job docs-linkcheck.yml:link-check:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
mint_inline shell_c_stdin shell_c_option shell_c_dashdash e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED
errexit_redirect_tested errexit_redirect_assign errexit_redirect_spaced e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
shell_c_rcfile shell_c_initfile shell_c_norc e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED
errexit_redirect_plain clean
shell_ic_rcfile shell_i_initfile shell_i_rcfile_script shell_i_rcfile_shadowed e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
shell_ic_rcfile shell_i_initfile e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@.github/workflows/e2e.yml
shell_c_rcfile_unread clean
shell_i_rcfile_unknown e2e.yml:e2e-tls:SOURCE_DYNAMIC@.github/workflows/e2e.yml
shell_i_rcfile_untraced clean
shell_i_rcfile_traced e2e.yml:e2e-tls:CREDENTIAL_USE_BEFORE_REGISTRATION@.github/workflows/e2e.yml
sourced_trace sourced_dot_trace sourced_nested_trace sourced_function_trace sourced_in_function sourced_unparsable sourced_own_trace rc_sources_trace rc_trace_script e2e.yml:e2e-tls:CREDENTIAL_USE_BEFORE_REGISTRATION@tools/scripts/zz_synthetic_mint.sh
sourced_own_errexit e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_mint.sh
sourced_trace_off sourced_subshell sourced_after_registration sourced_errexit_restored sourced_errexit_window sourced_caller_untraces clean
gen_openssl gen_uuidgen gen_urandom gen_random gen_date gen_python gen_quoted_paren errexit_substitution mask_captured mask_env_captured e2e.yml:e2e-tls:CREDENTIAL_UNREGISTERED@tools/scripts/zz_synthetic_gen.sh
gen_nested mask_fd_captured clean
nonce_bypass_removed edge-fake-hook-e2e.yml:edge-fake-hook-e2e:CREDENTIAL_UNREGISTERED@tools/scripts/edge_fake_hook_e2e.sh
nonce_bypass_overridden edge-fake-hook-e2e.yml:edge-fake-hook-e2e:CREDENTIAL_UNREGISTERED@tools/scripts/edge_fake_hook_e2e.sh
quickstart_unseeded docs-linkcheck.yml:link-check:CREDENTIAL_UNREGISTERED@tools/scripts/quickstart.sh
dyn_var dyn_c dyn_c_prefixed dyn_heredoc dyn_pipe dyn_eval dyn_make dyn_source dyn_procsub dyn_wrapper dyn_cmdword dyn_stdin_s e2e.yml:e2e-tls:SOURCE_DYNAMIC
dyn_script e2e.yml:e2e-tls:SOURCE_DYNAMIC@tools/scripts/zz_synthetic_dyn.sh
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
if [[ "${real_rc}" -eq 0 && -z "${real_out}" ]]; then record PASS "real tree: every workflow credential path is registered or condition-exempt"; else record FAIL "real tree: rc=${real_rc}: ${real_out}"; fi

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

if python - "${WORK}" "${SOURCES}" <<'PY'
import pathlib, runpy, sys; ns = runpy.run_path(sys.argv[2]); root = pathlib.Path(sys.argv[1]) / "source-fanout"; root.mkdir(); [(root / f"n{i}.sh").write_text(f"source n{i + 1}.sh\n" * 5 if i < 5 else "true\n") for i in range(6)]; scan = ns["Scan"](root); calls = []; effect = getattr(scan, "effect", None); setattr(scan, "effect", lambda *args: (calls.append(None), effect(*args))[1]) if effect else None; scan.run_job("probe.yml", "job", {"steps": [{"run": "source n0.sh"}]}, {}, {}, True); assert len(calls) <= 60 and not scan.issues, (len(calls), scan.issues)
PY
then record PASS "repeated sources reuse option effects"; else record FAIL "repeated sources reuse option effects"; fi
if python - "${WORK}" "${SOURCES}" <<'PY'
import pathlib, runpy, sys, time; ns, work, mint = runpy.run_path(sys.argv[2]), pathlib.Path(sys.argv[1]), 'minted="$(printf %s "${resp}" | jq -r .secret)"\ngha_mask_value "${minted}"\n'
def tree(name: str, files: dict) -> pathlib.Path: root = work / name; root.mkdir(); [(root / rel).write_text(text) for rel, text in files.items()]; return root  # noqa: E702
def scan(root: pathlib.Path, run: str) -> tuple: s, calls = ns["Scan"](root), []; effect = getattr(s, "effect", None); setattr(s, "effect", lambda *a: (calls.append(None), effect(*a))[1]) if effect else None; s.run_job("probe.yml", "job", {"steps": [{"run": run}]}, {}, {}, True); return {i[2].split("@")[0] for i in s.issues}, sorted(s.issues), len(calls)  # noqa: E702
pairs, layers, start = tree("all-pairs", {f"n{i}.sh": "".join(f"source n{j}.sh\n" for j in range(i + 1, 15)) or "true\n" for i in range(15)}), tree("layered", {f"l{d}_{w}.sh": "".join(f"source l{d + 1}_{v}.sh\n" for v in range(4)) if d < 5 else "true\n" for d in range(6) for w in range(4 if d else 1)}), time.monotonic(); first, again = scan(pairs, "source n0.sh\n" + mint), scan(pairs, "source n0.sh\n" + mint)  # noqa: E702
assert {"SOURCE_LIMIT", "CREDENTIAL_UNREGISTERED"} <= first[0] and first[1] == again[1] and first[2] <= 3 * ns["MAX_DRY"] and "SOURCE_LIMIT" in scan(layers, "source l0_0.sh")[0] and time.monotonic() - start < 60, (first[0], first[2])  # layered: depth 6 and 21 files stay under MAX_DEPTH and MAX_VISITS, so only the dry budget reports
PY
then record PASS "dry analysis budget fails closed"; else record FAIL "dry analysis budget fails closed"; fi
if python - "${WORK}" "${SOURCES}" <<'PY'
import pathlib, runpy, sys; ns, root = runpy.run_path(sys.argv[2]), pathlib.Path(sys.argv[1]) / "layered-jobs"; root.mkdir(); [(root / f"l{d}_{w}.sh").write_text("".join(f"source l{d + 1}_{v}.sh\n" for v in range(4)) if d < 5 else "true\n") for d in range(6) for w in range(4 if d else 1)]; scan = ns["Scan"](root); [scan.run_job("probe.yml", job, {"steps": [{"run": run}]}, {}, {}, True) for job, run in (("spent", "source l0_0.sh"), ("fresh", 'source l5_0.sh\nminted="$(printf %s "${resp}" | jq -r .secret)"\ngha_mask_value "${minted}"\n'))]; assert any(i[1] == "spent" for i in scan.issues) and not [i for i in scan.issues if i[1] == "fresh"], sorted(scan.issues)  # noqa: E702
PY
then record PASS "dry analysis budget resets per job"; else record FAIL "dry analysis budget resets per job"; fi
if python - "${WORK}" "${SOURCES}" <<'PY'
import pathlib, runpy, sys; ns, root = runpy.run_path(sys.argv[2]), pathlib.Path(sys.argv[1]) / "rc-refused"; root.mkdir(); [(root / f).write_text(t) for f, t in {"plain.rc": "set -x\n", "bang.rc": "#!/bin/bash\nset -x\n", "mint.sh": 'set -euo pipefail\nminted="$(printf %s "${resp}" | jq -r .secret)"\n[[ -n "${minted}" ]] || exit 1\ngha_mask_value "${minted}"\n'}.items()]; scans = [(ns["Scan"](root), f"bash {flag} {rc} -ic 'bash mint.sh'", cred) for flag in ("--rcfile", "--init-file") for rc in ("/plain.rc", "/bang.rc", "../rc-refused/bang.rc") for cred in (True, False)]; [s.run_job("probe.yml", "job", {"steps": [{"run": run}]}, {}, {}, cred) for s, run, cred in scans]; assert all(sorted(i[2].split("@")[0] for i in s.issues) == ["SOURCE_ESCAPE"] * cred for s, _, cred in scans), [(run, cred, sorted(s.issues)) for s, run, cred in scans]  # an absolute or escaping rcfile is refused unread: its own edge reports SOURCE_ESCAPE and it leaves no options
PY
then record PASS "absolute or escaping rcfile is refused unread"; else record FAIL "absolute or escaping rcfile is refused unread"; fi
echo "SUMMARY: ${PASS} pass, ${FAIL} fail, ${SKIP} skip"
[[ "${FAIL}" -eq 0 ]]
