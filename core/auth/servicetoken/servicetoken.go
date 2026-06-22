// Package servicetoken mints the stateless control-plane "service" tokens
// (typ=cordum-service) that authenticate internal JobResult/JobCancel
// broadcasts between Cordum control-plane services (scheduler, api-gateway,
// workflow-engine) under CORDUM_SDK_HANDSHAKE=enforce.
//
// A service token is a short-TTL, Ed25519-signed JWT-style token with the SAME
// wire format as a per-worker session token — so the scheduler's existing
// verifier validates it — but with a distinct "typ" header and a synthetic
// "_system" tenant. Unlike a worker token it carries NO Redis active-session
// record: a peer verifies it from the signature plus a reserved-identity
// allowlist only (checkActive=false). The scheme is non-spoofable because
// (1) only the control plane holds the Ed25519 signing key, (2) the verifier
// asserts the typ header on BOTH the worker and service paths, and (3) the
// subject must be a reserved control-plane identity (also reserved at handshake
// time, so a worker can never obtain a real token bearing such a subject).
//
// The package is deliberately a leaf: it imports only core/policysign and the
// standard library, so all three producer services can import it without an
// import cycle.
package servicetoken

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/policysign"
)

const (
	// Algorithm is the signature algorithm, shared with worker session tokens.
	Algorithm = policysign.AlgorithmEd25519
	// TypSession marks a per-worker session token (Redis-backed, revocable).
	TypSession = "cordum-session"
	// TypService marks a stateless control-plane service token.
	TypService = "cordum-service"
	// ReservedTenant is the synthetic tenant stamped on every service token.
	// No real tenant uses it; the leading underscore marks it system-reserved.
	ReservedTenant = "_system"
	// ServiceSDKVersion is synthetic, non-empty padding so Claims.Validate
	// (which requires sdk_ver) passes for a service token.
	ServiceSDKVersion = "cordum-service"
	// DefaultServiceTTL bounds the blast radius of a service token. Service
	// tokens carry no active record and so cannot be revoked early; a short
	// TTL plus key rotation is the (consciously chosen) revocation story.
	DefaultServiceTTL = 5 * time.Minute
)

// Reserved control-plane identities. These MUST equal the SenderId /
// RequestedBy values the three producers stamp on their internal broadcasts:
//   - scheduler: engine.defaultSenderID
//   - api-gateway: handlers_jobs.go literal "api-gateway"
//   - workflow-engine: engine_state.go literal "workflow-engine"
//
// They are the single source of truth for both minting (the token subject) and
// verifying (the allowlist VerifyService checks). The scheduler package carries
// a test that asserts IdentityScheduler == scheduler.defaultSenderID so a
// rename of one without the other is caught in CI.
const (
	IdentityScheduler = "cordum-scheduler"
	IdentityGateway   = "api-gateway"
	IdentityWorkflow  = "workflow-engine"
)

var reservedIdentities = map[string]struct{}{
	IdentityScheduler: {},
	IdentityGateway:   {},
	IdentityWorkflow:  {},
}

// IsReservedIdentity reports whether subject is a reserved control-plane
// service identity eligible to bear a service token.
func IsReservedIdentity(subject string) bool {
	_, ok := reservedIdentities[strings.TrimSpace(subject)]
	return ok
}

// Claims are the JWT-style claims of a token. The JSON tags MUST match
// scheduler.SessionTokenClaims so the scheduler's verifier decodes a service
// token's claims correctly; this is pinned by a cross-package round-trip test
// (MintService -> scheduler.VerifyService).
type Claims struct {
	Subject    string    `json:"sub"`
	Tenant     string    `json:"tenant"`
	SDKVersion string    `json:"sdk_ver"`
	JTI        string    `json:"jti"`
	IssuedAt   time.Time `json:"iat"`
	ExpiresAt  time.Time `json:"exp"`
}

// Validate ensures every required claim is present and exp is strictly after
// iat. It mirrors scheduler.SessionTokenClaims.Validate so a service token and
// a worker token are held to the same structural bar.
func (c Claims) Validate() error {
	var missing []string
	if strings.TrimSpace(c.Subject) == "" {
		missing = append(missing, "sub")
	}
	if strings.TrimSpace(c.Tenant) == "" {
		missing = append(missing, "tenant")
	}
	if strings.TrimSpace(c.SDKVersion) == "" {
		missing = append(missing, "sdk_ver")
	}
	if strings.TrimSpace(c.JTI) == "" {
		missing = append(missing, "jti")
	}
	if c.IssuedAt.IsZero() {
		missing = append(missing, "iat")
	}
	if c.ExpiresAt.IsZero() {
		missing = append(missing, "exp")
	}
	if len(missing) > 0 {
		return fmt.Errorf("servicetoken: missing claims: %s", strings.Join(missing, ", "))
	}
	if !c.ExpiresAt.After(c.IssuedAt) {
		return fmt.Errorf("servicetoken: exp must be after iat")
	}
	return nil
}

// Sign builds a signed base64url JWT-style token (header.claims.signature) for
// the given typ using the provided Ed25519 key. It is pure (no Redis, no global
// state) and mirrors the scheduler's worker-token signer so both token kinds
// share one wire format and one verifier.
func Sign(priv ed25519.PrivateKey, kid string, claims Claims, typ string) (string, error) {
	header := map[string]string{"alg": Algorithm, "kid": kid, "typ": typ}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("servicetoken: marshal header: %w", err)
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("servicetoken: marshal claims: %w", err)
	}
	headerSeg := base64.RawURLEncoding.EncodeToString(headerBytes)
	claimsSeg := base64.RawURLEncoding.EncodeToString(claimsBytes)
	signingInput := headerSeg + "." + claimsSeg
	sig, err := policysign.Sign(priv, kid, []byte(signingInput))
	if err != nil {
		return "", fmt.Errorf("servicetoken: sign: %w", err)
	}
	rawSig, err := base64.StdEncoding.DecodeString(sig.Value)
	if err != nil {
		return "", fmt.Errorf("servicetoken: decode signature: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(rawSig), nil
}

// MintService builds a service token for a reserved control-plane identity.
// now is injected for testability; the token expires at now+DefaultServiceTTL.
// It returns an error for any non-reserved subject so a caller cannot mint a
// service token impersonating an arbitrary worker.
func MintService(priv ed25519.PrivateKey, kid, subject string, now time.Time) (string, error) {
	subject = strings.TrimSpace(subject)
	if !IsReservedIdentity(subject) {
		return "", fmt.Errorf("servicetoken: subject %q is not a reserved control-plane identity", subject)
	}
	jti, err := newJTI()
	if err != nil {
		return "", fmt.Errorf("servicetoken: jti: %w", err)
	}
	now = now.UTC()
	claims := Claims{
		Subject:    subject,
		Tenant:     ReservedTenant,
		SDKVersion: ServiceSDKVersion,
		JTI:        jti,
		IssuedAt:   now,
		ExpiresAt:  now.Add(DefaultServiceTTL),
	}
	if err := claims.Validate(); err != nil {
		return "", err
	}
	return Sign(priv, kid, claims, TypService)
}

// PeekTyp decodes (WITHOUT verifying) the "typ" header field of a token, for
// routing only. The chosen verifier re-asserts typ AFTER the signature is
// checked, so a flipped typ header fails verification and cannot mis-route.
// Returns "" when the header segment cannot be decoded.
func PeekTyp(token string) string {
	token = strings.TrimSpace(token)
	idx := strings.IndexByte(token, '.')
	if idx <= 0 {
		return ""
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(token[:idx])
	if err != nil {
		return ""
	}
	var header struct {
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return ""
	}
	return header.Typ
}

func newJTI() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
