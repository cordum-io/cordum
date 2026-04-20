package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
)

// handleAuditExportHealth returns the current audit export backend status.
// GET /api/v1/audit/export/health
func (s *server) handleAuditExportHealth(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAuditRead, "admin") {
		return
	}

	entitlements := s.currentEntitlements()
	if !entitlements.FeatureEnabled("siem_export") && !entitlements.FeatureEnabled("audit_export") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(licensing.TierLimitHTTPError{
			Code:       "tier_limit_exceeded",
			Message:    "SIEM audit export requires an Enterprise license",
			Limit:      "siem_export",
			UpgradeURL: licensing.DefaultUpgradeURL,
		})
		return
	}

	exportType := strings.ToLower(strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_TYPE")))
	if exportType == "" {
		exportType = "none"
	}

	status := "disabled"
	if s.auditExporter != nil && exportType != "none" {
		status = "active"
	}

	writeJSON(w, map[string]any{
		"backend":  exportType,
		"status":   status,
		"entitled": true,
	})
}

// handleAuditExportTest sends a test event to the configured export backend.
// POST /api/v1/audit/export/test
func (s *server) handleAuditExportTest(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAuditRead, "admin") {
		return
	}

	entitlements := s.currentEntitlements()
	if !entitlements.FeatureEnabled("siem_export") && !entitlements.FeatureEnabled("audit_export") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(licensing.TierLimitHTTPError{
			Code:       "tier_limit_exceeded",
			Message:    "SIEM audit export requires an Enterprise license",
			Limit:      "siem_export",
			UpgradeURL: licensing.DefaultUpgradeURL,
		})
		return
	}

	if s.auditExporter == nil {
		writeErrorJSON(w, http.StatusBadRequest, "no audit export backend configured")
		return
	}

	testEvent := audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: "system.test",
		Severity:  audit.SeverityInfo,
		TenantID:  s.tenant,
		Action:    "audit_export_test",
		Decision:  "allow",
		Reason:    "manual test from dashboard",
	}

	// Send the test event through the configured exporter
	s.auditExporter.Send(testEvent)

	writeJSON(w, map[string]any{
		"success": true,
		"message": fmt.Sprintf("test event sent to %s backend", strings.ToLower(strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_TYPE")))),
	})
}

// handleAuditExportConfig returns the current export configuration (non-sensitive).
// GET /api/v1/audit/export/config
func (s *server) handleAuditExportConfig(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAuditRead, "admin") {
		return
	}

	entitlements := s.currentEntitlements()
	entitled := entitlements.FeatureEnabled("siem_export") || entitlements.FeatureEnabled("audit_export")

	exportType := strings.ToLower(strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_TYPE")))
	if exportType == "" {
		exportType = "none"
	}

	config := map[string]any{
		"type":     exportType,
		"entitled": entitled,
	}

	// Return non-sensitive config details per backend type
	switch exportType {
	case "webhook":
		url := strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_WEBHOOK_URL"))
		hasSecret := strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_WEBHOOK_SECRET")) != ""
		config["webhook_url"] = url
		config["webhook_hmac_enabled"] = hasSecret
	case "syslog":
		config["syslog_addr"] = strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_SYSLOG_ADDR"))
	case "datadog":
		config["dd_site"] = strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_DD_SITE"))
		config["dd_tags"] = strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_DD_TAGS"))
		config["dd_api_key_set"] = strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_DD_API_KEY")) != ""
	case "cloudwatch":
		config["cw_log_group"] = strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_CW_LOG_GROUP"))
		config["cw_log_stream"] = strings.TrimSpace(os.Getenv("CORDUM_AUDIT_EXPORT_CW_LOG_STREAM"))
		config["cw_region"] = regionFromCtx(r.Context())
	}

	// Retention info
	retentionTTL := audit.RetentionTTLFromEntitlements(entitlements)
	if retentionTTL == 0 {
		config["retention"] = "unlimited"
	} else {
		config["retention_days"] = int(retentionTTL.Hours() / 24)
	}

	writeJSON(w, config)
}

func regionFromCtx(_ context.Context) string {
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION"))
	}
	if region == "" {
		return "not configured"
	}
	return region
}
