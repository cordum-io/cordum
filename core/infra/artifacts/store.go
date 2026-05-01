package artifacts

import (
	"context"
	"errors"
)

// ErrArtifactNotFound is returned by Stat (and indirectly by Get) when no
// artifact exists for the requested pointer. Callers building exports use
// errors.Is(err, ErrArtifactNotFound) to record the artifact in a
// missing-artifacts manifest section instead of failing the whole bundle.
var ErrArtifactNotFound = errors.New("artifact not found")

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
//
// Stat returns metadata only. The Edge evidence-export bundler (EDGE-013)
// uses Stat instead of Get so building a session manifest does not require
// loading every artifact body into memory — bundles cap at thousands of
// pointers but bodies can be megabytes apiece.
type Store interface {
	Put(ctx context.Context, content []byte, meta Metadata) (string, error)
	Get(ctx context.Context, ptr string) ([]byte, Metadata, error)
	Stat(ctx context.Context, ptr string) (Metadata, error)
}
