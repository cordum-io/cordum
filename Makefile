PROTO_SRC = core/protocol/proto/v1
PB_OUT_CORE = core/protocol/pb/v1
PROTO_OUT_CORE = $(abspath $(PB_OUT_CORE))
PROTO_FILES = api.proto context.proto output_policy.proto
OPENAPI_OUT = docs/api/openapi

BIN_DIR ?= bin
# GOEXE is '.exe' on Windows and empty elsewhere (via `go env GOEXE`, which respects
# GOOS) so explicit `-o` outputs get the right extension and cross-compiles stay correct.
GOEXE := $(shell go env GOEXE)
SERVICES = cordum-api-gateway cordum-scheduler cordum-safety-kernel cordum-workflow-engine cordum-context-engine cordum-mcp cordumctl cordum-hook cordum-agentd cordum-claude

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u "+%Y-%m-%dT%H:%M:%SZ")

LDFLAGS = -s -w \
	-X 'github.com/cordum/cordum/core/infra/buildinfo.Version=$(VERSION)' \
	-X 'github.com/cordum/cordum/core/infra/buildinfo.Commit=$(COMMIT)' \
	-X 'github.com/cordum/cordum/core/infra/buildinfo.Date=$(BUILD_DATE)'

proto:
	@mkdir -p $(PROTO_OUT_CORE)
	cd $(PROTO_SRC) && PATH="$$PATH:$(shell go env GOPATH)/bin" protoc \
		-I . \
		-I $(CURDIR) \
		--go_out=$(PROTO_OUT_CORE) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_OUT_CORE) --go-grpc_opt=paths=source_relative \
		$(PROTO_FILES)

build: proto
	@if [ -n "$(SERVICE)" ]; then \
		$(MAKE) build-$(SERVICE); \
	else \
		$(MAKE) build-all; \
	fi

build-all: $(SERVICES:%=build-%)

build-%: proto
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$*$(GOEXE) ./cmd/$*

test:
	go test ./...

test-integration:
	go test -tags=integration ./...

coverage:
	./tools/scripts/coverage.sh

coverage-core:
	MIN_COVERAGE=80 ./tools/scripts/check_coverage.sh

openapi:
	./tools/scripts/gen_openapi.sh

openapi-validate:
	./tools/scripts/openapi-validate.sh

# docs-tables regenerates the code-derived enumeration tables (RBAC permissions,
# REST route index, MCP tool catalog) into the in-repo docs between their
# BEGIN/END markers. docs-tables-check fails (for CI) when they are out of date.
docs-tables:
	go run ./tools/scripts/gen_docs_tables

docs-tables-check:
	go run ./tools/scripts/gen_docs_tables -check

docker:
	@test -n "$(SERVICE)" || (echo "SERVICE is required (e.g. SERVICE=cordum-scheduler)" && exit 1)
	@BASE="$(SERVICE)"; BASE="$${BASE#cordum-}"; \
	IMAGE="${IMAGE:-cordum-$${BASE}}"; \
	docker build --build-arg SERVICE="$(SERVICE)" --build-arg VERSION="$(VERSION)" \
		--build-arg COMMIT="$(COMMIT)" --build-arg BUILD_DATE="$(BUILD_DATE)" \
		-t "$$IMAGE" .

smoke:
	./tools/scripts/platform_smoke.sh

edge-fake-hook-e2e:
	bash tools/scripts/edge_fake_hook_e2e.sh

verify-images:
	CORDUM_VERIFY_IMAGES=1 ./tools/scripts/verify_published_images.sh

demo-quickstart-test:
	CORDUM_INTEGRATION=1 ./demo/quickstart/integration_test.sh

demo-mock-bank-test:
	CORDUM_INTEGRATION=1 ./demo/mock-bank/integration_test.sh

dev-up:
	docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --build

dev-down:
	docker compose down

dev-logs:
	docker compose logs -f

# edge-rebuild-e2e rebuilds the local Edge binaries AND the api-gateway image
# in lockstep, then recreates the api-gateway container. Run BEFORE every
# `CORDUM_INTEGRATION=1 bash tools/scripts/edge_fake_hook_e2e.sh` invocation
# whenever cordum-hook, cordum-agentd, or any code under core/edge/* /
# core/controlplane/gateway/* has changed since the running stack was last
# built. EDGE-044 root cause: the gateway-side classifier lives in the
# api-gateway image; rebuilding only ./bin/cordum-hook + ./bin/cordum-agentd
# produces fresh agentd talking to a stale gateway, and the post-EDGE-041
# `_redacted` keys silently miss the bare-key classifier in the old image,
# which falls through to default-deny and breaks every rule match.
#
# EDGE-044 (extended): safety-kernel is rebuilt in lockstep too. The kernel makes
# the actual ALLOW/DENY decision over gRPC; a STALE kernel (e.g. the :latest base
# under a source-rebuilt gateway) silently FALSE-ALLOWs actions it should DENY
# (denied_count stays 0). Both governance components on the Edge decision path
# (api-gateway classifier + safety-kernel decision) must be fresh together.
edge-rebuild-e2e:
	go build -o ./bin/cordum-hook$(GOEXE) ./cmd/cordum-hook
	go build -o ./bin/cordum-agentd$(GOEXE) ./cmd/cordum-agentd
	go build -o ./bin/cordumctl$(GOEXE) ./cmd/cordumctl
	go build -o ./bin/cordum-claude$(GOEXE) ./cmd/cordum-claude
	docker compose -f docker-compose.yml -f docker-compose.dev.yml build api-gateway safety-kernel
	docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --no-deps api-gateway safety-kernel

help:
	@echo ""
	@echo "Cordum Makefile targets:"
	@echo ""
	@echo "  make help               Show this help message"
	@echo "  make build              Build all services (runs proto first)"
	@echo "  make build SERVICE=X    Build a single service (e.g. SERVICE=cordum-scheduler, cordum-hook, or cordum-agentd)"
	@echo "  make proto              Regenerate protobuf Go code"
	@echo "  make test               Run all Go tests"
	@echo "  make test-integration   Run integration tests (requires Docker)"
	@echo "  make coverage           Full coverage report"
	@echo "  make coverage-core      Core coverage check (80% minimum)"
	@echo "  make openapi            Validate cordum-api.yaml (Redocly lint)"
	@echo "  make docker SERVICE=X   Build Docker image for a service"
	@echo "  make smoke              Run platform smoke tests"
	@echo "  make edge-fake-hook-e2e Run Edge fake-hook E2E (CI-safe; SKIP without CORDUM_INTEGRATION=1)"
	@echo "  make verify-images      Verify published GHCR images (pull + cosign + multi-arch)"
	@echo "  make demo-quickstart-test  End-to-end test for the demo-quickstart pack"
	@echo "  make demo-mock-bank-test   End-to-end test for the demo-mock-bank pack (all three verdicts)"
	@echo "  make dev-up             Start all services via docker compose (with local rebuild)"
	@echo "  make dev-down           Stop all services"
	@echo "  make dev-logs           Tail docker compose logs"
	@echo "  make soak-ws            10-minute WebSocket soak test"
	@echo "  make soak-ws-quick      2-minute quick WebSocket soak test"
	@echo "  make soak-ws-full       2-hour full WebSocket soak test"
	@echo "  make release-local      Build dev cordum-hook/agentd/claude + TEST-ONLY-signed manifest (EDGE-151)"
	@echo ""

soak-ws:
	@echo "Running 10-minute WebSocket soak test..."
	bash tools/scripts/ws_soak_test.sh default

soak-ws-quick:
	@echo "Running 2-minute quick WebSocket soak test..."
	bash tools/scripts/ws_soak_test.sh quick

soak-ws-full:
	@echo "Running 2-hour full WebSocket soak test..."
	bash tools/scripts/ws_soak_test.sh full

# EDGE-151 — host-local dev release: cordum-hook + cordum-agentd +
# cordum-claude with a SHA256SUMS manifest detached-signed by the
# TEST-ONLY key under tools/test-keys/. NOT for production. install.sh
# only accepts these via --dev-allow-unsigned AND a fingerprint match
# against the TEST-ONLY value baked in via -ldflags. Invoked via `bash`
# so the target works regardless of the script's filesystem executable
# bit (git ls-files -s reports 100644 on some platforms / CI checkouts).
release-local:
	@bash tools/scripts/release-local.sh

# Do NOT add build-% / $(SERVICES:%=build-%) here: make skips pattern-rule search for phony targets (manual sec. 4.6), silently disabling the build-% recipe above ("Nothing to be done for build-<svc>"). Outputs live in bin/, so build-% is always out-of-date and rebuilds anyway.
.PHONY: help proto build build-all test test-integration coverage coverage-core openapi openapi-validate docs-tables docs-tables-check docker smoke verify-images demo-quickstart-test demo-mock-bank-test dev-up dev-down dev-logs edge-rebuild-e2e soak-ws soak-ws-quick soak-ws-full release-local
