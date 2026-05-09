package audit

import (
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/policy"
)

// PolicyDecisionAuditRecord is the folded reader shape for unified v2
// decisions emitted by job and edge evaluators.
type PolicyDecisionAuditRecord struct {
	Source   policy.DecisionSource
	Decision policy.DecisionType
	RuleID   string
	Event    SIEMEvent
}

// FoldPolicyDecisionEvents filters a mixed audit stream to policy.decision.v2
// records and parses the shared Source/Decision fields for downstream readers.
func FoldPolicyDecisionEvents(events []SIEMEvent) ([]PolicyDecisionAuditRecord, error) {
	out := make([]PolicyDecisionAuditRecord, 0, len(events))
	for _, event := range events {
		if event.EventType != EventPolicyDecisionV2 {
			continue
		}
		record, err := foldPolicyDecisionEvent(event)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func foldPolicyDecisionEvent(event SIEMEvent) (PolicyDecisionAuditRecord, error) {
	source, err := policy.ParseDecisionSource(strings.TrimSpace(event.Extra["source"]))
	if err != nil {
		return PolicyDecisionAuditRecord{}, fmt.Errorf("fold policy decision source: %w", err)
	}
	decision, err := policy.ParseDecisionType(strings.TrimSpace(event.Decision))
	if err != nil {
		return PolicyDecisionAuditRecord{}, fmt.Errorf("fold policy decision type: %w", err)
	}
	ruleID := strings.TrimSpace(event.MatchedRule)
	if ruleID == "" {
		return PolicyDecisionAuditRecord{}, fmt.Errorf("fold policy decision: rule_id is required")
	}
	return PolicyDecisionAuditRecord{
		Source:   source,
		Decision: decision,
		RuleID:   ruleID,
		Event:    cloneSIEMEvent(event),
	}, nil
}
