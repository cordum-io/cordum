package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cordum/cordum-sdk-conformance-simulator/internal/engine"
)

// Jobs registers the submit/list/get/cancel routes.
//
//	POST /api/v1/jobs          submitJob
//	GET  /api/v1/jobs          listJobs
//	GET  /api/v1/jobs/{id}     getJob
//	POST /api/v1/jobs/{id}/cancel cancelJob
func Jobs(mux *http.ServeMux, eng *engine.Engine) {
	mux.HandleFunc("/api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		switch r.Method {
		case http.MethodGet:
			script := engine.ScriptForRequest(r)
			key := engine.RouteKey(r)
			if eng.ShouldFire(script, key) {
				switch script {
				case engine.ScriptRateLimitOnce:
					w.Header().Set("Retry-After", "1")
					engine.WriteError(w, http.StatusTooManyRequests, "rate_limited", "scripted rate limit", map[string]any{
						"retry_after_seconds": 1,
					})
					return
				case engine.ScriptServer500Once,
					engine.ScriptServer500OneShot,
					engine.ScriptServer500ThreeTimes:
					engine.WriteError(w, http.StatusInternalServerError, "internal_error", "scripted failure", nil)
					return
				}
			}
			items := eng.ListJobs()
			status := strings.TrimSpace(r.URL.Query().Get("status"))
			topic := strings.TrimSpace(r.URL.Query().Get("topic"))
			if status != "" || topic != "" {
				filtered := items[:0:0]
				for _, j := range items {
					if status != "" && j.Status != status {
						continue
					}
					if topic != "" && j.Topic != topic {
						continue
					}
					filtered = append(filtered, j)
				}
				items = filtered
			}
			engine.WriteJSON(w, http.StatusOK, map[string]any{
				"items":       items,
				"next_cursor": "",
			})
		case http.MethodPost:
			script := engine.ScriptForRequest(r)
			key := engine.RouteKey(r)
			if eng.ShouldFire(script, key) {
				switch script {
				case engine.ScriptServer500Once,
					engine.ScriptServer500OneShot,
					engine.ScriptServer500ThreeTimes:
					engine.WriteError(w, http.StatusInternalServerError, "internal_error", "scripted failure", nil)
					return
				}
			}
			// Idempotency-Key replay: when the SAME key repeats, the
			// simulator must replay a success response (as if the
			// original had succeeded server-side). Fixtures pair this
			// with the server-500-three-times script to prove an SDK
			// carried the header across retries.
			if eng.SeenIdempotencyKey(r.Header.Get(engine.IdempotencyHeader), r.URL.Path) {
				engine.WriteJSON(w, http.StatusAccepted, map[string]any{
					"job_id":   "idempotent-replay",
					"trace_id": "idempotent-replay-trace",
				})
				return
			}
			var req struct {
				Topic    string            `json:"topic"`
				Prompt   string            `json:"prompt"`
				Priority string            `json:"priority"`
				Labels   map[string]string `json:"labels"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				engine.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not JSON", nil)
				return
			}
			if strings.TrimSpace(req.Topic) == "" {
				engine.WriteError(w, http.StatusBadRequest, "validation_failed", "topic is required", map[string]any{
					"field_errors": map[string][]string{
						"topic": {"required"},
					},
				})
				return
			}
			id := eng.NextID("job")
			trace := eng.NextID("trace")
			job := &engine.Job{
				ID:        id,
				JobID:     id,
				Topic:     req.Topic,
				Prompt:    req.Prompt,
				Priority:  req.Priority,
				Labels:    req.Labels,
				Status:    "succeeded",
				TraceID:   trace,
				UpdatedAt: eng.Timestamp(1),
			}
			eng.Mu().Lock()
			eng.Jobs[id] = job
			eng.Mu().Unlock()
			engine.WriteJSON(w, http.StatusAccepted, map[string]any{
				"job_id":   id,
				"trace_id": trace,
			})
		default:
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
		}
	})
	mux.HandleFunc("/api/v1/jobs/", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/")
		if rest == "" {
			engine.WriteError(w, http.StatusBadRequest, "missing_id", "job id required", nil)
			return
		}
		// POST /jobs/{id}/cancel
		if parts := strings.SplitN(rest, "/", 2); len(parts) == 2 && parts[1] == "cancel" {
			if r.Method != http.MethodPost {
				engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
				return
			}
			id := parts[0]
			eng.Mu().Lock()
			job, ok := eng.Jobs[id]
			if ok {
				job.Status = "cancelled"
				job.UpdatedAt = eng.Timestamp(2)
			}
			eng.Mu().Unlock()
			if !ok {
				engine.WriteError(w, http.StatusNotFound, "not_found", "job not found: "+id, map[string]any{"resource": "job"})
				return
			}
			// Cancel returns 204 No Content per the fixture contract —
			// the follow-up GET is what observes the new `cancelled`
			// state.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// GET /jobs/{id}
		id := rest
		script := engine.ScriptForRequest(r)
		key := engine.RouteKey(r)
		if eng.ShouldFire(script, key) {
			switch script {
			case engine.ScriptRateLimitOnce:
				w.Header().Set("Retry-After", "1")
				engine.WriteError(w, http.StatusTooManyRequests, "rate_limited", "scripted rate limit", map[string]any{
					"retry_after_seconds": 1,
				})
				return
			case engine.ScriptServer500Once,
				engine.ScriptServer500OneShot,
				engine.ScriptServer500ThreeTimes:
				engine.WriteError(w, http.StatusInternalServerError, "internal_error", "scripted failure", nil)
				return
			}
		}
		if r.Method != http.MethodGet {
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
			return
		}
		eng.Mu().Lock()
		job, ok := eng.Jobs[id]
		eng.Mu().Unlock()
		if !ok {
			engine.WriteError(w, http.StatusNotFound, "not_found", "job not found: "+id, map[string]any{"resource": "job"})
			return
		}
		engine.WriteJSON(w, http.StatusOK, job)
	})
}
