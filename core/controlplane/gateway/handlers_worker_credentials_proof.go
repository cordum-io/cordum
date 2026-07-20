package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/store"
)

var errWorkerCredentialAgentStoreUnavailable = errors.New("worker credential agent store unavailable")

func (s *server) resolveWorkerCredentialAgent(
	ctx context.Context,
	tenant string,
	req *createWorkerCredentialRequest,
	existing *workercredentials.Credential,
) error {
	if req == nil {
		return fmt.Errorf("worker credential request required")
	}
	req.AgentID = strings.TrimSpace(req.AgentID)
	anyProof, completeProof := workerCredentialProofFieldState(*req)
	if completeProof && req.AgentID == "" {
		return fmt.Errorf("agent_id is required with proof key enrollment")
	}
	if !anyProof && existing != nil && existing.HasProofKey() {
		return s.preserveWorkerCredentialAgent(ctx, tenant, req, existing)
	}
	if req.AgentID == "" {
		return nil
	}
	_, err := s.activeWorkerCredentialAgent(ctx, tenant, req.AgentID)
	return err
}

func (s *server) preserveWorkerCredentialAgent(
	ctx context.Context,
	tenant string,
	req *createWorkerCredentialRequest,
	existing *workercredentials.Credential,
) error {
	requestedAgentID := strings.TrimSpace(req.AgentID)
	if s.agentIdentityStore == nil {
		return errWorkerCredentialAgentStoreUnavailable
	}
	agent, err := s.agentIdentityStore.GetByWorkerID(ctx, existing.WorkerID)
	if err != nil {
		return fmt.Errorf("%w: %v", errWorkerCredentialAgentStoreUnavailable, err)
	}
	if agent == nil || (existing.AgentID != "" && existing.AgentID != agent.ID) {
		return fmt.Errorf("active proof credential is missing its authoritative agent link")
	}
	if err := validateWorkerCredentialAgent(agent, tenant); err != nil {
		return err
	}
	if requestedAgentID != "" && requestedAgentID != agent.ID {
		return fmt.Errorf("agent_id cannot change without proof key replacement")
	}
	req.AgentID = agent.ID
	return nil
}

func (s *server) activeWorkerCredentialAgent(ctx context.Context, tenant, agentID string) (*store.AgentIdentity, error) {
	if s.agentIdentityStore == nil {
		return nil, errWorkerCredentialAgentStoreUnavailable
	}
	agent, err := s.agentIdentityStore.Get(ctx, tenant, agentID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errWorkerCredentialAgentStoreUnavailable, err)
	}
	if err := validateWorkerCredentialAgent(agent, tenant); err != nil {
		return nil, err
	}
	return agent, nil
}

func validateWorkerCredentialAgent(agent *store.AgentIdentity, tenant string) error {
	if agent == nil {
		return fmt.Errorf("agent_id references nonexistent agent identity")
	}
	if strings.TrimSpace(agent.TenantID) != strings.TrimSpace(tenant) {
		return fmt.Errorf("agent_id references nonexistent agent identity")
	}
	if agent.Status != "active" {
		return fmt.Errorf("agent_id must reference an active agent identity")
	}
	return nil
}

func workerCredentialProofFieldState(req createWorkerCredentialRequest) (bool, bool) {
	values := []string{req.ProofKeyID, req.ProofAlgorithm, req.ProofPublicKeyPEM}
	provided := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			provided++
		}
	}
	return provided > 0, provided == len(values)
}
