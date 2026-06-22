#!/usr/bin/env bash
# Landing-side vet gate — run BEFORE squash-merging the shared consolidation
# branch (e.g. moe/epic-*/land-deliverables) into main.
#
# Why this exists (postmortem: task-5c18f890 / task-fd1c736c; see Serena
# mem:gotcha-moe-batch-cascade-drops-claimed-files):
# a consolidation/checkpoint can COMMIT a *_test.go file while DROPPING the
# production impl it references (a "cascade-drop"). The committed tree then
# references an undefined symbol. `go build ./...` does NOT catch this — it
# ignores _test.go files — so the break sits on the shared branch and only
# surfaces ~10 min later in the CI `test` (-race) / `lint` (vet) jobs, where it
# blocks unrelated tasks that share the package.
#
# `go vet ./...` DOES compile the test variant, so it fails in seconds on the
# dropped-impl class. This gate runs vet over BOTH Go modules: the root module
# AND the separate sdk/ module (root `go vet ./...` does not descend into
# nested modules, so a dropped SDK impl would otherwise slip through).
#
# Usage:
#   bash tools/scripts/landing_vet_gate.sh
# Exit 0 = vet clean across all modules, safe to squash-merge.
# Exit 1 = a committed test references a missing symbol (or another vet
#          failure) — DO NOT merge; re-land the dropped impl (or fix vet) first.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

echo "== landing vet gate =="
echo "repo root: $REPO_ROOT"

fail=0

echo "--- go vet ./...  (root module) ---"
if ! go vet ./...; then
	echo "::error:: root-module 'go vet ./...' failed — a committed *_test.go may reference a dropped impl symbol (go build ./... does NOT catch this). DO NOT squash-merge." >&2
	fail=1
fi

if [ -f sdk/go.mod ]; then
	echo "--- go vet ./...  (sdk/ module) ---"
	if ! ( cd sdk && go vet ./... ); then
		echo "::error:: sdk-module 'go vet ./...' failed. DO NOT squash-merge." >&2
		fail=1
	fi
fi

if [ "$fail" -ne 0 ]; then
	echo "LANDING VET GATE: FAIL — refuse to squash-merge until vet is clean." >&2
	exit 1
fi

echo "LANDING VET GATE: PASS — vet clean across all modules; safe to squash-merge."
