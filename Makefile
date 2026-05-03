PROTO_SRC = core/protocol/proto/v1
PB_OUT_CORE = core/protocol/pb/v1
PROTO_OUT_CORE = $(abspath $(PB_OUT_CORE))
PROTO_FILES = api.proto context.proto output_policy.proto
OPENAPI_OUT = docs/api/openapi

BIN_DIR ?= bin
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
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$* ./cmd/$*

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
edge-rebuild-e2e:
	go build -o ./bin/cordum-hook ./cmd/cordum-hook
	go build -o ./bin/cordum-agentd ./cmd/cordum-agentd
	go build -o ./bin/cordumctl ./cmd/cordumctl
	go build -o ./bin/cordum-claude ./cmd/cordum-claude
	docker compose -f docker-compose.yml -f docker-compose.dev.yml build api-gateway
	docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d --no-deps api-gateway

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
	@echo ""

soak-ws:
	@echo "Running 10-minute WebSocket soak test..."
	./tools/scripts/ws_soak_test.sh default

soak-ws-quick:
	@echo "Running 2-minute quick WebSocket soak test..."
	./tools/scripts/ws_soak_test.sh quick

soak-ws-full:
	@echo "Running 2-hour full WebSocket soak test..."
	./tools/scripts/ws_soak_test.sh full

.PHONY: help proto build build-all $(SERVICES:%=build-%) test test-integration coverage coverage-core openapi openapi-validate docker smoke verify-images demo-quickstart-test demo-mock-bank-test dev-up dev-down dev-logs edge-rebuild-e2e soak-ws soak-ws-quick soak-ws-full
