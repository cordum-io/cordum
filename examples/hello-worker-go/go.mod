module github.com/cordum-io/cordum/examples/hello-worker-go

go 1.24.0

toolchain go1.24.11

require (
	github.com/cordum/cordum/sdk v0.0.0
	github.com/redis/go-redis/v9 v9.7.0
)

replace github.com/cordum/cordum/sdk => ../../sdk
