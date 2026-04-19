# syntax=docker/dockerfile:1
#
# Build the demo-quickstart greeter worker.
#
# Mirrors demo/mock-bank/Dockerfile so the GHCR publish matrix in
# .github/workflows/docker.yml can build it with the same SERVICE
# convention. The image is intentionally small — alpine + a single static
# Go binary — because it is pulled by every quickstart user.
ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
COPY sdk/go.mod ./sdk/go.mod
ARG USE_LOCAL_CAP=0
ARG GOPRIVATE=github.com/cordum-io/cap/v2
ARG GONOSUMDB=github.com/cordum-io/cap/v2
ARG GONOPROXY=github.com/cordum-io/cap/v2
ENV GOPRIVATE=${GOPRIVATE}
ENV GONOSUMDB=${GONOSUMDB}
ENV GONOPROXY=${GONOPROXY}
RUN if [ "${USE_LOCAL_CAP}" != "1" ]; then \
      go mod edit -dropreplace github.com/cordum-io/cap/v2; \
    fi
RUN --mount=type=cache,target=/go/pkg/mod \
    GOFLAGS=-mod=mod go mod download

COPY . .
RUN if [ "${USE_LOCAL_CAP}" != "1" ]; then \
      go mod edit -dropreplace github.com/cordum-io/cap/v2; \
    fi
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    GOFLAGS=-mod=mod CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} go build \
      -ldflags "-s -w \
        -X 'github.com/cordum/cordum/core/infra/buildinfo.Version=${VERSION}' \
        -X 'github.com/cordum/cordum/core/infra/buildinfo.Commit=${COMMIT}' \
        -X 'github.com/cordum/cordum/core/infra/buildinfo.Date=${BUILD_DATE}'" \
      -o /out/demo-quickstart-worker ./demo/quickstart/worker

FROM alpine:3.21
RUN apk add --no-cache ca-certificates wget
RUN adduser -D -u 65532 cordum
USER cordum
WORKDIR /home/cordum

COPY --from=builder /out/demo-quickstart-worker /usr/local/bin/demo-quickstart-worker

ENV NATS_URL=nats://nats:4222 \
    REDIS_URL=

EXPOSE 9091
ENTRYPOINT ["/usr/local/bin/demo-quickstart-worker"]
