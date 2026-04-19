package gateway

import (
	"context"
	"strings"

	"github.com/cordum/cordum/core/infra/store"
)

// agentIdentityPreapprovalLookup implements PreapprovalLookup by
// reading AgentIdentity.PreapprovedMutatingTools from the agent
// identity store. Globs supported: trailing "*" matches any suffix,
// e.g. "cordum_install_*" matches "cordum_install_pack" but not
// "cordum_uninstall_pack". Exact match otherwise.
//
// Fail-closed: any store error resolves to false so a Redis outage
// can't silently grant bypass. The caller's normal approval-enqueue
// path takes over.
type agentIdentityPreapprovalLookup struct {
	store *store.AgentIdentityStore
}

func newAgentIdentityPreapprovalLookup(s *store.AgentIdentityStore) *agentIdentityPreapprovalLookup {
	return &agentIdentityPreapprovalLookup{store: s}
}

func (l *agentIdentityPreapprovalLookup) IsPreapproved(ctx context.Context, tenant, agentID, toolName string) bool {
	if l == nil || l.store == nil {
		return false
	}
	agentID = strings.TrimSpace(agentID)
	toolName = strings.TrimSpace(toolName)
	if agentID == "" || toolName == "" {
		return false
	}
	identity, err := l.store.Get(ctx, agentID)
	if err != nil || identity == nil {
		return false
	}
	// Tenant isolation: if the identity's Owner/Team scope doesn't
	// match the calling tenant, refuse. Empty owner (multi-tenant
	// fixture) is permissive — matches the existing AgentIdentity
	// behaviour on the Allowed* lists.
	if tenant != "" && identity.Owner != "" && !equalFoldTrim(identity.Owner, tenant) {
		return false
	}
	for _, pattern := range identity.PreapprovedMutatingTools {
		if matchToolPattern(pattern, toolName) {
			return true
		}
	}
	return false
}

// matchToolPattern supports exact match and trailing-* glob. Leading-*
// and interior-* would introduce regex semantics inside the audit
// trail; keeping the grammar narrow means the preapproval scope is
// easy to reason about.
func matchToolPattern(pattern, toolName string) bool {
	pattern = strings.TrimSpace(pattern)
	toolName = strings.TrimSpace(toolName)
	if pattern == "" || toolName == "" {
		return false
	}
	if pattern == toolName {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if prefix != "" && strings.HasPrefix(toolName, prefix) {
			return true
		}
	}
	return false
}

func equalFoldTrim(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
