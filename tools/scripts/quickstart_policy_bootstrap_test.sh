#!/usr/bin/env bash
# Release-compose policy signing bootstrap in quickstart.sh and install.sh
# (PR2-04-FOLLOW-BOOT). Stub docker and curl, as in
# quickstart_env_sharing_test.sh; a real cordumctl from $CORDUMCTL_BIN, else a
# go build of ./cmd/cordumctl.
set -euo pipefail
for name in $(compgen -e); do
  case "${name}" in CORDUM_POLICY_* | SAFETY_POLICY_*) unset "${name}" ;; esac
done

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT
fail() { echo "QUICKSTART POLICY BOOTSTRAP FAIL: $*" >&2; exit 1; }
ok() { echo "  OK  $*"; }
show_fail() { cat "${WORK}/$1.log" >&2; fail "$1: $2"; }
value() { grep -E "^$1=" "$2" 2>/dev/null | head -1 | cut -d= -f2- || true; }

CTL="${CORDUMCTL_BIN:-}"
if [[ -z "${CTL}" ]]; then
  mkdir -p "${WORK}/ctl"
  CTL="${WORK}/ctl/cordumctl$(go env GOEXE)"
  (cd "${REPO_ROOT}" && go build -o "${CTL}" ./cmd/cordumctl)
fi

mkdir -p "${WORK}/bin" "${WORK}/qs-tmp"
QS_TMP="${WORK}/qs-tmp"
cat > "${WORK}/bin/docker" <<'STUB'
#!/usr/bin/env bash
# Logs each compose subcommand and snapshots .env at pull and up.
[[ "${1:-}" == compose ]] || exit 0
shift
while [[ "${1:-}" == -f ]]; do shift 2; done
case "${1:-}" in
  version) ;;
  pull | up) echo "$1" >> .stub-compose.log; cp .env ".stub-env-$1" ;;
  *) echo "${1:-}" >> .stub-compose.log ;;
esac
exit 0
STUB
cat > "${WORK}/bin/curl" <<'STUB'
#!/usr/bin/env bash
if [[ " $* " == *"/api/v1/config"* ]]; then printf '200'; exit 0; fi
printf '{"nats":{"connected":true},"redis":{"ok":true}}\n'
STUB
chmod +x "${WORK}/bin/docker" "${WORK}/bin/curl"

make_tree() {
  mkdir -p "$1/tools/scripts" "$1/certs/ca" "$1/config"
  cp "${REPO_ROOT}/tools/scripts/quickstart.sh" "${REPO_ROOT}/tools/scripts/github_actions_mask_env.sh" "$1/tools/scripts/"
  chmod +x "$1/tools/scripts/quickstart.sh"
  cp "${REPO_ROOT}/config/safety.yaml" "$1/config/safety.yaml"
  printf 'services: {}\n' > "$1/docker-compose.yml"
  printf 'services: {}\n' > "$1/docker-compose.release.yml"
  printf 'test-ca\n' > "$1/certs/ca/ca.crt"
}
seed_env() {
  cat > "${TREE}/.env" <<'ENV'
CORDUM_API_KEY=seeded-api-key-for-policy-bootstrap-test
REDIS_PASSWORD=seeded-redis-password-for-policy-bootstrap-test
ENV
  cp "${TREE}/.env" "${WORK}/env.before"
}
# run_qs LOG [NAME=VALUE...]: quickstart on the release compose in TREE; the
# assignments are settings present only in the shell.
run_qs() {
  local log="${WORK}/$1.log"
  shift
  rm -f "${TREE}/.stub-compose.log" "${TREE}/.stub-env-up"
  (cd "${TREE}" && env "$@" PATH="${WORK}/bin:${PATH}" TMPDIR="${QS_TMP}" CORDUMCTL_BIN="${CTL}" \
    CORDUM_COMPOSE_FILES=docker-compose.release.yml \
    bash ./tools/scripts/quickstart.sh --skip-build --skip-smoke --health-timeout 3) > "${log}" 2>&1
}
# refused LOG ERROR: the run printed ERROR, left .env byte-identical, never
# reached compose up and left TMPDIR empty.
refused() {
  grep -Fq "$2" "${WORK}/$1.log" || show_fail "$1" "no $2 error"
  cmp -s "${WORK}/env.before" "${TREE}/.env" || fail "$1: .env changed"
  if grep -qx up "${TREE}/.stub-compose.log" 2>/dev/null; then fail "$1: compose up ran"; fi
  [[ -z "$(ls -A "${QS_TMP}")" ]] || fail "$1: TMPDIR not empty"
}
verify_default() { # FILE SIG PUB: policy verify with PUB as the only trusted key
  local out
  out="$(MSYS2_ENV_CONV_EXCL=SAFETY_POLICY_PUBLIC_KEY SAFETY_POLICY_PUBLIC_KEY="$3" "${CTL}" policy verify --in "$1" --sig "$2")" || return 1
  [[ "${out}" == *"verified against key_id=default "* ]]
}

TREE="${WORK}/tree"
make_tree "${TREE}"
seed_env
run_qs a || show_fail a "fresh release run exited non-zero"
priv="$(value CORDUM_POLICY_SIGNING_KEY "${TREE}/.env")"
pub="$(value SAFETY_POLICY_PUBLIC_KEY "${TREE}/.env")"
sig="$(value SAFETY_POLICY_SIGNATURE "${TREE}/.env")"
[[ -n "${priv}" && -n "${pub}" && "${sig}" == "'{"*"}'" ]] || fail "a: .env lacks a policy signing setting"
for name in CORDUM_POLICY_SIGNING_KEY SAFETY_POLICY_PUBLIC_KEY SAFETY_POLICY_SIGNATURE; do
  [[ "$(value "${name}" "${TREE}/.stub-env-up")" == "$(value "${name}" "${TREE}/.env")" ]] || fail "a: compose up saw a different ${name}"
done
sig="${sig#\'}"
printf '%s' "${sig%\'}" > "${WORK}/safety.sig"
verify_default "${TREE}/config/safety.yaml" "${WORK}/safety.sig" "${pub}" || fail "a: policy verify rejected the persisted signature"
if grep -Fq -- "${priv}" "${WORK}/a.log"; then fail "a: quickstart output contains the private key"; fi
[[ -z "$(ls -A "${QS_TMP}")" ]] || fail "a: quickstart left files in TMPDIR"
ok "(a) fresh run persists a signature that policy verify accepts under key_id=default; no private key in output; TMPDIR empty"

printf 'scratch policy\n' > "${WORK}/scratch.yaml"
MSYS2_ENV_CONV_EXCL=CORDUM_POLICY_SIGNING_KEY CORDUM_POLICY_SIGNING_KEY="${priv}" "${CTL}" policy sign \
  --in "${WORK}/scratch.yaml" --out "${WORK}/scratch.sig" 2> /dev/null ||
  fail "b: policy sign with the persisted private key failed"
verify_default "${WORK}/scratch.yaml" "${WORK}/scratch.sig" "${pub}" || fail "b: content signed with the private key did not verify"
ok "(b) the persisted private key signs under the key id the persisted public key is trusted under"

cp "${TREE}/.env" "${WORK}/env.before"
run_qs c || show_fail c "re-run exited non-zero"
cmp -s "${WORK}/env.before" "${TREE}/.env" || fail "c: re-run changed .env"
ok "(c) re-run leaves .env byte-identical"

OPERATOR="CORDUM_POLICY_SIGNING_KEY=operator-signing-key
SAFETY_POLICY_PUBLIC_KEY=operator-public-key
SAFETY_POLICY_SIGNATURE=operator-signature"
seed_env
printf '%s\n' "${OPERATOR}" >> "${TREE}/.env"
run_qs d1 || show_fail d1 "run with operator .env settings exited non-zero"
[[ "$(grep -E '^(CORDUM|SAFETY)_POLICY_' "${TREE}/.env")" == "${OPERATOR}" ]] || fail "d1: operator .env lines changed"
seed_env
run_qs d2 CORDUM_POLICY_SIGNING_KEY=operator-signing-key SAFETY_POLICY_PUBLIC_KEY=operator-public-key \
  SAFETY_POLICY_SIGNATURE=operator-signature || show_fail d2 "run with shell-only settings exited non-zero"
if grep -Eq '^(CORDUM|SAFETY)_POLICY_' "${TREE}/.env"; then fail "d2: shell-only settings were written to .env"; fi
ok "(d) operator values in .env stay unchanged; shell-only values are not written to .env"

seed_env
printf 'SAFETY_POLICY_PUBLIC_KEY=operator-public-key\n' >> "${TREE}/.env"
cp "${TREE}/.env" "${WORK}/env.before"
if run_qs e1; then fail "e1: a partial .env setting was accepted"; fi
refused e1 "policy-signing-partial:"
seed_env
if run_qs e2 CORDUM_POLICY_SIGNING_KEY=operator-signing-key; then fail "e2: a partial shell setting was accepted"; fi
refused e2 "policy-signing-partial:"
ok "(e) a partial setting, in .env or in the shell, stops before any write"

seed_env
if run_qs f CORDUM_POLICY_SIGNING_KEY_ID=prod-1; then fail "f: a non-default key id was accepted"; fi
refused f "policy-signing-key-id:"
ok "(f) a non-default CORDUM_POLICY_SIGNING_KEY_ID stops before any write"

INSTALL_TREE="${WORK}/install"
make_tree "${INSTALL_TREE}"
mkdir "${INSTALL_TREE}/.git"
run_install() {
  rm -f "${INSTALL_TREE}/.stub-compose.log"
  (cd "${WORK}" && env -u GITHUB_ACTIONS PATH="${WORK}/bin:${PATH}" TMPDIR="${QS_TMP}" CORDUMCTL_BIN="${CTL}" \
    USE_RELEASE_IMAGES=1 DEST_DIR="${INSTALL_TREE}" bash "${REPO_ROOT}/tools/scripts/install.sh") > "${WORK}/$1.log" 2>&1
}
run_install g1 || show_fail g1 "first release install exited non-zero"
if grep -qx pull "${INSTALL_TREE}/.stub-compose.log"; then fail "g1: first install pulled before .env existed"; fi
for name in CORDUM_POLICY_SIGNING_KEY SAFETY_POLICY_PUBLIC_KEY SAFETY_POLICY_SIGNATURE; do
  [[ -n "$(value "${name}" "${INSTALL_TREE}/.stub-env-up")" ]] || fail "g1: compose up lacked ${name}"
done
run_install g2 || show_fail g2 "release re-install exited non-zero"
[[ "$(grep -x -e pull -e up "${INSTALL_TREE}/.stub-compose.log" | tr '\n' ' ')" == "pull up " ]] ||
  fail "g2: re-install did not pull before up"
ok "(g) install.sh: a first release install skips pull and starts with all three; a re-install pulls before up"
echo "PASS: quickstart policy signing bootstrap"
