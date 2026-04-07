package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/pools"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
)

type createWorkerCredentialRequest struct {
	WorkerID      string   `json:"worker_id"`
	AllowedPools  []string `json:"allowed_pools"`
	AllowedTopics []string `json:"allowed_topics"`
}

type workerCredentialResponse struct {
	WorkerID      string   `json:"worker_id"`
	AllowedPools  []string `json:"allowed_pools,omitempty"`
	AllowedTopics []string `json:"allowed_topics,omitempty"`
	PackID        string   `json:"pack_id,omitempty"`
	CreatedBy     string   `json:"created_by"`
	CreatedAt     string   `json:"created_at"`
	RevokedAt     string   `json:"revoked_at,omitempty"`
}

type issueWorkerCredentialResponse struct {
	workerCredentialResponse
	Token string `json:"token"`
}

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
	if err := validateWorkerID(req.WorkerID); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateWorkerCredentialAccess(r, req.AllowedPools, req.AllowedTopics); err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
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

	createdBy := strings.TrimSpace(policyActorID(r))
	if createdBy == "" {
		createdBy = "admin"
	}
	issued, err := s.workerCredentialStore.Create(r.Context(), workercredentials.IssueInput{
		WorkerID:      req.WorkerID,
		AllowedPools:  req.AllowedPools,
		AllowedTopics: req.AllowedTopics,
		CreatedBy:     createdBy,
	})
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	s.publishConfigChanged("system", "workers")
	action := "create"
	verb := "create"
	if existing != nil {
		action = "rotate"
		verb = "rotate"
	}
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

	s.publishConfigChanged("system", "workers")
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
			return fmt.Errorf("topic %q not found", topic)
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
		CreatedBy:     record.CreatedBy,
		CreatedAt:     record.CreatedAt,
		RevokedAt:     record.RevokedAt,
	}
}
