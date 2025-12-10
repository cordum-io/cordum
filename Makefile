PROTO_SRC = api/proto/v1
PB_OUT    = pkg/pb/v1

proto:
	PATH="$$PATH:$(HOME)/go/bin" protoc \
		-I . \
		-I $(PROTO_SRC) \
		--go_out=$(PB_OUT) --go_opt=paths=source_relative \
		--go-grpc_out=$(PB_OUT) --go-grpc_opt=paths=source_relative \
		$(PROTO_SRC)/safety.proto \
		$(PROTO_SRC)/api.proto \
		$(PROTO_SRC)/job.proto \
		$(PROTO_SRC)/heartbeat.proto \
		$(PROTO_SRC)/packet.proto

build-scheduler: proto
	go build -o bin/cortex-scheduler ./cmd/cortex-scheduler

build-worker-echo: proto
	go build -o bin/cortex-worker-echo ./cmd/cortex-worker-echo

build-worker-chat: proto
	go build -o bin/cortex-worker-chat ./cmd/cortex-worker-chat

build-worker-chat-advanced: proto
	go build -o bin/cortex-worker-chat-advanced ./cmd/cortex-worker-chat-advanced

build-worker-orchestrator: proto
	go build -o bin/cortex-worker-orchestrator ./cmd/cortex-worker-orchestrator

build-worker-code-llm: proto
	go build -o bin/cortex-worker-code-llm ./cmd/cortex-worker-code-llm

build-api-gateway: proto
	go build -o bin/cortex-api-gateway ./cmd/cortex-api-gateway

build-safety-kernel: proto
	go build -o bin/cortex-safety-kernel ./cmd/cortex-safety-kernel

build-worker-repo-scan: proto
	go build -o bin/cortex-worker-repo-scan ./cmd/cortex-worker-repo-scan

build-worker-repo-partition: proto
	go build -o bin/cortex-worker-repo-partition ./cmd/cortex-worker-repo-partition

build-worker-repo-lint: proto
	go build -o bin/cortex-worker-repo-lint ./cmd/cortex-worker-repo-lint

build-worker-repo-tests: proto
	go build -o bin/cortex-worker-repo-tests ./cmd/cortex-worker-repo-tests

build-worker-repo-report: proto
	go build -o bin/cortex-worker-repo-report ./cmd/cortex-worker-repo-report

build-worker-repo-orchestrator: proto
	go build -o bin/cortex-worker-repo-orchestrator ./cmd/cortex-worker-repo-orchestrator

build: build-scheduler build-worker-echo build-worker-chat build-worker-chat-advanced build-worker-orchestrator build-worker-code-llm build-api-gateway build-safety-kernel build-worker-repo-scan build-worker-repo-partition build-worker-repo-lint build-worker-repo-tests build-worker-repo-report build-worker-repo-orchestrator

dev-up:
	docker-compose up -d --build

dev-down:
	docker-compose down

dev-logs:
	docker-compose logs -f

dev-test-echo:
	go run ./tools/scripts/send_echo_job.go

.PHONY: proto build build-scheduler build-worker-echo build-worker-chat build-worker-chat-advanced build-worker-orchestrator build-worker-code-llm build-api-gateway build-safety-kernel dev-up dev-down dev-logs dev-test-echo
