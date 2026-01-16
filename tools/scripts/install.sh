#!/usr/bin/env bash
set -euo pipefail

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
docker compose version >/dev/null 2>&1 || { echo "docker compose plugin required" >&2; exit 1; }

if [ -d "${DEST_DIR}/.git" ]; then
  echo "using existing repo at ${DEST_DIR}"
else
  echo "cloning ${REPO_URL} to ${DEST_DIR}"
  git clone --depth 1 --branch "${VERSION}" "${REPO_URL}" "${DEST_DIR}"
fi

cd "${DEST_DIR}"

if [ "${USE_RELEASE_IMAGES}" = "1" ]; then
  echo "starting from Docker Hub images (CORDUM_VERSION=${CORDUM_VERSION})"
  export CORDUM_VERSION
  docker compose -f docker-compose.release.yml pull
  docker compose -f docker-compose.release.yml up -d
else
  echo "building from source"
  docker compose build
  docker compose up -d
fi

cat <<'EOF'
Cordum is up.
- Dashboard: http://localhost:8082
- API: http://localhost:8081
EOF
