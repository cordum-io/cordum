#!/usr/bin/env bash
set -euo pipefail

# Regenerate the protobuf-derived OpenAPI subset and validate the
# canonical OpenAPI 3 spec with Redocly. Both outputs have to stay in
# sync with the runtime — the protobuf swagger carries the gRPC-gateway
# surface that the dashboard's legacy import depends on, and the
# canonical spec is the source of truth for SDK generation.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CANONICAL_SPEC_REL="docs/api/openapi/cordum-api.yaml"
CANONICAL_SPEC="$ROOT_DIR/$CANONICAL_SPEC_REL"
PROTO_SWAGGER_OUT="docs/api/openapi/cordum.swagger.json"

cd "$ROOT_DIR"

# Step 1: regenerate the protobuf swagger artifact via protoc. Opt out
# with SKIP_PROTO_SWAGGER=1 (useful on machines that don't have the
# protoc-gen-openapiv2 plugin installed yet and just want redocly lint).
if [[ "${SKIP_PROTO_SWAGGER:-0}" != "1" ]]; then
	if command -v protoc >/dev/null 2>&1; then
		PROTO_DIR="core/protocol/proto/v1"
		OUT_DIR="$(dirname "$PROTO_SWAGGER_OUT")"
		mkdir -p "$OUT_DIR"
		if (cd "$PROTO_DIR" && protoc -I . -I "$ROOT_DIR" \
			--openapiv2_out="$ROOT_DIR/$OUT_DIR" \
			--openapiv2_opt logtostderr=true,generate_unbound_methods=true \
			$(ls *.proto 2>/dev/null) 2>/dev/null); then
			echo "regenerated $PROTO_SWAGGER_OUT"
		else
			echo "protoc-gen-openapiv2 unavailable; skipping proto swagger regen" >&2
		fi
	else
		echo "protoc not found; skipping proto swagger regen (set SKIP_PROTO_SWAGGER=1 to silence)" >&2
	fi
fi

# Step 2: validate the canonical spec.
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
