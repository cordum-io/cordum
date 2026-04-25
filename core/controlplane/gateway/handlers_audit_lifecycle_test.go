package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

// These tests cover the SIEM event helpers added for API key + RBAC role
// lifecycle changes. They lock in the event_type, severity, identity, and
// extra-field shape so SIEM rules downstream don't break silently when a
// future refactor renames a field. The handler integration paths are
// already covered by the existing TestHandleRevokeKey* tests; this file
// verifies the audit-emit side effect.
//
// Pattern follows handlers_workers_revoke_test.go's recordingAuditSender
// (defined in that file's _test.go and reused across the package).

func newAuditAdminCtx(method, path, principal, tenant string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	return req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &auth.AuthContext{
		Role:        "admin",
		Tenant:      tenant,
		PrincipalID: principal,
	}))
}

func TestEmitAPIKeyCreated_PublishesSIEMEvent(t *testing.T) {
	sink := &recordingAuditSender{}
	s := &server{auditExporter: sink, tenant: "default"}

	mk := &auth.ManagedKey{
		ID:     "key-abc",
		Name:   "ci-pipeline",
		Tenant: "default",
		Scopes: []string{"jobs:read", "audit:read"},
	}
	req := newAuditAdminCtx(http.MethodPost, "/api/v1/auth/keys", "alice", "default")

	s.emitAPIKeyCreated(req, mk)

	if len(sink.events) != 1 {
		t.Fatalf("expected 1 SIEM event, got %d", len(sink.events))
	}
	ev := sink.events[0]
	if ev.EventType != audit.EventAuthAPIKeyCreated {
		t.Fatalf("event_type=%q, want %q", ev.EventType, audit.EventAuthAPIKeyCreated)
	}
	if ev.Severity != audit.SeverityMedium {
		t.Fatalf("severity=%q, want MEDIUM", ev.Severity)
	}
	if ev.TenantID != "default" {
		t.Fatalf("tenant=%q, want default", ev.TenantID)
	}
	if ev.Action != "create" {
		t.Fatalf("action=%q, want create", ev.Action)
	}
	if ev.Identity != "alice" {
		t.Fatalf("identity=%q, want alice", ev.Identity)
	}
	if ev.Extra["key_id"] != "key-abc" {
		t.Fatalf("extra.key_id=%q, want key-abc", ev.Extra["key_id"])
	}
	if ev.Extra["key_name"] != "ci-pipeline" {
		t.Fatalf("extra.key_name=%q, want ci-pipeline", ev.Extra["key_name"])
	}
	if ev.Extra["scopes"] != "jobs:read,audit:read" {
		t.Fatalf("extra.scopes=%q, want jobs:read,audit:read", ev.Extra["scopes"])
	}
}

func TestEmitAPIKeyCreated_NilExporter_NoOp(t *testing.T) {
	// Defensive: emit helpers must not panic when no SIEM exporter wired.
	s := &server{auditExporter: nil}
	s.emitAPIKeyCreated(newAuditAdminCtx(http.MethodPost, "/x", "alice", "default"), &auth.ManagedKey{ID: "k", Tenant: "default"})
}

func TestEmitAPIKeyRevoked_PublishesSIEMEvent(t *testing.T) {
	sink := &recordingAuditSender{}
	s := &server{auditExporter: sink, tenant: "default"}

	req := newAuditAdminCtx(http.MethodDelete, "/api/v1/auth/keys/key-x", "alice", "default")

	s.emitAPIKeyRevoked(req, "key-x", "default")

	if len(sink.events) != 1 {
		t.Fatalf("expected 1 SIEM event, got %d", len(sink.events))
	}
	ev := sink.events[0]
	if ev.EventType != audit.EventAuthAPIKeyRevoked {
		t.Fatalf("event_type=%q, want %q", ev.EventType, audit.EventAuthAPIKeyRevoked)
	}
	if ev.Severity != audit.SeverityHigh {
		t.Fatalf("severity=%q, want HIGH (revocation typically follows compromise)", ev.Severity)
	}
	if ev.Action != "revoke" {
		t.Fatalf("action=%q, want revoke", ev.Action)
	}
	if ev.Identity != "alice" {
		t.Fatalf("identity=%q, want alice", ev.Identity)
	}
	if ev.Extra["key_id"] != "key-x" {
		t.Fatalf("extra.key_id=%q, want key-x", ev.Extra["key_id"])
	}
}

func TestEmitRoleUpserted_CreateOperation_PublishesSIEMEvent(t *testing.T) {
	sink := &recordingAuditSender{}
	s := &server{auditExporter: sink, tenant: "default"}

	role := &auth.RoleDefinition{
		Name:        "ops-runner",
		Permissions: []string{"jobs:write", "approvals:read"},
		Inherits:    []string{"viewer"},
	}
	req := newAuditAdminCtx(http.MethodPut, "/api/v1/auth/roles/ops-runner", "alice", "default")

	s.emitRoleUpserted(req, role, "create")

	if len(sink.events) != 1 {
		t.Fatalf("expected 1 SIEM event, got %d", len(sink.events))
	}
	ev := sink.events[0]
	if ev.EventType != audit.EventAuthRoleUpserted {
		t.Fatalf("event_type=%q, want %q", ev.EventType, audit.EventAuthRoleUpserted)
	}
	if ev.Severity != audit.SeverityHigh {
		t.Fatalf("severity=%q, want HIGH", ev.Severity)
	}
	if ev.Extra["role_name"] != "ops-runner" {
		t.Fatalf("extra.role_name=%q, want ops-runner", ev.Extra["role_name"])
	}
	if ev.Extra["operation"] != "create" {
		t.Fatalf("extra.operation=%q, want create", ev.Extra["operation"])
	}
	if ev.Extra["permissions"] != "jobs:write,approvals:read" {
		t.Fatalf("extra.permissions=%q, want jobs:write,approvals:read", ev.Extra["permissions"])
	}
	if ev.Extra["inherits"] != "viewer" {
		t.Fatalf("extra.inherits=%q, want viewer", ev.Extra["inherits"])
	}
}

func TestEmitRoleUpserted_UpdateOperation_PublishesSIEMEvent(t *testing.T) {
	sink := &recordingAuditSender{}
	s := &server{auditExporter: sink, tenant: "default"}

	role := &auth.RoleDefinition{
		Name:        "ops-runner",
		Permissions: []string{"jobs:write"},
	}
	req := newAuditAdminCtx(http.MethodPut, "/api/v1/auth/roles/ops-runner", "alice", "default")

	s.emitRoleUpserted(req, role, "update")

	if len(sink.events) != 1 {
		t.Fatalf("expected 1 SIEM event, got %d", len(sink.events))
	}
	if op := sink.events[0].Extra["operation"]; op != "update" {
		t.Fatalf("extra.operation=%q, want update", op)
	}
}

func TestEmitRoleDeleted_PublishesSIEMEvent(t *testing.T) {
	sink := &recordingAuditSender{}
	s := &server{auditExporter: sink, tenant: "default"}

	req := newAuditAdminCtx(http.MethodDelete, "/api/v1/auth/roles/ops-runner", "alice", "default")

	s.emitRoleDeleted(req, "ops-runner")

	if len(sink.events) != 1 {
		t.Fatalf("expected 1 SIEM event, got %d", len(sink.events))
	}
	ev := sink.events[0]
	if ev.EventType != audit.EventAuthRoleDeleted {
		t.Fatalf("event_type=%q, want %q", ev.EventType, audit.EventAuthRoleDeleted)
	}
	if ev.Severity != audit.SeverityHigh {
		t.Fatalf("severity=%q, want HIGH (delete may be cleanup-after-attack)", ev.Severity)
	}
	if ev.Action != "delete_role" {
		t.Fatalf("action=%q, want delete_role", ev.Action)
	}
	if ev.Extra["role_name"] != "ops-runner" {
		t.Fatalf("extra.role_name=%q, want ops-runner", ev.Extra["role_name"])
	}
}
