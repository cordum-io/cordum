package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/cordum/cordum-sdk-conformance-simulator/internal/engine"
)

// Policies registers the policy-bundle + policy-audit surfaces.
//
//	GET  /api/v1/policy/bundles          listPolicyBundles
//	GET  /api/v1/policy/bundles/{id}     getPolicyBundle
//	POST /api/v1/policy/publish          publishPolicy
//	POST /api/v1/policy/rollback         rollbackPolicy
//	GET  /api/v1/policy/audit            getPolicyAudit (paginated)
func Policies(mux *http.ServeMux, eng *engine.Engine) {
	mux.HandleFunc("/api/v1/policy/bundles", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		if r.Method != http.MethodGet {
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
			return
		}
		eng.Mu().Lock()
		items := make([]*engine.PolicyBundle, 0, len(eng.PolicyBundles))
		for _, b := range eng.PolicyBundles {
			items = append(items, b)
		}
		eng.Mu().Unlock()
		engine.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	mux.HandleFunc("/api/v1/policy/bundles/", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		if r.Method != http.MethodGet {
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/policy/bundles/")
		eng.Mu().Lock()
		bundle, ok := eng.PolicyBundles[id]
		eng.Mu().Unlock()
		if !ok {
			engine.WriteError(w, http.StatusNotFound, "not_found", "policy bundle not found: "+id, map[string]any{"resource": "policy_bundle"})
			return
		}
		engine.WriteJSON(w, http.StatusOK, bundle)
	})
	mux.HandleFunc("/api/v1/policy/publish", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		if r.Method != http.MethodPost {
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
			return
		}
		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == "" {
			req.ID = "default"
		}
		if req.Name == "" {
			req.Name = req.ID
		}
		bundle := eng.PublishBundle(req.ID, req.Name)
		engine.WriteJSON(w, http.StatusOK, bundle)
	})
	mux.HandleFunc("/api/v1/policy/rollback", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		if r.Method != http.MethodPost {
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
			return
		}
		var req struct {
			ID             string `json:"id"`
			TargetVersion  int    `json:"target_version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ID == "" {
			req.ID = "default"
		}
		bundle, ok := eng.RollbackBundle(req.ID, req.TargetVersion)
		if !ok {
			engine.WriteError(w, http.StatusNotFound, "not_found", "policy bundle not found: "+req.ID, map[string]any{"resource": "policy_bundle"})
			return
		}
		engine.WriteJSON(w, http.StatusOK, bundle)
	})
	mux.HandleFunc("/api/v1/policy/audit", func(w http.ResponseWriter, r *http.Request) {
		ac := eng.AuthFromRequest(r)
		if !ac.Authenticated() {
			engine.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		if r.Method != http.MethodGet {
			engine.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", r.Method+" not allowed", nil)
			return
		}
		cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
		limitStr := strings.TrimSpace(r.URL.Query().Get("limit"))
		limit := 10
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
		items, next := eng.PageAudit(cursor, limit)
		engine.WriteJSON(w, http.StatusOK, map[string]any{
			"items":       items,
			"next_cursor": next,
		})
	})
}
