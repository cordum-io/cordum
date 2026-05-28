FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
COPY sdk ./sdk
RUN go mod download
COPY examples/multi-topic-worker-go ./examples/multi-topic-worker-go
RUN CGO_ENABLED=0 go build -trimpath -o /bin/worker ./examples/multi-topic-worker-go

FROM alpine:3.21
COPY --from=builder /bin/worker /usr/local/bin/worker
ENTRYPOINT ["/usr/local/bin/worker"]
