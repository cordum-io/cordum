package auth

import "testing"

func TestMatchScopes(t *testing.T) {
	tests := []struct {
		name     string
		granted  []string
		required string
		want     bool
	}{
		{
			name:     "empty granted allows for back-compat",
			granted:  nil,
			required: "jobs:write",
			want:     true,
		},
		{
			name:     "exact match allows",
			granted:  []string{"jobs:read"},
			required: "jobs:read",
			want:     true,
		},
		{
			name:     "resource wildcard allows any verb on resource",
			granted:  []string{"jobs:*"},
			required: "jobs:write",
			want:     true,
		},
		{
			name:     "multiple granted scopes allow matching wildcard",
			granted:  []string{"jobs:read", "audit:*"},
			required: "audit:write",
			want:     true,
		},
		{
			name:     "same resource different verb denies without wildcard",
			granted:  []string{"jobs:read"},
			required: "jobs:write",
			want:     false,
		},
		{
			name:     "cross resource denies",
			granted:  []string{"jobs:*"},
			required: "audit:read",
			want:     false,
		},
		{
			name:     "legacy admin scope allows any required scope",
			granted:  []string{"admin"},
			required: "jobs:write",
			want:     true,
		},
		{
			name:     "legacy read scope allows read only",
			granted:  []string{"read"},
			required: "audit:read",
			want:     true,
		},
		{
			name:     "legacy read scope denies write",
			granted:  []string{"read"},
			required: "jobs:write",
			want:     false,
		},
		{
			name:     "legacy write scope allows write",
			granted:  []string{"write"},
			required: "jobs:write",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchScopes(tt.granted, tt.required)
			if got != tt.want {
				t.Fatalf("MatchScopes(%v, %q) = %v, want %v", tt.granted, tt.required, got, tt.want)
			}
		})
	}
}

func TestPathToScope(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		scope  string
		ok     bool
	}{
		{
			name:   "jobs read",
			method: "GET",
			path:   "/api/v1/jobs/job-123",
			scope:  "jobs:read",
			ok:     true,
		},
		{
			name:   "head maps to read",
			method: "HEAD",
			path:   "/api/v1/jobs",
			scope:  "jobs:read",
			ok:     true,
		},
		{
			name:   "jobs write",
			method: "POST",
			path:   "/api/v1/jobs",
			scope:  "jobs:write",
			ok:     true,
		},
		{
			name:   "audit read",
			method: "GET",
			path:   "/api/v1/audit/events",
			scope:  "audit:read",
			ok:     true,
		},
		{
			name:   "workflow write",
			method: "PATCH",
			path:   "/api/v1/workflows/wf-1",
			scope:  "workflows:write",
			ok:     true,
		},
		{
			name:   "approvals write",
			method: "POST",
			path:   "/api/v1/approvals/job-1/approve",
			scope:  "approvals:write",
			ok:     true,
		},
		{
			name:   "unknown path denies for scoped keys",
			method: "GET",
			path:   "/api/v1/unknown",
			scope:  "",
			ok:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := PathToScope(tt.method, tt.path)
			if ok != tt.ok {
				t.Fatalf("PathToScope(%q, %q) ok = %v, want %v", tt.method, tt.path, ok, tt.ok)
			}
			if got != tt.scope {
				t.Fatalf("PathToScope(%q, %q) scope = %q, want %q", tt.method, tt.path, got, tt.scope)
			}
		})
	}
}
