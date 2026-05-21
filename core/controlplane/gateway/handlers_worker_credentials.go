package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/controlplane/gateway/pools"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/licensing"
)

type createWorkerCredentialRequest struct {
	WorkerID      string   `json:"worker_id"`
	AllowedPools  []string `json:"allowed_pools"`
	AllowedTopics []string `json:"allowed_topics"`
	AgentID       string   `json:"agent_id,omitempty"`
}

type workerCredentialResponse struct {
	WorkerID      string   `json:"worker_id"`
	TenantID      string   `json:"tenant_id,omitempty"`
	AllowedPools  []string `json:"allowed_pools,omitempty"`
	AllowedTopics []string `json:"allowed_topics,omitempty"`
	PackID        string   `json:"pack_id,omitempty"`
	AgentID       string   `json:"agent_id,omitempty"`
	CreatedBy     string   `json:"created_by"`
	CreatedAt     string   `json:"created_at"`
	RevokedAt     string   `json:"revoked_at,omitempty"`
}

type issueWorkerCredentialResponse struct {
	workerCredentialResponse
	Token string `json:"token"`
}

const (
	maxCredentialArrayItems  = 100
	maxCredentialArrayString = 128
)

func writeWorkerCredentialAccessError(w http.ResponseWriter, status int, err error) {
	code := errorCodeWorkerCredBindingInvalid
	if errors.Is(err, ErrPoolNotFound) {
		code = errorCodePoolNotFound
	} else if errors.Is(err, ErrTopicNotFound) {
		code = errorCodeTopicNotFound
	}
	writeJSONError(w, status, code, err.Error())
}

func (s *server) workerCredentialTenant(w http.ResponseWriter, r *http.Request) (string, bool) {
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeAuthRequestInvalid, "tenant required")
		return "", false
	}
	return tenant, true
}

func (s *server) handleListWorkerCredentials(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermWorkerCredentialsRead, "admin") {
		return
	}
	if s.workerCredentialStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "worker credential store unavailable")
		return
	}
	tenant, ok := s.workerCredentialTenant(w, r)
	if !ok {
		return
	}

	records, err := s.workerCredentialStore.List(r.Context(), tenant)
	if err != nil {
		writeInternalError(w, r, "list worker credentials", err)
		return
	}

	items := make([]workerCredentialResponse, 0, len(records))
	for _, record := range records {
		items = append(items, workerCredentialResponseFromRecord(record))
	}
	writeJSON(w, map[string]any{"items": items})
}

func (s *server) handleCreateWorkerCredential(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermWorkerCredentialsWrite, "admin") {
		return
	}
	if s.workerCredentialStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "worker credential store unavailable")
		return
	}
	tenant, ok := s.workerCredentialTenant(w, r)
	if !ok {
		return
	}

	var req createWorkerCredentialRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	req.WorkerID = strings.TrimSpace(req.WorkerID)
	req.AllowedPools = trimStringSlice(req.AllowedPools)
	req.AllowedTopics = trimStringSlice(req.AllowedTopics)
	if err := validateWorkerID(req.WorkerID); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeWorkerCredBindingInvalid, err.Error())
		return
	}
	if err := validateStringArray("allowed_pools", req.AllowedPools, maxCredentialArrayItems, maxCredentialArrayString); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeWorkerCredBindingInvalid, err.Error())
		return
	}
	if err := validateStringArray("allowed_topics", req.AllowedTopics, maxCredentialArrayItems, maxCredentialArrayString); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeWorkerCredBindingInvalid, err.Error())
		return
	}
	req.AgentID = strings.TrimSpace(req.AgentID)
	if req.AgentID != "" && s.agentIdentityStore != nil {
		agent, err := s.agentIdentityStore.Get(r.Context(), "", req.AgentID)
		if err != nil {
			writeInternalError(w, r, "validate agent identity", err)
			return
		}
		if agent == nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeWorkerCredBindingInvalid, "agent_id references nonexistent agent identity")
			return
		}
	}
	if err := s.validateWorkerCredentialAccess(r, req.AllowedPools, req.AllowedTopics); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrPoolNotFound) || errors.Is(err, ErrTopicNotFound) {
			status = http.StatusNotFound
		}
		writeWorkerCredentialAccessError(w, status, err)
		return
	}

	existing, err := s.workerCredentialStore.Get(r.Context(), tenant, req.WorkerID)
	if err != nil {
		writeInternalError(w, r, "get worker credential", err)
		return
	}

	createdBy := strings.TrimSpace(policybundles.PolicyActorID(r))
	if createdBy == "" {
		createdBy = "admin"
	}
	issued, err := s.workerCredentialStore.CreateWithLimit(r.Context(), workercredentials.IssueInput{
		TenantID:      tenant,
		WorkerID:      req.WorkerID,
		AllowedPools:  req.AllowedPools,
		AllowedTopics: req.AllowedTopics,
		AgentID:       req.AgentID,
		CreatedBy:     createdBy,
	}, workercredentials.CreateLimit{
		Entitlements:     s.currentEntitlements(),
		ConnectedWorkers: s.connectedWorkerCount(),
	})
	if err != nil {
		var limitErr *licensing.TierLimitError
		if errors.As(err, &limitErr) {
			writeTierLimitJSON(w, limitErr)
			return
		}
		if errors.Is(err, workercredentials.ErrCredentialNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkerCredNotFound, "worker credential not found")
			return
		}
		writeJSONError(w, http.StatusBadRequest, errorCodeWorkerCredBindingInvalid, err.Error())
		return
	}

	if s.agentIdentityStore != nil {
		if req.AgentID != "" {
			if linkErr := s.agentIdentityStore.LinkWorker(r.Context(), req.AgentID, req.WorkerID); linkErr != nil {
				slog.Error("link worker to agent identity failed",
					"worker_id", req.WorkerID,
					"agent_id", req.AgentID,
					"error", linkErr,
				)
				writeInternalError(w, r, "link worker to agent identity", linkErr)
				return
			}
		} else {
			// Clear stale reverse-lookup when credential is rotated without agent_id.
			if unlinkErr := s.agentIdentityStore.UnlinkWorker(r.Context(), req.WorkerID); unlinkErr != nil {
				slog.Error("unlink worker from agent identity failed",
					"worker_id", req.WorkerID,
					"error", unlinkErr,
				)
				writeInternalError(w, r, "unlink worker from agent identity", unlinkErr)
				return
			}
		}
	}

	s.publishConfigChanged("system", "workers")
	action := "create"
	verb := "create"
	if existing != nil {
		action = "rotate"
		verb = "rotate"
	}
	slog.Info("worker credential issued",
		"worker_id", req.WorkerID,
		"created_by", createdBy,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
		"rotated", existing != nil,
		"allowed_pools", len(req.AllowedPools),
		"allowed_topics", len(req.AllowedTopics),
	)
	s.appendAuditEntryNamed(r.Context(), action, "worker_credential", req.WorkerID, req.WorkerID, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), verb+" worker credential "+req.WorkerID)

	status := http.StatusCreated
	if existing != nil {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	writeJSON(w, issueWorkerCredentialResponse{
		workerCredentialResponse: workerCredentialResponseFromRecord(issued.Credential),
		Token:                    issued.Token,
	})
}

func (s *server) handleDeleteWorkerCredential(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermWorkerCredentialsWrite, "admin") {
		return
	}
	if s.workerCredentialStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "worker credential store unavailable")
		return
	}
	tenant, ok := s.workerCredentialTenant(w, r)
	if !ok {
		return
	}

	workerID := strings.TrimSpace(r.PathValue("worker_id"))
	if err := validateWorkerID(workerID); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeWorkerCredBindingInvalid, err.Error())
		return
	}

	existing, err := s.workerCredentialStore.Get(r.Context(), tenant, workerID)
	if err != nil {
		writeInternalError(w, r, "get worker credential", err)
		return
	}
	if existing == nil {
		writeJSONError(w, http.StatusNotFound, errorCodeWorkerCredNotFound, "worker credential not found")
		return
	}

	if err := s.workerCredentialStore.Revoke(r.Context(), tenant, workerID); err != nil {
		if errors.Is(err, workercredentials.ErrCredentialNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeWorkerCredNotFound, "worker credential not found")
			return
		}
		writeInternalError(w, r, "revoke worker credential", err)
		return
	}
	if s.sessionIssuer != nil {
		if err := s.sessionIssuer.RevokeByAgent(r.Context(), tenant, workerID); err != nil {
			writeInternalError(w, r, "revoke worker session by credential", err)
			return
		}
	}

	// Clear agent reverse-lookup so revoked credentials don't inject stale agent_id.
	if s.agentIdentityStore != nil {
		if unlinkErr := s.agentIdentityStore.UnlinkWorker(r.Context(), workerID); unlinkErr != nil {
			slog.Error("unlink worker from agent identity on revoke failed",
				"worker_id", workerID,
				"error", unlinkErr,
			)
			writeInternalError(w, r, "unlink worker from agent identity on revoke", unlinkErr)
			return
		}
	}

	s.publishConfigChanged("system", "workers")
	slog.Warn("worker credential revoked",
		"worker_id", workerID,
		"created_by", existing.CreatedBy,
		"pack_id", existing.PackID,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "revoke", "worker_credential", workerID, workerID, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "revoke worker credential "+workerID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) validateWorkerCredentialAccess(r *http.Request, allowedPools, allowedTopics []string) error {
	for _, pool := range allowedPools {
		pool = strings.TrimSpace(pool)
		if err := pools.ValidatePoolName(pool); err != nil {
			return err
		}
		if err := s.ensurePoolExists(r.Context(), pool); err != nil {
			return err
		}
	}

	for _, topic := range allowedTopics {
		topic = strings.TrimSpace(topic)
		if err := pools.ValidateTopicName(topic); err != nil {
			return err
		}
		reg, registryEmpty, err := s.topicRegistrationForSubmit(r.Context(), "", topic)
		if err != nil {
			return err
		}
		if !registryEmpty && reg == nil {
			return topicNotFoundError{topic: topic}
		}
	}
	return nil
}

func validateWorkerID(workerID string) error {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return fmt.Errorf("worker id required")
	}
	if strings.ContainsAny(workerID, " \t\r\n") {
		return fmt.Errorf("worker id must not contain whitespace")
	}
	return nil
}

func workerCredentialResponseFromRecord(record workercredentials.Credential) workerCredentialResponse {
	return workerCredentialResponse{
		WorkerID:      record.WorkerID,
		TenantID:      record.TenantID,
		AllowedPools:  record.AllowedPools,
		AllowedTopics: record.AllowedTopics,
		PackID:        record.PackID,
		AgentID:       record.AgentID,
		CreatedBy:     record.CreatedBy,
		CreatedAt:     record.CreatedAt,
		RevokedAt:     record.RevokedAt,
	}
}
