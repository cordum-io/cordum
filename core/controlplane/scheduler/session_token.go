package scheduler

// Phase-2 boundary-hardening worker session tokens.
//
// On a successful handshake (cap/sdk/go.HandshakeRequest), the scheduler
// mints an Ed25519-signed session token that the worker attaches to every
// subsequent BusPacket. Token verification is the authoritative trust
// signal the scheduler uses for dispatch and policy decisions; heartbeat
// timestamps remain telemetry only (see epic rail "heartbeat is telemetry,
// not authority").
//
// Wire format is a JWS-like compact serialisation:
//
//   base64url(header) "." base64url(claims) "." base64url(signature)
//
// where header is `{"alg":"ed25519","kid":"<key-id>"}` and claims is a
// JSON object with fixed fields. Signature is the Ed25519 signature over
// `header64 + "." + claims64` using the issuer's private key. Signing
// reuses the policysign primitives directly so the trust-domain key
// material is the same as the policy bundle signer (existing operator
// muscle memory; one rotation procedure).
//
// Redis layout:
//
//   session:worker:<agent_id>       JSON {"jti","exp_unix"} TTL = exp - now
//   session:revoked:<tenant>:<jti>  "1" with TTL = exp - now
//
// The per-agent record lets the handler invalidate the prior token on
// renew (single active token per worker). The per-tenant revocation key
// is consulted on every Verify call so admin revoke takes effect on the
// next inbound packet.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/policysign"
	"github.com/redis/go-redis/v9"
)

// Defaults chosen to match the plan: 1h lifetime, 60 s skew tolerance.
const (
	DefaultSessionTokenLifetime = time.Hour
	DefaultSessionTokenSkew     = 60 * time.Second
	sessionTokenAlgorithm       = policysign.AlgorithmEd25519
	sessionWorkerKeyPrefix      = "session:worker:"
	sessionRevokedKeyPrefix     = "session:revoked:"
)

// Errors returned by the session-token issuer/verifier. Callers compare
// with errors.Is to map them to specific HandshakeReject* sentinels or
// JSON-RPC error codes.
var (
	ErrSessionTokenMalformed     = errors.New("scheduler: session token malformed")
	ErrSessionTokenExpired       = errors.New("scheduler: session token expired")
	ErrSessionTokenNotYetValid   = errors.New("scheduler: session token not yet valid")
	ErrSessionTokenSignature     = errors.New("scheduler: session token signature invalid")
	ErrSessionTokenUnknownKey    = errors.New("scheduler: session token signed with unknown key")
	ErrSessionTokenRevoked       = errors.New("scheduler: session token revoked")
	ErrSessionTokenSuperseded    = errors.New("scheduler: session token superseded by newer issue")
	ErrSessionTokenStoreUnready  = errors.New("scheduler: session token store not configured")
	ErrSessionTokenMissingClaims = errors.New("scheduler: session token missing required claims")
)

// SessionTokenClaims are the JWT-style claims embedded in a session
// token. Field names match RFC 7519 conventions where applicable so a
// future hand-off to a standard JWT library is trivial.
type SessionTokenClaims struct {
	Subject    string    `json:"sub"`
	Tenant     string    `json:"tenant"`
	SDKVersion string    `json:"sdk_ver"`
	JTI        string    `json:"jti"`
	IssuedAt   time.Time `json:"iat"`
	ExpiresAt  time.Time `json:"exp"`
}

// Validate checks the structural requirements of a claims set. Time
// bounds are checked separately (see verifyClaimsTime) so callers that
// only need the structural check (e.g. the renew handler before it
// re-mints) can do that without a clock tolerance dance.
func (c SessionTokenClaims) Validate() error {
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
		return fmt.Errorf("%w: %s", ErrSessionTokenMissingClaims, strings.Join(missing, ", "))
	}
	if !c.ExpiresAt.After(c.IssuedAt) {
		return fmt.Errorf("%w: exp <= iat", ErrSessionTokenMissingClaims)
	}
	return nil
}

// SessionTokenIssuer mints, verifies, renews, and revokes worker session
// tokens. A nil Redis client disables all persistence — Verify still
// works (signature + exp), but supersede/revoke checks are skipped and
// emit ErrSessionTokenStoreUnready when callers explicitly invoke them.
type SessionTokenIssuer struct {
	privateKey ed25519.PrivateKey
	keyID      string
	trust      *policysign.TrustStore
	redis      redis.UniversalClient
	lifetime   time.Duration
	skew       time.Duration
	now        func() time.Time
}

// SessionTokenIssuerOptions tunes a SessionTokenIssuer. Zero-valued fields
// fall back to documented defaults.
type SessionTokenIssuerOptions struct {
	Lifetime time.Duration
	Skew     time.Duration
	// Now lets tests inject a deterministic clock. Production callers
	// leave it nil and get time.Now.
	Now func() time.Time
}

// NewSessionTokenIssuer constructs an issuer.
//
// privateKey + keyID identify the signing key; trust holds the verifier
// view (must include keyID). rdb is the Redis client used for the
// per-agent active-token record and the revocation set; pass nil for
// in-memory-only deployments (tests).
func NewSessionTokenIssuer(privateKey ed25519.PrivateKey, keyID string, trust *policysign.TrustStore, rdb redis.UniversalClient, opts SessionTokenIssuerOptions) (*SessionTokenIssuer, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, policysign.ErrInvalidPrivateKey
	}
	id := strings.TrimSpace(keyID)
	if id == "" {
		return nil, policysign.ErrEmptyKeyID
	}
	if trust == nil {
		return nil, errors.New("scheduler: session token issuer requires a trust store")
	}
	if _, ok := trust.Lookup(id); !ok {
		return nil, fmt.Errorf("scheduler: signing key id %q not present in trust store", id)
	}
	lifetime := opts.Lifetime
	if lifetime <= 0 {
		lifetime = DefaultSessionTokenLifetime
	}
	skew := opts.Skew
	if skew < 0 {
		skew = DefaultSessionTokenSkew
	}
	if skew == 0 {
		skew = DefaultSessionTokenSkew
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &SessionTokenIssuer{
		privateKey: privateKey,
		keyID:      id,
		trust:      trust,
		redis:      rdb,
		lifetime:   lifetime,
		skew:       skew,
		now:        now,
	}, nil
}

// Lifetime returns the configured token lifetime. Workers schedule
// renew at iat + lifetime/2.
func (i *SessionTokenIssuer) Lifetime() time.Duration {
	if i == nil {
		return DefaultSessionTokenLifetime
	}
	return i.lifetime
}

// Issue mints a fresh session token for (agentID, tenant, sdkVer). If a
// previous token exists for the agent, its JTI is recorded in the
// revocation set so any in-flight packet still carrying it is refused
// (single-active-token invariant). The new token's per-agent record is
// then stored with TTL matching the new exp.
func (i *SessionTokenIssuer) Issue(ctx context.Context, agentID, tenant, sdkVer string) (string, SessionTokenClaims, error) {
	if i == nil {
		return "", SessionTokenClaims{}, ErrSessionTokenStoreUnready
	}
	if strings.TrimSpace(agentID) == "" || strings.TrimSpace(tenant) == "" || strings.TrimSpace(sdkVer) == "" {
		return "", SessionTokenClaims{}, fmt.Errorf("%w: issue requires agent_id, tenant, sdk_ver", ErrSessionTokenMissingClaims)
	}
	now := i.now().UTC()
	jti, err := newJTI()
	if err != nil {
		return "", SessionTokenClaims{}, fmt.Errorf("scheduler: jti generation: %w", err)
	}
	claims := SessionTokenClaims{
		Subject:    agentID,
		Tenant:     tenant,
		SDKVersion: sdkVer,
		JTI:        jti,
		IssuedAt:   now,
		ExpiresAt:  now.Add(i.lifetime),
	}
	if err := claims.Validate(); err != nil {
		return "", SessionTokenClaims{}, err
	}
	token, err := i.signClaims(claims)
	if err != nil {
		return "", SessionTokenClaims{}, err
	}
	if err := i.persistIssue(ctx, claims); err != nil {
		return "", SessionTokenClaims{}, err
	}
	return token, claims, nil
}

// Verify parses, signature-checks, and authoritatively validates token.
// The optional checkActive flag, when true, additionally consults the
// per-agent record + revocation set in Redis. checkActive=false is for
// callers that have already done that work (e.g. inbound packet middle-
// ware that batches the lookup) or for offline replay.
func (i *SessionTokenIssuer) Verify(ctx context.Context, token string, checkActive bool) (SessionTokenClaims, error) {
	if i == nil {
		return SessionTokenClaims{}, ErrSessionTokenStoreUnready
	}
	claims, err := i.parseAndVerifySignature(token)
	if err != nil {
		return SessionTokenClaims{}, err
	}
	if err := i.verifyClaimsTime(claims); err != nil {
		return SessionTokenClaims{}, err
	}
	if checkActive {
		if err := i.assertActive(ctx, claims); err != nil {
			return SessionTokenClaims{}, err
		}
	}
	return claims, nil
}

// Renew validates the prior token (signature + structural claims; expiry
// is allowed within skew so a worker that was briefly offline can still
// renew), revokes its JTI, and issues a fresh token for the same
// (agent, tenant, sdkVer). A token whose signature does not verify or
// whose exp is older than skew is refused.
func (i *SessionTokenIssuer) Renew(ctx context.Context, token string) (string, SessionTokenClaims, error) {
	if i == nil {
		return "", SessionTokenClaims{}, ErrSessionTokenStoreUnready
	}
	claims, err := i.parseAndVerifySignature(token)
	if err != nil {
		return "", SessionTokenClaims{}, err
	}
	now := i.now().UTC()
	if claims.ExpiresAt.Add(i.skew).Before(now) {
		return "", SessionTokenClaims{}, ErrSessionTokenExpired
	}
	if claims.IssuedAt.After(now.Add(i.skew)) {
		return "", SessionTokenClaims{}, ErrSessionTokenNotYetValid
	}
	if i.redis != nil {
		if revoked, err := i.isRevoked(ctx, claims.Tenant, claims.JTI); err != nil {
			return "", SessionTokenClaims{}, err
		} else if revoked {
			return "", SessionTokenClaims{}, ErrSessionTokenRevoked
		}
	}
	return i.Issue(ctx, claims.Subject, claims.Tenant, claims.SDKVersion)
}

// Revoke marks (tenant, jti) revoked until exp. Subsequent Verify calls
// with checkActive=true will refuse a token bearing this JTI. Idempotent
// — a revoke on an already-revoked JTI is a no-op success.
func (i *SessionTokenIssuer) Revoke(ctx context.Context, tenant, jti string, exp time.Time) error {
	if i == nil || i.redis == nil {
		return ErrSessionTokenStoreUnready
	}
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(jti) == "" {
		return fmt.Errorf("%w: revoke requires tenant + jti", ErrSessionTokenMissingClaims)
	}
	ttl := exp.Sub(i.now())
	if ttl <= 0 {
		// Already past exp — natural expiry handles it.
		return nil
	}
	key := revokedKey(tenant, jti)
	return i.redis.Set(ctx, key, "1", ttl).Err()
}

// RevokeByAgent revokes the currently active token for agent (looked up
// from the per-agent record). Returns nil if no active token is on file.
func (i *SessionTokenIssuer) RevokeByAgent(ctx context.Context, tenant, agentID string) error {
	if i == nil || i.redis == nil {
		return ErrSessionTokenStoreUnready
	}
	rec, err := i.loadActive(ctx, agentID)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	return i.Revoke(ctx, tenant, rec.JTI, time.Unix(rec.ExpUnix, 0))
}

// signClaims serialises claims and produces a compact JWS-like token.
func (i *SessionTokenIssuer) signClaims(claims SessionTokenClaims) (string, error) {
	header := map[string]string{"alg": sessionTokenAlgorithm, "kid": i.keyID, "typ": "cordum-session"}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("scheduler: marshal session header: %w", err)
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("scheduler: marshal session claims: %w", err)
	}
	headerSeg := base64.RawURLEncoding.EncodeToString(headerBytes)
	claimsSeg := base64.RawURLEncoding.EncodeToString(claimsBytes)
	signingInput := headerSeg + "." + claimsSeg
	sig, err := policysign.Sign(i.privateKey, i.keyID, []byte(signingInput))
	if err != nil {
		return "", fmt.Errorf("scheduler: sign session token: %w", err)
	}
	rawSig, err := base64.StdEncoding.DecodeString(sig.Value)
	if err != nil {
		return "", fmt.Errorf("scheduler: decode signature segment: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(rawSig), nil
}

// parseAndVerifySignature splits, base64-decodes, JSON-decodes, and
// verifies the Ed25519 signature on token. It does not consult Redis or
// check time bounds — those are separate steps so callers can compose
// (e.g. Renew tolerates a slightly expired token).
func (i *SessionTokenIssuer) parseAndVerifySignature(token string) (SessionTokenClaims, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return SessionTokenClaims{}, ErrSessionTokenMalformed
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return SessionTokenClaims{}, fmt.Errorf("%w: expected 3 segments, got %d", ErrSessionTokenMalformed, len(parts))
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return SessionTokenClaims{}, fmt.Errorf("%w: header: %v", ErrSessionTokenMalformed, err)
	}
	var header struct {
		Alg string `json:"alg"`
		KID string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return SessionTokenClaims{}, fmt.Errorf("%w: header parse: %v", ErrSessionTokenMalformed, err)
	}
	if !strings.EqualFold(header.Alg, sessionTokenAlgorithm) {
		return SessionTokenClaims{}, fmt.Errorf("%w: alg=%q", ErrSessionTokenSignature, header.Alg)
	}
	if strings.TrimSpace(header.KID) == "" {
		return SessionTokenClaims{}, fmt.Errorf("%w: kid missing", ErrSessionTokenSignature)
	}
	pub, ok := i.trust.Lookup(header.KID)
	if !ok {
		return SessionTokenClaims{}, fmt.Errorf("%w: %s", ErrSessionTokenUnknownKey, header.KID)
	}
	rawSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return SessionTokenClaims{}, fmt.Errorf("%w: signature: %v", ErrSessionTokenMalformed, err)
	}
	if len(rawSig) != ed25519.SignatureSize {
		return SessionTokenClaims{}, fmt.Errorf("%w: signature length %d", ErrSessionTokenMalformed, len(rawSig))
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	sig := policysign.Signature{
		Algorithm:   sessionTokenAlgorithm,
		KeyID:       header.KID,
		Value:       base64.StdEncoding.EncodeToString(rawSig),
		Hash:        policysign.HashPayload(signingInput),
		SignedBytes: len(signingInput),
	}
	if err := policysign.Verify(pub, signingInput, sig); err != nil {
		if errors.Is(err, policysign.ErrVerifyFailed) {
			return SessionTokenClaims{}, ErrSessionTokenSignature
		}
		return SessionTokenClaims{}, fmt.Errorf("%w: %v", ErrSessionTokenSignature, err)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return SessionTokenClaims{}, fmt.Errorf("%w: claims: %v", ErrSessionTokenMalformed, err)
	}
	var claims SessionTokenClaims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return SessionTokenClaims{}, fmt.Errorf("%w: claims parse: %v", ErrSessionTokenMalformed, err)
	}
	if err := claims.Validate(); err != nil {
		return SessionTokenClaims{}, err
	}
	return claims, nil
}

func (i *SessionTokenIssuer) verifyClaimsTime(claims SessionTokenClaims) error {
	now := i.now().UTC()
	if claims.IssuedAt.After(now.Add(i.skew)) {
		return ErrSessionTokenNotYetValid
	}
	if claims.ExpiresAt.Add(i.skew).Before(now) {
		return ErrSessionTokenExpired
	}
	return nil
}

func (i *SessionTokenIssuer) assertActive(ctx context.Context, claims SessionTokenClaims) error {
	if i.redis == nil {
		return ErrSessionTokenStoreUnready
	}
	if revoked, err := i.isRevoked(ctx, claims.Tenant, claims.JTI); err != nil {
		return err
	} else if revoked {
		return ErrSessionTokenRevoked
	}
	rec, err := i.loadActive(ctx, claims.Subject)
	if err != nil {
		return err
	}
	if rec == nil {
		// No active record on file — treat as superseded since Issue
		// always writes one. This catches the case where the worker's
		// record was wiped (manual flush) yet the worker keeps replaying
		// an old token.
		return ErrSessionTokenSuperseded
	}
	if rec.JTI != claims.JTI {
		return ErrSessionTokenSuperseded
	}
	return nil
}

func (i *SessionTokenIssuer) persistIssue(ctx context.Context, claims SessionTokenClaims) error {
	if i.redis == nil {
		return nil
	}
	// Read prior record (if any) and revoke it so the previously issued
	// token cannot still authorise inbound packets.
	prior, err := i.loadActive(ctx, claims.Subject)
	if err != nil {
		return err
	}
	if prior != nil && prior.JTI != claims.JTI {
		// Best-effort revoke of the prior JTI; ignore failures from
		// already-expired entries (Set with negative TTL returns from
		// our Revoke as nil).
		_ = i.Revoke(ctx, claims.Tenant, prior.JTI, time.Unix(prior.ExpUnix, 0))
	}
	rec := activeRecord{JTI: claims.JTI, ExpUnix: claims.ExpiresAt.Unix(), Tenant: claims.Tenant}
	payload, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("scheduler: marshal active record: %w", err)
	}
	ttl := claims.ExpiresAt.Sub(i.now())
	if ttl <= 0 {
		return ErrSessionTokenExpired
	}
	return i.redis.Set(ctx, workerKey(claims.Subject), payload, ttl).Err()
}

func (i *SessionTokenIssuer) loadActive(ctx context.Context, agentID string) (*activeRecord, error) {
	raw, err := i.redis.Get(ctx, workerKey(agentID)).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("scheduler: load active session: %w", err)
	}
	var rec activeRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("scheduler: parse active session: %w", err)
	}
	return &rec, nil
}

func (i *SessionTokenIssuer) isRevoked(ctx context.Context, tenant, jti string) (bool, error) {
	res, err := i.redis.Exists(ctx, revokedKey(tenant, jti)).Result()
	if err != nil {
		return false, fmt.Errorf("scheduler: check revocation: %w", err)
	}
	return res > 0, nil
}

type activeRecord struct {
	JTI     string `json:"jti"`
	ExpUnix int64  `json:"exp_unix"`
	Tenant  string `json:"tenant"`
}

// parseActiveRecord decodes the JSON active-token record persisted by
// SessionTokenIssuer.persistIssue. Exposed package-level so siblings
// (TrustResolver in trust_state.go) can consume the same wire format
// without duplicating the schema.
func parseActiveRecord(raw []byte) (*activeRecord, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rec activeRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func workerKey(agentID string) string { return sessionWorkerKeyPrefix + agentID }
func revokedKey(tenant, jti string) string {
	return sessionRevokedKeyPrefix + tenant + ":" + jti
}

// newJTI returns a 128-bit random hex-encoded identifier suitable for
// uniquely tagging a session token. Uses crypto/rand exclusively
// (math/rand would weaken the supersede invariant).
func newJTI() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
