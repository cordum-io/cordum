package scheduler

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"reflect"
	"strings"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/store"
)

// HandshakeCredentialStore exposes only authoritative internal credential APIs.
type HandshakeCredentialStore interface {
	GetByWorkerID(context.Context, string) (*workercredentials.Credential, error)
}

// HandshakeAgentStore resolves the canonical worker-to-agent linkage.
type HandshakeAgentStore interface {
	GetByWorkerID(context.Context, string) (*store.AgentIdentity, error)
}

// CredentialHandshakeTrustResolver joins credential, proof-key, and agent data.
type CredentialHandshakeTrustResolver struct {
	credentials HandshakeCredentialStore
	agents      HandshakeAgentStore
}

type handshakeResolutionError struct {
	internal bool
	detail   string
}

func (e *handshakeResolutionError) Error() string { return "scheduler: worker trust resolution failed" }

func NewCredentialHandshakeTrustResolver(credentials HandshakeCredentialStore, agents HandshakeAgentStore) (*CredentialHandshakeTrustResolver, error) {
	if missingHandshakeDependency(reflect.ValueOf(credentials)) || missingHandshakeDependency(reflect.ValueOf(agents)) {
		return nil, errors.New("scheduler: credential and agent stores required")
	}
	return &CredentialHandshakeTrustResolver{credentials: credentials, agents: agents}, nil
}

func (r *CredentialHandshakeTrustResolver) Resolve(ctx context.Context, workerID, keyID string) (*HandshakeTrustIdentity, error) {
	if r == nil || r.credentials == nil || r.agents == nil {
		return nil, internalResolution("identity_resolver_unavailable")
	}
	credential, err := r.credentials.GetByWorkerID(ctx, workerID)
	if err != nil {
		return nil, internalResolution("credential_store_unavailable")
	}
	if failure := validateHandshakeCredential(credential, workerID, keyID); failure != nil {
		return nil, failure
	}
	identity, err := r.agents.GetByWorkerID(ctx, workerID)
	if err != nil {
		return nil, internalResolution("agent_store_unavailable")
	}
	if failure := validateHandshakeAgent(credential, identity); failure != nil {
		return nil, failure
	}
	publicKey, revoked, err := credential.ResolveProofKey(keyID)
	if err != nil {
		return nil, internalResolution("proof_key_store_unavailable_or_invalid")
	}
	if revoked {
		return nil, authenticationResolution("proof_key_revoked")
	}
	if publicKey == nil {
		return nil, authenticationResolution("proof_key_unknown")
	}
	return resolvedHandshakeIdentity(credential, identity, publicKey), nil
}

func validateHandshakeCredential(credential *workercredentials.Credential, workerID, keyID string) error {
	if credential == nil {
		return authenticationResolution("worker_credential_unknown")
	}
	if credential.Revoked() {
		return authenticationResolution("worker_credential_revoked")
	}
	if credential.WorkerID != workerID || strings.TrimSpace(credential.TenantID) == "" {
		return authenticationResolution("worker_credential_binding_mismatch")
	}
	if credential.ProofKeyID != keyID || credential.ProofAlgorithm != workercredentials.ProofAlgorithmECDSAP256SHA256 {
		return authenticationResolution("proof_key_binding_mismatch")
	}
	return nil
}

func validateHandshakeAgent(credential *workercredentials.Credential, identity *store.AgentIdentity) error {
	if identity == nil {
		return authenticationResolution("agent_identity_unknown")
	}
	if identity.Status != "active" {
		return authenticationResolution("agent_identity_inactive")
	}
	if strings.TrimSpace(identity.ID) == "" || strings.TrimSpace(identity.TenantID) == "" || identity.TenantID != credential.TenantID {
		return authenticationResolution("agent_tenant_binding_mismatch")
	}
	if legacyAgentID := strings.TrimSpace(credential.AgentID); legacyAgentID != "" && legacyAgentID != identity.ID {
		return authenticationResolution("credential_agent_binding_mismatch")
	}
	return nil
}

func resolvedHandshakeIdentity(credential *workercredentials.Credential, identity *store.AgentIdentity, publicKey *ecdsa.PublicKey) *HandshakeTrustIdentity {
	return &HandshakeTrustIdentity{
		WorkerID: credential.WorkerID, AgentID: strings.TrimSpace(credential.AgentID), TenantID: credential.TenantID,
		ProofKeyID: credential.ProofKeyID, PublicKey: publicKey,
		AllowedTopics: append([]string(nil), credential.AllowedTopics...), AgentName: identity.Name,
	}
}

func internalResolution(detail string) error {
	return &handshakeResolutionError{internal: true, detail: detail}
}

func authenticationResolution(detail string) error {
	return &handshakeResolutionError{detail: detail}
}

func resolutionFailure(err error) *handshakeFailure {
	var resolution *handshakeResolutionError
	if errors.As(err, &resolution) {
		if resolution.internal {
			return failHandshake(internalErrorReason(), resolution.detail)
		}
		return failHandshake(authenticationFailedReason(), resolution.detail)
	}
	if err != nil {
		return failHandshake(internalErrorReason(), "identity_resolver_unavailable")
	}
	return failHandshake(authenticationFailedReason(), "worker_identity_unknown")
}
