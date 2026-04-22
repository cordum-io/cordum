#!/usr/bin/env bash
set -euo pipefail

# Validate the canonical OpenAPI 3 spec with Redocly.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CANONICAL_SPEC_REL="docs/api/openapi/cordum-api.yaml"
CANONICAL_SPEC="$ROOT_DIR/$CANONICAL_SPEC_REL"

if [[ ! -f "$CANONICAL_SPEC" ]]; then
	echo "canonical spec not found: $CANONICAL_SPEC" >&2
	exit 1
fi

if ! command -v npx >/dev/null 2>&1; then
	echo "npx not found; install Node.js/npm to validate $CANONICAL_SPEC" >&2
	exit 1
fi

cd "$ROOT_DIR"
npx --yes @redocly/cli@latest lint "$CANONICAL_SPEC_REL"

echo "validated $CANONICAL_SPEC_REL"
