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
// Tenant isolation is enforced by AgentIdentityStore.Get(ctx, tenant, agentID),
// not by Owner/Team comparisons. Fail-closed: missing tenant/input or any store
// error resolves to false so the caller's normal approval-enqueue path takes over.
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
	tenant = strings.TrimSpace(tenant)
	agentID = strings.TrimSpace(agentID)
	toolName = strings.TrimSpace(toolName)
	if tenant == "" || agentID == "" || toolName == "" {
		return false
	}
	identity, err := l.store.Get(ctx, tenant, agentID)
	if err != nil || identity == nil {
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
