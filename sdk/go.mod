module github.com/yaront1111/coretex-os/sdk

go 1.24.0

toolchain go1.24.11

require (
	github.com/coretexos/cap/v2 v2.0.6
	github.com/google/uuid v1.6.0
	github.com/nats-io/nats.go v1.48.0
	google.golang.org/grpc v1.64.0
	google.golang.org/protobuf v1.36.8
)

require (
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/nats-io/nkeys v0.4.11 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/net v0.22.0 // indirect
	golang.org/x/sys v0.32.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240318140521-94a12d6c2237 // indirect
)

replace github.com/coretexos/cap/v2 => ../../cap
