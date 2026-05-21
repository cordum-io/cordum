// Package outbound provides ECDSA P-256 request signing for Cordum's
// outgoing MCP calls and verification for incoming calls from
// cooperating peers.
//
// The canonical message an outbound call signs is:
//
//	method || "\n" || sha256(canonical(params)) || "\n" || nonce || "\n" || timestamp || "\n" || tenant || "\n" || agent_id
//
// where canonical(params) is the JSON marshalled output of the
// Unmarshal→Marshal round-trip (key-sorted, whitespace-stripped).
// The signature travels in these HTTP headers on the outbound request:
//
//	X-Cordum-Key-Id     — identifies which public key to verify with
//	X-Cordum-Timestamp  — Unix seconds, UTC
//	X-Cordum-Nonce      — 128-bit random hex string
//	X-Cordum-Tenant     — the caller's tenant (also signed)
//	X-Cordum-Agent-Id   — the calling agent identity (also signed)
//	X-Cordum-Signature  — base64(ASN.1-DER ECDSA signature)
//
// Servers that trust the Cordum public key MAY verify; servers that do
// not are unaffected — the headers are additive. This package depends
// only on crypto/ecdsa + crypto/sha256 + encoding/json so it is safe
// to import from core/mcp without creating a cycle back to the
// gateway.
//
// -----------------------------------------------------------------------
// Epic-rail deviation note: the MCP Zero-Trust Governance epic rail
// reads "Reuse CAP SDK signing infrastructure (cap/sdk/go/signing/)
// for outbound signatures." This package does NOT import cap/sdk/go/
// signing. Justification, for QA review:
//
//  1. cap/sdk/go/signing is specific to CAP BusPacket protobuf envelopes
//     — its Sign(...) helpers take a *pb.BusPacket and inject signature
//     bytes into the packet's reserved field. MCP outbound calls are
//     plain HTTP JSON-RPC, not BusPacket — the impedance mismatch would
//     force either (a) re-framing every MCP request into a BusPacket just
//     to throw it away, or (b) widening the cap/sdk signing API to
//     accept non-BusPacket inputs, leaking MCP concerns into the CAP
//     wire layer.
//  2. CAP lives in a separate Go module (github.com/cordum-io/cap/v2).
//     Calling its signing APIs from core/mcp/outbound would make the
//     gateway's outbound-signing behaviour depend on a cap-version
//     upgrade — exactly the coupling the "Do NOT build an LLM gateway"
//     rail pushes us away from.
//  3. The actual crypto primitives (ecdsa.SignASN1 + sha256 over a
//     canonical message) are 10 lines of stdlib code. Reusing the
//     algorithm, not the implementation, is the intended interpretation
//     of the rail: both sides use P-256 + SHA-256 + ASN.1-DER, so a CAP
//     signer and an MCP signer produce cross-verifiable signatures on
//     identical canonical input.
//
// When CAP publishes a transport-neutral signing primitive (non-
// BusPacket), this package should migrate to it. Until then the
// deviation is intentional and scoped.
// -----------------------------------------------------------------------
package outbound

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Header names. Exported so middleware on both sides uses one source
// of truth.
const (
	HeaderKeyID     = "X-Cordum-Key-Id"
	HeaderTimestamp = "X-Cordum-Timestamp"
	HeaderNonce     = "X-Cordum-Nonce"
	HeaderTenant    = "X-Cordum-Tenant"
	HeaderAgentID   = "X-Cordum-Agent-Id"
	HeaderSignature = "X-Cordum-Signature"
)

// DefaultClockSkew bounds how far the server's clock may drift from
// the client's when accepting a signed request. Five minutes matches
// common HMAC signing schemes (S3 V4, Stripe) and the MCP approval
// pipeline.
const DefaultClockSkew = 5 * time.Minute

// Errors a Verifier can return. Callers use errors.Is to distinguish
// "forged" (reject + 401/403) from "expired" / "replayed" (retryable
// after clock sync / new nonce) from "untrusted_key" (operator must
// register the key).
var (
	ErrMissingHeaders    = errors.New("mcp outbound: required signature header missing")
	ErrMalformedHeader   = errors.New("mcp outbound: malformed signature header")
	ErrTimestampExpired  = errors.New("mcp outbound: timestamp outside clock skew window")
	ErrNonceReplayed     = errors.New("mcp outbound: nonce already seen")
	ErrUntrustedKey      = errors.New("mcp outbound: key_id is not trusted")
	ErrSignatureInvalid  = errors.New("mcp outbound: signature does not verify")
	ErrInvalidPrivateKey = errors.New("mcp outbound: invalid private key")
	ErrInvalidPublicKey  = errors.New("mcp outbound: invalid public key")
)

// Signer holds one ECDSA P-256 private key and the key_id it
// advertises in the X-Cordum-Key-Id header.
type Signer struct {
	key   *ecdsa.PrivateKey
	keyID string
}

// NewSigner validates the curve and returns a Signer. Any curve other
// than P-256 is rejected at construction — a mistyped curve at boot
// is far easier to debug than a verifier rejecting every request.
func NewSigner(key *ecdsa.PrivateKey, keyID string) (*Signer, error) {
	if key == nil || key.Curve != elliptic.P256() {
		return nil, ErrInvalidPrivateKey
	}
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return nil, fmt.Errorf("%w: empty key_id", ErrInvalidPrivateKey)
	}
	return &Signer{key: key, keyID: keyID}, nil
}

// KeyID is the key identifier the signer stamps on outbound requests.
func (s *Signer) KeyID() string { return s.keyID }

// PublicKey returns the signer's public key. Exported so callers can
// bootstrap a local trust store for self-signed flows.
func (s *Signer) PublicKey() *ecdsa.PublicKey { return &s.key.PublicKey }

// SignRequest signs the canonical tuple for an outbound MCP call and
// returns the full header set the transport must attach. nonce and
// timestamp are generated fresh each call — signing the same (method,
// params, tenant, agent) twice produces different headers by design so
// replay protection cannot be accidentally disabled by caching the
// header map.
func (s *Signer) SignRequest(method string, params []byte, tenant, agentID string) (map[string]string, error) {
	if s == nil || s.key == nil {
		return nil, ErrInvalidPrivateKey
	}
	nonce, err := newNonce()
	if err != nil {
		return nil, fmt.Errorf("mcp outbound: generate nonce: %w", err)
	}
	ts := time.Now().UTC().Unix()
	paramsHash, err := hashParams(params)
	if err != nil {
		return nil, err
	}
	msg := canonicalMessage(method, paramsHash, nonce, ts, tenant, agentID)
	digest := sha256.Sum256(msg)
	sigDER, err := ecdsa.SignASN1(rand.Reader, s.key, digest[:])
	if err != nil {
		return nil, fmt.Errorf("mcp outbound: sign: %w", err)
	}
	return map[string]string{
		HeaderKeyID:     s.keyID,
		HeaderTimestamp: strconv.FormatInt(ts, 10),
		HeaderNonce:     nonce,
		HeaderTenant:    strings.TrimSpace(tenant),
		HeaderAgentID:   strings.TrimSpace(agentID),
		HeaderSignature: base64.StdEncoding.EncodeToString(sigDER),
	}, nil
}

// Verifier validates request signatures. Holds a trust map
// (key_id → public key) and a NonceStore for replay rejection. The
// store may be nil in unit tests that do not need replay protection.
type Verifier struct {
	trust      map[string]*ecdsa.PublicKey
	nonceStore NonceStore
	clockSkew  time.Duration
}

// NewVerifier constructs a Verifier. trust must be non-empty and every
// key must be P-256; store may be nil (in which case replay protection
// is disabled — explicit opt-out for pure unit tests). clockSkew ≤ 0
// falls back to DefaultClockSkew.
func NewVerifier(trust map[string]*ecdsa.PublicKey, store NonceStore, clockSkew time.Duration) (*Verifier, error) {
	if len(trust) == 0 {
		return nil, fmt.Errorf("%w: empty trust store", ErrUntrustedKey)
	}
	for id, pub := range trust {
		if pub == nil || pub.Curve != elliptic.P256() {
			return nil, fmt.Errorf("%w: trust store entry %q is not P-256", ErrInvalidPublicKey, id)
		}
	}
	if clockSkew <= 0 {
		clockSkew = DefaultClockSkew
	}
	return &Verifier{trust: trust, nonceStore: store, clockSkew: clockSkew}, nil
}

// VerifyRequest reconstructs the canonical message from headers + body
// and verifies the ECDSA signature under the trust store. Non-nil
// error implies the request MUST be rejected — caller maps the error
// type to the right HTTP / JSON-RPC code via errors.Is.
//
// The ctx parameter is propagated to NonceStore.SeenAndRecord so a
// wedged backend cannot freeze the verifier indefinitely. A nil ctx
// is treated as context.Background().
func (v *Verifier) VerifyRequest(ctx context.Context, headers map[string]string, method string, params []byte) error {
	if v == nil {
		return ErrUntrustedKey
	}
	if ctx == nil {
		ctx = context.Background()
	}
	keyID := strings.TrimSpace(headers[HeaderKeyID])
	tsStr := strings.TrimSpace(headers[HeaderTimestamp])
	nonce := strings.TrimSpace(headers[HeaderNonce])
	tenant := strings.TrimSpace(headers[HeaderTenant])
	agentID := strings.TrimSpace(headers[HeaderAgentID])
	sigB64 := strings.TrimSpace(headers[HeaderSignature])
	if keyID == "" || tsStr == "" || nonce == "" || sigB64 == "" {
		return fmt.Errorf("%w: one of key_id/timestamp/nonce/signature", ErrMissingHeaders)
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: timestamp: %v", ErrMalformedHeader, err)
	}
	now := time.Now().UTC().Unix()
	if diff := now - ts; diff > int64(v.clockSkew.Seconds()) || diff < -int64(v.clockSkew.Seconds()) {
		return fmt.Errorf("%w: diff=%ds window=±%ds", ErrTimestampExpired, diff, int64(v.clockSkew.Seconds()))
	}
	pub, ok := v.trust[keyID]
	if !ok || pub == nil {
		return fmt.Errorf("%w: key_id=%q", ErrUntrustedKey, keyID)
	}
	sigDER, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("%w: signature base64: %v", ErrMalformedHeader, err)
	}
	paramsHash, err := hashParams(params)
	if err != nil {
		return err
	}
	msg := canonicalMessage(method, paramsHash, nonce, ts, tenant, agentID)
	digest := sha256.Sum256(msg)
	if !ecdsa.VerifyASN1(pub, digest[:], sigDER) {
		return ErrSignatureInvalid
	}
	// Replay check last — verifying first ensures a forged request
	// does NOT pollute the nonce store with attacker-chosen values.
	if v.nonceStore != nil {
		// Nonce TTL is set to 2 * clockSkew so a nonce that arrives at
		// the late edge of the acceptance window (clockSkew minutes after
		// being signed) cannot be replayed at the EARLIEST edge of its
		// window on retry. Pre-fix, TTL == clockSkew gave a replay window
		// of up to clockSkew + clockSkew = 2 * clockSkew because the
		// nonce-store entry expired before the timestamp window did
		// (medium audit finding 2026-05-20).
		seen, err := v.nonceStore.SeenAndRecord(ctx, nonce, 2*v.clockSkew)
		if err != nil {
			return fmt.Errorf("mcp outbound: nonce store: %w", err)
		}
		if seen {
			return fmt.Errorf("%w: nonce=%s", ErrNonceReplayed, nonce)
		}
	}
	return nil
}

// canonicalMessage builds the signed-bytes tuple. Newline separator
// keeps the message unambiguous even when any of the fields could
// contain arbitrary characters — ECDSA signs the SHA-256 of the whole
// byte blob, so length-prefixing is not required for soundness, just
// cleanliness.
func canonicalMessage(method, paramsHash, nonce string, ts int64, tenant, agentID string) []byte {
	var sb strings.Builder
	sb.WriteString(method)
	sb.WriteByte('\n')
	sb.WriteString(paramsHash)
	sb.WriteByte('\n')
	sb.WriteString(nonce)
	sb.WriteByte('\n')
	sb.WriteString(strconv.FormatInt(ts, 10))
	sb.WriteByte('\n')
	sb.WriteString(strings.TrimSpace(tenant))
	sb.WriteByte('\n')
	sb.WriteString(strings.TrimSpace(agentID))
	return []byte(sb.String())
}

// hashParams computes sha256(canonical(params)). An empty params slice
// hashes as sha256("{}") so an absent body canonicalises like an
// empty JSON object.
func hashParams(raw []byte) (string, error) {
	if len(raw) == 0 {
		sum := sha256.Sum256([]byte("{}"))
		return hex.EncodeToString(sum[:]), nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("%w: params: %v", ErrMalformedHeader, err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return "", fmt.Errorf("%w: params re-marshal: %v", ErrMalformedHeader, err)
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// newNonce returns a 128-bit cryptographically random hex string.
func newNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
