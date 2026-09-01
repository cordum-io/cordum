#!/usr/bin/env bash
# Source-only helpers for masking generated GitHub Actions credentials.
gha__error() {
  printf 'github-actions-mask-env: %s\n' "$1" >&2
}
gha__valid_name() {
  [[ "${1-}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]
}
gha__valid_value() {
  local value="${1-}"
  [[ -n "${value}" && "${value}" != *$'\r'* && "${value}" != *$'\n'* ]]
}
gha__resolve_python() {
  local candidate
  for candidate in /usr/bin/python3 /usr/bin/python; do
    [[ -x "${candidate}" && ! -L "${candidate}" ]] && { printf '%s\n' "${candidate}"; return 0; }
  done
  [[ "${GITHUB_ACTIONS:-}" == "true" ]] && return 1
  for candidate in "$(command -v python3 2>/dev/null || true)" "$(command -v python 2>/dev/null || true)"; do
    [[ "${candidate}" == /* && -x "${candidate}" && ! -L "${candidate}" ]] && { printf '%s\n' "${candidate}"; return 0; }
  done
  return 1
}
GHA__PYTHON="$(gha__resolve_python 2>/dev/null || true)"
readonly GHA__PYTHON
gha__require_github_env() {
  if [[ -z "${GITHUB_ENV-}" ]]; then
    gha__error 'GITHUB_ENV is unavailable'
    return 1
  fi
  if [[ -L "${GITHUB_ENV}" ]]; then
    gha__error 'GITHUB_ENV must not be a symlink'
    return 1
  fi
}
gha__github_env_identity() {
  local resolved lexical identity
  gha__require_github_env || return 1
  if [[ "${GITHUB_ENV}" != /* || ! -f "${GITHUB_ENV}" || ! -w "${GITHUB_ENV}" ]]; then
    gha__error 'GITHUB_ENV must be an existing writable regular file'
    return 1
  fi
  resolved="$(realpath -e -- "${GITHUB_ENV}" 2>/dev/null)" || return 1
  lexical="$(realpath -s -- "${GITHUB_ENV}" 2>/dev/null)" || return 1
  if [[ "${resolved}" != "${lexical}" ]]; then
    gha__error 'GITHUB_ENV path must not contain symlinks'
    return 1
  fi
  identity="$(stat -Lc '%d:%i:%f:%s' -- "${GITHUB_ENV}" 2>/dev/null)" || return 1
  printf '%s\n' "${identity}"
}
gha_mask_value() {
  local value="${1-}" escaped
  if [[ "$#" -ne 1 ]]; then
    gha__error 'mask registration requires one value'
    return 1
  fi
  if ! gha__valid_value "${value}"; then
    gha__error 'refusing an empty or multiline value'
    return 1
  fi
  escaped="${value//%/%25}"
  if ! printf '%s\n' "::add-mask::${escaped}"; then
    gha__error 'mask registration failed'
    return 1
  fi
}
gha_mask_env() {
  local name="${1-}" value="${2-}"
  if [[ "$#" -ne 2 ]]; then
    gha__error 'environment masking requires one name and value'
    return 1
  fi
  if ! gha__valid_name "${name}"; then
    gha__error 'invalid environment variable name'
    return 1
  fi
  if ! gha__valid_value "${value}"; then
    gha__error "refusing an empty or multiline value for ${name}"
    return 1
  fi
  gha__require_github_env || return 1
  gha_mask_value "${value}" || return 1
  if ! printf '%s=%s\n' "${name}" "${value}" 2>/dev/null >> "${GITHUB_ENV}"; then
    gha__error "environment write failed for ${name}"
    return 1
  fi
  if ! export "${name}=${value}"; then
    gha__error "same-step export failed for ${name}"
    return 1
  fi
}
gha__parse_import_args() {
  local -n result_names="$1"
  local -n result_command="$2"
  shift 2
  local name
  declare -A declared=()
  while [[ "$#" -gt 0 && "$1" != -- ]]; do
    name="$1"
    if ! gha__valid_name "${name}" || [[ -n "${declared[${name}]+x}" ]]; then
      gha__error 'invalid or duplicate imported name'
      return 1
    fi
    declared["${name}"]=1
    result_names+=("${name}")
    shift
  done
  if [[ "${#result_names[@]}" -eq 0 || "$#" -eq 0 || "$1" != -- ]]; then
    gha__error 'import requires declared names followed by --'
    return 1
  fi
  shift
  if [[ "$#" -eq 0 ]]; then
    gha__error 'import command is missing'
    return 1
  fi
  result_command=("$@")
}
gha__capture_assignments() {
  local names_ref="$1" command_ref="$2" values_ref="$3"
  local -n imported_names="${names_ref}" imported_command="${command_ref}" imported_values="${values_ref}"
  local output status line name value
  declare -A wanted=() seen=()
  for name in "${imported_names[@]}"; do wanted["${name}"]=1; done
  if output="$(unset GITHUB_ENV; "${imported_command[@]}" 2>&1)"; then status=0; else status=$?; fi
  if [[ "${status}" -ne 0 ]]; then
    gha__error "import command failed with status ${status}"
    return 1
  fi
  while IFS= read -r line || [[ -n "${line}" ]]; do
    if [[ "${line}" != *=* ]]; then gha__error 'malformed import output'; return 1; fi
    name="${line%%=*}"; value="${line#*=}"
    if ! gha__valid_name "${name}" || [[ -z "${wanted[${name}]+x}" || -n "${seen[${name}]+x}" ]]; then
      gha__error 'unexpected or duplicate import name'
      return 1
    fi
    if ! gha__valid_value "${value}"; then gha__error 'invalid imported value'; return 1; fi
    seen["${name}"]=1; imported_values["${name}"]="${value}"
  done <<< "${output}"
  for name in "${imported_names[@]}"; do
    if [[ -z "${seen[${name}]+x}" ]]; then gha__error "missing assignment for ${name}"; return 1; fi
  done
}
gha_mask_env_from_command() {
  local -a names=() command=()
  local name payload='' initial_identity current_identity
  declare -A values=()
  gha__parse_import_args names command "$@" || return 1
  initial_identity="$(gha__github_env_identity)" || return 1
  gha__capture_assignments names command values || return 1
  current_identity="$(gha__github_env_identity)" || return 1
  if [[ "${current_identity}" != "${initial_identity}" ]]; then
    gha__error 'GITHUB_ENV changed while importing assignments'
    return 1
  fi
  for name in "${names[@]}"; do gha_mask_value "${values[${name}]}" || return 1; done
  for name in "${names[@]}"; do
    printf -v payload '%s%s=%s\n' "${payload}" "${name}" "${values[${name}]}"
  done
  current_identity="$(gha__github_env_identity)" || return 1
  if [[ "${current_identity}" != "${initial_identity}" ]]; then
    gha__error 'GITHUB_ENV changed before assignment write'
    return 1
  fi
  if ! printf '%s' "${payload}" 2>/dev/null >> "${GITHUB_ENV}"; then
    gha__error 'batch environment write failed'
    return 1
  fi
  for name in "${names[@]}"; do
    if ! export "${name}=${values[${name}]}"; then
      gha__error "same-step export failed for ${name}"
      return 1
    fi
  done
}
gha__parse_redact_args() {
  local -n result_names="$1" result_paths="$2"
  shift 2
  local name value
  declare -A declared=()
  while [[ "$#" -gt 0 && "$1" != -- ]]; do
    name="$1"
    if ! gha__valid_name "${name}" || [[ -n "${declared[${name}]+x}" ]]; then
      gha__error 'invalid or duplicate redaction name'
      return 1
    fi
    value="${!name-}"
    if ! gha__valid_value "${value}"; then
      gha__error "missing or invalid redaction value for ${name}"
      return 1
    fi
    declared["${name}"]=1; result_names+=("${name}"); shift
  done
  if [[ "${#result_names[@]}" -eq 0 || "$#" -eq 0 || "$1" != -- ]]; then
    gha__error 'redaction requires names followed by --'
    return 1
  fi
  shift
  if [[ "$#" -eq 0 ]]; then gha__error 'redaction path is missing'; return 1; fi
  result_paths=("$@")
}
gha__validate_redact_paths() {
  local path
  for path in "$@"; do
    if [[ ! -e "${path}" || -L "${path}" || ( ! -f "${path}" && ! -d "${path}" ) ]]; then
      gha__error 'redaction path is missing or unsafe'
      return 1
    fi
  done
}
IFS= read -r -d '' GHA__REDACTOR_PY <<'PY' || true
import os, stat, sys, tempfile
MAX_BYTES = 128 * 1024 * 1024
def reject_link_components(raw):
    path = os.path.abspath(raw); drive, tail = os.path.splitdrive(path)
    current = drive + os.sep if tail.startswith(os.sep) else drive
    for part in tail.split(os.sep):
        if not part: continue
        current = os.path.join(current, part)
        if stat.S_ISLNK(os.lstat(current).st_mode): raise OSError("symlink")
def files(paths):
    for raw in paths:
        reject_link_components(raw)
        if os.path.isfile(raw): yield raw; continue
        if not os.path.isdir(raw): raise OSError("path")
        for root, dirs, names in os.walk(raw, followlinks=False):
            if any(os.path.islink(os.path.join(root, name)) for name in dirs): raise OSError("symlink")
            for name in names:
                path = os.path.join(root, name)
                mode = os.lstat(path).st_mode
                if stat.S_ISLNK(mode) or not stat.S_ISREG(mode): raise OSError("unsafe")
                yield path
def read_regular(path):
    before = os.lstat(path)
    if not stat.S_ISREG(before.st_mode) or before.st_nlink != 1 or before.st_size > MAX_BYTES: raise OSError("unsafe")
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0))
    with os.fdopen(fd, "rb") as source:
        opened = os.fstat(source.fileno())
        if (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino) or opened.st_nlink != 1: raise OSError("changed")
        data = source.read(MAX_BYTES + 1)
    if len(data) > MAX_BYTES or b"\0" in data: raise OSError("binary")
    return data, before
def marker_for(data, values):
    for index in range(256):
        marker = f"[CORDUM-REDACTED-{index}]".encode("ascii")
        if marker not in data and all(marker not in value and value not in marker for value in values): return marker
    raise OSError("marker")
def replace(path, values):
    data, before = read_regular(path)
    marker = marker_for(data, values)
    redacted = data
    for value in values: redacted = redacted.replace(value, marker)
    if redacted == data: return
    current = os.lstat(path)
    if (current.st_dev, current.st_ino, current.st_size, current.st_mtime_ns, current.st_nlink) != (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, 1): raise OSError("changed")
    mode = stat.S_IMODE(before.st_mode)
    fd, temporary = tempfile.mkstemp(prefix=".gha-redact-", dir=os.path.dirname(path) or ".")
    try:
        with os.fdopen(fd, "wb") as target:
            target.write(redacted); target.flush(); os.fsync(target.fileno())
        os.chmod(temporary, mode); os.replace(temporary, path)
    finally:
        if os.path.exists(temporary): os.unlink(temporary)
    final, _ = read_regular(path)
    if any(value in final for value in values): raise OSError("residual")
def main():
    split = sys.argv.index("--")
    names, paths = sys.argv[1:split], sys.argv[split + 1:]
    values = []
    for name in names:
        value = os.environ.get(name, "")
        if not value or "\r" in value or "\n" in value: raise OSError("environment")
        values.append(value.encode("utf-8"))
    values.sort(key=len, reverse=True)
    for path in files(paths): replace(path, values)
try: main()
except Exception: raise SystemExit(1)
PY
readonly GHA__REDACTOR_PY
gha__python_redact() {
  (
  unset PYTHONHOME PYTHONPATH PYTHONSTARTUP PYTHONINSPECT PYTHONWARNINGS
  printf '%s\n' "${GHA__REDACTOR_PY}" | "${GHA__PYTHON}" -I -S - "$@"
  )
}
gha_redact_paths() {
  local -a names=() paths=()
  gha__parse_redact_args names paths "$@" || return 1
  gha__validate_redact_paths "${paths[@]}" || return 1
  if [[ -z "${GHA__PYTHON}" ]]; then
    gha__error 'python is required for artifact redaction'
    return 1
  fi
  if ! gha__python_redact "${names[@]}" -- "${paths[@]}" 2>/dev/null; then
    gha__error 'artifact redaction failed'
    return 1
  fi
}
