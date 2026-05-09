package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func decisionFromPolicyCheckResponse(source policy.DecisionSource, rule policy.Rule, binding policyBundleBinding, resp *pb.PolicyCheckResponse) policy.Decision {
	decisionType := policyDecisionTypeFromProto(resp.GetDecision())
	ruleID := firstNonEmptyString(resp.GetRuleId(), rule.ID)
	reason := strings.TrimSpace(resp.GetReason())
	return policy.Decision{Source: source, RuleID: ruleID, BundleID: binding.BundleID, BundleVersion: binding.Version, Type: decisionType, Trace: []policy.TraceStep{{RuleID: ruleID, BundleID: binding.BundleID, DecisionType: decisionType, Reason: reason, Timestamp: time.Now().UTC(), Constraints: rawProtoConstraints(resp.GetConstraints())}}, Timestamp: time.Now().UTC()}
}

func decisionFromConfigPolicyDecision(source policy.DecisionSource, rule policy.Rule, binding policyBundleBinding, decision config.PolicyDecision) policy.Decision {
	decisionType := policyDecisionTypeFromString(decision.Decision)
	ruleID := firstNonEmptyString(decision.RuleID, rule.ID)
	reason := strings.TrimSpace(decision.Reason)
	return policy.Decision{Source: source, RuleID: ruleID, BundleID: binding.BundleID, BundleVersion: binding.Version, Type: decisionType, Trace: []policy.TraceStep{{RuleID: ruleID, BundleID: binding.BundleID, DecisionType: decisionType, Reason: reason, Timestamp: time.Now().UTC(), Constraints: rawConfigConstraints(decision.Constraints)}}, Timestamp: time.Now().UTC()}
}

func policyDecisionTypeFromProto(decision pb.DecisionType) policy.DecisionType {
	switch decision {
	case pb.DecisionType_DECISION_TYPE_DENY:
		return policy.DecisionDeny
	case pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:
		return policy.DecisionRequireHuman
	case pb.DecisionType_DECISION_TYPE_THROTTLE:
		return policy.DecisionThrottle
	case pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS:
		return policy.DecisionAllowWithConstraints
	case pb.DecisionType_DECISION_TYPE_QUARANTINE:
		return policy.DecisionQuarantine
	case pb.DecisionType_DECISION_TYPE_REDACT:
		return policy.DecisionRedact
	default:
		return policy.DecisionAllow
	}
}

func policyDecisionTypeFromString(decision string) policy.DecisionType {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case "deny", "block":
		return policy.DecisionDeny
	case "require_approval", "require_human":
		return policy.DecisionRequireHuman
	case "throttle":
		return policy.DecisionThrottle
	case "constrain", "allow_with_constraints":
		return policy.DecisionAllowWithConstraints
	case "quarantine":
		return policy.DecisionQuarantine
	case "redact":
		return policy.DecisionRedact
	default:
		return policy.DecisionAllow
	}
}

func rawProtoConstraints(constraints *pb.PolicyConstraints) json.RawMessage {
	if constraints == nil {
		return nil
	}
	data, err := protojson.Marshal(constraints)
	if err != nil || string(data) == "{}" {
		return nil
	}
	return json.RawMessage(data)
}

func rawConfigConstraints(constraints config.PolicyConstraints) json.RawMessage {
	if policybundles.IsConstraintsEmpty(constraints) {
		return nil
	}
	data, err := json.Marshal(constraints)
	if err != nil || string(data) == "{}" {
		return nil
	}
	return json.RawMessage(data)
}

func bundleStoreEvaluateError(action string, err error) error {
	kind := policyEvaluateUnavailable
	if errors.Is(err, policy.ErrBundleNotFound) || errors.Is(err, policy.ErrBundleVersionNotFound) || errors.Is(err, policy.ErrNoDeploymentForScope) {
		kind = policyEvaluateNotFound
	}
	return newPolicyEvaluateError(kind, action, err)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func clonePolicyEvalStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func clonePolicyEvalAnyMap(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func policyEvaluateErrorMessage(err error) string {
	var evalErr *policyEvaluateError
	if errors.As(err, &evalErr) && evalErr.Message != "" {
		return evalErr.Message
	}
	return fmt.Sprint(err)
}
