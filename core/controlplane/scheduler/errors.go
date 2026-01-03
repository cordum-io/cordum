package scheduler

import "errors"

var (
	// ErrNoPoolMapping indicates no configured pool for a topic.
	ErrNoPoolMapping = errors.New("no_pool_mapping")
	// ErrNoWorkers indicates no workers available in the target pool.
	ErrNoWorkers = errors.New("no_workers")
	// ErrPoolOverloaded indicates all workers in a pool are overloaded.
	ErrPoolOverloaded = errors.New("pool_overloaded")
	// ErrTenantLimit indicates a tenant concurrency limit has been reached.
	ErrTenantLimit = errors.New("tenant_limit")
)
