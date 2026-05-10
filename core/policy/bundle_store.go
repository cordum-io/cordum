package policy

import (
	"context"
	"errors"
	"time"
)

// BundleStore is the unified storage interface for Policy Studio bundles
// (epic-d9a6c0a1). Both job-side (safetykernel) and edge-side consumers
// read through this interface; the Redis implementation lives in
// bundle_store_redis.go (Backend 2 step-4).
//
// Design intent:
//   - Bundles are authored once and deployed multiple times. Versions
//     capture immutable RuleSnapshot at deploy time for tamper-evident
//     rollback.
//   - Deployments bind a (bundle, version) pair to a RuleScope. The
//     active deployment is the one evaluators consult; rollbacks are
//     append-only and re-bind the active pointer to a prior version.
//   - All multi-key mutations (DeployVersionToScope, RollbackDeployment)
//     execute inside one Redis Lua script so concurrent deploys
//     serialize through Redis itself, not through Go-level WATCH/MULTI
//     (see memory mem-12f1ceeb — go-redis WATCH+TxPipelined corrupts the
//     connection pool when miniredis returns errors).
type BundleStore interface {
	// Bundle CRUD ----------------------------------------------------------

	// CreateBundle persists a new Bundle. The Bundle's ID must be set; an
	// existing bundle with the same ID returns ErrBundleExists.
	CreateBundle(ctx context.Context, b *Bundle) error

	// GetBundle returns the Bundle envelope (without RuleSnapshot — those
	// live in Versions). Returns ErrBundleNotFound when no such bundle.
	GetBundle(ctx context.Context, id string) (*Bundle, error)

	// ListBundlesByScope returns every bundle whose ScopeBinding matches
	// the supplied scope. An empty result is not an error.
	ListBundlesByScope(ctx context.Context, scope RuleScope) ([]*Bundle, error)

	// Bundle versioning ---------------------------------------------------

	// CreateBundleVersion appends a new version to bundleID. Idempotent on
	// duplicate version numbers (returns ErrBundleVersionExists). Versions
	// are immutable once written.
	CreateBundleVersion(ctx context.Context, bundleID string, v *BundleVersion) error

	// ListBundleVersions returns all versions for the bundle in
	// chronological deploy order (oldest first).
	ListBundleVersions(ctx context.Context, bundleID string) ([]*BundleVersion, error)

	// GetBundleVersion returns one specific version. Returns
	// ErrBundleVersionNotFound when the (bundleID, version) pair is unknown.
	GetBundleVersion(ctx context.Context, bundleID, version string) (*BundleVersion, error)

	// Deployment ----------------------------------------------------------

	// DeployVersionToScope rebinds the active deployment for scope to the
	// given (bundleID, version). The previous active deployment (if any)
	// is preserved in deployment history. Returns the recorded Deployment
	// on success; ErrBundleVersionNotFound if the version doesn't exist.
	DeployVersionToScope(ctx context.Context, bundleID, version string, scope RuleScope) (*Deployment, error)

	// RollbackDeployment reverts the active deployment for scope to the
	// (bundle, version) pair that was active immediately before the
	// current binding was established. Each deploy event records its
	// PrevBundleID + PrevVersion at write time, so rollback restores the
	// prior active pair regardless of any rollback markers in between
	// (e.g. deploy v1, deploy v2, rollback to v1, deploy v3, rollback
	// returns to v1 — the active state just before v3 — not v2). Chained
	// rollbacks unwind one step per call.
	//
	// Returns ErrNoRollbackTarget when no prior deploy exists (e.g. only
	// the original deploy is in history, or its PrevBundleID is empty).
	// On success the rollback itself is appended to history as a new
	// Deployment with Action=DeploymentActionRollback whose PrevBundleID
	// + PrevVersion fields capture the version we rolled away from
	// (informational; rollback does not consume its own prev fields).
	RollbackDeployment(ctx context.Context, scope RuleScope) (*Deployment, error)

	// GetActiveDeployment returns the active deployment for scope.
	// Returns ErrNoDeploymentForScope when nothing is currently bound.
	GetActiveDeployment(ctx context.Context, scope RuleScope) (*Deployment, error)

	// ListDeploymentHistory returns up to limit entries from the scope's
	// deployment history, newest first. The history is bounded to the
	// last 100 entries by the implementation.
	ListDeploymentHistory(ctx context.Context, scope RuleScope, limit int) ([]*Deployment, error)

	// Rule binding -------------------------------------------------------

	// AddRuleToBundle appends ruleID to the bundle's RuleIDs set. The
	// caller's RuleStore is consulted via ruleExists so the binding
	// rejects unknown rule IDs (returns ErrRuleNotFound). Idempotent —
	// calling twice with the same ruleID leaves RuleIDs unchanged on the
	// second call. Returns the updated Bundle on success, ErrBundleNotFound
	// when the bundle doesn't exist. Concurrent AddRuleToBundle calls on
	// the same bundle but distinct ruleIDs converge — Lua atomicity
	// ensures both rule IDs end up in RuleIDs[] without a lost write.
	AddRuleToBundle(
		ctx context.Context,
		bundleID, ruleID string,
		ruleExists func(ctx context.Context, ruleID string) (bool, error),
	) (*Bundle, error)
}

// DeploymentAction discriminates a Deployment record between a fresh
// deploy and a rollback to a prior version. The two paths share the rest
// of the envelope; downstream readers branch on Action when they need to
// surface the distinction (e.g. the dashboard "Deployments" tab).
type DeploymentAction string

const (
	// DeploymentActionDeploy records a fresh deploy of a (bundle, version)
	// pair to a scope. The Version field carries the deployed version.
	DeploymentActionDeploy DeploymentAction = "deploy"

	// DeploymentActionRollback records a rollback to a prior version. The
	// Version field carries the version rolled BACK TO (i.e. the new
	// active version after the rollback completes), not the version
	// rolled away from.
	DeploymentActionRollback DeploymentAction = "rollback"
)

// Deployment is the audit-ready record of one binding event between a
// (bundle, version) and a scope. Stored in scope deployment history; the
// most recent entry whose Action == DeploymentActionDeploy or Rollback
// determines the active deployment.
//
// Each deploy event records PrevBundleID + PrevVersion at write time —
// the (bundle, version) pair that was active immediately before this
// event ran. Rollback uses those fields to restore the prior active
// pair regardless of any deploy/rollback events that landed in between.
// On a deploy event, the prev pair is the active state just before this
// deploy. On a rollback event, the prev pair is the active state just
// before the rollback (i.e. the version we rolled away from); rollback
// does not consume its own prev fields.
type Deployment struct {
	BundleID     string           `json:"bundle_id"`
	Version      string           `json:"version"`
	Scope        RuleScope        `json:"scope"`
	DeployedAt   time.Time        `json:"deployed_at"`
	DeployedBy   string           `json:"deployed_by,omitempty"`
	AuditHash    string           `json:"audit_hash,omitempty"`
	Action       DeploymentAction `json:"action"`
	PrevBundleID string           `json:"prev_bundle_id,omitempty"`
	PrevVersion  string           `json:"prev_version,omitempty"`
}

// Typed errors returned by BundleStore implementations. Consumers should
// branch on these via errors.Is for stable contract tests.
var (
	ErrBundleNotFound        = errors.New("policy: bundle not found")
	ErrBundleExists          = errors.New("policy: bundle already exists")
	ErrBundleVersionNotFound = errors.New("policy: bundle version not found")
	ErrBundleVersionExists   = errors.New("policy: bundle version already exists")
	ErrNoDeploymentForScope  = errors.New("policy: no active deployment for scope")
	ErrNoRollbackTarget      = errors.New("policy: no prior deployment to roll back to")
)
