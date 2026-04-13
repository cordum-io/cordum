package audit

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cordum/cordum/core/licensing"
)

func newAuditEntitlementResolver(t *testing.T, plan licensing.Plan, mutate func(*licensing.Entitlements)) *licensing.EntitlementResolver {
	t.Helper()

	entitlements := licensing.DefaultEntitlements(plan)
	if mutate != nil {
		mutate(&entitlements)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	payloadBytes, err := json.Marshal(licensing.Claims{
		Plan:         string(plan),
		Entitlements: &entitlements,
	})
	if err != nil {
		t.Fatalf("marshal license payload: %v", err)
	}

	licenseBytes, err := json.Marshal(map[string]any{
		"payload":   json.RawMessage(payloadBytes),
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payloadBytes)),
	})
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}

	t.Setenv("CORDUM_LICENSE_FILE", "")
	t.Setenv("CORDUM_LICENSE_TOKEN", string(licenseBytes))
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY_PATH", "")
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(publicKey))

	resolver := licensing.NewEntitlementResolver()
	resolver.Init()
	return resolver
}

func TestRetentionTTLFromEntitlements(t *testing.T) {
	tests := []struct {
		name         string
		entitlements licensing.Entitlements
		want         time.Duration
	}{
		{
			name:         "community default",
			entitlements: licensing.DefaultEntitlements(licensing.PlanCommunity),
			want:         7 * 24 * time.Hour,
		},
		{
			name: "custom days",
			entitlements: licensing.Entitlements{
				AuditRetentionDays: 30,
			},
			want: 30 * 24 * time.Hour,
		},
		{
			name: "unlimited",
			entitlements: licensing.Entitlements{
				AuditRetentionDays: licensing.Unlimited,
			},
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RetentionTTLFromEntitlements(tc.entitlements); got != tc.want {
				t.Fatalf("RetentionTTLFromEntitlements() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNewExporterFromEnvWithEntitlementsDisablesSIEMForCommunity(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "webhook")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL", "https://example.com/hook")

	exp, err := NewExporterFromEnvWithEntitlements(newAuditEntitlementResolver(t, licensing.PlanCommunity, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp != nil {
		t.Fatal("expected nil exporter when community plan lacks SIEM export entitlement")
	}
}

func TestNewExporterFromEnvWithEntitlementsSetsRetentionTTL(t *testing.T) {
	t.Setenv("CORDUM_AUDIT_EXPORT_TYPE", "webhook")
	t.Setenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL", "https://example.com/hook")

	exp, err := NewExporterFromEnvWithEntitlements(newAuditEntitlementResolver(t, licensing.PlanCommunity, func(entitlements *licensing.Entitlements) {
		entitlements.SIEMExport = true
		entitlements.AuditExport = true
		entitlements.AuditRetentionDays = 30
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exp == nil {
		t.Fatal("expected exporter for entitled SIEM export")
	}
	defer func() { _ = exp.Close() }()

	if got := exp.RetentionTTL(); got != 30*24*time.Hour {
		t.Fatalf("RetentionTTL() = %s, want %s", got, 30*24*time.Hour)
	}
}

func TestRequireLegalHoldEntitlement(t *testing.T) {
	err := RequireLegalHoldEntitlement(newAuditEntitlementResolver(t, licensing.PlanCommunity, nil))
	var limitErr *licensing.TierLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected tier limit error, got %v", err)
	}
	if limitErr.Limit != "legal_hold" {
		t.Fatalf("limit = %q, want legal_hold", limitErr.Limit)
	}

	if err := RequireLegalHoldEntitlement(newAuditEntitlementResolver(t, licensing.PlanEnterprise, nil)); err != nil {
		t.Fatalf("enterprise legal hold should be allowed, got %v", err)
	}
	if !LegalHoldEnabled(licensing.DefaultEntitlements(licensing.PlanEnterprise)) {
		t.Fatal("expected enterprise legal hold feature to be enabled")
	}
}
