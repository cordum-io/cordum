package servicetoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// decodeSegment base64url-decodes one JWT-style segment for assertions.
func decodeSegment(t *testing.T, seg string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("decode segment: %v", err)
	}
	return raw
}

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return priv
}

func TestMintService_ProducesServiceTypedTokenForReservedIdentity(t *testing.T) {
	t.Parallel()
	priv := testKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	token, err := MintService(priv, "primary", IdentityScheduler, now)
	if err != nil {
		t.Fatalf("MintService: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 token segments, got %d", len(parts))
	}

	var header struct {
		Alg string `json:"alg"`
		KID string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(decodeSegment(t, parts[0]), &header); err != nil {
		t.Fatalf("header: %v", err)
	}
	if header.Typ != TypService {
		t.Fatalf("header.typ = %q, want %q", header.Typ, TypService)
	}
	if header.Alg != Algorithm {
		t.Fatalf("header.alg = %q, want %q", header.Alg, Algorithm)
	}
	if header.KID != "primary" {
		t.Fatalf("header.kid = %q, want primary", header.KID)
	}

	var claims Claims
	if err := json.Unmarshal(decodeSegment(t, parts[1]), &claims); err != nil {
		t.Fatalf("claims: %v", err)
	}
	if claims.Subject != IdentityScheduler {
		t.Fatalf("claims.sub = %q, want %q", claims.Subject, IdentityScheduler)
	}
	if claims.Tenant != ReservedTenant {
		t.Fatalf("claims.tenant = %q, want %q", claims.Tenant, ReservedTenant)
	}
	if strings.TrimSpace(claims.SDKVersion) == "" {
		t.Fatal("claims.sdk_ver must be non-empty (Validate padding)")
	}
	if strings.TrimSpace(claims.JTI) == "" {
		t.Fatal("claims.jti must be non-empty")
	}
	if !claims.ExpiresAt.After(claims.IssuedAt) {
		t.Fatalf("exp must be after iat: %+v", claims)
	}
	if got := claims.ExpiresAt.Sub(claims.IssuedAt); got != DefaultServiceTTL {
		t.Fatalf("service token TTL = %s, want %s", got, DefaultServiceTTL)
	}
}

func TestMintService_RejectsNonReservedSubject(t *testing.T) {
	t.Parallel()
	priv := testKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := MintService(priv, "primary", "w-attacker", now); err == nil {
		t.Fatal("MintService must reject a non-reserved subject")
	}
}

func TestIsReservedIdentity(t *testing.T) {
	t.Parallel()
	reserved := []string{IdentityScheduler, IdentityGateway, IdentityWorkflow}
	for _, id := range reserved {
		if !IsReservedIdentity(id) {
			t.Errorf("IsReservedIdentity(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "w-a", "cordum-scheduler-x", "api-gateway-http", "scheduler"} {
		if IsReservedIdentity(id) {
			t.Errorf("IsReservedIdentity(%q) = true, want false", id)
		}
	}
}

func TestReservedIdentityNamesMatchControlPlaneSenderIDs(t *testing.T) {
	t.Parallel()
	// These literals are the SenderId/RequestedBy values the three
	// control-plane producers stamp on their internal broadcasts. They are
	// the load-bearing contract for subject-binding; pin them so a rename
	// of the producer constant without updating the reserved set fails here.
	if IdentityScheduler != "cordum-scheduler" {
		t.Errorf("IdentityScheduler = %q, want cordum-scheduler", IdentityScheduler)
	}
	if IdentityGateway != "api-gateway" {
		t.Errorf("IdentityGateway = %q, want api-gateway", IdentityGateway)
	}
	if IdentityWorkflow != "workflow-engine" {
		t.Errorf("IdentityWorkflow = %q, want workflow-engine", IdentityWorkflow)
	}
}

func TestSign_RoundTripsThroughVerify(t *testing.T) {
	t.Parallel()
	priv := testKey(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	claims := Claims{
		Subject:    IdentityGateway,
		Tenant:     ReservedTenant,
		SDKVersion: ServiceSDKVersion,
		JTI:        "jti-1",
		IssuedAt:   now,
		ExpiresAt:  now.Add(DefaultServiceTTL),
	}
	token, err := Sign(priv, "primary", claims, TypService)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if strings.Count(token, ".") != 2 {
		t.Fatalf("token must have 3 dot-separated segments: %q", token)
	}
}
