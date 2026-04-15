package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

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

func (s *server) handleListWorkerCredentials(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.workerCredentialStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "worker credential store unavailable")
		return
	}

	records, err := s.workerCredentialStore.List(r.Context())
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
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.workerCredentialStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "worker credential store unavailable")
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
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateStringArray("allowed_pools", req.AllowedPools, maxCredentialArrayItems, maxCredentialArrayString); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateStringArray("allowed_topics", req.AllowedTopics, maxCredentialArrayItems, maxCredentialArrayString); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	req.AgentID = strings.TrimSpace(req.AgentID)
	if req.AgentID != "" && s.agentIdentityStore != nil {
		agent, err := s.agentIdentityStore.Get(r.Context(), req.AgentID)
		if err != nil {
			writeInternalError(w, r, "validate agent identity", err)
			return
		}
		if agent == nil {
			writeErrorJSON(w, http.StatusBadRequest, "agent_id references nonexistent agent identity")
			return
		}
	}
	if err := s.validateWorkerCredentialAccess(r, req.AllowedPools, req.AllowedTopics); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrPoolNotFound) || errors.Is(err, ErrTopicNotFound) {
			status = http.StatusNotFound
		}
		writeErrorJSON(w, status, err.Error())
		return
	}

	existing, err := s.workerCredentialStore.Get(r.Context(), req.WorkerID)
	if err != nil {
		writeInternalError(w, r, "get worker credential", err)
		return
	}
	if existing == nil {
		registeredWorkers, connectedWorkers, _, err := s.effectiveWorkerCount(r.Context())
		if err != nil {
			writeInternalError(w, r, "count workers", err)
			return
		}
		projectedWorkers := connectedWorkers
		if registeredWorkers+1 > projectedWorkers {
			projectedWorkers = registeredWorkers + 1
		}
		if limitErr := licensing.CheckWorkerLimit(int64(projectedWorkers), s.currentEntitlements()); limitErr != nil {
			writeTierLimitJSON(w, limitErr)
			return
		}
	}

	createdBy := strings.TrimSpace(policyActorID(r))
	if createdBy == "" {
		createdBy = "admin"
	}
	issued, err := s.workerCredentialStore.Create(r.Context(), workercredentials.IssueInput{
		WorkerID:      req.WorkerID,
		AllowedPools:  req.AllowedPools,
		AllowedTopics: req.AllowedTopics,
		AgentID:       req.AgentID,
		CreatedBy:     createdBy,
	})
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
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
		"actor", policyActorID(r),
		"role", policyRole(r),
		"rotated", existing != nil,
		"allowed_pools", len(req.AllowedPools),
		"allowed_topics", len(req.AllowedTopics),
	)
	s.appendAuditEntryNamed(r.Context(), action, "worker_credential", req.WorkerID, req.WorkerID, policyActorID(r), policyRole(r), verb+" worker credential "+req.WorkerID)

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
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.workerCredentialStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "worker credential store unavailable")
		return
	}

	workerID := strings.TrimSpace(r.PathValue("worker_id"))
	if err := validateWorkerID(workerID); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	existing, err := s.workerCredentialStore.Get(r.Context(), workerID)
	if err != nil {
		writeInternalError(w, r, "get worker credential", err)
		return
	}
	if existing == nil {
		writeErrorJSON(w, http.StatusNotFound, "worker credential not found")
		return
	}

	if err := s.workerCredentialStore.Revoke(r.Context(), workerID); err != nil {
		if errors.Is(err, workercredentials.ErrCredentialNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "worker credential not found")
			return
		}
		writeInternalError(w, r, "revoke worker credential", err)
		return
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
		"actor", policyActorID(r),
		"role", policyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "revoke", "worker_credential", workerID, workerID, policyActorID(r), policyRole(r), "revoke worker credential "+workerID)
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
		reg, registryEmpty, err := s.topicRegistrationForSubmit(r.Context(), topic)
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
		AllowedPools:  record.AllowedPools,
		AllowedTopics: record.AllowedTopics,
		PackID:        record.PackID,
		AgentID:       record.AgentID,
		CreatedBy:     record.CreatedBy,
		CreatedAt:     record.CreatedAt,
		RevokedAt:     record.RevokedAt,
	}
}
