package scheduler

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/store"
)

type fakeHandshakeCredentialStore struct {
	credential     *workercredentials.Credential
	nextCredential *workercredentials.Credential
	getErr         error
	getCalls       int
}

func (s *fakeHandshakeCredentialStore) GetByWorkerID(context.Context, string) (*workercredentials.Credential, error) {
	s.getCalls++
	if s.getCalls > 1 && s.nextCredential != nil {
		return s.nextCredential, s.getErr
	}
	return s.credential, s.getErr
}

type fakeHandshakeAgentStore struct {
	identity *store.AgentIdentity
	err      error
}

func (s *fakeHandshakeAgentStore) GetByWorkerID(context.Context, string) (*store.AgentIdentity, error) {
	return s.identity, s.err
}

func TestNewCredentialHandshakeTrustResolverRejectsTypedNilStores(t *testing.T) {
	var credentials *fakeHandshakeCredentialStore
	var agents *fakeHandshakeAgentStore
	if _, err := NewCredentialHandshakeTrustResolver(credentials, &fakeHandshakeAgentStore{}); err == nil {
		t.Fatal("typed-nil credential store accepted")
	}
	if _, err := NewCredentialHandshakeTrustResolver(&fakeHandshakeCredentialStore{}, agents); err == nil {
		t.Fatal("typed-nil agent store accepted")
	}
}

func TestCredentialHandshakeTrustResolverResolvesAgreedAuthority(t *testing.T) {
	credentials, agents := validHandshakeAuthority(t)
	resolver, err := NewCredentialHandshakeTrustResolver(credentials, agents)
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}
	identity, err := resolver.Resolve(context.Background(), "worker-victim", "worker-key-v1")
	if err != nil {
		t.Fatalf("resolve authority: %v", err)
	}
	if identity.WorkerID != "worker-victim" || identity.AgentID != "agent-victim" || identity.TenantID != "tenant-victim" || identity.PublicKey == nil {
		t.Fatalf("resolved authority mismatch: %+v", identity)
	}
	identity.AllowedTopics[0] = "mutated"
	if credentials.credential.AllowedTopics[0] != "job.allowed" {
		t.Fatal("resolved topics alias credential storage")
	}
}

func TestCredentialHandshakeTrustResolverDoesNotInventMissingCredentialAgentID(t *testing.T) {
	credentials, agents := validHandshakeAuthority(t)
	credentials.credential.AgentID = ""
	resolver, err := NewCredentialHandshakeTrustResolver(credentials, agents)
	if err != nil {
		t.Fatalf("new resolver: %v", err)
	}

	identity, err := resolver.Resolve(context.Background(), "worker-victim", "worker-key-v1")
	if err != nil {
		t.Fatalf("resolve legacy credential: %v", err)
	}
	if identity == nil || identity.AgentID != "" {
		t.Fatalf("resolved identity = %+v, want no unasserted agent authority", identity)
	}
}

func TestCredentialHandshakeTrustResolverRejectsRecordDisagreement(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeHandshakeCredentialStore, *fakeHandshakeAgentStore)
		detail string
	}{
		{name: "missing credential", detail: "unknown", mutate: func(c *fakeHandshakeCredentialStore, _ *fakeHandshakeAgentStore) { c.credential = nil }},
		{name: "revoked credential", detail: "revoked", mutate: func(c *fakeHandshakeCredentialStore, _ *fakeHandshakeAgentStore) {
			c.credential.RevokedAt = "2026-07-16T00:00:00Z"
		}},
		{name: "missing proof key", detail: "proof", mutate: func(c *fakeHandshakeCredentialStore, _ *fakeHandshakeAgentStore) { c.credential.ProofKeyID = "" }},
		{name: "inactive identity", detail: "inactive", mutate: func(_ *fakeHandshakeCredentialStore, a *fakeHandshakeAgentStore) { a.identity.Status = "suspended" }},
		{name: "tenant mismatch", detail: "tenant", mutate: func(_ *fakeHandshakeCredentialStore, a *fakeHandshakeAgentStore) {
			a.identity.TenantID = "tenant-attacker"
		}},
		{name: "legacy agent mismatch", detail: "agent", mutate: func(c *fakeHandshakeCredentialStore, _ *fakeHandshakeAgentStore) {
			c.credential.AgentID = "agent-attacker"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			credentials, agents := validHandshakeAuthority(t)
			tc.mutate(credentials, agents)
			resolver, _ := NewCredentialHandshakeTrustResolver(credentials, agents)
			identity, err := resolver.Resolve(context.Background(), "worker-victim", "worker-key-v1")
			failure := resolutionFailure(err)
			if identity != nil || failure.reason != authenticationFailedReason() || !containsFold(failure.detail, tc.detail) {
				t.Fatalf("identity=%+v failure=%+v", identity, failure)
			}
		})
	}
}

func TestCredentialHandshakeTrustResolverProofStoreErrorIsInternal(t *testing.T) {
	credentials, agents := validHandshakeAuthority(t)
	credentials.credential.ProofPublicKeyPEM = "not a public key"
	resolver, _ := NewCredentialHandshakeTrustResolver(credentials, agents)
	identity, err := resolver.Resolve(context.Background(), "worker-victim", "worker-key-v1")
	failure := resolutionFailure(err)
	if identity != nil || failure.reason != internalErrorReason() || !containsFold(failure.detail, "proof") {
		t.Fatalf("identity=%+v failure=%+v", identity, failure)
	}
}

func TestCredentialHandshakeTrustResolverUsesOneCredentialSnapshot(t *testing.T) {
	credentials, agents := validHandshakeAuthority(t)
	originalKey, _, err := credentials.credential.ResolveProofKey("worker-key-v1")
	if err != nil {
		t.Fatalf("parse original proof key: %v", err)
	}
	rotated := *credentials.credential
	rotatedKey := generateProtocolKey(t)
	rotated.ProofPublicKeyPEM = handshakePublicKeyPEM(t, &rotatedKey.PublicKey)
	rotated.AgentID, rotated.TenantID = "agent-attacker", "tenant-attacker"
	credentials.nextCredential = &rotated

	resolver, _ := NewCredentialHandshakeTrustResolver(credentials, agents)
	identity, err := resolver.Resolve(context.Background(), "worker-victim", "worker-key-v1")
	if err != nil || identity == nil {
		t.Fatalf("resolve snapshot: identity=%+v err=%v", identity, err)
	}
	if credentials.getCalls != 1 || identity.PublicKey.X.Cmp(originalKey.X) != 0 || identity.TenantID != "tenant-victim" {
		t.Fatalf("mixed credential snapshots: calls=%d identity=%+v", credentials.getCalls, identity)
	}
}

func TestCredentialHandshakeTrustResolverCredentialStoreErrorIsInternal(t *testing.T) {
	credentials, agents := validHandshakeAuthority(t)
	credentials.getErr = errors.New("redis unavailable")
	resolver, _ := NewCredentialHandshakeTrustResolver(credentials, agents)
	_, err := resolver.Resolve(context.Background(), "worker-victim", "worker-key-v1")
	if failure := resolutionFailure(err); failure.reason != internalErrorReason() || !containsFold(failure.detail, "credential") {
		t.Fatalf("failure=%+v", failure)
	}
}

func validHandshakeAuthority(t *testing.T) (*fakeHandshakeCredentialStore, *fakeHandshakeAgentStore) {
	t.Helper()
	key := generateProtocolKey(t)
	credentials := &fakeHandshakeCredentialStore{
		credential: &workercredentials.Credential{
			WorkerID: "worker-victim", TenantID: "tenant-victim", AgentID: "agent-victim",
			ProofKeyID: "worker-key-v1", ProofAlgorithm: workercredentials.ProofAlgorithmECDSAP256SHA256,
			ProofPublicKeyPEM: handshakePublicKeyPEM(t, &key.PublicKey), AllowedTopics: []string{"job.allowed"},
		},
	}
	agents := &fakeHandshakeAgentStore{identity: &store.AgentIdentity{
		ID: "agent-victim", TenantID: "tenant-victim", Name: "Victim", Status: "active",
	}}
	return credentials, agents
}

func handshakePublicKeyPEM(t *testing.T, key *ecdsa.PublicKey) string {
	t.Helper()
	encoded, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("marshal handshake public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded}))
}
