package gateway

import (
	"context"
	"time"
)

// RouteRegistrar, AuthConfigProvider, PublicPathProvider are now in
// gateway/auth/ and re-exported via auth_compat.go type aliases.

// AuditEvent captures an HTTP request summary for audit export.
type AuditEvent struct {
	Time       time.Time  `json:"time"`
	Method     string     `json:"method"`
	Route      string     `json:"route"`
	Path       string     `json:"path"`
	Status     int        `json:"status"`
	DurationMs int64      `json:"duration_ms"`
	RemoteAddr string     `json:"remote_addr"`
	UserAgent  string     `json:"user_agent"`
	Tenant     string     `json:"tenant"`
	Principal  string     `json:"principal"`
	Role       string     `json:"role"`
	AuthSource AuthSource `json:"auth_source,omitempty"`
	RequestID  string     `json:"request_id"`
}

// AuditExporter allows auth providers to emit audit events.
type AuditExporter interface {
	ExportAudit(ctx context.Context, event AuditEvent) error
}
