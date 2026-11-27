package servicetoken

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordum/cordum/core/policysign"
)

// DefaultSkew is the clock tolerance used by the stateless verifier.
const DefaultSkew = 60 * time.Second

var (
	ErrMalformed   = errors.New("servicetoken: malformed token")
	ErrSignature   = errors.New("servicetoken: invalid signature")
	ErrUnknownKey  = errors.New("servicetoken: unknown key")
	ErrExpired     = errors.New("servicetoken: expired token")
	ErrNotYetValid = errors.New("servicetoken: token not yet valid")
	ErrWrongType   = errors.New("servicetoken: wrong token type")
	ErrNotReserved = errors.New("servicetoken: subject is not reserved")
)

// VerifySignature authenticates and decodes a token without validating claims
// or time bounds. Callers must apply their own claims policy before any effect.
// lookup receives a trimmed, lower-case key ID, as with TrustStore.Lookup.
func VerifySignature(token string, lookup func(kid string) (ed25519.PublicKey, bool)) (Claims, string, error) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return Claims{}, "", fmt.Errorf("%w: expected 3 segments, got %d", ErrMalformed, len(parts))
	}
	header, err := parseHeader(parts[0])
	if err != nil {
		return Claims{}, "", err
	}
	if lookup == nil {
		return Claims{}, "", ErrUnknownKey
	}
	pub, ok := lookup(header.KID)
	if !ok {
		return Claims{}, "", ErrUnknownKey
	}
	rawSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, "", fmt.Errorf("%w: signature: %v", ErrMalformed, err)
	}
	if len(rawSig) != ed25519.SignatureSize {
		return Claims{}, "", fmt.Errorf("%w: signature length %d", ErrMalformed, len(rawSig))
	}
	signingInput := []byte(parts[0] + "." + parts[1])
	sig := policysign.Signature{
		Algorithm: Algorithm, KeyID: header.KID,
		Value: base64.StdEncoding.EncodeToString(rawSig),
		Hash:  policysign.HashPayload(signingInput), SignedBytes: len(signingInput),
	}
	if err := policysign.Verify(pub, signingInput, sig); err != nil {
		return Claims{}, "", fmt.Errorf("%w: %v", ErrSignature, err)
	}
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, "", fmt.Errorf("%w: claims: %v", ErrMalformed, err)
	}
	var claims Claims
	if err := json.Unmarshal(claimsBytes, &claims); err != nil {
		return Claims{}, "", fmt.Errorf("%w: claims parse: %v", ErrMalformed, err)
	}
	return claims, header.Typ, nil
}

// Verify authenticates either token kind and validates shared claims and time
// bounds. The caller remains responsible for binding the key to an identity
// and enforcing audience, revocation and other application-specific policy.
func Verify(token string, lookup func(kid string) (ed25519.PublicKey, bool), now time.Time) (Claims, string, error) {
	claims, typ, err := VerifySignature(token, lookup)
	if err != nil {
		return Claims{}, "", err
	}
	if err := claims.Validate(); err != nil {
		return Claims{}, "", fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if claims.IssuedAt.After(now.Add(DefaultSkew)) {
		return Claims{}, "", ErrNotYetValid
	}
	if claims.ExpiresAt.Add(DefaultSkew).Before(now) {
		return Claims{}, "", ErrExpired
	}
	return claims, typ, nil
}

// VerifyService verifies a stateless service token with a reserved subject.
func VerifyService(token string, trust *policysign.TrustStore, now time.Time) (Claims, error) {
	claims, typ, err := Verify(token, trust.Lookup, now)
	if err != nil {
		return Claims{}, err
	}
	if typ != TypService {
		return Claims{}, ErrWrongType
	}
	if !IsReservedIdentity(claims.Subject) {
		return Claims{}, ErrNotReserved
	}
	return claims, nil
}

type tokenHeader struct {
	Alg string `json:"alg"`
	KID string `json:"kid"`
	Typ string `json:"typ"`
}

func parseHeader(segment string) (tokenHeader, error) {
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return tokenHeader{}, fmt.Errorf("%w: header: %v", ErrMalformed, err)
	}
	var header tokenHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return tokenHeader{}, fmt.Errorf("%w: header parse: %v", ErrMalformed, err)
	}
	if !strings.EqualFold(header.Alg, Algorithm) {
		return tokenHeader{}, fmt.Errorf("%w: alg=%q", ErrSignature, header.Alg)
	}
	if header.Typ != TypSession && header.Typ != TypService {
		return tokenHeader{}, fmt.Errorf("%w: unrecognized typ %q", ErrMalformed, header.Typ)
	}
	header.KID = strings.ToLower(strings.TrimSpace(header.KID))
	if header.KID == "" {
		return tokenHeader{}, fmt.Errorf("%w: kid missing", ErrSignature)
	}
	return header, nil
}
