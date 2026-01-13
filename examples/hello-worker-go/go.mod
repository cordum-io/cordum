module github.com/cordum-io/cordum/examples/hello-worker-go

go 1.24.0

toolchain go1.24.11

require (
	github.com/cordum/cordum/sdk v0.0.0
	github.com/redis/go-redis/v9 v9.17.2
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)

replace github.com/cordum/cordum/sdk => ../../sdk
