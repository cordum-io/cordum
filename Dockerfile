# syntax=docker/dockerfile:1
ARG GO_VERSION=1.24

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
    --mount=type=cache,target=/root/.cache/go-build \
    GOFLAGS=-mod=mod go mod download

COPY . .
RUN if [ "${USE_LOCAL_CAP}" != "1" ]; then \
      go mod edit -dropreplace github.com/cordum-io/cap/v2; \
    fi
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# SERVICE must match a directory under cmd/ (e.g. cordum-scheduler).
ARG SERVICE
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN test -n "${SERVICE}" || (echo "SERVICE build arg required" && false)
# Resolve target dir.
RUN TARGET="${SERVICE}" ; \
    if [ -d "./cmd/${TARGET}" ]; then : ; \
    else echo "Service dir ./cmd/${TARGET} not found for SERVICE=${SERVICE}" && false; fi

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOFLAGS=-mod=mod CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
      -ldflags "-s -w -X github.com/cordum/cordum/core/infra/buildinfo.Version=${VERSION} -X github.com/cordum/cordum/core/infra/buildinfo.Commit=${COMMIT} -X github.com/cordum/cordum/core/infra/buildinfo.Date=${BUILD_DATE}" \
      -o /out/${SERVICE} ./cmd/${SERVICE}

FROM alpine:3.19
RUN apk add --no-cache ca-certificates git
RUN adduser -D -u 65532 cordum
USER cordum
WORKDIR /home/cordum

ARG SERVICE
COPY --from=builder /out/${SERVICE} /usr/local/bin/app

ENV NATS_URL=nats://nats:4222 \
    REDIS_URL=redis://redis:6379 \
    SAFETY_KERNEL_ADDR=cordum-safety-kernel:50051

ENTRYPOINT ["/usr/local/bin/app"]
