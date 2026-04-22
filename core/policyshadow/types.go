// Package policyshadow holds the types and storage primitives for the
// Phase 2 shadow-policy feature. A shadow policy is a candidate bundle
// that runs alongside the active bundle during safety-kernel
// evaluation but NEVER affects the actual decision — its results are
// logged as separate audit events so operators can see what a proposed
// policy would have done in production.
//
// The package is deliberately dependency-light (config service + stdlib
// only) so both the kernel and the gateway can import it without
// pulling each other in.
package policyshadow

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// ShadowPolicy is the full stored representation of a candidate policy
// under evaluation. ShadowBundleID is the stable handle used by the
// results audit events and the dashboard; BundleID ties the shadow to
// a specific active bundle (one shadow per bundle at a time).
type ShadowPolicy struct {
	// ShadowBundleID is the unique identifier for this shadow policy
	// (`shadow-<12 hex chars>`). Consumers key on this independently
	// of the active bundle's lifecycle so a shadow can be correlated
	// in audit events even after it is superseded.
	ShadowBundleID string `json:"shadow_bundle_id"`

	// BundleID is the ID of the active bundle this shadow is tied to.
	// Only one shadow exists per BundleID at a time.
	BundleID string `json:"bundle_id"`

	// TenantID scopes the shadow to a single tenant; cross-tenant
	// access is rejected at the handler layer.
	TenantID string `json:"tenant_id"`

	// Content is the raw YAML source of the candidate policy. The
	// kernel parses it at eval time using the same path as the active
	// bundle.
	Content string `json:"content"`

	// CreatedAt is when the shadow document was first stored.
	CreatedAt time.Time `json:"created_at"`

	// ActivatedAt is when the shadow was most recently activated —
	// equal to CreatedAt on first activation, bumped on re-activation.
	ActivatedAt time.Time `json:"activated_at"`

	// CreatedBy is the principal ID that activated the shadow.
	CreatedBy string `json:"created_by"`

	// Metadata is arbitrary operator-supplied key/value pairs (e.g.
	// ticket references, experiment names). Not interpreted by the
	// kernel or results API.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// ShadowPolicySummary is the lightweight projection embedded inside
// bundle detail API responses. It omits the full Content so a listing
// of bundles doesn't accidentally leak unreviewed YAML into a paged
// response.
type ShadowPolicySummary struct {
	ShadowBundleID string    `json:"shadow_bundle_id"`
	BundleID       string    `json:"bundle_id"`
	TenantID       string    `json:"tenant_id"`
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	ActivatedAt    time.Time `json:"activated_at"`
}

// Summary returns the detail-view projection of this shadow policy.
func (s *ShadowPolicy) Summary() *ShadowPolicySummary {
	if s == nil {
		return nil
	}
	return &ShadowPolicySummary{
		ShadowBundleID: s.ShadowBundleID,
		BundleID:       s.BundleID,
		TenantID:       s.TenantID,
		CreatedBy:      s.CreatedBy,
		CreatedAt:      s.CreatedAt,
		ActivatedAt:    s.ActivatedAt,
	}
}

// ShadowBundleIDPrefix is the stable prefix every generated shadow
// bundle ID starts with. Consumers (kernel dual-eval, results API,
// dashboard) match on it to distinguish shadows from active bundles.
const ShadowBundleIDPrefix = "shadow-"

// shadowBundleIDHexLen is the number of hex characters in a generated
// ID suffix. 12 hex = 48 bits = ~1 in 280 trillion collision chance
// for any reasonable operator population.
const shadowBundleIDHexLen = 12

// NewShadowBundleID returns a freshly generated, globally unique ID
// for a shadow bundle. Format: `shadow-<12 lowercase hex chars>`.
// Uses crypto/rand so IDs are unguessable; crypto/rand.Read is never
// expected to fail but if it does we panic because this is called at
// activation time — there's no sensible recovery that preserves the
// "unique ID" contract.
func NewShadowBundleID() string {
	// 6 bytes -> 12 hex chars.
	b := make([]byte, shadowBundleIDHexLen/2)
	if _, err := rand.Read(b); err != nil {
		panic("policyshadow: crypto/rand failed: " + err.Error())
	}
	return ShadowBundleIDPrefix + hex.EncodeToString(b)
}
