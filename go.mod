module github.com/cordum/cordum

go 1.24.0

toolchain go1.24.11

require (
	github.com/alicebob/miniredis/v2 v2.34.0
	github.com/cordum-io/cap/v2 v2.0.9
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/nats-io/nats.go v1.48.0
	github.com/prometheus/client_golang v1.23.2
	github.com/redis/go-redis/v9 v9.5.1
	github.com/santhosh-tekuri/jsonschema/v5 v5.3.1
	github.com/cordum/cordum/sdk v0.0.0
	google.golang.org/grpc v1.64.0
	google.golang.org/protobuf v1.36.8
	gopkg.in/yaml.v3 v3.0.1
)

// Use published CAP module
require (
	github.com/alicebob/gopher-json v0.0.0-20230218143504-906a9b012302 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/klauspost/compress v1.18.1 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/nats-io/nkeys v0.4.11 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/crypto v0.43.0 // indirect
	golang.org/x/net v0.45.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.30.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240318140521-94a12d6c2237 // indirect
)

replace github.com/cordum-io/cap/v2 => ../cap

replace github.com/cordum/cordum/sdk => ./sdk
