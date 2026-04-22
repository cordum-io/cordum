package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/cordum/cordum-sdk-conformance-simulator/internal/engine"
)

// Workflows registers the workflow CRUD + run surface.
//
//	GET    /api/v1/workflows
//	POST   /api/v1/workflows
//	GET    /api/v1/workflows/{id}
//	DELETE /api/v1/workflows/{id}
//	POST   /api/v1/workflows/{id}/runs     startWorkflowRun
func Workflows(mux *http.ServeMux, eng *engine.Engine) {
	mux.HandleFunc("/api/v1/workflows", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		switch r.Method {
		case http.MethodGet:
			engine.WriteJSON(w, http.StatusOK, map[string]any{
				"items":       eng.ListWorkflows(),
				"next_cursor": "",
			})
		case http.MethodPost:
			var req struct {
				Name  string `json:"name"`
				Steps any    `json:"steps"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				engine.WriteError(w, http.StatusBadRequest, "invalid_json", "request body is not JSON", nil)
				return
			}
			if strings.TrimSpace(req.Name) == "" {
				engine.WriteError(w, http.StatusBadRequest, "validation_failed", "name is required", map[string]any{
					"field_errors": map[string][]string{"name": {"required"}},
				})
				return
			}
			id := eng.NextID("wf")
			wf := &engine.Workflow{
				ID:        id,
				Name:      req.Name,
				Steps:     req.Steps,
				CreatedAt: eng.Timestamp(0),
			}
			eng.Mu().Lock()
			eng.Workflows[id] = wf
			eng.Mu().Unlock()
			engine.WriteJSON(w, http.StatusCreated, wf)
		default:
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
		}
	})
	mux.HandleFunc("/api/v1/workflows/", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/workflows/")
		if parts := strings.SplitN(rest, "/", 2); len(parts) == 2 && parts[1] == "runs" {
			if r.Method != http.MethodPost {
				engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
				return
			}
			id := parts[0]
			eng.Mu().Lock()
			_, ok := eng.Workflows[id]
			eng.Mu().Unlock()
			if !ok {
				engine.WriteError(w, http.StatusNotFound, "not_found", "workflow not found: "+id, map[string]any{"resource": "workflow"})
				return
			}
			runID := eng.NextID("run")
			run := &engine.WorkflowRun{
				RunID:      runID,
				WorkflowID: id,
				Status:     "running",
				StartedAt:  eng.Timestamp(1),
			}
			eng.Mu().Lock()
			eng.WorkflowRuns[runID] = run
			eng.Mu().Unlock()
			engine.WriteJSON(w, http.StatusAccepted, run)
			return
		}
		id := rest
		eng.Mu().Lock()
		wf, ok := eng.Workflows[id]
		eng.Mu().Unlock()
		if !ok {
			engine.WriteError(w, http.StatusNotFound, "not_found", "workflow not found: "+id, map[string]any{"resource": "workflow"})
			return
		}
		switch r.Method {
		case http.MethodGet:
			engine.WriteJSON(w, http.StatusOK, wf)
		case http.MethodDelete:
			eng.Mu().Lock()
			delete(eng.Workflows, id)
			eng.Mu().Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
		}
	})
}
