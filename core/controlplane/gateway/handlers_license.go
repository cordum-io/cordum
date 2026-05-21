package gateway

import (
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
)

func (s *server) handleGetLicense(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermLicenseRead, "admin") {
		return
	}

	info := s.currentLicenseInfo()
	resp := map[string]any{
		"plan":         string(s.resolvedPlan()),
		"entitlements": s.currentEntitlements(),
		"rights":       s.currentLicenseRights(),
	}
	if info != nil {
		resp["license"] = info
		resp["expiry_status"] = strings.TrimSpace(info.Status)
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handleReloadLicense(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	if !s.requireLicensePermission(w, r, licensing.BreakGlassPermissionLicenseRotate) {
		return
	}

	resolver := s.entitlementResolver()
	if resolver == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "license resolver unavailable")
		return
	}

	plan, _ := resolver.Reload()
	info := s.currentLicenseInfo()
	resp := map[string]any{
		"status":  "reloaded",
		"plan":    string(plan),
		"license": info,
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

func (s *server) handleGetLicenseUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermLicenseRead, "admin") {
		return
	}

	tenantID, err := s.usageTenant(r)
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	registeredWorkers, connectedWorkers, workerCount, err := s.effectiveWorkerCount(r.Context(), "")
	if err != nil {
		writeInternalError(w, r, "worker count", err)
		return
	}
	activeJobs, err := s.activeJobCount(r.Context(), tenantID)
	if err != nil {
		writeInternalError(w, r, "active job count", err)
		return
	}
	activeWorkflows, err := s.activeWorkflowCount(r.Context(), tenantID)
	if err != nil {
		writeInternalError(w, r, "active workflow count", err)
		return
	}
	schemaCount, err := s.schemaCount(r.Context())
	if err != nil {
		writeInternalError(w, r, "schema count", err)
		return
	}
	policyBundleCount, err := s.customPolicyBundleCount(r.Context())
	if err != nil {
		writeInternalError(w, r, "policy bundle count", err)
		return
	}

	entitlements := s.currentEntitlements()
	resp := map[string]any{
		"tenant_id": tenantID,
		"plan":      string(s.resolvedPlan()),
		"license":   s.currentLicenseInfo(),
		"usage": map[string]any{
			"workers": map[string]any{
				"current":    workerCount,
				"allowed":    entitlements.MaxWorkers,
				"registered": registeredWorkers,
				"connected":  connectedWorkers,
			},
			"concurrent_jobs": map[string]any{
				"current": activeJobs,
				"allowed": entitlements.MaxConcurrentJobs,
			},
			"active_workflows": map[string]any{
				"current": activeWorkflows,
				"allowed": entitlements.MaxActiveWorkflows,
			},
			"workflow_steps": map[string]any{
				"allowed": entitlements.MaxWorkflowSteps,
			},
			"schemas": map[string]any{
				"current": schemaCount,
				"allowed": entitlements.MaxSchemaCount,
			},
			"policy_bundles": map[string]any{
				"current": policyBundleCount,
				"allowed": entitlements.MaxPolicyBundles,
			},
			"requests_per_second": map[string]any{
				"allowed": entitlements.RequestsPerSecond,
			},
			"prompt_chars": map[string]any{
				"allowed": s.promptCharLimit(),
			},
			"body_bytes": map[string]any{
				"allowed": s.jsonBodyBytesLimit(),
			},
			"approval_mode": map[string]any{
				"allowed": s.approvalModeLimit(),
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}
