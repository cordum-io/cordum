FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
COPY sdk ./sdk
RUN go mod download
COPY examples/demo-guardrails/worker ./examples/demo-guardrails/worker
RUN CGO_ENABLED=0 go build -o /bin/worker ./examples/demo-guardrails/worker

FROM alpine:3.20
COPY --from=builder /bin/worker /usr/local/bin/worker
ENTRYPOINT ["/usr/local/bin/worker"]
