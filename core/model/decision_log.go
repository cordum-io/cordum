package model

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	DefaultDecisionQueryLimit = 50
	MaxDecisionQueryLimit     = 500
	defaultDecisionQueryRange = 24 * time.Hour
)

const (
	DecisionVerdictAllow           = "allow"
	DecisionVerdictDeny            = "deny"
	DecisionVerdictConstrain       = "constrain"
	DecisionVerdictRequireApproval = "require_approval"
	DecisionVerdictThrottle        = "throttle"
)

// DecisionLogRecord captures the governance fields required for the Policy
// Decision Log and stays wire-compatible with audit.SIEMEvent projections.
type DecisionLogRecord struct {
	JobID            string                `json:"job_id,omitempty"`
	Tenant           string                `json:"tenant,omitempty"`
	AgentID          string                `json:"agent_id,omitempty"`
	Topic            string                `json:"topic,omitempty"`
	Verdict          SafetyDecision        `json:"verdict,omitempty"`
	RuleID           string                `json:"rule_id,omitempty"`
	PolicyVersion    string                `json:"policy_version,omitempty"`
	Reason           string                `json:"reason,omitempty"`
	Constraints      *pb.PolicyConstraints `json:"constraints,omitempty"`
	ApprovalStatus   ApprovalStatus        `json:"approval_status,omitempty"`
	ApprovalDecision ApprovalDecision      `json:"approval_decision,omitempty"`
	Timestamp        int64                 `json:"timestamp,omitempty"`
}

// DecisionQuery scopes Policy Decision Log lookups for a single tenant.
type DecisionQuery struct {
	Tenant  string         `json:"tenant,omitempty"`
	Since   int64          `json:"since,omitempty"`
	Until   int64          `json:"until,omitempty"`
	Topic   string         `json:"topic,omitempty"`
	RuleID  string         `json:"rule_id,omitempty"`
	Verdict SafetyDecision `json:"verdict,omitempty"`
	AgentID string         `json:"agent_id,omitempty"`
	Cursor  string         `json:"cursor,omitempty"`
	Limit   int            `json:"limit,omitempty"`
}

// DecisionPage is one page of Policy Decision Log results.
type DecisionPage struct {
	Items      []DecisionLogRecord `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

// Normalize applies defaults and validates the query.
func (q DecisionQuery) Normalize(now time.Time) (DecisionQuery, error) {
	normalized := q
	normalized.Tenant = strings.TrimSpace(normalized.Tenant)
	normalized.Topic = strings.TrimSpace(normalized.Topic)
	normalized.RuleID = strings.TrimSpace(normalized.RuleID)
	normalized.AgentID = strings.TrimSpace(normalized.AgentID)
	normalized.Cursor = strings.TrimSpace(normalized.Cursor)

	if normalized.Tenant == "" {
		return DecisionQuery{}, fmt.Errorf("decision query tenant is required")
	}

	if normalized.Limit <= 0 {
		normalized.Limit = DefaultDecisionQueryLimit
	}
	if normalized.Limit > MaxDecisionQueryLimit {
		return DecisionQuery{}, fmt.Errorf("decision query limit must be <= %d", MaxDecisionQueryLimit)
	}

	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	switch {
	case normalized.Since == 0 && normalized.Until == 0:
		normalized.Until = now.UnixMilli()
		normalized.Since = now.Add(-defaultDecisionQueryRange).UnixMilli()
	case normalized.Since == 0:
		normalized.Since = time.UnixMilli(normalized.Until).Add(-defaultDecisionQueryRange).UnixMilli()
	case normalized.Until == 0:
		normalized.Until = now.UnixMilli()
	}

	if normalized.Until < normalized.Since {
		return DecisionQuery{}, fmt.Errorf("decision query until must be >= since")
	}

	if normalized.Verdict != "" {
		if _, err := normalized.Verdict.DecisionLogWireValue(); err != nil {
			return DecisionQuery{}, err
		}
	}

	if normalized.Cursor != "" {
		if _, _, err := DecodeDecisionCursor(normalized.Cursor); err != nil {
			return DecisionQuery{}, err
		}
	}

	return normalized, nil
}

// DecisionLogWireValue returns the lowercase wire value for API transport.
func (d SafetyDecision) DecisionLogWireValue() (string, error) {
	switch d {
	case SafetyAllow:
		return DecisionVerdictAllow, nil
	case SafetyDeny:
		return DecisionVerdictDeny, nil
	case SafetyAllowWithConstraints:
		return DecisionVerdictConstrain, nil
	case SafetyRequireApproval:
		return DecisionVerdictRequireApproval, nil
	case SafetyThrottle:
		return DecisionVerdictThrottle, nil
	default:
		return "", fmt.Errorf("unknown decision verdict %q", d)
	}
}

// ParseDecisionLogVerdict converts the API wire enum to the canonical model enum.
func ParseDecisionLogVerdict(raw string) (SafetyDecision, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "":
		return "", nil
	case DecisionVerdictAllow:
		return SafetyAllow, nil
	case DecisionVerdictDeny:
		return SafetyDeny, nil
	case DecisionVerdictConstrain:
		return SafetyAllowWithConstraints, nil
	case DecisionVerdictRequireApproval:
		return SafetyRequireApproval, nil
	case DecisionVerdictThrottle:
		return SafetyThrottle, nil
	default:
		return "", fmt.Errorf("unknown decision verdict %q", raw)
	}
}

// EncodeDecisionCursor produces the opaque cursor format used by the API.
func EncodeDecisionCursor(timestamp int64, id string) string {
	payload := fmt.Sprintf("%d:%s", timestamp, id)
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

// DecodeDecisionCursor parses the opaque cursor format used by the API.
func DecodeDecisionCursor(cursor string) (int64, string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return 0, "", fmt.Errorf("decode decision cursor: %w", err)
	}
	raw := string(decoded)
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, "", fmt.Errorf("decode decision cursor: invalid cursor payload")
	}
	timestamp, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("decode decision cursor timestamp: %w", err)
	}
	return timestamp, parts[1], nil
}
