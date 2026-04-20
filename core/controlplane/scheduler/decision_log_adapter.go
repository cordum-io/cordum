package scheduler

import (
	"context"
	"strings"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// NoopDecisionLogStore preserves existing scheduler behavior when the Policy
// Decision Log integration has not been wired yet.
type NoopDecisionLogStore struct{}

func (NoopDecisionLogStore) AppendDecision(context.Context, model.DecisionLogRecord) error {
	return nil
}

func (NoopDecisionLogStore) QueryDecisions(context.Context, model.DecisionQuery) (model.DecisionPage, error) {
	return model.DecisionPage{Items: []model.DecisionLogRecord{}}, nil
}

func buildDecisionLogRecord(req *pb.JobRequest, record SafetyDecisionRecord) model.DecisionLogRecord {
	return model.DecisionLogRecord{
		JobID:            strings.TrimSpace(req.GetJobId()),
		Tenant:           ExtractTenant(req),
		AgentID:          decisionLogAgentID(req),
		Topic:            strings.TrimSpace(req.GetTopic()),
		Verdict:          record.Decision,
		RuleID:           strings.TrimSpace(record.RuleID),
		PolicyVersion:    decisionLogPolicyVersion(record.PolicySnapshot),
		Reason:           strings.TrimSpace(record.Reason),
		Constraints:      record.Constraints,
		ApprovalStatus:   record.ApprovalStatus,
		ApprovalDecision: record.ApprovalDecision,
		Timestamp:        decisionLogTimestampMillis(record.CheckedAt),
	}
}

func decisionLogAgentID(req *pb.JobRequest) string {
	if req == nil {
		return ""
	}
	if labels := req.GetLabels(); labels != nil {
		if agentID := strings.TrimSpace(labels["agent_id"]); agentID != "" {
			return agentID
		}
	}
	if meta := req.GetMeta(); meta != nil {
		if labels := meta.GetLabels(); labels != nil {
			return strings.TrimSpace(labels["agent_id"])
		}
	}
	return ""
}

func decisionLogPolicyVersion(snapshot string) string {
	snapshot = strings.TrimSpace(snapshot)
	if snapshot == "" {
		return ""
	}
	if i := strings.Index(snapshot, "|"); i >= 0 {
		return snapshot[:i]
	}
	return snapshot
}

func decisionLogTimestampMillis(ts int64) int64 {
	if ts <= 0 {
		return time.Now().UTC().UnixMilli()
	}
	switch {
	case ts < 1_000_000_000_000:
		return ts * 1000
	case ts < 1_000_000_000_000_000:
		return ts
	case ts < 1_000_000_000_000_000_000:
		return ts / 1000
	default:
		return ts / 1_000_000
	}
}
