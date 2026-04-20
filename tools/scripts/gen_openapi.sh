#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROTO_SRC="$ROOT_DIR/core/protocol/proto/v1"
OUT_DIR="$ROOT_DIR/docs/api/openapi"
PROTO_FILES=(api.proto context.proto output_policy.proto)
CANONICAL_SPEC="$OUT_DIR/cordum-api.yaml"
to_windows_path() {
	local path="$1"
	if command -v cygpath >/dev/null 2>&1; then
		cygpath -w "$path"
	elif [[ "$path" == /mnt/[a-zA-Z]/* ]]; then
		local path_without_mount="${path#/mnt/}"
		local drive_letter="${path_without_mount%%/*}"
		local rest_of_path="${path#"/mnt/$drive_letter"}"
		printf '%s:%s\n' "${drive_letter^^}" "$rest_of_path"
	else
		printf '%s\n' "$path"
	fi
}
LINT_SPEC="$CANONICAL_SPEC"
LINT_SPEC="$(to_windows_path "$CANONICAL_SPEC")"
GO_CMD="${GO_CMD:-}"
if [[ -z "$GO_CMD" ]]; then
	if command -v go >/dev/null 2>&1; then
		GO_CMD="$(command -v go)"
	elif [[ -x "/c/Program Files/Go/bin/go.exe" ]]; then
		GO_CMD="/c/Program Files/Go/bin/go.exe"
	elif [[ -x "/mnt/c/Program Files/Go/bin/go.exe" ]]; then
		GO_CMD="/mnt/c/Program Files/Go/bin/go.exe"
	else
		echo "go not found; install Go or set GO_CMD" >&2
		exit 1
	fi
fi
GO_BIN="$("$GO_CMD" env GOPATH)/bin"
PATH="$PATH:$GO_BIN"

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
if [[ "$ROOT_DIR" == /mnt/[a-zA-Z]/* ]] && command -v powershell.exe >/dev/null 2>&1; then
	ROOT_DIR_WIN="$(to_windows_path "$ROOT_DIR")"
	PROTO_SRC_WIN="$(to_windows_path "$PROTO_SRC")"
	OUT_DIR_WIN="$(to_windows_path "$OUT_DIR")"
	powershell.exe -NoProfile -Command "\$env:PATH = \"\$env:USERPROFILE\\go\\bin;\" + \$env:PATH; Push-Location '$PROTO_SRC_WIN'; try { protoc -I . -I '$ROOT_DIR_WIN' --openapiv2_out='$OUT_DIR_WIN' --openapiv2_opt=logtostderr=true,generate_unbound_methods=true,allow_merge=true,merge_file_name=cordum ${PROTO_FILES[*]} } finally { Pop-Location }"
else
	cd "$PROTO_SRC"
	protoc \
		-I . \
		-I "$ROOT_DIR" \
		--openapiv2_out="$OUT_DIR" \
		--openapiv2_opt=logtostderr=true,generate_unbound_methods=true,allow_merge=true,merge_file_name=cordum \
		"${PROTO_FILES[@]}"
fi

if ! command -v npx >/dev/null 2>&1; then
	echo "npx not found; install Node.js/npm to validate $CANONICAL_SPEC" >&2
	exit 1
fi

npx --yes @redocly/cli@latest lint "$LINT_SPEC"

echo "openapi artifacts written to $OUT_DIR"
