package gateway

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/governance"
)

// govHealthHMACKey is a 32-byte test key. audit.WithHMACKey PANICS on a
// non-empty key shorter than 32 bytes, so the length here is load-bearing.
var govHealthHMACKey = []byte("0123456789abcdef0123456789abcdef")

// seedGovHealthChain appends n events to the tenant's audit chain through a
// real Chainer. When key is non-nil the events carry HMAC-SHA256 tags, so a
// later VerifyChain that lacks the key must fail-closed (HMACSeen=true,
// HMACVerified=0) rather than report a false-green.
func seedGovHealthChain(t *testing.T, s *server, tenant string, n int, key []byte) {
	t.Helper()
	var opts []audit.ChainerOption
	if len(key) > 0 {
		opts = append(opts, audit.WithHMACKey(key))
	}
	chainer := audit.NewChainer(s.redisClient(), "", opts...)
	for i := 0; i < n; i++ {
		ev := audit.SIEMEvent{
			EventType: audit.EventSafetyDecision,
			Severity:  audit.SeverityInfo,
			TenantID:  tenant,
			Action:    "seed",
			JobID:     "job-" + string(rune('0'+i)),
		}
		if err := chainer.Append(context.Background(), &ev); err != nil {
			t.Fatalf("seed append[%d]: %v", i, err)
		}
	}
}

// TestGovernanceVerifyChain_HMACSeenWithoutKeyDowngradesToPartial is the RED
// test for the governance-health false-green: an HMAC-signed chain verified
// WITHOUT the HMAC key leaves HMACVerified=0 with Status=ok, which the handler
// mapped to ChainStatusOK — a false-green on the governance health surface.
// After the fix it must fail-closed to ChainStatusPartial, mirroring
// /api/v1/audit/verify and the compliance-export fix (task-33485ac3).
func TestGovernanceVerifyChain_HMACSeenWithoutKeyDowngradesToPartial(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedGovHealthChain(t, s, "default", 3, govHealthHMACKey)
	// Server has no HMAC key configured -> nothing is injected into the
	// VerifyOptions, so the HMAC tags are seen but never verified.
	s.auditChainer = nil

	status, err := newGovernanceHealthDeps(s, "default").VerifyChain(context.Background())
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if status != governance.ChainStatusPartial {
		t.Fatalf("status = %q, want %q (HMAC seen but no key must fail-closed to partial)", status, governance.ChainStatusPartial)
	}
}

// TestGovernanceVerifyChain_HMACSignedVerifiesWithKey proves the inject path:
// when the server's chainer holds the HMAC key, an intact HMAC chain verifies
// and stays ChainStatusOK (the key is sourced server-side, never a query param).
func TestGovernanceVerifyChain_HMACSignedVerifiesWithKey(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedGovHealthChain(t, s, "default", 3, govHealthHMACKey)
	s.auditChainer = audit.NewChainer(s.redisClient(), "", audit.WithHMACKey(govHealthHMACKey))

	status, err := newGovernanceHealthDeps(s, "default").VerifyChain(context.Background())
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if status != governance.ChainStatusOK {
		t.Fatalf("status = %q, want %q (intact HMAC chain with key must verify ok)", status, governance.ChainStatusOK)
	}
}

// TestGovernanceVerifyChain_WrongKeyIsCompromised proves HMAC tags are actually
// checked once a key is injected: a chain whose tags do not match the server's
// key (a forgery from the server's perspective) reads as ChainStatusCompromised,
// never ChainStatusOK. RED on the old code, which never injected the key.
func TestGovernanceVerifyChain_WrongKeyIsCompromised(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedGovHealthChain(t, s, "default", 3, govHealthHMACKey)
	wrongKey := []byte("fedcba9876543210fedcba9876543210")
	s.auditChainer = audit.NewChainer(s.redisClient(), "", audit.WithHMACKey(wrongKey))

	status, err := newGovernanceHealthDeps(s, "default").VerifyChain(context.Background())
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if status != governance.ChainStatusCompromised {
		t.Fatalf("status = %q, want %q (HMAC mismatch must read compromised)", status, governance.ChainStatusCompromised)
	}
}

// TestGovernanceVerifyChain_NonHMACChainStaysOK guards against over-downgrade:
// a non-HMAC (dev) chain carries no HMAC tags, so HMACSeen is false and the
// fail-closed downgrade must NOT fire — ChainStatusOK is preserved.
func TestGovernanceVerifyChain_NonHMACChainStaysOK(t *testing.T) {
	s, _, _ := newTestGateway(t)
	seedGovHealthChain(t, s, "default", 3, nil) // no HMAC key -> plain chain
	s.auditChainer = nil

	status, err := newGovernanceHealthDeps(s, "default").VerifyChain(context.Background())
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if status != governance.ChainStatusOK {
		t.Fatalf("status = %q, want %q (non-HMAC chain must not be downgraded)", status, governance.ChainStatusOK)
	}
}
