package scheduler

import (
	"context"
	"strings"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policy"
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
	timestamp := decisionLogTimestampMillis(record.CheckedAt)
	ruleID := strings.TrimSpace(record.RuleID)
	reason := strings.TrimSpace(record.Reason)
	decisionType := unifiedDecisionTypeForVerdict(record.Decision)
	bundleID, bundleVersion := decisionLogBundleBinding(record.PolicySnapshot)

	var trace []policy.TraceStep
	if decisionType != "" {
		trace = []policy.TraceStep{{
			RuleID:       ruleID,
			BundleID:     bundleID,
			DecisionType: decisionType,
			Reason:       reason,
			Timestamp:    time.UnixMilli(timestamp).UTC(),
		}}
	}

	return model.DecisionLogRecord{
		JobID:            strings.TrimSpace(req.GetJobId()),
		Tenant:           ExtractTenant(req),
		AgentID:          decisionLogAgentID(req),
		Topic:            strings.TrimSpace(req.GetTopic()),
		Verdict:          record.Decision,
		RuleID:           ruleID,
		PolicyVersion:    decisionLogPolicyVersion(record.PolicySnapshot),
		Reason:           reason,
		Constraints:      record.Constraints,
		ApprovalStatus:   record.ApprovalStatus,
		ApprovalDecision: record.ApprovalDecision,
		Timestamp:        timestamp,

		// Unified projection — Source is always job for scheduler-side
		// records; Type/Trace derive from the legacy verdict fields. Bundle
		// fields are best-effort from PolicySnapshot ("<version>|<id>" or
		// "<version>"); empty when the snapshot doesn't encode a bundle.
		Source:        policy.DecisionSourceJob,
		Type:          decisionType,
		BundleID:      bundleID,
		BundleVersion: bundleVersion,
		Trace:         trace,
	}
}

// unifiedDecisionTypeForVerdict maps the legacy SafetyDecision verdict onto
// the unified policy.DecisionType. quarantine + redact have no SafetyDecision
// equivalent on the job-side scheduler today, so they're unreachable from
// here — they only arrive on edge-side decisions which use a separate emit
// path. Returning "" for an empty verdict keeps the transition window safe.
func unifiedDecisionTypeForVerdict(d model.SafetyDecision) policy.DecisionType {
	switch d {
	case model.SafetyAllow:
		return policy.DecisionAllow
	case model.SafetyDeny:
		return policy.DecisionDeny
	case model.SafetyAllowWithConstraints:
		return policy.DecisionAllowWithConstraints
	case model.SafetyRequireApproval:
		return policy.DecisionRequireHuman
	case model.SafetyThrottle:
		return policy.DecisionThrottle
	default:
		return ""
	}
}

// decisionLogBundleBinding extracts the bundle id + version from the
// scheduler's policy snapshot string. The snapshot is encoded as
// "<bundle_version>|<bundle_id>" by the safety kernel; older snapshots
// carry just "<bundle_version>". Empty inputs yield empty outputs so the
// caller can detect "no binding available".
func decisionLogBundleBinding(snapshot string) (string, string) {
	snapshot = strings.TrimSpace(snapshot)
	if snapshot == "" {
		return "", ""
	}
	if i := strings.Index(snapshot, "|"); i >= 0 {
		version := strings.TrimSpace(snapshot[:i])
		id := strings.TrimSpace(snapshot[i+1:])
		return id, version
	}
	return "", snapshot
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
