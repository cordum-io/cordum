package gateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/controlplane/gateway/pools"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/redis/go-redis/v9"
)

type createTopicRequest struct {
	Name           string   `json:"name"`
	Pool           string   `json:"pool"`
	InputSchemaID  string   `json:"input_schema_id"`
	OutputSchemaID string   `json:"output_schema_id"`
	PackID         string   `json:"pack_id"`
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

const (
	maxTopicArrayItems  = 100
	maxTopicArrayString = 128
)

func (s *server) handleListTopics(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermTopicsRead, "admin", "operator", "viewer") {
		return
	}
	if s.topicRegistry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "topic registry unavailable")
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeTopicSchemaViolation, err.Error())
		return
	}

	snap, err := s.topicRegistry.ListForTenant(r.Context(), tenant)
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
	if !s.requirePermissionOrRole(w, r, auth.PermTopicsWrite, "admin") {
		return
	}
	if s.topicRegistry == nil || s.configSvc == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "topic registry unavailable")
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeTopicSchemaViolation, err.Error())
		return
	}

	var req createTopicRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Pool = strings.TrimSpace(req.Pool)
	req.Requires = trimStringSlice(req.Requires)
	req.RiskTags = trimStringSlice(req.RiskTags)
	if err := pools.ValidateTopicName(req.Name); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeTopicSchemaViolation, err.Error())
		return
	}
	if err := validateStringArray("requires", req.Requires, maxTopicArrayItems, maxTopicArrayString); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeTopicSchemaViolation, err.Error())
		return
	}
	if err := validateStringArray("risk_tags", req.RiskTags, maxTopicArrayItems, maxTopicArrayString); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeTopicSchemaViolation, err.Error())
		return
	}
	if req.Pool != "" {
		if err := pools.ValidatePoolName(req.Pool); err != nil {
			writeJSONError(w, http.StatusBadRequest, errorCodeTopicSchemaViolation, err.Error())
			return
		}
		if err := s.ensurePoolExists(r.Context(), req.Pool); err != nil {
			if errors.Is(err, ErrPoolNotFound) {
				writeJSONError(w, http.StatusNotFound, errorCodePoolNotFound, err.Error())
				return
			}
			writeJSONError(w, http.StatusBadRequest, errorCodeTopicSchemaViolation, err.Error())
			return
		}
	}

	existing, _, err := s.topicRegistry.GetForTenant(r.Context(), tenant, req.Name)
	if err != nil {
		writeInternalError(w, r, "get topic", err)
		return
	}

	record := topicregistry.Registration{
		Name:           req.Name,
		TenantID:       tenant,
		Pool:           req.Pool,
		InputSchemaID:  req.InputSchemaID,
		OutputSchemaID: req.OutputSchemaID,
		PackID:         req.PackID,
		Requires:       req.Requires,
		RiskTags:       req.RiskTags,
		Status:         req.Status,
	}
	if err := s.topicRegistry.Set(r.Context(), record); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeTopicSchemaViolation, err.Error())
		return
	}

	s.publishConfigChanged("system", "topics")
	slog.Info("topic registration updated",
		"topic", req.Name,
		"pool", req.Pool,
		"pack_id", req.PackID,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
		"replaced", existing != nil,
	)
	s.appendAuditEntryNamed(r.Context(), "create", "topic", req.Name, req.Name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "register topic "+req.Name)

	status := http.StatusCreated
	if existing != nil {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	writeJSON(w, record)
}

func (s *server) handleDeleteTopic(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermTopicsWrite, "admin") {
		return
	}
	if s.topicRegistry == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "topic registry unavailable")
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeTopicSchemaViolation, err.Error())
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	if err := pools.ValidateTopicName(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, errorCodeTopicSchemaViolation, err.Error())
		return
	}
	// GetForTenant filters by tenant: an admin in another tenant sees the
	// same response shape as if the topic does not exist (404 + no leak),
	// which is the existence-oracle hardening the audit demanded.
	existing, _, err := s.topicRegistry.GetForTenant(r.Context(), tenant, name)
	if err != nil {
		writeInternalError(w, r, "get topic", err)
		return
	}
	if existing == nil || existing.TenantID != tenant {
		writeJSONError(w, http.StatusNotFound, errorCodeTopicNotFound, "topic not found")
		return
	}
	if err := s.topicRegistry.DeleteForTenant(r.Context(), tenant, []string{name}); err != nil {
		writeInternalError(w, r, "delete topic", err)
		return
	}
	removedMapping, err := s.removeTopicFromPoolConfig(r.Context(), name)
	if err != nil {
		writeInternalError(w, r, "remove topic pool mapping", err)
		return
	}
	s.publishConfigChanged("system", "topics")
	if removedMapping {
		s.publishConfigChanged("system", "default")
	}
	slog.Warn("topic registration deleted",
		"topic", name,
		"pool", existing.Pool,
		"pack_id", existing.PackID,
		"actor", policybundles.PolicyActorID(r),
		"role", policybundles.PolicyRole(r),
	)
	s.appendAuditEntryNamed(r.Context(), "delete", "topic", name, name, policybundles.PolicyActorID(r), policybundles.PolicyRole(r), "delete topic "+name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) removeTopicFromPoolConfig(ctx context.Context, topic string) (bool, error) {
	if s.configSvc == nil {
		return false, nil
	}
	for attempt := 0; attempt < 3; attempt++ {
		removed, err := s.removeTopicFromPoolConfigOnce(ctx, topic)
		if errors.Is(err, configsvc.ErrRevisionConflict) {
			continue
		}
		return removed, err
	}
	return false, configsvc.ErrRevisionConflict
}

func (s *server) removeTopicFromPoolConfigOnce(ctx context.Context, topic string) (bool, error) {
	doc, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, "default")
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	topics, poolMap, err := extractPoolsFromConfig(doc)
	if err != nil {
		return false, err
	}
	if _, ok := topics[topic]; !ok {
		return false, nil
	}
	delete(topics, topic)
	writePoolsToConfig(doc, topics, poolMap)
	if err := s.configSvc.Set(ctx, doc); err != nil {
		return false, err
	}
	return true, nil
}

func (s *server) ensurePoolExists(ctx context.Context, pool string) error {
	doc, err := s.configSvc.Get(ctx, configsvc.ScopeSystem, "default")
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return poolNotFoundError{pool: pool}
		}
		return err
	}
	_, poolMap, err := extractPoolsFromConfig(doc)
	if err != nil {
		return err
	}
	if _, ok := poolMap[pool]; !ok {
		return poolNotFoundError{pool: pool}
	}
	return nil
}

func (s *server) topicRegistrationForSubmit(ctx context.Context, tenantID, topic string) (*topicregistry.Registration, bool, error) {
	if s == nil || s.topicRegistry == nil {
		return nil, true, nil
	}
	return s.topicRegistry.GetForTenant(ctx, tenantID, topic)
}

func (s *server) registeredTopicNamesForTenant(ctx context.Context, tenantID string, limit int) ([]string, bool, error) {
	if s == nil || s.topicRegistry == nil {
		return nil, false, nil
	}
	snap, err := s.topicRegistry.ListForTenant(ctx, tenantID)
	if err != nil {
		return nil, false, err
	}
	names := make([]string, 0, len(snap.Items))
	for _, item := range snap.Items {
		if item.Status == topicregistry.StatusDisabled {
			continue
		}
		names = append(names, item.Name)
	}
	if limit > 0 && len(names) > limit {
		return names[:limit], true, nil
	}
	return names, false, nil
}
