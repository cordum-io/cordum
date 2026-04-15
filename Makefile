PROTO_SRC = core/protocol/proto/v1
PB_OUT_CORE = core/protocol/pb/v1
PROTO_OUT_CORE = $(abspath $(PB_OUT_CORE))
PROTO_FILES = api.proto context.proto output_policy.proto
OPENAPI_OUT = docs/api/openapi

BIN_DIR ?= bin
SERVICES = cordum-api-gateway cordum-scheduler cordum-safety-kernel cordum-workflow-engine cordum-context-engine cordum-mcp cordumctl

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

docker:
	@test -n "$(SERVICE)" || (echo "SERVICE is required (e.g. SERVICE=cordum-scheduler)" && exit 1)
	@BASE="$(SERVICE)"; BASE="$${BASE#cordum-}"; \
	IMAGE="${IMAGE:-cordum-$${BASE}}"; \
	docker build --build-arg SERVICE="$(SERVICE)" --build-arg VERSION="$(VERSION)" \
		--build-arg COMMIT="$(COMMIT)" --build-arg BUILD_DATE="$(BUILD_DATE)" \
		-t "$$IMAGE" .

smoke:
	./tools/scripts/platform_smoke.sh

dev-up:
	docker compose up -d --build

dev-down:
	docker compose down

dev-logs:
	docker compose logs -f

help:
	@echo ""
	@echo "Cordum Makefile targets:"
	@echo ""
	@echo "  make help               Show this help message"
	@echo "  make build              Build all services (runs proto first)"
	@echo "  make build SERVICE=X    Build a single service (e.g. SERVICE=cordum-scheduler)"
	@echo "  make proto              Regenerate protobuf Go code"
	@echo "  make test               Run all Go tests"
	@echo "  make test-integration   Run integration tests (requires Docker)"
	@echo "  make coverage           Full coverage report"
	@echo "  make coverage-core      Core coverage check (80% minimum)"
	@echo "  make openapi            Regenerate protobuf swagger + validate cordum-api.yaml"
	@echo "  make docker SERVICE=X   Build Docker image for a service"
	@echo "  make smoke              Run platform smoke tests"
	@echo "  make dev-up             Start all services via docker compose"
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

.PHONY: help proto build build-all $(SERVICES:%=build-%) test test-integration coverage coverage-core openapi docker smoke dev-up dev-down dev-logs soak-ws soak-ws-quick soak-ws-full
