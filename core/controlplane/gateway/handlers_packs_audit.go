package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

// handlers_packs_audit.go — SIEM emission helpers for the topic
// registry lifecycle. Kept in its own file so the audit plumbing
// stays near the topic-registry call sites in handlers_packs.go
// without widening that file further.

// emitTopicRegisteredAudit fires one topic_registered SIEMEvent per
// topic name registered as part of a pack install. Batching the pack
// install into a single multi-topic event would lose the per-topic
// correlation the results API + dashboard need; one event per topic
// is the right granularity for the audit chain.
//
// Nil-safe: a gateway wired without an auditExporter (dev mode)
// silently skips emission rather than failing the install path.
func (s *server) emitTopicRegisteredAudit(ctx context.Context, packID string, topicNames []string, actorID string) {
	if s == nil || s.auditExporter == nil {
		return
	}
	actor := strings.TrimSpace(actorID)
	if actor == "" {
		actor = "system"
	}
	now := time.Now().UTC()
	for _, name := range topicNames {
		if strings.TrimSpace(name) == "" {
			continue
		}
		s.auditExporter.Send(audit.SIEMEvent{
			Timestamp: now,
			EventType: audit.EventTopicRegistered,
			Severity:  audit.SeverityInfo,
			TenantID:  tenantFromContext(ctx),
			Identity:  actor,
			Action:    "topic_registered",
			Extra: map[string]string{
				"pack_id":    packID,
				"topic_name": name,
				"actor_id":   actor,
			},
		})
	}
}

// emitTopicUnregisteredAudit mirrors the registered variant for the
// uninstall path. Fired after DeleteMany succeeds so a rollback that
// re-adds the topics doesn't emit spurious unregister events.
func (s *server) emitTopicUnregisteredAudit(ctx context.Context, packID string, topicNames []string, actorID string) {
	if s == nil || s.auditExporter == nil {
		return
	}
	actor := strings.TrimSpace(actorID)
	if actor == "" {
		actor = "system"
	}
	now := time.Now().UTC()
	for _, name := range topicNames {
		if strings.TrimSpace(name) == "" {
			continue
		}
		s.auditExporter.Send(audit.SIEMEvent{
			Timestamp: now,
			EventType: audit.EventTopicUnregistered,
			Severity:  audit.SeverityMedium,
			TenantID:  tenantFromContext(ctx),
			Identity:  actor,
			Action:    "topic_unregistered",
			Extra: map[string]string{
				"pack_id":    packID,
				"topic_name": name,
				"actor_id":   actor,
			},
		})
	}
}

// packOpActor best-effort resolves the caller's principal for an audit
// event. Falls back to "admin" when no auth context is present (e.g.
// tests) so emission stays non-nil.
func packOpActor(r *http.Request) string {
	if auth := auth.FromRequest(r); auth != nil {
		if id := strings.TrimSpace(auth.PrincipalID); id != "" {
			return id
		}
	}
	return "admin"
}

// tenantFromContext extracts the tenant from the auth context value
// stashed by mcpAuth / tenantMiddleware. Unknown returns empty so the
// SIEMEvent.TenantID defaults to blank (downstream handles per-tenant
// chain mapping; the chainer treats empty as the system tenant).
func tenantFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if auth := auth.FromContext(ctx); auth != nil {
		return strings.TrimSpace(auth.Tenant)
	}
	return ""
}
