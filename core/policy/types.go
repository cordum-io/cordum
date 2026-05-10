package policy

import (
	"encoding/json"
	"time"
)

// Rule is the unified authoring surface for both job-side and edge-side
// policy. The envelope (ID, Name, Type, Scope, Status, Version, Audit) is
// shared across all rule types; the type-specific Match/Decide payloads carry
// the per-RuleType schema as raw JSON to preserve lossless roundtrip with
// orval-generated dashboard types and protobuf google.protobuf.Struct
// transport.
type Rule struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Type        RuleType        `json:"type"`
	Scope       RuleScope       `json:"scope"`
	Status      RuleStatus      `json:"status"`
	Version     string          `json:"version"`
	Audit       AuditMetadata   `json:"audit"`
	Match       json.RawMessage `json:"match"`
	Decide      json.RawMessage `json:"decide"`
	Description string          `json:"description,omitempty"`
}

// Decision is the unified evaluator output. A single Decision may aggregate
// multiple rule evaluations via Trace; Type holds the final outcome the
// evaluator derived from the trace. Source distinguishes the producing
// evaluator (job pipeline vs edge classifier path); InputRef and OutputRef
// are pointers into the blob store rather than embedded payloads.
type Decision struct {
	Source        DecisionSource `json:"source"`
	RuleID        string         `json:"rule_id"`
	BundleID      string         `json:"bundle_id,omitempty"`
	BundleVersion string         `json:"bundle_version,omitempty"`
	Type          DecisionType   `json:"type"`
	Trace         []TraceStep    `json:"trace,omitempty"`
	InputRef      string         `json:"input_ref,omitempty"`
	OutputRef     string         `json:"output_ref,omitempty"`
	AuditHash     string         `json:"audit_hash,omitempty"`
	Timestamp     time.Time      `json:"timestamp"`
}

// TraceStep records one rule evaluation contributing to a Decision. Multiple
// TraceSteps are emitted when a rule chain or bundle composition produces a
// final decision through ordered evaluation. Constraints carries the raw
// constraints payload (e.g. the existing safetykernel BudgetConstraints/
// SandboxProfile JSON) when the evaluator applied an allow_with_constraints
// path; downstream consumers parse it per RuleType.
type TraceStep struct {
	RuleID       string          `json:"rule_id"`
	BundleID     string          `json:"bundle_id,omitempty"`
	DecisionType DecisionType    `json:"decision_type"`
	Reason       string          `json:"reason,omitempty"`
	Timestamp    time.Time       `json:"timestamp"`
	Constraints  json.RawMessage `json:"constraints,omitempty"`
}

// Bundle is a deployable set of rules with a deployment scope. Rules are
// referenced by ID rather than embedded so a bundle can rotate its members
// without forcing rule duplication; the snapshot at deploy time lives in
// BundleVersion.RuleSnapshot for tamper-evident rollback.
type Bundle struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	RuleIDs      []string        `json:"rule_ids,omitempty"`
	ScopeBinding RuleScope       `json:"scope_binding"`
	Versions     []BundleVersion `json:"versions,omitempty"`
	Metadata     BundleMetadata  `json:"metadata,omitzero"`
}

// BundleMetadata carries per-bundle authoring metadata that does not affect
// match/decide payloads. EdgeMode is the per-bundle enforcement posture for
// edge-scoped bundles, replacing today's global EdgePolicyMode switch.
type BundleMetadata struct {
	EdgeMode EdgeMode `json:"edge_mode,omitempty"`
}

// BundleVersion is the immutable record of a Bundle deploy. RuleSnapshot
// captures the full Rule contents at deploy time so rollback and audit do
// not depend on the live rule store; AuditHash chains the version into the
// audit chain.
type BundleVersion struct {
	Version      string    `json:"version"`
	RuleSnapshot []Rule    `json:"rule_snapshot,omitempty"`
	DeployedAt   time.Time `json:"deployed_at"`
	AuditHash    string    `json:"audit_hash,omitempty"`
}

// RuleScope narrows where a rule (or bundle) applies. When Kind is
// RuleScopeGlobal, Value is unused and may be empty; for the other kinds
// Value carries the corresponding identifier (tenant id, workflow id, edge
// fleet name, edge user id).
type RuleScope struct {
	Kind  RuleScopeKind `json:"kind"`
	Value string        `json:"value,omitempty"`
}

// AuditMetadata records who created or last updated a Rule or Bundle. The
// Updated* fields are zero-valued on first creation and populated by the
// first subsequent edit. PackSource is populated when the rule originated
// from a policy pack bundle (`policybundles.PolicyRuleSourceFromBundle`)
// rather than from interactive authoring via the RuleStore — the
// dashboard's pack_id + tier columns + Source filter depend on it
// surfacing on every legacy-bundle-loaded rule.
type AuditMetadata struct {
	CreatedAt  time.Time   `json:"created_at"`
	CreatedBy  string      `json:"created_by"`
	UpdatedAt  time.Time   `json:"updated_at,omitzero"`
	UpdatedBy  string      `json:"updated_by,omitempty"`
	PackSource *PackSource `json:"pack_source,omitempty"`
}

// PackSource carries pack-level provenance for rules loaded from
// `cordumctl pack install` YAML bundles. Mirrors
// `policybundles.PolicyRuleSource` field-for-field so legacy → unified
// translation is lossless without a circular dep on the policybundles
// package. RuleStore-authored rules leave this nil.
type PackSource struct {
	FragmentID  string `json:"fragment_id,omitempty"`
	Tier        string `json:"tier,omitempty"`
	PackID      string `json:"pack_id,omitempty"`
	OverlayName string `json:"overlay_name,omitempty"`
	Version     string `json:"version,omitempty"`
	InstalledAt string `json:"installed_at,omitempty"`
	Sha256      string `json:"sha256,omitempty"`
}
