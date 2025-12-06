PROTO_SRC = api/proto/v1
PB_OUT    = pkg/pb/v1

proto:
	protoc \
		-I . \
		-I $(PROTO_SRC) \
		--go_out=$(PB_OUT) --go_opt=paths=source_relative \
		$(PROTO_SRC)/job.proto \
		$(PROTO_SRC)/heartbeat.proto \
		$(PROTO_SRC)/packet.proto

build-scheduler: proto
	go build -o bin/cortex-scheduler ./cmd/cortex-scheduler

build-worker-echo: proto
	go build -o bin/cortex-worker-echo ./cmd/cortex-worker-echo

build: build-scheduler build-worker-echo

.PHONY: proto build build-scheduler build-worker-echo
