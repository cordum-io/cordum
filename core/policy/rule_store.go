package policy

import (
	"context"
	"errors"
	"fmt"
)

// RuleStore is the unified storage interface for individual Rules in the
// Policy Studio (epic-d9a6c0a1 Backend 5c). The Redis implementation
// lives in `core/policy/rule_store_redis.go`.
//
// Design intent (mirrors BundleStore):
//   - Rules are authored individually and may be referenced by multiple
//     bundles via Bundle.RuleIDs[]. The store owns Rule lifecycle; bundle
//     membership is owned by BundleStore (AddRuleToBundle).
//   - All multi-key mutations execute inside Redis Lua scripts so
//     concurrent UpdateRule calls serialize through Redis itself, not
//     via Go-level WATCH/MULTI/EXEC (per memory mem-12f1ceeb — go-redis
//     WATCH+TxPipelined corrupts the pool when miniredis errors).
//   - Optimistic concurrency: UpdateRule takes the caller's expected
//     `Version` and refuses the write when it doesn't match the stored
//     value. Stale callers get ErrRuleStaleVersion carrying the current
//     version + audit hash so the dashboard can render a reload banner.
//
// Audit metadata is server-only on writes:
//   - CreateRule sets CreatedAt = UpdatedAt = time.Now() and Version = "v1".
//   - UpdateRule preserves CreatedAt, sets UpdatedAt = time.Now(), and
//     bumps Version with `bumpVersion`.
//   - Client-supplied Audit.* / Version / ID fields on Create are
//     silently ignored (the store overwrites them) — handlers should
//     additionally reject those at the HTTP boundary so the rejection is
//     visible in the API contract.
type RuleStore interface {
	// CreateRule persists a new Rule. Returns ErrRuleExists when a Rule
	// with the same ID already exists. The caller's Version / Audit
	// fields are overwritten server-side.
	CreateRule(ctx context.Context, r *Rule) (*Rule, error)

	// GetRule returns the Rule by ID. Returns ErrRuleNotFound when no
	// such rule.
	GetRule(ctx context.Context, id string) (*Rule, error)

	// UpdateRule writes the Rule iff its stored Version equals
	// expectedVersion. On success, Version is bumped + UpdatedAt is
	// refreshed; the updated Rule is returned. On stale version,
	// returns *ErrRuleStaleVersion with the server's current_version +
	// current_audit_hash so the caller can render a meaningful conflict
	// banner. ErrRuleNotFound when the rule doesn't exist.
	UpdateRule(ctx context.Context, r *Rule, expectedVersion string) (*Rule, error)

	// DeleteRule removes the Rule. Returns ErrRuleNotFound when the
	// rule doesn't exist.
	DeleteRule(ctx context.Context, id string) error

	// ListRulesByScope returns every Rule whose Scope matches the
	// supplied scope, ordered by ID ascending for deterministic test +
	// pagination behaviour. An empty result is not an error.
	ListRulesByScope(ctx context.Context, scope RuleScope) ([]*Rule, error)

	// ListRules returns every Rule in the store regardless of scope,
	// ordered by ID ascending. Used by the unified
	// `GET /api/v1/policy/rules` surface (Backend 5d) which merges
	// store-authored Rules with YAML-bundle-loaded Rules from
	// `policybundles.RulesFromPolicyContent`. An empty result is not an
	// error. Implementations should accept a context-bound cancel so
	// large stores don't pin a connection.
	ListRules(ctx context.Context) ([]*Rule, error)
}

// Typed errors returned by RuleStore implementations. Consumers branch
// via errors.Is for sentinel cases; ErrRuleStaleVersion is a struct so
// callers can extract the conflict body without re-fetching.
var (
	// ErrRuleNotFound is returned when a Rule lookup misses.
	ErrRuleNotFound = errors.New("policy: rule not found")
	// ErrRuleExists is returned by CreateRule on duplicate ID.
	ErrRuleExists = errors.New("policy: rule already exists")
)

// ErrRuleStaleVersion is the optimistic-concurrency conflict surface for
// UpdateRule. The handler maps this to HTTP 409 + body
// `{ "current_version": ..., "current_audit_hash": ... }` so a client
// like Dashboard 3E can present a reload-banner without an extra fetch.
type ErrRuleStaleVersion struct {
	CurrentVersion   string
	CurrentAuditHash string
}

func (e *ErrRuleStaleVersion) Error() string {
	return fmt.Sprintf(
		"policy: rule stale version (current=%s, audit_hash=%s)",
		e.CurrentVersion, e.CurrentAuditHash,
	)
}

// IsStaleVersionError reports whether err wraps a *ErrRuleStaleVersion
// so handlers can extract conflict metadata for the response body.
func IsStaleVersionError(err error) (*ErrRuleStaleVersion, bool) {
	var stale *ErrRuleStaleVersion
	if errors.As(err, &stale) {
		return stale, true
	}
	return nil, false
}
