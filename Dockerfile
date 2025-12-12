# syntax=docker/dockerfile:1
ARG GO_VERSION=1.24

FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
# Bring in vendored CAP module for replace path resolution before download.
COPY third_party/coretexos/cap ./third_party/coretexos/cap
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

# SERVICE must match a directory under cmd/ (e.g. coretex-scheduler).
ARG SERVICE
RUN test -n "${SERVICE}" || (echo "SERVICE build arg required" && false)
# Allow coretex-* names by normalizing to cortex-* directory names.
RUN TARGET="${SERVICE/coretex-/cortex-}" && test -d "./cmd/${TARGET}" || (echo "Service dir ./cmd/${TARGET} not found for SERVICE=${SERVICE}" && false)

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    TARGET="${SERVICE/coretex-/cortex-}" && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/${SERVICE} ./cmd/${TARGET}

FROM alpine:3.19
RUN apk add --no-cache ca-certificates git
RUN adduser -D -u 65532 coretex
USER coretex
WORKDIR /home/coretex

ARG SERVICE
COPY --from=builder /out/${SERVICE} /usr/local/bin/app

ENV NATS_URL=nats://nats:4222 \
    REDIS_URL=redis://redis:6379 \
    SAFETY_KERNEL_ADDR=coretex-safety-kernel:50051

ENTRYPOINT ["/usr/local/bin/app"]
