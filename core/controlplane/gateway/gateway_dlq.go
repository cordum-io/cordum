package gateway

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/memory"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DLQ handlers
func (s *server) handleListDLQ(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "dlq store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	limit := int64(100)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	limit = clampListLimit(limit)
	entries, err := s.dlqStore.List(r.Context(), limit)
	if err != nil {
		slog.Error("dlq list failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list dlq entries")
		return
	}
	if s.jobStore != nil {
		filtered := make([]memory.DLQEntry, 0, len(entries))
		for _, entry := range entries {
			tenant, _ := s.jobStore.GetTenant(r.Context(), entry.JobID)
			if err := s.requireTenantAccess(r, tenant); err != nil {
				continue
			}
			filtered = append(filtered, entry)
		}
		entries = filtered
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, entries)
}

func (s *server) handleListDLQPage(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "dlq store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	limit := int64(100)
	if q := r.URL.Query().Get("limit"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			limit = v
		}
	}
	limit = clampListLimit(limit)
	cursor := int64(0)
	if q := r.URL.Query().Get("cursor"); q != "" {
		if v, err := strconv.ParseInt(q, 10, 64); err == nil && v > 0 {
			cursor = v
		}
	}
	entries, err := s.dlqStore.ListByScore(r.Context(), cursor, limit)
	if err != nil {
		slog.Error("dlq list by score failed", "error", err)
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list dlq entries")
		return
	}
	if s.jobStore != nil {
		filtered := make([]memory.DLQEntry, 0, len(entries))
		for _, entry := range entries {
			tenant, _ := s.jobStore.GetTenant(r.Context(), entry.JobID)
			if err := s.requireTenantAccess(r, tenant); err != nil {
				continue
			}
			filtered = append(filtered, entry)
		}
		entries = filtered
	}
	var nextCursor *int64
	if int64(len(entries)) == limit {
		last := entries[len(entries)-1]
		if !last.CreatedAt.IsZero() {
			nc := last.CreatedAt.Unix() - 1
			nextCursor = &nc
		}
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]any{
		"items":       entries,
		"next_cursor": nextCursor,
	})
}

func (s *server) handleDeleteDLQ(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "dlq store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing job_id")
		return
	}
	if s.jobStore != nil {
		if tenant, _ := s.jobStore.GetTenant(r.Context(), jobID); tenant != "" {
			if err := s.requireTenantAccess(r, tenant); err != nil {
				writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
				return
			}
		}
	}
	if err := s.dlqStore.Delete(r.Context(), jobID); err != nil {
		slog.Error("dlq delete failed", "error", err, "job_id", jobID) // #nosec -- job id is validated and used for diagnostics.
		writeErrorJSON(w, http.StatusInternalServerError, "failed to delete dlq entry")
		return
	}
	dlqDeleteTopic, _ := s.jobStore.GetTopic(r.Context(), jobID)
	s.appendAuditEntryNamed(r.Context(), "delete", "dlq", jobID, dlqDeleteTopic, policyActorID(r), policyRole(r), "delete dlq entry "+jobID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleRetryDLQ(w http.ResponseWriter, r *http.Request) {
	if s.dlqStore == nil || s.jobStore == nil || s.memStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "dlq, job, or memory store unavailable")
		return
	}
	if err := s.requireRole(r, "admin"); err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	jobID := r.PathValue("job_id")
	if jobID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing job_id")
		return
	}
	if tenant, _ := s.jobStore.GetTenant(r.Context(), jobID); tenant != "" {
		if err := s.requireTenantAccess(r, tenant); err != nil {
			writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
			return
		}
	}
	origReq, origReqErr := s.jobStore.GetJobRequest(r.Context(), jobID)
	if origReqErr != nil {
		slog.Warn("dlq retry missing original job request", "job_id", jobID, "error", origReqErr) // #nosec -- job id is validated and used for diagnostics.
		origReq = nil
	}
	entry, err := s.dlqStore.Get(r.Context(), jobID)
	if err != nil {
		writeErrorJSON(w, http.StatusNotFound, "dlq entry not found")
		return
	}
	topic := entry.Topic
	if topic == "" {
		if t, err := s.jobStore.GetTopic(r.Context(), jobID); err == nil {
			topic = t
		}
	}
	if topic == "" {
		writeErrorJSON(w, http.StatusBadRequest, "missing topic for retry")
		return
	}
	newJobID := jobID + "-retry-" + uuid.NewString()[:8]
	traceID := "dlq-retry-" + jobID
	var ctxPtr string
	origCtxKey := memory.MakeContextKey(jobID)
	if data, err := s.memStore.GetContext(r.Context(), origCtxKey); err == nil {
		newCtxKey := memory.MakeContextKey(newJobID)
		if err := s.memStore.PutContext(r.Context(), newCtxKey, data); err == nil {
			ctxPtr = memory.PointerForKey(newCtxKey)
		}
	}

	tenant, _ := s.jobStore.GetTenant(r.Context(), jobID)
	team, _ := s.jobStore.GetTeam(r.Context(), jobID)
	principal, _ := s.jobStore.GetPrincipal(r.Context(), jobID)

	envOverrides := map[string]string{
		"tenant_id":    tenant,
		"team_id":      team,
		"retry_of_job": jobID,
	}
	labelOverrides := map[string]string{
		"retry":        "true",
		"dlq_entry":    jobID,
		"retry_of_job": jobID,
	}
	var baseEnv map[string]string
	var baseLabels map[string]string
	if origReq != nil {
		baseEnv = origReq.GetEnv()
		baseLabels = origReq.GetLabels()
	}

	jobReq := &pb.JobRequest{
		JobId:       newJobID,
		Topic:       topic,
		ContextPtr:  ctxPtr,
		TenantId:    tenant,
		PrincipalId: principal,
		Env:         mergeStringMap(baseEnv, envOverrides),
		Labels:      mergeStringMap(baseLabels, labelOverrides),
	}
	if origReq != nil {
		jobReq.Meta = origReq.GetMeta()
	}

	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        "api-gateway",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}

	if err := s.jobStore.SetJobMeta(r.Context(), jobReq); err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to persist job metadata")
		return
	}
	if err := s.jobStore.AddJobToTrace(r.Context(), traceID, newJobID); err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to persist trace metadata")
		return
	}
	if err := s.jobStore.SetState(r.Context(), newJobID, scheduler.JobStatePending); err != nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "failed to initialize job state")
		return
	}

	if err := s.bus.Publish(capsdk.SubjectSubmit, packet); err != nil {
		slog.Error("dlq retry publish failed", "error", err, "job_id", newJobID) // #nosec -- job id is generated and safe for logs.
		writeErrorJSON(w, http.StatusInternalServerError, "failed to retry dlq entry")
		return
	}

	if err := s.dlqStore.Delete(r.Context(), jobID); err != nil {
		slog.Error("dlq delete after retry failed", "job_id", jobID, "error", err) // #nosec -- job id is validated and used for diagnostics.
	}
	dlqRetryTopic, _ := s.jobStore.GetTopic(r.Context(), jobID)
	s.appendAuditEntryNamed(r.Context(), "retry", "dlq", jobID, dlqRetryTopic, policyActorID(r), policyRole(r), "retry dlq entry "+jobID)
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, map[string]string{"job_id": newJobID})
}

func mergeStringMap(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}
