package edge

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidUpstream        = errors.New("invalid mcp upstream")
	ErrUpstreamAlreadyExists  = errors.New("mcp upstream already exists")
	ErrUpstreamNotFound       = errors.New("mcp upstream not found")
	ErrSecretMustUseRef       = errors.New("mcp upstream secret must use secret:// reference")
	ErrInvalidTransport       = errors.New("invalid mcp upstream transport")
	ErrUnsafeEndpoint         = errors.New("unsafe mcp upstream endpoint")
	ErrShellMetacharsRejected = errors.New("mcp upstream command contains shell metacharacters")
	ErrUpstreamNotAllowlisted = errors.New("mcp upstream not allowlisted")
	ErrUpstreamLimitExceeded  = errors.New("mcp upstream tenant limit exceeded")
)

// UpstreamServer is the runtime registry record for an approved upstream MCP
// server. Config-layer bootstrap records live in core/infra/config; this type
// adds tenant scope, lifecycle timestamps, and normalized validation metadata.
//
// ResolvedIPs pins the hostname's DNS resolution at registration time so
// use-time callers (RevalidateMCPUpstreamAtUse) can detect DNS-rebinding
// where the host later resolves to an internal/cloud-metadata IP.
type UpstreamServer struct {
	Name          string            `json:"name"`
	Transport     string            `json:"transport"`
	Endpoint      string            `json:"endpoint,omitempty"`
	Command       []string          `json:"command,omitempty"`
	TenantID      string            `json:"tenant_id"`
	AuthSecretRef string            `json:"auth_secret_ref,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
	Risk          string            `json:"risk"`
	Enabled       bool              `json:"enabled"`
	ResolvedIPs   []string          `json:"resolved_ips,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// MCPUpstreamRegistry stores tenant-scoped approved MCP upstream servers.
type MCPUpstreamRegistry interface {
	Create(ctx context.Context, upstream *UpstreamServer) error
	Get(ctx context.Context, tenantID, name string) (*UpstreamServer, bool, error)
	List(ctx context.Context, tenantID string) ([]UpstreamServer, error)
	Update(ctx context.Context, upstream *UpstreamServer) error
	Disable(ctx context.Context, tenantID, name string) error
	Enable(ctx context.Context, tenantID, name string) error
}

func normalizeMCPUpstream(in *UpstreamServer, now time.Time) (UpstreamServer, error) {
	if in == nil {
		return UpstreamServer{}, ErrInvalidUpstream
	}
	out := *in
	out.Name = strings.TrimSpace(out.Name)
	out.Transport = strings.TrimSpace(out.Transport)
	out.Endpoint = strings.TrimSpace(out.Endpoint)
	out.TenantID = strings.TrimSpace(out.TenantID)
	out.AuthSecretRef = strings.TrimSpace(out.AuthSecretRef)
	out.Risk = strings.TrimSpace(out.Risk)
	if out.Risk == "" {
		out.Risk = "medium"
	}
	out.Command = cloneStrings(out.Command)
	out.Labels = cloneMCPUpstreamLabels(out.Labels)
	if out.CreatedAt.IsZero() {
		out.CreatedAt = now
	}
	out.UpdatedAt = now
	return out, nil
}

func cloneMCPUpstreamLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	for i := range out {
		out[i] = strings.TrimSpace(out[i])
	}
	return out
}
