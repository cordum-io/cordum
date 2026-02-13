package gateway

import (
	"context"
	"net/http"
	"time"
)

// RouteRegistrar allows auth providers to attach additional HTTP routes.
type RouteRegistrar interface {
	RegisterRoutes(mux *http.ServeMux, wrap func(route string, fn http.HandlerFunc) http.HandlerFunc)
}

// AuthConfigProvider allows auth providers to supply UI auth configuration.
type AuthConfigProvider interface {
	AuthConfig() AuthConfig
}

// PublicPathProvider allows auth providers to skip auth for specific paths.
type PublicPathProvider interface {
	IsPublicPath(path string) bool
}

// AuditEvent captures an HTTP request summary for audit export.
type AuditEvent struct {
	Time       time.Time `json:"time"`
	Method     string    `json:"method"`
	Route      string    `json:"route"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMs int64     `json:"duration_ms"`
	RemoteAddr string    `json:"remote_addr"`
	UserAgent  string    `json:"user_agent"`
	Tenant     string    `json:"tenant"`
	Principal  string    `json:"principal"`
	Role       string    `json:"role"`
	AuthSource AuthSource `json:"auth_source,omitempty"`
	RequestID  string    `json:"request_id"`
}

// AuditExporter allows auth providers to emit audit events.
type AuditExporter interface {
	ExportAudit(ctx context.Context, event AuditEvent) error
}
