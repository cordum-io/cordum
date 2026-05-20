package gateway

import (
	"errors"
	"testing"

	"github.com/cordum/cordum/core/licensing"
)

func TestLegalHold_NonTierBranchEmitsTierLimitShape(t *testing.T) {
	got := legalHoldTierLimitHTTPError(errors.New("entitlement resolver unavailable"))

	if got.Code != "tier_limit_exceeded" {
		t.Fatalf("Code = %q, want tier_limit_exceeded", got.Code)
	}
	if got.Limit != "legal_hold" {
		t.Fatalf("Limit = %q, want legal_hold", got.Limit)
	}
	if got.UpgradeURL != licensing.DefaultUpgradeURL {
		t.Fatalf("UpgradeURL = %q, want %q", got.UpgradeURL, licensing.DefaultUpgradeURL)
	}
	if got.Message == "" {
		t.Fatal("Message is empty, want dashboard-safe upgrade message")
	}
}

func TestLegalHold_TierLimitErrorPreservesLimitFields(t *testing.T) {
	got := legalHoldTierLimitHTTPError(&licensing.TierLimitError{
		Limit:      "legal_hold",
		Current:    1,
		Allowed:    0,
		UpgradeURL: "https://example.com/upgrade",
	})

	if got.Code != "tier_limit_exceeded" || got.Limit != "legal_hold" {
		t.Fatalf("tier error shape = %+v", got)
	}
	if got.Current != 1 || got.Allowed != 0 {
		t.Fatalf("Current/Allowed = %d/%d, want 1/0", got.Current, got.Allowed)
	}
	if got.UpgradeURL != "https://example.com/upgrade" {
		t.Fatalf("UpgradeURL = %q", got.UpgradeURL)
	}
}
