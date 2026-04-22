#!/usr/bin/env bash
# Guards the MCP onboarding docs against JSON drift:
# parses every ```json fence in docs/mcp/quickstart-*.md and asserts
# the shape matches what the real gateway + cordumctl accept. Any
# drift fails CI rather than landing as broken copy-paste in the docs.
#
# Requires: bash 4+, jq, python3.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MCP_DOCS_DIR="${REPO_ROOT}/docs/mcp"
FIXTURE_DIR="${REPO_ROOT}/tools/scripts/mcp_snippet_fixtures"

fail() { echo "ERROR: $*" >&2; exit 1; }
pass() { echo "  OK  $*"; }

need() { command -v "$1" >/dev/null 2>&1 || fail "required tool not found: $1"; }
need jq

# Resolve a working Python interpreter — python3 on Linux/CI, python on Windows/MSYS.
PYTHON=""
for candidate in python3 python; do
  if command -v "${candidate}" >/dev/null 2>&1 && "${candidate}" -c 'import sys; sys.exit(0 if sys.version_info >= (3, 8) else 1)' >/dev/null 2>&1; then
    PYTHON="${candidate}"
    break
  fi
done
[[ -n "${PYTHON}" ]] || fail "python3 (>=3.8) required; not found on PATH"

[[ -d "${MCP_DOCS_DIR}" ]] || fail "docs/mcp not found at ${MCP_DOCS_DIR}"
[[ -d "${FIXTURE_DIR}"   ]] || fail "fixture dir not found at ${FIXTURE_DIR}"

# extract_json_fences <md-file>
# Emits each ```json ... ``` fence block as NUL-separated JSON to stdout.
extract_json_fences() {
  "${PYTHON}" - "$1" <<'PY'
import sys, re
path = sys.argv[1]
with open(path, 'r', encoding='utf-8') as f:
    src = f.read()
# ```json ... ``` — non-greedy, dotall so newlines match
for m in re.finditer(r"```json\s*\n(.*?)\n```", src, flags=re.DOTALL):
    block = m.group(1).strip()
    if not block:
        continue
    sys.stdout.write(block)
    sys.stdout.write("\x00")
PY
}

# validate_shape <block-json> <fixture-json> <label>
# Enumerates EVERY path in the fixture (leaf + container) and asserts
# the block has a value at that path AND the value's JSON type matches
# the fixture's. Catches both missing required keys and type drifts
# (e.g. a stringified "true" where the fixture declares a boolean).
validate_shape() {
  local block="$1" fixture="$2" label="$3"
  local missing
  missing="$(jq -nr --argjson b "${block}" --argjson f "${fixture}" '
    [ $f | paths ] as $paths
    | $paths[]
    | . as $p
    | ($f | getpath($p)) as $expected
    | ($b | getpath($p)) as $actual
    | select(
        $actual == null or
        (($expected | type) != ($actual | type))
      )
    | ($p | map(tostring) | join(".")) as $k
    | if $actual == null then "\($k) (missing)"
      else "\($k) (expected \($expected | type), got \($actual | type))"
      end
  ')"
  if [[ -n "${missing}" ]]; then
    echo "${label}: required-shape violations:" >&2
    echo "${missing}" | sed 's/^/    - /' >&2
    return 1
  fi
  return 0
}

# normalise <json-string> — strip comments-less JSON through jq to catch
# syntax errors and re-emit canonical form.
normalise() { jq -c . <<<"$1"; }

STATUS=0
TOTAL=0
MATCHED=0

process_doc() {
  local doc="$1" expected_prefix="$2"
  echo "Scanning: ${doc}"
  [[ -f "${doc}" ]] || { echo "  (missing — skip)"; return 0; }

  local tmpfile
  tmpfile="$(mktemp)"
  trap 'rm -f "${tmpfile}"' RETURN
  extract_json_fences "${doc}" >"${tmpfile}"

  # Null-separated blocks.
  local idx=0
  while IFS= read -r -d '' block; do
    TOTAL=$((TOTAL+1))
    idx=$((idx+1))
    local label="${doc##*/}[#${idx}]"
    local canonical
    if ! canonical="$(normalise "${block}" 2>&1)"; then
      echo "  FAIL ${label}: invalid JSON — ${canonical}" >&2
      STATUS=1
      continue
    fi

    # Classify: stdio snippet, http snippet, or env-only (terminal env block).
    local kind=""
    if jq -e '[.. | objects | select(.type? == "http")] | length > 0' <<<"${canonical}" >/dev/null 2>&1; then
      kind="http"
    elif jq -e '[.. | objects | select(.command? == "cordumctl")] | length > 0' <<<"${canonical}" >/dev/null 2>&1; then
      kind="stdio"
    elif jq -e '[.. | objects | select(has("terminal.integrated.env.linux"))] | length > 0' <<<"${canonical}" >/dev/null 2>&1; then
      kind="vscode-env"
    fi

    if [[ -z "${kind}" ]]; then
      echo "  SKIP ${label}: not a recognised MCP config snippet (no 'cordumctl' command, no 'http' type, no VS Code env)"
      continue
    fi

    local fixture_file="${FIXTURE_DIR}/${expected_prefix}.${kind}.json"
    if [[ ! -f "${fixture_file}" ]]; then
      echo "  SKIP ${label}: no fixture at ${fixture_file} (kind=${kind})"
      continue
    fi

    local fixture
    fixture="$(jq -c . "${fixture_file}")"
    if validate_shape "${canonical}" "${fixture}" "${label}"; then
      pass "${label} (kind=${kind}) matches ${fixture_file##*/}"
      MATCHED=$((MATCHED+1))
    else
      STATUS=1
    fi
  done <"${tmpfile}"
}

process_doc "${MCP_DOCS_DIR}/quickstart-claude-code.md" "claude-code"
process_doc "${MCP_DOCS_DIR}/quickstart-cursor.md"      "cursor"
process_doc "${MCP_DOCS_DIR}/quickstart-vscode.md"      "vscode"

echo
echo "Scanned ${TOTAL} JSON snippet(s); matched ${MATCHED} against fixtures."
if [[ "${STATUS}" -ne 0 ]]; then
  fail "one or more MCP onboarding snippets drifted from the fixture contract"
fi
echo "All MCP onboarding snippets match fixture shapes."
