#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROTO_SRC="$ROOT_DIR/core/protocol/proto/v1"
OUT_DIR="$ROOT_DIR/docs/api/openapi"
PROTO_FILES=(api.proto context.proto)

if ! command -v protoc >/dev/null 2>&1; then
	echo "protoc not found; install protobuf compiler first" >&2
	exit 1
fi
if ! command -v protoc-gen-openapiv2 >/dev/null 2>&1; then
	echo "protoc-gen-openapiv2 not found; install with:" >&2
	echo "  go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.22.0" >&2
	exit 1
fi

mkdir -p "$OUT_DIR"
cd "$PROTO_SRC"
PATH="$PATH:$HOME/go/bin" protoc \
	-I . \
	-I "$ROOT_DIR" \
	--openapiv2_out="$OUT_DIR" \
	--openapiv2_opt=logtostderr=true,generate_unbound_methods=true,allow_merge=true,merge_file_name=cordum \
	"${PROTO_FILES[@]}"

echo "openapi spec written to $OUT_DIR"
