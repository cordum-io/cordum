#!/usr/bin/env bash
# Full OpenAPI validation pipeline:
#   1. Python SDK endpoint coverage: one generated module per operationId
#   2. redocly lint for schema-level issues
#   3. openapi-audit for route <-> spec coverage
#   4. oasdiff breaking check against the committed base
#
# oasdiff emits ERR-level findings for removed operations, narrowed schemas,
# and other backward-incompatible changes. Additive changes pass freely. A
# commit message containing "allow-breaking-openapi" is the documented escape
# hatch for intentional breaking changes; it sets OPENAPI_ALLOW_BREAKING=1 in
# CI before this script runs.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SPEC="$ROOT_DIR/docs/api/openapi/cordum-api.yaml"
GATEWAY_DIR="$ROOT_DIR/core/controlplane/gateway"

GO_CMD="${GO_CMD:-go}"

echo "==> Python SDK endpoint coverage"
# sdk/python/tests/test_generated_coverage.py makes the same count, but sdk-python CI runs only
# when sdk/python changes, so a spec-only change could leave the Python client behind.
SDK_API_DIR="$ROOT_DIR/sdk/python/src/cordum_sdk/_generated/api"
OPERATION_IDS="$(grep -c -E '^[[:space:]]*operationId:[[:space:]]*[^[:space:]]' "$SPEC")"
SDK_MODULES="$(find "$SDK_API_DIR" -name '*.py' ! -name '__init__.py' | wc -l)"
if [[ "$OPERATION_IDS" -ne "$SDK_MODULES" ]]; then
	echo "error: $SDK_MODULES Python SDK endpoint modules for $OPERATION_IDS operationIds; regenerate sdk/python (sdk/python/README.md, Regenerating from the spec)" >&2
	exit 1
fi

echo "==> redocly lint"
# Pin @redocly/cli so CI behaviour is deterministic across runs; bump
# REDOCLY_CLI_VERSION here to take a new release.
REDOCLY_CLI_VERSION="${REDOCLY_CLI_VERSION:-1.34.1}"
if command -v npx >/dev/null 2>&1; then
	npx --yes "@redocly/cli@${REDOCLY_CLI_VERSION}" lint "$SPEC"
else
	echo "npx not found; skipping redocly lint (CI always has npm)" >&2
fi

echo "==> openapi-audit (route<->spec coverage)"
"$GO_CMD" run "$ROOT_DIR/tools/openapi-audit" \
	--spec "$SPEC" \
	--gateway-dir "$GATEWAY_DIR"

echo "==> oasdiff breaking check"
if ! command -v oasdiff >/dev/null 2>&1; then
	echo "oasdiff not installed; install with: go install github.com/tufin/oasdiff@latest" >&2
	exit 1
fi

BASE_REF="${OPENAPI_BASE_REF:-origin/main}"
BASE_FILE="$(mktemp -t cordum-api-base.XXXXXX.yaml)"
trap 'rm -f "$BASE_FILE"' EXIT

if git -C "$ROOT_DIR" show "$BASE_REF:docs/api/openapi/cordum-api.yaml" > "$BASE_FILE" 2>/dev/null; then
	if [[ "${OPENAPI_ALLOW_BREAKING:-0}" == "1" ]]; then
		echo "OPENAPI_ALLOW_BREAKING=1 set; reporting breaking changes without failing"
		oasdiff breaking "$BASE_FILE" "$SPEC" || true
	else
		oasdiff breaking --fail-on ERR "$BASE_FILE" "$SPEC"
	fi
else
	echo "warning: could not read $BASE_REF:docs/api/openapi/cordum-api.yaml; skipping oasdiff" >&2
fi

echo "openapi-validate OK"
