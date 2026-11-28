package gateway

import (
	"context"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
)

// appendNamedAuditEntry returns persistence failures for callers that require an audit entry.
func (s *server) appendNamedAuditEntry(ctx context.Context, action, resourceType, resourceID, resourceName, actorID, role, message string) error {
	return s.appendPolicyAudit(ctx, policybundles.PolicyAuditEntry{
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		ResourceName: resourceName,
		ActorID:      actorID,
		Role:         role,
		Message:      message,
	})
}

// appendAuditEntryNamed is only for informational and refusal records, never
// required evidence. appendPolicyAudit logs any persistence failure.
func (s *server) appendAuditEntryNamed(ctx context.Context, action, resourceType, resourceID, resourceName, actorID, role, message string) {
	_ = s.appendPolicyAudit(ctx, policybundles.PolicyAuditEntry{
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		ResourceName: resourceName,
		ActorID:      actorID,
		Role:         role,
		Message:      message,
	})
}
