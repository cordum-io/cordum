package edge

import (
	"strings"

	"github.com/cordum/cordum/core/policylabels"
)

const (
	// LabelPolicyAttachmentID carries the synthetic bundle key used to scope a
	// job/session-specific policy override at Safety Kernel evaluate time.
	LabelPolicyAttachmentID = policylabels.PolicyAttachmentID

	// LabelDecisionAuditEmittedBy marks agentd evidence whose fresh Gateway
	// evaluate response already emitted the shared policy decision audit record.
	LabelDecisionAuditEmittedBy = "policy.audit_emitted_by"
	// LabelDecisionAuditEmittedByGateway is the value used when Gateway
	// evaluate already emitted the policy decision audit record.
	LabelDecisionAuditEmittedByGateway = "gateway"
	// LabelGatewayDecisionEventID links the agentd evidence event to the
	// Gateway-persisted hook.policy_decision event that already emitted audit.
	LabelGatewayDecisionEventID = "policy.gateway_event_id"
)

// JobPolicyAttachmentID returns the synthetic bundle key for a Cordum job.
func JobPolicyAttachmentID(jobID string) string {
	return policylabels.JobAttachmentID(jobID)
}

// SessionPolicyAttachmentID returns the synthetic bundle key for an Edge session.
func SessionPolicyAttachmentID(sessionID string) string {
	return policylabels.SessionAttachmentID(sessionID)
}

// WithPolicyAttachmentLabel returns a label copy with attachmentID pinned.
func WithPolicyAttachmentLabel(labels Labels, attachmentID string) Labels {
	out := cloneLabels(labels)
	attachmentID = strings.TrimSpace(attachmentID)
	if attachmentID != "" {
		out[LabelPolicyAttachmentID] = attachmentID
	}
	return out
}
