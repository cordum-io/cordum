PROTO_SRC = core/protocol/proto/v1
PB_OUT    = sdk/gen/go/cordum/v1
PB_OUT_CORE = core/protocol/pb/v1
PROTO_OUT = $(abspath $(PB_OUT))
PROTO_OUT_CORE = $(abspath $(PB_OUT_CORE))
PROTO_FILES = api.proto context.proto

BIN_DIR ?= bin
SERVICES = cordum-api-gateway cordum-scheduler cordum-safety-kernel cordum-workflow-engine cordum-context-engine cordumctl

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u "+%Y-%m-%dT%H:%M:%SZ")

LDFLAGS = -s -w \
	-X 'github.com/cordum/cordum/core/infra/buildinfo.Version=$(VERSION)' \
	-X 'github.com/cordum/cordum/core/infra/buildinfo.Commit=$(COMMIT)' \
	-X 'github.com/cordum/cordum/core/infra/buildinfo.Date=$(BUILD_DATE)'

proto:
	@mkdir -p $(PROTO_OUT) $(PROTO_OUT_CORE)
	cd $(PROTO_SRC) && PATH="$$PATH:$(HOME)/go/bin" protoc \
		-I . \
		-I $(CURDIR) \
		--go_out=$(PROTO_OUT_CORE) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_OUT_CORE) --go-grpc_opt=paths=source_relative \
		$(PROTO_FILES)
	cd $(PROTO_SRC) && PATH="$$PATH:$(HOME)/go/bin" protoc \
		-I . \
		-I $(CURDIR) \
		--go_out=$(PROTO_OUT) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_OUT) --go-grpc_opt=paths=source_relative \
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

.PHONY: proto build build-all $(SERVICES:%=build-%) test test-integration docker smoke dev-up dev-down dev-logs
