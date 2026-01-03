package locks

import (
	"context"
	"time"
)

// Mode controls whether a lock is shared or exclusive.
type Mode string

const (
	ModeExclusive Mode = "exclusive"
	ModeShared    Mode = "shared"
)

// Lock captures the current lock ownership state.
type Lock struct {
	Resource  string         `json:"resource"`
	Mode      Mode           `json:"mode"`
	Owners    map[string]int `json:"owners,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
	ExpiresAt time.Time      `json:"expires_at"`
}

// Store manages resource locks.
type Store interface {
	Acquire(ctx context.Context, resource, owner string, mode Mode, ttl time.Duration) (*Lock, bool, error)
	Release(ctx context.Context, resource, owner string) (*Lock, bool, error)
	Renew(ctx context.Context, resource, owner string, ttl time.Duration) (*Lock, bool, error)
	Get(ctx context.Context, resource string) (*Lock, error)
}
