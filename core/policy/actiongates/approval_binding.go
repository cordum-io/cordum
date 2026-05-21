package actiongates

import (
	"context"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type approvalBindingFailure struct {
	Decision  pb.DecisionType
	Code      string
	Reason    string
	SubReason string
}

func (f approvalBindingFailure) failed() bool {
	return f.Decision != pb.DecisionType_DECISION_TYPE_UNSPECIFIED
}

func bindApprovalRef(
	ctx context.Context,
	lookup ApprovalLookup,
	actx *auth.AuthContext,
	act *config.ActionDescriptor,
	approvalRef string,
) (*edge.EdgeApproval, approvalBindingFailure) {
	ref := strings.TrimSpace(approvalRef)
	if ref == "" {
		return nil, approvalBindingFailure{
			Decision:  pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN,
			Code:      CodeRequireHuman,
			Reason:    "action requires backend-verified human approval",
			SubReason: "missing_approval",
		}
	}
	if lookup == nil {
		return nil, approvalBindingFailure{
			Decision:  pb.DecisionType_DECISION_TYPE_DENY,
			Code:      CodeInternalError,
			Reason:    "approval lookup unavailable",
			SubReason: "approval_lookup_failed:nil_lookup",
		}
	}
	approval, ok, err := lookup.LookupByApprovalRef(ctx, actx.Tenant, ref)
	if err != nil {
		return nil, approvalLookupFailure(err)
	}
	if !ok || approval == nil {
		return nil, approvalBindingFailure{
			Decision:  pb.DecisionType_DECISION_TYPE_DENY,
			Code:      CodeNotFound,
			Reason:    "no approval record for this action",
			SubReason: "approval_not_found",
		}
	}
	if failure := validateApprovalBinding(approval, actx, CanonicalActionHash(act)); failure.failed() {
		return nil, failure
	}
	return approval, approvalBindingFailure{}
}

func approvalLookupFailure(err error) approvalBindingFailure {
	return approvalBindingFailure{
		Decision:  pb.DecisionType_DECISION_TYPE_DENY,
		Code:      CodeInternalError,
		Reason:    "approval lookup failed",
		SubReason: "approval_lookup_failed:" + sanitizeErr(err),
	}
}

func validateApprovalBinding(
	approval *edge.EdgeApproval,
	actx *auth.AuthContext,
	actionHash string,
) approvalBindingFailure {
	if approval.TenantID != actx.Tenant {
		return approvalBindingAccessDenied("approval is for a different tenant", "approval_tenant_mismatch")
	}
	if approval.ResolverID != "" && approval.ResolverID == actx.PrincipalID {
		return approvalBindingAccessDenied("self-approval is not allowed", "self_approval")
	}
	if approval.Status != edge.ApprovalStatusApproved {
		return approvalBindingConflict("approval_status_" + string(approval.Status))
	}
	if approval.Decision != edge.ApprovalDecisionApprove {
		return approvalBindingConflict("approval_decision_" + string(approval.Decision))
	}
	if approval.ConsumedAt != nil {
		return approvalBindingConflict("approval_consumed")
	}
	if approval.ExpiresAt != nil && time.Now().UTC().After(*approval.ExpiresAt) {
		return approvalBindingConflict("approval_expired")
	}
	if approval.ActionHash != actionHash {
		return approvalBindingConflict("approval_mismatch")
	}
	return approvalBindingFailure{}
}

func approvalBindingAccessDenied(reason, subReason string) approvalBindingFailure {
	return approvalBindingFailure{
		Decision:  pb.DecisionType_DECISION_TYPE_DENY,
		Code:      CodeAccessDenied,
		Reason:    reason,
		SubReason: subReason,
	}
}

func approvalBindingConflict(subReason string) approvalBindingFailure {
	return approvalBindingFailure{
		Decision:  pb.DecisionType_DECISION_TYPE_DENY,
		Code:      CodeConflict,
		Reason:    "approval cannot be used for this action",
		SubReason: subReason,
	}
}
