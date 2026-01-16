FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
COPY sdk ./sdk
RUN go mod download
COPY examples/hello-worker-go ./examples/hello-worker-go
RUN CGO_ENABLED=0 go build -o /bin/worker ./examples/hello-worker-go

FROM alpine:3.20
COPY --from=builder /bin/worker /usr/local/bin/worker
ENTRYPOINT ["/usr/local/bin/worker"]
