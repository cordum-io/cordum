package policybundles

import (
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	PolicySnapshotsScope = "system"
	PolicySnapshotsID    = "policy_snapshots"
	PolicySnapshotsKey   = "snapshots"
	PolicyAuditScope     = "system"
	PolicyAuditID        = "policy_audit"
	PolicyAuditKey       = "entries"
	PolicyStudioPrefix   = "secops/"

	// PolicyInvariantsBundleKey identifies the dedicated security-floor
	// bundle authored by SecOps. Rules from this bundle are applied with
	// DENY-uncrossable precedence by ApplyInvariants — invariant DENY
	// rules are emitted at the front of merged.Rules so first-match
	// evaluators short-circuit to deny, and invariant ALLOW rules are
	// emitted at the back so any explicit DENY (any source) still wins.
	PolicyInvariantsBundleKey = PolicyStudioPrefix + "invariants"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type PolicyBundleSnapshot struct {
	ID        string         `json:"id"`
	CreatedAt string         `json:"created_at"`
	Note      string         `json:"note,omitempty"`
	Bundles   map[string]any `json:"bundles"`
}

type PolicyBundleSnapshotSummary struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Note      string `json:"note,omitempty"`
}

type PolicyBundleSummary struct {
	ID          string `json:"id"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source"`
	Author      string `json:"author,omitempty"`
	Message     string `json:"message,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
	Version     string `json:"version,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
	Sha256      string `json:"sha256,omitempty"`
	RuleCount   int    `json:"rule_count"`
}

type PolicyBundleDetail struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Enabled   bool   `json:"enabled"`
	Author    string `json:"author,omitempty"`
	Message   string `json:"message,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type PolicyBundleUpsertRequest struct {
	Content string `json:"content"`
	Enabled *bool  `json:"enabled"`
	Author  string `json:"author"`
	Message string `json:"message"`
}

type PolicyPublishRequest struct {
	BundleIDs []string `json:"bundle_ids"`
	Author    string   `json:"author"`
	Message   string   `json:"message"`
	Note      string   `json:"note"`
}

type PolicyRollbackRequest struct {
	SnapshotID string `json:"snapshot_id"`
	Author     string `json:"author"`
	Message    string `json:"message"`
	Note       string `json:"note"`
}

type OutputRuleToggleRequest struct {
	Enabled *bool `json:"enabled"`
}

type PolicyAuditEntry struct {
	ID             string            `json:"id"`
	Action         string            `json:"action"`
	ResourceType   string            `json:"resource_type,omitempty"`
	ResourceID     string            `json:"resource_id,omitempty"`
	ResourceName   string            `json:"resource_name,omitempty"`
	ActorID        string            `json:"actor_id,omitempty"`
	Role           string            `json:"role,omitempty"`
	AuthSource     auth.AuthSource   `json:"auth_source,omitempty"`
	AgentID        string            `json:"agent_id,omitempty"`
	AgentName      string            `json:"agent_name,omitempty"`
	AgentRiskTier  string            `json:"agent_risk_tier,omitempty"`
	BundleIDs      []string          `json:"bundle_ids,omitempty"`
	Message        string            `json:"message,omitempty"`
	Reason         string            `json:"reason,omitempty"`
	Decision       string            `json:"decision,omitempty"`
	MatchedRule    string            `json:"matched_rule,omitempty"`
	PolicyVersion  string            `json:"policy_version,omitempty"`
	Extra          map[string]string `json:"extra,omitempty"`
	SnapshotBefore string            `json:"snapshot_before,omitempty"`
	SnapshotAfter  string            `json:"snapshot_after,omitempty"`
	CreatedAt      string            `json:"created_at"`
}

type PolicyRuleSource struct {
	FragmentID  string `json:"fragment_id"`
	Tier        string `json:"tier,omitempty"`
	PackID      string `json:"pack_id,omitempty"`
	OverlayName string `json:"overlay_name,omitempty"`
	Version     string `json:"version,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
	Sha256      string `json:"sha256,omitempty"`
}

type PolicyRuleParseError struct {
	FragmentID string `json:"fragment_id"`
	Error      string `json:"error"`
}
