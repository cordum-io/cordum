package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/pools"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/redis/go-redis/v9"
)

type createTopicRequest struct {
	Name           string   `json:"name"`
	Pool           string   `json:"pool"`
	InputSchemaID  string   `json:"input_schema_id"`
	OutputSchemaID string   `json:"output_schema_id"`
	Requires       []string `json:"requires"`
	RiskTags       []string `json:"risk_tags"`
	Status         string   `json:"status"`
}

type topicResponse struct {
	Name              string   `json:"name"`
	Pool              string   `json:"pool"`
	InputSchemaID     string   `json:"input_schema_id,omitempty"`
	OutputSchemaID    string   `json:"output_schema_id,omitempty"`
	PackID            string   `json:"pack_id,omitempty"`
	Requires          []string `json:"requires,omitempty"`
	RiskTags          []string `json:"risk_tags,omitempty"`
	Status            string   `json:"status"`
	ActiveWorkerCount int      `json:"active_worker_count"`
}

func (s *server) handleListTopics(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin", "operator", "viewer"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.topicRegistry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "topic registry unavailable")
		return
	}

	snap, err := s.topicRegistry.List(r.Context())
	if err != nil {
		writeInternalError(w, r, "list topics", err)
		return
	}
	workerSnap, err := s.snapshotFromRedis()
	if err != nil && !errors.Is(err, redis.Nil) {
		// Keep the endpoint available even when the runtime snapshot is cold/unavailable.
		workerSnap = nil
	}
	counts := topicregistry.ActiveWorkerCountsByTopic(workerSnap, snap.Items)
	items := make([]topicResponse, 0, len(snap.Items))
	for _, item := range snap.Items {
		items = append(items, topicResponse{
			Name:              item.Name,
			Pool:              item.Pool,
			InputSchemaID:     item.InputSchemaID,
			OutputSchemaID:    item.OutputSchemaID,
			PackID:            item.PackID,
			Requires:          item.Requires,
			RiskTags:          item.RiskTags,
			Status:            item.Status,
			ActiveWorkerCount: counts[item.Name],
		})
	}
	writeJSON(w, map[string]any{
		"items":          items,
		"registry_empty": snap.RegistryEmpty,
	})
}

func (s *server) handleCreateTopic(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.topicRegistry == nil || s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "topic registry unavailable")
		return
	}

	var req createTopicRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Pool = strings.TrimSpace(req.Pool)
	if err := pools.ValidateTopicName(req.Name); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := pools.ValidatePoolName(req.Pool); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.ensurePoolExists(r.Context(), req.Pool); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeErrorJSON(w, http.StatusNotFound, err.Error())
			return
		}
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	existing, _, err := s.topicRegistry.Get(r.Context(), req.Name)
	if err != nil {
		writeInternalError(w, r, "get topic", err)
		return
	}

	record := topicregistry.Registration{
		Name:           req.Name,
		Pool:           req.Pool,
		InputSchemaID:  req.InputSchemaID,
		OutputSchemaID: req.OutputSchemaID,
		Requires:       req.Requires,
		RiskTags:       req.RiskTags,
		Status:         req.Status,
	}
	if err := s.topicRegistry.Set(r.Context(), record); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}

	s.publishConfigChanged("system", "topics")
	s.appendAuditEntryNamed(r.Context(), "create", "topic", req.Name, req.Name, policyActorID(r), policyRole(r), "register topic "+req.Name)

	status := http.StatusCreated
	if existing != nil {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	writeJSON(w, record)
}

func (s *server) handleDeleteTopic(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if s.topicRegistry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "topic registry unavailable")
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := pools.ValidateTopicName(name); err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	existing, _, err := s.topicRegistry.Get(r.Context(), name)
	if err != nil {
		writeInternalError(w, r, "get topic", err)
		return
	}
	if existing == nil {
		writeErrorJSON(w, http.StatusNotFound, "topic not found")
		return
	}
	if err := s.topicRegistry.Delete(r.Context(), name); err != nil {
		writeInternalError(w, r, "delete topic", err)
		return
	}
	s.publishConfigChanged("system", "topics")
	s.appendAuditEntryNamed(r.Context(), "delete", "topic", name, name, policyActorID(r), policyRole(r), "delete topic "+name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) ensurePoolExists(ctx context.Context, pool string) error {
	doc, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return fmt.Errorf("pool %q not found", pool)
		}
		return err
	}
	_, poolMap, err := extractPoolsFromConfig(doc)
	if err != nil {
		return err
	}
	if _, ok := poolMap[pool]; !ok {
		return fmt.Errorf("pool %q not found", pool)
	}
	return nil
}

func (s *server) topicRegistrationForSubmit(ctx context.Context, topic string) (*topicregistry.Registration, bool, error) {
	if s == nil || s.topicRegistry == nil {
		return nil, true, nil
	}
	return s.topicRegistry.Get(ctx, topic)
}
