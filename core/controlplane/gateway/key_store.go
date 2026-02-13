package gateway

import (
	"context"
	"time"
)

// ManagedKey represents a runtime-created API key stored in Redis.
type ManagedKey struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Prefix     string    `json:"prefix"`
	KeyHash    string    `json:"key_hash"`
	Tenant     string    `json:"tenant"`
	Scopes     []string  `json:"scopes"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsed   time.Time `json:"last_used,omitempty"`
	UsageCount int64     `json:"usage_count"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
	Revoked    bool      `json:"revoked"`
}

// KeyStore manages runtime-created API keys.
type KeyStore interface {
	List(ctx context.Context, tenant string) ([]*ManagedKey, error)
	Create(ctx context.Context, key *ManagedKey, rawSecret string) error
	Revoke(ctx context.Context, id string, tenant string) error
	ValidateKey(ctx context.Context, rawKey string) (*ManagedKey, error)
	RecordUsage(ctx context.Context, id string) error
}

// Redis key prefixes for managed API keys.
// #nosec G101 -- Redis key prefixes, not credentials.
const (
	apiKeyPrefix            = "apikey:"
	apiKeyPrefixIndexPrefix = "apikey:prefix:"
	apiKeyTenantPrefix      = "apikey:tenant:"
)
