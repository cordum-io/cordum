#!/usr/bin/env bash
set -euo pipefail

# ============================================================================
# EDGE-151 — binary integrity pre-activation gate
# ----------------------------------------------------------------------------
# Production release pubkey fingerprint pinned here at script-edit-time.
# Empty means production signing has not yet been provisioned, in which case
# only `--dev-allow-unsigned` is accepted (with a TEST-ONLY-* key under
# tools/test-keys/). When the production GPG key is provisioned the value
# below MUST be filled in AND the matching public key committed at
# tools/keys/cordum-release.pub.asc. See docs/security/binary-signing.md
# for the threat model and rotation procedure.
# ============================================================================
CORDUM_PROD_FINGERPRINT_PIN="${CORDUM_RELEASE_FINGERPRINT:-}"

# Audit-log context — populated as the verify flow progresses; consumed by
# emit_audit so each event line carries the signing scheme + signer fingerprint
# alongside the per-binary fields. Empty values are emitted as "" — schema
# remains stable for downstream SIEM mappings per docs/security/binary-signing.md §8.
CORDUM_AUDIT_SIG_SCHEME=""
CORDUM_AUDIT_FINGERPRINT=""

emit_audit() {
  local event="$1" reason="${2:-}" hash="${3:-}" rel="${4:-}" exit_code="${5:-0}"
  # No secrets, no full paths — `rel` is already a manifest-relative basename
  # because the manifest itself never carries absolute paths (rejected up
  # front by reject_path). Reason strings are controlled BINARY-VERIFY-FAIL
  # text from this script, so no JSON-escaping pass is needed.
  printf '{"event":"%s","hash":"%s","path":"%s","sig_scheme":"%s","fingerprint":"%s","reason":"%s","exit_code":%d}\n' \
    "$event" "$hash" "$rel" "$CORDUM_AUDIT_SIG_SCHEME" "$CORDUM_AUDIT_FINGERPRINT" "$reason" "$exit_code" >&2
}

# emit_floor_audit emits a `binary-floor-advance` or `binary-floor-rollback`
# JSON-line on stderr. Schema is intentionally additive over the
# binary-verify event so downstream SIEMs can pin to the same field set
# (event, sig_scheme, fingerprint, exit_code) and add three new fields
# (from, to, operator, reason). EDGE-151-DOWNGRADE.
emit_floor_audit() {
  local event="$1" from="$2" to="$3" operator="${4:-unknown}" reason="${5:-}"
  printf '{"event":"%s","from":"%s","to":"%s","sig_scheme":"%s","fingerprint":"%s","operator":"%s","reason":"%s","exit_code":0}\n' \
    "$event" "$from" "$to" "$CORDUM_AUDIT_SIG_SCHEME" "$CORDUM_AUDIT_FINGERPRINT" "$operator" "$reason" >&2
}

# semver_lt returns 0 (true) if $1 is strictly less than $2, 1 (false)
# otherwise. Returns 2 when either side is unparseable. Handles
# vMAJOR.MINOR.PATCH[-PRE]; pre-release ordering uses natural-sort on the
# alpha-prefix + digit-suffix pattern (rc2 < rc10) and falls back to lex.
# Intentionally narrower than tools/sign/version.go's SemverCompare; if
# release tags ever drift beyond this shape, update the Go and bash
# comparators in lockstep so install scripts and CI gate agree.
semver_lt() {
  local a="${1#v}" b="${2#v}"
  local a_main b_main a_pre b_pre
  case "$a" in *-*) a_main="${a%%-*}"; a_pre="${a#*-}" ;; *) a_main="$a"; a_pre="" ;; esac
  case "$b" in *-*) b_main="${b%%-*}"; b_pre="${b#*-}" ;; *) b_main="$b"; b_pre="" ;; esac
  local IFS=.
  set -- $a_main; [ "$#" -eq 3 ] || return 2
  local a_maj="$1" a_min="$2" a_pat="$3"
  set -- $b_main; [ "$#" -eq 3 ] || return 2
  local b_maj="$1" b_min="$2" b_pat="$3"
  unset IFS
  case "$a_maj$a_min$a_pat" in *[!0-9]*) return 2 ;; esac
  case "$b_maj$b_min$b_pat" in *[!0-9]*) return 2 ;; esac
  if [ "$a_maj" -lt "$b_maj" ]; then return 0; fi
  if [ "$a_maj" -gt "$b_maj" ]; then return 1; fi
  if [ "$a_min" -lt "$b_min" ]; then return 0; fi
  if [ "$a_min" -gt "$b_min" ]; then return 1; fi
  if [ "$a_pat" -lt "$b_pat" ]; then return 0; fi
  if [ "$a_pat" -gt "$b_pat" ]; then return 1; fi
  # M.N.P equal — pre-release ordering.
  if [ -z "$a_pre" ] && [ -z "$b_pre" ]; then return 1; fi
  if [ -z "$a_pre" ]; then return 1; fi   # release > pre
  if [ -z "$b_pre" ]; then return 0; fi   # pre < release
  local a_alpha="${a_pre%%[0-9]*}" a_num="${a_pre#"${a_pre%%[0-9]*}"}"
  local b_alpha="${b_pre%%[0-9]*}" b_num="${b_pre#"${b_pre%%[0-9]*}"}"
  if [ "$a_alpha" = "$b_alpha" ]; then
    case "$a_num" in *[!0-9]*|'') ;; *)
      case "$b_num" in *[!0-9]*|'') ;; *)
        if [ "$a_num" -lt "$b_num" ]; then return 0; fi
        if [ "$a_num" -gt "$b_num" ]; then return 1; fi
        return 1
      ;; esac
    ;; esac
  fi
  [ "$a_pre" \< "$b_pre" ] && return 0
  return 1
}

# resolve_floor_path prints the resolved binary-version-floor.json path on
# stdout. Honours $CORDUM_BINARY_FLOOR_FILE; falls back to
# $HOME/.cordum/binary-version-floor.json (or $TMPDIR equivalent when
# HOME is unset, matching agentd's defaultStateDir convention).
resolve_floor_path() {
  if [ -n "${CORDUM_BINARY_FLOOR_FILE:-}" ]; then
    printf '%s' "$CORDUM_BINARY_FLOOR_FILE"
    return
  fi
  local home="${HOME:-}"
  if [ -z "$home" ]; then
    home="${TMPDIR:-/tmp}/cordum-install-state"
  fi
  printf '%s' "$home/.cordum/binary-version-floor.json"
}

# read_floor_version prints the persisted floor version on stdout, or ""
# when the floor file is missing or empty. Malformed JSON is an error
# (callers verify_fail on non-zero exit).
read_floor_version() {
  local path="$1"
  [ -f "$path" ] || { printf ''; return 0; }
  # sed-only fallback to avoid jq requirement. Tolerates whitespace and
  # any field ordering; reads the first `"version":"vN.N.N..."` value.
  local v
  v=$(sed -nE 's/.*"version"[[:space:]]*:[[:space:]]*"([^"]*)".*/\1/p' "$path" | head -1)
  [ -n "$v" ] || { printf ''; return 0; }
  printf '%s' "$v"
}

# write_floor_atomic writes a fresh floor file at $1 with version $2,
# sig_scheme $3, fingerprint $4, operator $5, reason $6. Atomic via
# write-tmp + rename in the same directory. Mirrors AdvanceFloor in
# tools/sign/version.go.
write_floor_atomic() {
  local path="$1" version="$2" scheme="$3" fpr="$4" operator="$5" reason="$6"
  local dir
  dir="$(dirname "$path")"
  mkdir -p "$dir"
  local stamp
  stamp="$(date -u +'%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u '+%Y-%m-%dT%H:%M:%SZ')"
  local tmp
  tmp="$(mktemp "$dir/.binary-version-floor.json.tmp.XXXXXX")"
  printf '{"version":"%s","advanced_at":"%s","sig_scheme":"%s","fingerprint":"%s","operator":"%s","reason":"%s"}\n' \
    "$version" "$stamp" "$scheme" "$fpr" "$operator" "$reason" >"$tmp"
  mv -f "$tmp" "$path"
}

verify_fail() {
  local reason="$*"
  emit_audit "binary-verify-fail" "$reason" "" "" 1
  echo "BINARY-VERIFY-FAIL: $reason" >&2
  exit 1
}

normalise_fpr() {
  printf '%s' "$1" | tr -d ' \t\r\n' | tr '[:lower:]' '[:upper:]'
}

# reject_path returns 0 when the supplied manifest-relative path is unsafe
# (absolute, parent-traversing, or Windows-drive-rooted), 1 otherwise.
reject_path() {
  local p="$1"
  case "$p" in
    /*|\\*) return 0 ;;
    [A-Za-z]:*) return 0 ;;
  esac
  case "/$p/" in
    */../*) return 0 ;;
  esac
  return 1
}

run_binary_verify() {
  local rdir="$1" dev="$2" install_to="$3"
  local rollback_override="${4:-0}" rollback_reason="${5:-}"
  [ -d "$rdir" ] || verify_fail "release-dir not found: $rdir"

  local manifest="$rdir/SHA256SUMS"
  local sig="$rdir/SHA256SUMS.asc"
  [ -f "$manifest" ] || verify_fail "manifest not found"
  [ -f "$sig" ]      || verify_fail "unsigned manifest"

  command -v gpg       >/dev/null 2>&1 || verify_fail "gpg required for signature verification"
  command -v sha256sum >/dev/null 2>&1 || verify_fail "sha256sum required for binary verification"

  local script_dir repo_root
  script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
  repo_root="$(cd -- "$script_dir/../.." >/dev/null 2>&1 && pwd)"

  local pubkey
  if [ "$dev" = "1" ]; then
    pubkey="$repo_root/tools/test-keys/TEST-ONLY-release.pub.asc"
    [ -f "$pubkey" ] || verify_fail "dev mode but TEST-ONLY pubkey missing at $pubkey"
    CORDUM_AUDIT_SIG_SCHEME="dev"
  else
    pubkey="$repo_root/tools/keys/cordum-release.pub.asc"
    [ -f "$pubkey" ] || verify_fail "production release pubkey not provisioned at tools/keys/cordum-release.pub.asc; pass --dev-allow-unsigned for TEST-ONLY mode"
    [ -n "$CORDUM_PROD_FINGERPRINT_PIN" ] || verify_fail "production release fingerprint not provisioned (set CORDUM_RELEASE_FINGERPRINT or pin in install.sh header)"
    CORDUM_AUDIT_SIG_SCHEME="gpg"
  fi

  local tmp_home
  tmp_home="$(mktemp -d -t cordum-install-gpg-XXXXXX)"
  chmod 700 "$tmp_home" 2>/dev/null || true
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp_home'" EXIT

  gpg --homedir "$tmp_home" --batch --quiet --import "$pubkey" 2>/dev/null \
    || verify_fail "gpg --import failed for $pubkey"

  local imported_fpr
  imported_fpr=$(gpg --homedir "$tmp_home" --with-colons --list-keys 2>/dev/null \
    | awk -F: '$1=="fpr" {print $10; exit}')
  [ -n "$imported_fpr" ] || verify_fail "failed to extract fingerprint from pubkey"
  CORDUM_AUDIT_FINGERPRINT="$imported_fpr"

  if [ "$dev" = "1" ]; then
    # Refuse to honour --dev-allow-unsigned unless the imported UID carries
    # the TEST-ONLY marker; otherwise an attacker could ship a production
    # key file under tools/test-keys/ and ride the dev path.
    gpg --homedir "$tmp_home" --with-colons --list-keys 2>/dev/null \
      | awk -F: '$1=="uid" {print $10}' \
      | grep -q 'TEST-ONLY' \
      || verify_fail "dev mode but imported pubkey UID lacks TEST-ONLY marker"
    # Belt-and-braces: reject if the dev mode would ever accept the
    # production-pinned fingerprint (no mistakenly cross-signed manifests).
    if [ -n "$CORDUM_PROD_FINGERPRINT_PIN" ]; then
      local got_n want_n
      got_n=$(normalise_fpr "$imported_fpr")
      want_n=$(normalise_fpr "$CORDUM_PROD_FINGERPRINT_PIN")
      if [ "$got_n" = "$want_n" ]; then
        verify_fail "--dev-allow-unsigned refuses production fingerprint"
      fi
    fi
  else
    local got_n want_n
    got_n=$(normalise_fpr "$imported_fpr")
    want_n=$(normalise_fpr "$CORDUM_PROD_FINGERPRINT_PIN")
    if [ "$got_n" != "$want_n" ]; then
      verify_fail "release pubkey fingerprint $got_n does not match pinned $want_n"
    fi
  fi

  gpg --homedir "$tmp_home" --batch --quiet --verify "$sig" "$manifest" 2>/dev/null \
    || verify_fail "gpg signature invalid"

  # EDGE-151-DOWNGRADE: parse the embedded `# version: vN.N.N` line (if
  # present) and enforce the persisted version floor. Missing version line
  # is allowed (legacy release-bundle compatibility), but if a floor is
  # already persisted any version-less candidate triggers downgrade-refusal
  # so an attacker cannot bypass the gate by stripping the metadata.
  local candidate_version persisted_floor floor_path
  candidate_version=$(sed -nE '1s/^# version:[[:space:]]*(.*)$/\1/p' "$manifest")
  floor_path="$(resolve_floor_path)"
  persisted_floor="$(read_floor_version "$floor_path")"

  if [ -n "$persisted_floor" ] && [ -z "$candidate_version" ]; then
    verify_fail "downgrade attempt: candidate manifest has no embedded version but floor is $persisted_floor"
  fi
  if [ -n "$candidate_version" ]; then
    # Explicitly reject malformed candidate versions BEFORE the compare —
    # semver_lt's exit-2 sentinel must never silently become "not less
    # than" and ride the gate through. Probe against v0.0.0 (always
    # parseable) to detect parseability of the candidate.
    if ! semver_lt "v0.0.0" "$candidate_version" \
        && ! semver_lt "$candidate_version" "v0.0.0" \
        && [ "$candidate_version" != "v0.0.0" ]; then
      verify_fail "invalid manifest version: $candidate_version"
    fi
  fi
  if [ -n "$candidate_version" ] && [ -n "$persisted_floor" ]; then
    # Validate persisted_floor too — a malformed floor file is a hard
    # error (we never silently treat an unreadable floor as absent).
    if ! semver_lt "v0.0.0" "$persisted_floor" \
        && ! semver_lt "$persisted_floor" "v0.0.0" \
        && [ "$persisted_floor" != "v0.0.0" ]; then
      verify_fail "malformed floor file: persisted version $persisted_floor"
    fi
    set +e
    semver_lt "$candidate_version" "$persisted_floor"
    cmp_rc=$?
    set -e
    if [ "$cmp_rc" -eq 0 ]; then
      if [ "$rollback_override" = "1" ]; then
        if [ -z "$rollback_reason" ]; then
          verify_fail "--rollback-operator-override requires --rollback-reason <text>"
        fi
      else
        verify_fail "downgrade attempt $candidate_version < $persisted_floor"
      fi
    fi
  fi

  while IFS= read -r line || [ -n "$line" ]; do
    [ -z "$line" ] && continue
    # EDGE-151-DOWNGRADE: skip the leading `# version: vN.N.N` metadata
    # line (and any other future `#`-prefixed comment lines) so the hash
    # loop only sees real entries.
    case "$line" in '#'*) continue ;; esac
    local hash="${line%% *}"
    local rel="${line#* }"
    rel="${rel# }"
    # Strip the leading "*" that GNU coreutils sha256sum emits in binary
    # mode (`<hash> *<path>`), so we accept both binary- and text-mode
    # manifests symmetrically.
    rel="${rel#\*}"
    if [ -z "$hash" ] || [ -z "$rel" ]; then
      verify_fail "malformed manifest line"
    fi
    if reject_path "$rel"; then
      verify_fail "manifest path traversal: $rel"
    fi
    local target="$rdir/$rel"
    [ -f "$target" ] || verify_fail "binary missing $rel"
    local got
    got=$(sha256sum --text "$target" | awk '{print $1}')
    if [ "$got" != "$hash" ]; then
      verify_fail "hash mismatch $rel"
    fi
    emit_audit "binary-verify-ok" "" "$hash" "$rel" 0
  done < "$manifest"

  if [ "$(uname -s)" = "Darwin" ] && command -v codesign >/dev/null 2>&1; then
    local previous_scheme="$CORDUM_AUDIT_SIG_SCHEME"
    CORDUM_AUDIT_SIG_SCHEME="codesign"
    while IFS= read -r line || [ -n "$line" ]; do
      [ -z "$line" ] && continue
      case "$line" in '#'*) continue ;; esac
      local rel="${line#* }"; rel="${rel# }"; rel="${rel#\*}"
      local target="$rdir/$rel"
      [ -f "$target" ] || continue
      codesign --verify --deep --strict "$target" 2>/dev/null \
        || verify_fail "codesign verify failed $rel"
      emit_audit "binary-verify-ok" "" "" "$rel" 0
    done < "$manifest"
    CORDUM_AUDIT_SIG_SCHEME="$previous_scheme"
  fi

  if [ -n "$install_to" ]; then
    mkdir -p "$install_to"
    while IFS= read -r line || [ -n "$line" ]; do
      [ -z "$line" ] && continue
      case "$line" in '#'*) continue ;; esac
      local hash="${line%% *}"
      local rel="${line#* }"; rel="${rel# }"; rel="${rel#\*}"
      local src="$rdir/$rel"
      local dst="$install_to/$rel"
      mkdir -p "$(dirname "$dst")"
      if ! mv -f "$src" "$dst" 2>/dev/null; then
        # Cross-fs fallback: copy to staging then rename in-place.
        cp -f "$src" "$dst.cordum-staging"
        mv -f "$dst.cordum-staging" "$dst"
      fi
      # Recompute SHA-256 AFTER the move — defence-in-depth against a
      # sig-then-swap race where the attacker substitutes the file between
      # verification and activation.
      local post
      post=$(sha256sum --text "$dst" | awk '{print $1}')
      if [ "$post" != "$hash" ]; then
        verify_fail "post-activation hash mismatch $rel"
      fi
      chmod +x "$dst" 2>/dev/null || true
      emit_audit "binary-verify-ok" "" "$post" "$rel" 0
    done < "$manifest"
    # Skip the `# version:` metadata line from the activation count.
    local activated_n
    activated_n=$(grep -v -c -E '^#|^$' "$manifest")
    echo "[install] activated ${activated_n} binaries under $install_to"

    # EDGE-151-DOWNGRADE: advance the persisted floor on a successful
    # activation. Upgrade and operator-override-rollback both funnel
    # through write_floor_atomic — the audit event distinguishes them.
    if [ -n "$candidate_version" ]; then
      local operator="${USER:-${LOGNAME:-unknown}}"
      if [ "$rollback_override" = "1" ] && semver_lt "$candidate_version" "${persisted_floor:-v0.0.0}"; then
        write_floor_atomic "$floor_path" "$candidate_version" \
          "$CORDUM_AUDIT_SIG_SCHEME" "$CORDUM_AUDIT_FINGERPRINT" \
          "$operator" "$rollback_reason"
        emit_floor_audit "binary-floor-rollback" "${persisted_floor:-}" \
          "$candidate_version" "$operator" "$rollback_reason"
      else
        write_floor_atomic "$floor_path" "$candidate_version" \
          "$CORDUM_AUDIT_SIG_SCHEME" "$CORDUM_AUDIT_FINGERPRINT" \
          "$operator" ""
        emit_floor_audit "binary-floor-advance" "${persisted_floor:-}" \
          "$candidate_version" "$operator" ""
      fi
    fi
  else
    local n
    n=$(grep -v -c -E '^#|^$' "$manifest")
    echo "[install] release-dir verified: $rdir ($n binaries match manifest)"
  fi
}

# ============================================================================
# Argv parsing — when --release-dir is supplied, short-circuit to the
# binary-verify gate and skip the orchestrator block below.
# ============================================================================
RELEASE_DIR=""
DEV_ALLOW_UNSIGNED=0
INSTALL_TO="${CORDUM_INSTALL_DIR:-}"
ROLLBACK_OVERRIDE=0
ROLLBACK_REASON=""
while [ $# -gt 0 ]; do
  case "$1" in
    --release-dir)               RELEASE_DIR="$2"; shift 2 ;;
    --release-dir=*)             RELEASE_DIR="${1#--release-dir=}"; shift ;;
    --dev-allow-unsigned)        DEV_ALLOW_UNSIGNED=1; shift ;;
    --install-dir)               INSTALL_TO="$2"; shift 2 ;;
    --install-dir=*)             INSTALL_TO="${1#--install-dir=}"; shift ;;
    --rollback-operator-override) ROLLBACK_OVERRIDE=1; shift ;;
    --rollback-reason)           ROLLBACK_REASON="$2"; shift 2 ;;
    --rollback-reason=*)         ROLLBACK_REASON="${1#--rollback-reason=}"; shift ;;
    --) shift; break ;;
    -*) break ;;
    *)  break ;;
  esac
done

if [ -n "$RELEASE_DIR" ]; then
  # Cap rollback-reason at 256 chars per audit-event field hygiene.
  if [ -n "$ROLLBACK_REASON" ]; then
    ROLLBACK_REASON="${ROLLBACK_REASON:0:256}"
  fi
  if [ "$ROLLBACK_OVERRIDE" = "1" ] && [ -z "$ROLLBACK_REASON" ]; then
    verify_fail "--rollback-operator-override requires --rollback-reason <text>"
  fi
  run_binary_verify "$RELEASE_DIR" "$DEV_ALLOW_UNSIGNED" "$INSTALL_TO" \
    "$ROLLBACK_OVERRIDE" "$ROLLBACK_REASON"
  exit 0
fi

# ============================================================================
# === Cordum platform install (existing orchestrator) ========================
# ============================================================================
REPO_URL=${REPO_URL:-https://github.com/cordum-io/cordum.git}
DEST_DIR=${DEST_DIR:-cordum}
VERSION=${VERSION:-main}
USE_RELEASE_IMAGES=${USE_RELEASE_IMAGES:-0}
CORDUM_VERSION=${CORDUM_VERSION:-latest}

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

require git
require docker
compose_cmd=()
if docker compose version >/dev/null 2>&1; then
  compose_cmd=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose_cmd=(docker-compose)
  echo "[install] warning: docker-compose v1 detected; prefer Docker Compose v2." >&2
else
  echo "docker compose plugin required" >&2
  exit 1
fi

if ! docker info >/dev/null 2>&1; then
  echo "cannot connect to the Docker daemon." >&2
  echo "ensure Docker is running and your user can access /var/run/docker.sock." >&2
  echo "on Linux, add your user to the docker group or re-run with sudo." >&2
  exit 1
fi

port_in_use() {
  local port="$1"
  if command -v ss >/dev/null 2>&1; then
    if ss -ltn 2>/dev/null | awk '{print $4}' | grep -E "(^|:)${port}$" >/dev/null 2>&1; then
      return 0
    fi
    return 1
  fi
  if command -v lsof >/dev/null 2>&1; then
    if lsof -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; then
      return 0
    fi
    return 1
  fi
  if command -v netstat >/dev/null 2>&1; then
    if netstat -ltn 2>/dev/null | awk '{print $4}' | grep -E "(^|:)${port}$" >/dev/null 2>&1; then
      return 0
    fi
    return 1
  fi
  return 2
}

warn_port() {
  local port="$1"
  local name="$2"
  if port_in_use "${port}"; then
    echo "[install] warning: port ${port} (${name}) is already in use; compose may fail to bind." >&2
  fi
}

warn_port 8081 "api-gateway http"
warn_port 8082 "dashboard"
warn_port 8080 "api-gateway grpc"
warn_port 9092 "gateway metrics"
warn_port 9093 "workflow-engine http"
warn_port 50051 "safety-kernel grpc"
warn_port 50070 "context-engine grpc"
warn_port 4222 "nats client"
warn_port 6379 "redis"

API_KEY=${CORDUM_API_KEY:-${API_KEY:-}}
if [[ -z "${API_KEY}" ]]; then
  echo "CORDUM_API_KEY is required; export it before running install." >&2
  exit 1
fi
export CORDUM_API_KEY="${API_KEY}"
TENANT_ID=${CORDUM_TENANT_ID:-default}
export CORDUM_TENANT_ID="${TENANT_ID}"

if [ -d "${DEST_DIR}/.git" ]; then
  echo "using existing repo at ${DEST_DIR}"
else
  echo "cloning ${REPO_URL} to ${DEST_DIR}"
  git clone --depth 1 --branch "${VERSION}" "${REPO_URL}" "${DEST_DIR}"
fi

cd "${DEST_DIR}"

if [ "${USE_RELEASE_IMAGES}" = "1" ]; then
  echo "starting from GHCR images (CORDUM_VERSION=${CORDUM_VERSION})"
  export CORDUM_VERSION
  "${compose_cmd[@]}" -f docker-compose.release.yml pull
  "${compose_cmd[@]}" -f docker-compose.release.yml up -d
else
  echo "building from source"
  "${compose_cmd[@]}" build
  "${compose_cmd[@]}" up -d
fi

cat <<'EOF'
Cordum is up.
- Dashboard: http://localhost:8082
- API: http://localhost:8081
Next steps (OSS, from repo root):
- Quickstart: ./tools/scripts/quickstart.sh
- Smoke test: CORDUM_API_KEY=<your-api-key> CORDUM_TENANT_ID=default ./tools/scripts/platform_smoke.sh
- Guardrails demo: ./tools/scripts/demo_guardrails_run.sh
- Mock bank demo: ./tools/scripts/demo_mock_bank.sh
EOF
