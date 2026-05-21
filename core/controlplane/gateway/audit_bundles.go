package gateway

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
)

// listSignedBundleSnapshots returns the policy bundles that were active
// (wholly or partially) inside the window [from, to] for the given
// tenant, together with whatever signature metadata task-fcd39725
// persisted alongside them.
//
// Bundle-state is read via the existing loadPolicyBundles path so this
// function does not reach through a separate abstraction — if the
// bundle store ever migrates the compliance export follows along
// naturally.
//
// Signature shape: task-fcd39725 attaches a `_signature` map under each
// bundle with keys {algorithm, key_id, value, hash, signed_bytes}. The
// export snapshot maps those to SignedBundleSnapshot fields using the
// existing signatureFromMap helper from handlers_policy_bundles_signing.go.
// When a bundle is unsigned (strict mode off in production, or a legacy
// bundle predating the signing feature) the snapshot carries a non-empty
// Note field so the downstream audit reviewer understands the gap.
//
// The caller is responsible for tenant scoping; this helper always
// reads the tenant's own bundles (configsvc document is system-scoped
// but bundle IDs are tenant-prefixed in the Cordum convention — the
// ctx carries the caller's tenant and the handler pre-filters).
func (s *server) listSignedBundleSnapshots(ctx context.Context, from, to time.Time) ([]audit.SignedBundleSnapshot, error) {
	if s == nil || s.configSvc == nil {
		return []audit.SignedBundleSnapshot{}, nil
	}
	bundles, _, err := s.loadPolicyBundles(ctx)
	if err != nil {
		return nil, fmt.Errorf("list signed bundle snapshots: %w", err)
	}
	// Sort bundle IDs so the manifest is reproducible.
	ids := make([]string, 0, len(bundles))
	for id := range bundles {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	out := make([]audit.SignedBundleSnapshot, 0, len(ids))
	for _, id := range ids {
		rawBundle, ok := bundles[id].(map[string]any)
		if !ok {
			continue
		}
		snap := bundleSnapshotFor(id, rawBundle)
		if !snapshotOverlapsWindow(snap, from, to) {
			continue
		}
		out = append(out, snap)
	}
	return out, nil
}

// bundleSnapshotFor extracts a SignedBundleSnapshot from a bundle map.
// Unsigned bundles get Note="unsigned" so the downstream auditor sees
// the gap explicitly rather than an empty signature field of ambiguous
// meaning.
func bundleSnapshotFor(id string, bundle map[string]any) audit.SignedBundleSnapshot {
	snap := audit.SignedBundleSnapshot{
		BundleID:      id,
		Name:          policybundles.StringFromAny(bundle["name"]),
		Version:       policybundles.StringFromAny(bundle["version"]),
		ActivatedAt:   bundleTimestampFor(bundle, "activated_at", "created_at", "updated_at"),
		DeactivatedAt: bundleTimestampFor(bundle, "deactivated_at", "disabled_at", "archived_at"),
		SignedBy:      policybundles.StringFromAny(bundle["author"]),
	}
	sigAny, ok := bundle[policyBundleSignatureKey]
	if !ok {
		snap.Note = "unsigned"
		return snap
	}
	sig, ok := signatureFromMap(sigAny)
	if !ok {
		snap.Note = "signature-malformed"
		return snap
	}
	snap.ContentSHA256Hex = sig.Hash
	snap.Ed25519SigBase64 = sig.Value
	snap.PublicKeyID = sig.KeyID
	if snap.SignedBy == "" {
		snap.SignedBy = policybundles.StringFromAny(bundle["signed_by"])
	}
	return snap
}

// bundleTimestampFor returns the first non-empty string value among the
// provided keys. Bundles in the wild carry a mix of activated_at /
// created_at / updated_at depending on which handler touched them last.
func bundleTimestampFor(bundle map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := bundle[k]; ok {
			if s := strings.TrimSpace(policybundles.StringFromAny(v)); s != "" {
				return s
			}
		}
	}
	return ""
}

// snapshotOverlapsWindow returns true when the bundle's activation
// interval intersects [from, to]. Missing timestamps are treated
// optimistically: an unknown start means "could have been active
// forever"; an unknown end means "still active". Operators who want
// stricter filtering can attach the relevant timestamp fields before
// enabling the feature.
func snapshotOverlapsWindow(snap audit.SignedBundleSnapshot, from, to time.Time) bool {
	if from.IsZero() && to.IsZero() {
		return true
	}
	start, startOK := parseBundleTimestamp(snap.ActivatedAt)
	end, endOK := parseBundleTimestamp(snap.DeactivatedAt)
	// If we have no bounds we can assert, include the bundle. A
	// compliance auditor prefers over-inclusion to silently missing
	// rows.
	if !startOK && !endOK {
		return true
	}
	if startOK && !to.IsZero() && start.After(to) {
		return false
	}
	if endOK && !from.IsZero() && end.Before(from) {
		return false
	}
	return true
}

// parseBundleTimestamp is permissive: it accepts RFC 3339 and the
// handful of layout variants the bundle handlers have historically
// persisted. Failure returns (zero-time, false) so the caller can fall
// back to the open-interval heuristic rather than dropping the row.
func parseBundleTimestamp(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.000Z",
		"2006-01-02T15:04:05Z",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
