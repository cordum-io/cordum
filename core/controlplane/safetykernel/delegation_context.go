package safetykernel

import (
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const envDelegationPolicyEnabled = "CORDUM_DELEGATION_POLICY_ENABLED"

func delegationContextFromRequest(req *pb.PolicyCheckRequest) *config.DelegationContext {
	if req == nil || !env.Bool(envDelegationPolicyEnabled) {
		return nil
	}
	return config.DelegationContextFromLabels(req.GetLabels())
}
