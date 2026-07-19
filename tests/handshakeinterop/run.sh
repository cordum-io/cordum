#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 /path/to/cap-source" >&2
  exit 2
fi
: "${CAP_HANDSHAKE_REDIS_URL:?CAP_HANDSHAKE_REDIS_URL is required}"

cordum_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cap_root="$(cd "$1" && pwd)"

assert_cap_revision() {
  local cap_sha cap_version suffix pinned_sha
  cap_sha="$(git -C "$cap_root" rev-parse HEAD)"
  cap_version="$(cd "$cordum_root" && go list -m -f '{{.Version}}' github.com/cordum-io/cap/v2)"
  suffix="${cap_version##*-}"
  [[ "$suffix" =~ ^[0-9a-f]{12}$ ]] || {
    echo "Cordum CAP version lacks a 12-hex revision suffix: $cap_version" >&2
    return 1
  }
  pinned_sha="$(git -C "$cap_root" rev-parse "${suffix}^{commit}")"
  [[ "$cap_sha" == "$pinned_sha" ]] || {
    echo "CAP source $cap_sha does not match Cordum pin $pinned_sha" >&2
    return 1
  }
  if [[ -n "${CAP_HANDSHAKE_CAP_SHA:-}" && "$cap_sha" != "$CAP_HANDSHAKE_CAP_SHA" ]]; then
    echo "CAP source $cap_sha does not match CAP_HANDSHAKE_CAP_SHA" >&2
    return 1
  fi
}

assert_cap_revision
temp_root="$(mktemp -d "${RUNNER_TEMP:-/tmp}/cap-handshake-interop.XXXXXX")"
cleanup() { rm -rf -- "$temp_root"; }
trap cleanup EXIT

build_go_client() {
  local consumer="$temp_root/go-consumer"
  local proxy="$temp_root/go-proxy"
  local sha version proxy_url
  sha="$(git -C "$cap_root" rev-parse --short=12 HEAD)"
  version="v2.5.3-handshake.0.g${sha}"
  python "$cap_root/tools/onboarding/build_go_proxy.py" \
    --root "$cap_root" --output "$proxy" --version "$version"
  mkdir -p "$consumer/client" "$consumer/.gomodcache"
  cp "$cap_root/test/interop/handshake/go_client.go" "$consumer/client/"
  cp "$cap_root/test/interop/handshake/go_client_mutations.go" "$consumer/client/"
  proxy_url="$(python -c 'import pathlib,sys; print(pathlib.Path(sys.argv[1]).resolve().as_uri())' "$proxy")"
  (
    cd "$consumer"
    go mod init example.com/cap-handshake-interop
    env GOWORK=off GOMODCACHE="$consumer/.gomodcache" GOPROXY="$proxy_url" GOSUMDB=off \
      go mod download "github.com/cordum-io/cap/v2@$version"
    go mod edit -require="github.com/cordum-io/cap/v2@$version"
    env GOWORK=off GOMODCACHE="$consumer/.gomodcache" \
      GOPROXY="$proxy_url,https://proxy.golang.org,direct" \
      GONOSUMDB=github.com/cordum-io/cap/v2 go mod tidy
    env GOWORK=off GOMODCACHE="$consumer/.gomodcache" \
      GOPROXY="$proxy_url,https://proxy.golang.org,direct" \
      GONOSUMDB=github.com/cordum-io/cap/v2 go mod vendor
    GOWORK=off GOPROXY=off go build -mod=vendor -o "$temp_root/go-client" ./client
  )
  export CAP_HANDSHAKE_GO_CLIENT="$temp_root/go-client"
}

build_python_client() {
  local dist="$temp_root/python-dist"
  local source_tree="$temp_root/python-source"
  local builder="$temp_root/python-builder-venv"
  local venv="$temp_root/python-venv"
  mkdir -p "$dist" "$source_tree"
  git -C "$cap_root" archive HEAD sdk/python | tar -x -C "$source_tree"
  python -m venv "$builder"
  "$builder/bin/python" -m pip install --disable-pip-version-check "build==1.2.2.post1"
  "$builder/bin/python" -m build --outdir "$dist" "$source_tree/sdk/python"
  mapfile -t wheels < <(compgen -G "$dist/*.whl")
  [[ "${#wheels[@]}" -eq 1 ]]
  python -m venv "$venv"
  "$venv/bin/python" -m pip install --disable-pip-version-check --no-cache-dir "${wheels[0]}"
  "$venv/bin/python" -m pip check
  cp "$cap_root/test/interop/handshake/python_client.py" "$temp_root/python_client.py"
  export CAP_HANDSHAKE_PYTHON_BIN="$venv/bin/python"
  export CAP_HANDSHAKE_PYTHON_CLIENT="$temp_root/python_client.py"
}

build_node_client() {
  local artifacts="$temp_root/node-artifacts"
  local consumer="$temp_root/node-consumer"
  local source_tree="$temp_root/node-source"
  mkdir -p "$artifacts" "$consumer" "$source_tree"
  git -C "$cap_root" archive HEAD sdk/node proto | tar -x -C "$source_tree"
  (
    cd "$source_tree/sdk/node"
    npm ci
    npm run build
    npm pack --pack-destination "$artifacts"
  )
  mapfile -t packages < <(compgen -G "$artifacts/*.tgz")
  [[ "${#packages[@]}" -eq 1 ]]
  (
    cd "$consumer"
    npm init -y >/dev/null
    npm install --ignore-scripts "${packages[0]}" "typescript@5.9.3" "@types/node@18.19.130"
    cp "$cap_root/test/interop/handshake/node_client.ts" .
    cat >tsconfig.json <<'JSON'
{"compilerOptions":{"target":"ES2022","module":"CommonJS","moduleResolution":"Node","strict":true,"esModuleInterop":true,"skipLibCheck":true,"outDir":"dist"},"include":["node_client.ts"]}
JSON
    ./node_modules/.bin/tsc --project tsconfig.json
  )
  export CAP_HANDSHAKE_NODE_BIN="$(command -v node)"
  export CAP_HANDSHAKE_NODE_CLIENT="$consumer/dist/node_client.js"
}

build_go_client
build_python_client
build_node_client

cd "$cordum_root"
go test -v -tags=handshakeinterop -count=1 -timeout=4m ./tests/handshakeinterop
