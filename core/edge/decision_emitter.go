package edge

import (
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/policy"
)

// EdgeDecisionEmitOptions carries optional context for unified edge decisions.
type EdgeDecisionEmitOptions struct {
	BundleID       string
	BundleVersion  string
	InputRef       string
	OutputRef      string
	AuditHash      string
	Classification *ActionClassification
}

// EmitDecisionForEdgeEvent converts a persisted edge policy-decision event into
// the shared policy.Decision shape without changing the legacy EdgeDecision.
func EmitDecisionForEdgeEvent(event AgentActionEvent, opts EdgeDecisionEmitOptions) (policy.Decision, error) {
	ruleID := strings.TrimSpace(event.RuleID)
	if ruleID == "" {
		return policy.Decision{}, fmt.Errorf("edge policy decision requires rule_id")
	}
	decisionType, err := policyDecisionTypeForEdge(event.Decision)
	if err != nil {
		return policy.Decision{}, err
	}
	timestamp := edgeDecisionTimestamp(event.Timestamp)
	trace := []policy.TraceStep{{
		RuleID:       ruleID,
		DecisionType: decisionType,
		Reason:       edgeDecisionTraceReason(event, opts.Classification),
		Timestamp:    timestamp,
	}}
	return policy.Decision{
		Source:        policy.DecisionSourceEdge,
		RuleID:        ruleID,
		BundleID:      strings.TrimSpace(opts.BundleID),
		BundleVersion: strings.TrimSpace(opts.BundleVersion),
		Type:          decisionType,
		Trace:         trace,
		InputRef:      strings.TrimSpace(opts.InputRef),
		OutputRef:     strings.TrimSpace(opts.OutputRef),
		AuditHash:     strings.TrimSpace(opts.AuditHash),
		Timestamp:     timestamp,
	}, nil
}

func policyDecisionTypeForEdge(decision EdgeDecision) (policy.DecisionType, error) {
	switch decision {
	case DecisionAllow:
		return policy.DecisionAllow, nil
	case DecisionDeny:
		return policy.DecisionDeny, nil
	case DecisionRequireApproval:
		return policy.DecisionRequireHuman, nil
	case DecisionThrottle:
		return policy.DecisionThrottle, nil
	case DecisionConstrain:
		return policy.DecisionRedact, nil
	case DecisionRecorded:
		return policy.DecisionAllow, nil
	default:
		return "", fmt.Errorf("unsupported edge decision %q", strings.TrimSpace(string(decision)))
	}
}

func edgeDecisionTimestamp(eventTime time.Time) time.Time {
	if eventTime.IsZero() {
		return time.Now().UTC()
	}
	return eventTime.UTC()
}

func edgeDecisionTraceReason(event AgentActionEvent, classification *ActionClassification) string {
	reason := strings.TrimSpace(event.DecisionReason)
	if reason == "" && classification != nil {
		reason = strings.TrimSpace(classification.ActionName)
	}
	if reason == "" {
		reason = strings.TrimSpace(string(event.Decision))
	}
	if event.Decision == DecisionRecorded && !strings.Contains(strings.ToLower(reason), "recorded") {
		return "recorded: " + reason
	}
	return reason
}
