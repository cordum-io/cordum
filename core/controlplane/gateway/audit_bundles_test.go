package gateway

import (
	"reflect"
	"testing"
	"time"
)

func TestBundleSnapshotFor_UnsignedGetsNote(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"name":       "Demo policy",
		"version":    "v1",
		"created_at": "2026-04-17T20:00:00Z",
	}
	got := bundleSnapshotFor("secops/demo", raw)
	if got.BundleID != "secops/demo" {
		t.Errorf("BundleID = %q", got.BundleID)
	}
	if got.Note != "unsigned" {
		t.Errorf("Note = %q, want unsigned", got.Note)
	}
	if got.Ed25519SigBase64 != "" || got.ContentSHA256Hex != "" {
		t.Errorf("signature fields should be empty for unsigned bundle: %+v", got)
	}
}

func TestBundleSnapshotFor_SignedPopulatesFields(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"name":         "Signed policy",
		"version":      "v2",
		"author":       "ops@cordum.io",
		"activated_at": "2026-04-17T20:00:00Z",
		policyBundleSignatureKey: map[string]any{
			"algorithm":    "ed25519",
			"key_id":       "primary-2026",
			"value":        "AAAA",
			"hash":         "deadbeefdeadbeef",
			"signed_bytes": 123,
		},
	}
	got := bundleSnapshotFor("secops/signed", raw)
	if got.Note != "" {
		t.Errorf("signed bundle should have empty Note, got %q", got.Note)
	}
	if got.PublicKeyID != "primary-2026" {
		t.Errorf("PublicKeyID = %q", got.PublicKeyID)
	}
	if got.Ed25519SigBase64 != "AAAA" {
		t.Errorf("signature = %q", got.Ed25519SigBase64)
	}
	if got.ContentSHA256Hex != "deadbeefdeadbeef" {
		t.Errorf("hash = %q", got.ContentSHA256Hex)
	}
	if got.SignedBy != "ops@cordum.io" {
		t.Errorf("SignedBy = %q", got.SignedBy)
	}
}

func TestBundleSnapshotFor_MalformedSignatureNoted(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"name": "Broken",
		// Empty map — signatureFromMap returns (zero, false).
		policyBundleSignatureKey: map[string]any{},
	}
	got := bundleSnapshotFor("secops/broken", raw)
	if got.Note != "signature-malformed" {
		t.Errorf("Note = %q, want signature-malformed", got.Note)
	}
}

func TestSnapshotOverlapsWindow(t *testing.T) {
	t.Parallel()
	from := time.Date(2026, 4, 17, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 4, 17, 23, 59, 59, 0, time.UTC)

	cases := []struct {
		name    string
		active  string
		deactiv string
		want    bool
	}{
		{"missing-both-includes", "", "", true},
		{"activated-before-window", "2026-04-16T00:00:00Z", "", true},
		{"activated-after-window", "2026-04-18T00:00:00Z", "", false},
		{"deactivated-before-window", "", "2026-04-16T23:59:00Z", false},
		{"deactivated-inside-window", "2026-04-16T00:00:00Z", "2026-04-17T10:00:00Z", true},
		{"malformed-timestamps-includes", "garbage", "also-garbage", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := bundleSnapshotFor("x", map[string]any{
				"activated_at":   c.active,
				"deactivated_at": c.deactiv,
			})
			if got := snapshotOverlapsWindow(snap, from, to); got != c.want {
				t.Errorf("overlap = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBundleTimestampFor_FirstNonEmpty(t *testing.T) {
	t.Parallel()
	b := map[string]any{
		"created_at": "",
		"updated_at": "2026-04-17T12:00:00Z",
	}
	got := bundleTimestampFor(b, "activated_at", "created_at", "updated_at")
	if got != "2026-04-17T12:00:00Z" {
		t.Errorf("got %q, want fallback to updated_at", got)
	}
}

// TestListSignedBundleSnapshots_SeedsFromStore stands up a live gateway
// with miniredis, writes two bundles via the PUT handler (one signed,
// one unsigned by toggling CORDUM_POLICY_STRICT=off), then asserts
// listSignedBundleSnapshots returns both with the right signature
// shape. Exercises the full load path (configSvc.Get + normalize
// JSON) rather than a fake store so the helper stays honest.
func TestListSignedBundleSnapshots_SeedsFromStore(t *testing.T) {
	// Signed bundle.
	t.Setenv("CORDUM_POLICY_STRICT", "warn")
	_, _ = setTestSigningEnv(t, "warn")
	s, _, _ := newTestGateway(t)

	if rec := putSignedBundle(t, s, "secops/signed", policyContent); rec.Code != 200 {
		t.Fatalf("signed bundle put: %d %s", rec.Code, rec.Body.String())
	}

	// Second bundle, unsigned: clear the signing env AND set mode=off
	// so signPolicyBundleContent returns an empty outcome (no sig).
	clearTestSigningEnv(t)
	t.Setenv("CORDUM_POLICY_STRICT", "off")
	if rec := putSignedBundle(t, s, "secops/unsigned", policyContent); rec.Code != 200 {
		t.Fatalf("unsigned bundle put: %d %s", rec.Code, rec.Body.String())
	}

	snaps, err := s.listSignedBundleSnapshots(testContext(), time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("listSignedBundleSnapshots: %v", err)
	}
	byID := map[string]bool{}
	for _, snap := range snaps {
		byID[snap.BundleID] = true
		if snap.BundleID == "secops/signed" {
			if snap.Note != "" || snap.Ed25519SigBase64 == "" {
				t.Errorf("signed bundle snapshot missing sig: %+v", snap)
			}
		}
		if snap.BundleID == "secops/unsigned" {
			if snap.Note != "unsigned" {
				t.Errorf("unsigned bundle should carry Note=unsigned, got %+v", snap)
			}
		}
	}
	if !byID["secops/signed"] || !byID["secops/unsigned"] {
		t.Errorf("missing expected bundles, got %+v", byID)
	}
	// Deterministic ordering.
	copied := append([]string(nil), "secops/signed", "secops/unsigned")
	actual := []string{}
	for _, s := range snaps {
		actual = append(actual, s.BundleID)
	}
	// Sort and compare (both should be lexicographic).
	if !reflect.DeepEqual(actual, copied) {
		t.Errorf("bundle order = %v, want %v (lexicographic)", actual, copied)
	}
}
