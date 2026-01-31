package runtime

import capruntime "github.com/cordum-io/cap/v2/sdk/go/runtime"

type (
	Agent                      = capruntime.Agent
	BlobStore                  = capruntime.BlobStore
	Context                    = capruntime.Context
	Handler[TIn any, TOut any] = capruntime.Handler[TIn, TOut]
	InMemoryBlobStore          = capruntime.InMemoryBlobStore
	JobOption                  = capruntime.JobOption
	NATSConn                   = capruntime.NATSConn
	RedisBlobStore             = capruntime.RedisBlobStore
)

// Register wires a typed handler to a topic using the CAP runtime.
func Register[TIn any, TOut any](agent *Agent, topic string, handler Handler[TIn, TOut], opts ...JobOption) {
	capruntime.Register(agent, topic, handler, opts...)
}

// WithRetries overrides the default retry count for a handler.
func WithRetries(retries int) JobOption {
	return capruntime.WithRetries(retries)
}

// NewRedisBlobStore creates a Redis-backed blob store.
func NewRedisBlobStore(redisURL string) (*RedisBlobStore, error) {
	return capruntime.NewRedisBlobStore(redisURL)
}

// NewInMemoryBlobStore returns a new in-memory blob store.
func NewInMemoryBlobStore() *InMemoryBlobStore {
	return capruntime.NewInMemoryBlobStore()
}
