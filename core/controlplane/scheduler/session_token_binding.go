package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/auth/servicetoken"
)

var ErrSessionTokenBindingMismatch = errors.New("scheduler: session token authority binding mismatch")

// SessionBinding is the authoritative identity attached to an authenticated
// worker session. WorkerID is the token subject; AgentID is the linked agent.
type SessionBinding struct {
	WorkerID   string
	AgentID    string
	Tenant     string
	Audience   string
	ProofKeyID string
	SDKVersion string
}

func (b SessionBinding) Validate() error {
	fields := []struct{ name, value string }{
		{"worker_id", b.WorkerID}, {"agent_id", b.AgentID}, {"tenant", b.Tenant},
		{"aud", b.Audience}, {"proof_key_id", b.ProofKeyID}, {"sdk_ver", b.SDKVersion},
	}
	missing := make([]string, 0, len(fields))
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf("%w: %s", ErrSessionTokenMissingClaims, strings.Join(missing, ", "))
	}
	return nil
}

func (b SessionBinding) normalized() SessionBinding {
	b.WorkerID = strings.TrimSpace(b.WorkerID)
	b.AgentID = strings.TrimSpace(b.AgentID)
	b.Tenant = strings.TrimSpace(b.Tenant)
	b.Audience = strings.TrimSpace(b.Audience)
	b.ProofKeyID = strings.TrimSpace(b.ProofKeyID)
	b.SDKVersion = strings.TrimSpace(b.SDKVersion)
	return b
}

// Binding returns the worker authority encoded in the claims.
func (c SessionTokenClaims) Binding() SessionBinding {
	return SessionBinding{
		WorkerID: c.Subject, AgentID: c.AgentID, Tenant: c.Tenant,
		Audience: c.Audience, ProofKeyID: c.ProofKeyID, SDKVersion: c.SDKVersion,
	}
}

func (c SessionTokenClaims) hasBindingClaims() bool {
	return c.AgentID != "" || c.Audience != "" || c.ProofKeyID != ""
}

func (c SessionTokenClaims) missingBindingClaims() []string {
	missing := make([]string, 0, 3)
	for _, field := range []struct{ name, value string }{
		{"agent_id", c.AgentID}, {"aud", c.Audience}, {"proof_key_id", c.ProofKeyID},
	} {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}
	return missing
}

func (c SessionTokenClaims) validateBound() error {
	if !c.hasBindingClaims() {
		return fmt.Errorf("%w: agent_id, aud, proof_key_id", ErrSessionTokenMissingClaims)
	}
	return c.Binding().Validate()
}

// IssueBound mints a Redis-tracked token whose subject is the worker ID.
func (i *SessionTokenIssuer) IssueBound(ctx context.Context, binding SessionBinding) (string, SessionTokenClaims, error) {
	if i == nil || i.redis == nil {
		return "", SessionTokenClaims{}, ErrSessionTokenStoreUnready
	}
	binding = binding.normalized()
	if err := binding.Validate(); err != nil {
		return "", SessionTokenClaims{}, err
	}
	claims, err := i.newBoundClaims(binding)
	if err != nil {
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

func (i *SessionTokenIssuer) newBoundClaims(binding SessionBinding) (SessionTokenClaims, error) {
	jti, err := newJTI()
	if err != nil {
		return SessionTokenClaims{}, fmt.Errorf("scheduler: jti generation: %w", err)
	}
	now := i.now().UTC()
	claims := SessionTokenClaims{
		Subject: binding.WorkerID, AgentID: binding.AgentID, Tenant: binding.Tenant,
		Audience: binding.Audience, ProofKeyID: binding.ProofKeyID, SDKVersion: binding.SDKVersion,
		JTI: jti, IssuedAt: now, ExpiresAt: now.Add(i.lifetime),
	}
	return claims, claims.Validate()
}

// VerifyBound rejects legacy sessions that do not carry worker proof bindings.
func (i *SessionTokenIssuer) VerifyBound(ctx context.Context, token string, checkActive bool) (SessionTokenClaims, error) {
	claims, err := i.Verify(ctx, token, checkActive)
	if err != nil {
		return SessionTokenClaims{}, err
	}
	if err := claims.validateBound(); err != nil {
		return SessionTokenClaims{}, err
	}
	return claims, nil
}

// RenewBound rotates only the current token with the exact expected authority.
func (i *SessionTokenIssuer) RenewBound(ctx context.Context, token string, expected SessionBinding) (string, SessionTokenClaims, error) {
	expected = expected.normalized()
	if err := expected.Validate(); err != nil {
		return "", SessionTokenClaims{}, err
	}
	return i.renew(ctx, token, &expected)
}

func (i *SessionTokenIssuer) renew(ctx context.Context, token string, expected *SessionBinding) (string, SessionTokenClaims, error) {
	if i == nil || i.redis == nil {
		return "", SessionTokenClaims{}, ErrSessionTokenStoreUnready
	}
	claims, typ, err := i.parseAndVerifySignature(token)
	if err != nil {
		return "", SessionTokenClaims{}, err
	}
	if typ != servicetoken.TypSession {
		return "", SessionTokenClaims{}, fmt.Errorf("%w: only session tokens may be renewed", ErrSessionTokenMalformed)
	}
	if err := i.verifyClaimsTime(claims); err != nil {
		return "", SessionTokenClaims{}, err
	}
	if err := verifyExpectedBinding(claims, expected); err != nil {
		return "", SessionTokenClaims{}, err
	}
	if err := i.assertActive(ctx, claims); err != nil {
		return "", SessionTokenClaims{}, err
	}
	fresh, err := i.rotatedClaims(claims)
	if err != nil {
		return "", SessionTokenClaims{}, err
	}
	token, err = i.signClaims(fresh)
	if err != nil {
		return "", SessionTokenClaims{}, err
	}
	if err := i.persistRenew(ctx, claims, fresh); err != nil {
		return "", SessionTokenClaims{}, err
	}
	return token, fresh, nil
}

func verifyExpectedBinding(claims SessionTokenClaims, expected *SessionBinding) error {
	if expected == nil {
		return nil
	}
	if err := claims.validateBound(); err != nil {
		return err
	}
	if claims.Binding() != *expected {
		return ErrSessionTokenBindingMismatch
	}
	return nil
}

func (i *SessionTokenIssuer) rotatedClaims(prior SessionTokenClaims) (SessionTokenClaims, error) {
	jti, err := newJTI()
	if err != nil {
		return SessionTokenClaims{}, fmt.Errorf("scheduler: jti generation: %w", err)
	}
	now := i.now().UTC()
	prior.JTI = jti
	prior.IssuedAt = now
	prior.ExpiresAt = now.Add(i.lifetime)
	return prior, prior.Validate()
}
