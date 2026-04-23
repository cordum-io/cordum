#!/usr/bin/env bash
set -euo pipefail

# Validate the canonical OpenAPI 3 spec with Redocly. This script used to
# also regenerate the protobuf-derived Swagger sidecar via protoc-gen-openapiv2,
# but that artifact was retired in task-d9d7f428 — `cordum-api.yaml` is the
# single canonical spec.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CANONICAL_SPEC_REL="docs/api/openapi/cordum-api.yaml"
CANONICAL_SPEC="$ROOT_DIR/$CANONICAL_SPEC_REL"

cd "$ROOT_DIR"

if [[ ! -f "$CANONICAL_SPEC" ]]; then
	echo "canonical spec not found: $CANONICAL_SPEC" >&2
	exit 1
fi

if ! command -v npx >/dev/null 2>&1; then
	echo "npx not found; install Node.js/npm to validate $CANONICAL_SPEC" >&2
	exit 1
fi

REDOCLY_CLI_VERSION="${REDOCLY_CLI_VERSION:-1.34.1}"
npx --yes "@redocly/cli@${REDOCLY_CLI_VERSION}" lint "$CANONICAL_SPEC_REL"

echo "validated $CANONICAL_SPEC_REL"
