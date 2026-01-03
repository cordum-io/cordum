package artifacts

import "context"

// RetentionClass controls artifact TTL semantics.
type RetentionClass string

const (
	RetentionShort    RetentionClass = "short"
	RetentionStandard RetentionClass = "standard"
	RetentionAudit    RetentionClass = "audit"
)

// Metadata describes stored artifacts.
type Metadata struct {
	ContentType string            `json:"content_type,omitempty"`
	SizeBytes   int64             `json:"size_bytes,omitempty"`
	Retention   RetentionClass    `json:"retention,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// Store provides artifact pointer storage.
type Store interface {
	Put(ctx context.Context, content []byte, meta Metadata) (string, error)
	Get(ctx context.Context, ptr string) ([]byte, Metadata, error)
}
