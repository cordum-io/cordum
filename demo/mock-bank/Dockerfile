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
    GOFLAGS=-mod=mod go mod download

COPY . .
RUN if [ "${USE_LOCAL_CAP}" != "1" ]; then \
      go mod edit -dropreplace github.com/cordum-io/cap/v2; \
    fi
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

RUN --mount=type=cache,target=/go/pkg/mod \
    GOFLAGS=-mod=mod CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
      -ldflags "-s -w" \
      -o /out/mock-bank-worker ./demo/mock-bank/worker

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
RUN adduser -D -u 65532 cordum
USER cordum
WORKDIR /home/cordum

COPY --from=builder /out/mock-bank-worker /usr/local/bin/app

ENV NATS_URL=nats://nats:4222 \
    REDIS_URL=

ENTRYPOINT ["/usr/local/bin/app"]
