package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/calibration"
	"github.com/redis/go-redis/v9"
)

// edgeCalibrationReportKeyPrefix prefixes the stored report JSON:
// edge:calibration_report:<tenant>:<id>, kept with no TTL. The id has a
// fixed shape with no ':', so a tenant id cannot reach another tenant's key.
const edgeCalibrationReportKeyPrefix = "edge:calibration_report:"

var edgeCalibrationReportIDPattern = regexp.MustCompile(`^cal-[0-9a-f]{32}$`)

type edgeCalibrationReportRequest struct {
	Since  time.Time         `json:"since"`
	Until  time.Time         `json:"until"`
	Repos  []string          `json:"repos,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

func edgeCalibrationReportKey(tenantID, id string) string {
	return edgeCalibrationReportKeyPrefix + tenantID + ":" + id
}

// handleCreateEdgeCalibrationReport builds a calibration report over the
// caller tenant's observe-mode Edge sessions, stores it under its
// content-derived id and returns it with 201.
func (s *server) handleCreateEdgeCalibrationReport(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermPolicyWrite, "admin") {
		return
	}
	store := s.edgeStoreOrUnavailable(w, r)
	if store == nil {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	var body edgeCalibrationReportRequest
	if err := decodeJSONBody(w, r, &body); err != nil {
		writeEdgeJSONDecodeError(w, r, err, "invalid calibration report request")
		return
	}
	client := s.redisClient()
	if client == nil || s.configSvc == nil {
		writeEdgeCalibrationUnavailable(w, r)
		return
	}
	ruleIDs, err := s.activePolicyRuleIDs(r.Context())
	if err != nil {
		writeEdgeInternalError(w, r, "load policy rules for calibration report", err)
		return
	}
	shadow, shadowTruncated, err := edgeCalibrationShadowVerdicts(r.Context(), client, tenantID, body.Since, body.Until)
	if err != nil {
		writeEdgeInternalError(w, r, "read shadow results for calibration report", err)
		return
	}
	report, err := calibration.Build(r.Context(), store, calibration.Request{
		TenantID: tenantID, Since: body.Since, Until: body.Until,
		Repos: body.Repos, Labels: body.Labels, RuleIDs: ruleIDs,
		Shadow: shadow, ShadowTruncated: shadowTruncated,
	})
	if errors.Is(err, edgecore.ErrValidation) {
		writeEdgeError(w, r, http.StatusBadRequest, edgeErrCodeInvalidRequest, err.Error(), nil)
		return
	}
	if err != nil {
		writeEdgeInternalError(w, r, "build calibration report", err)
		return
	}
	payload, err := json.Marshal(report)
	if err != nil {
		writeEdgeInternalError(w, r, "marshal calibration report", err)
		return
	}
	if err := client.Set(r.Context(), edgeCalibrationReportKey(tenantID, report.ID), payload, 0).Err(); err != nil {
		writeEdgeInternalError(w, r, "store calibration report", err)
		return
	}
	writeEdgeCalibrationReport(w, http.StatusCreated, payload)
}

// handleGetEdgeCalibrationReport returns a stored report of the caller's
// tenant; any other id is 404. Without RBAC the legacy roles are the
// default holders of policy.read.
func (s *server) handleGetEdgeCalibrationReport(w http.ResponseWriter, r *http.Request) {
	if !s.requireEdgePermissionOrRole(w, r, auth.PermPolicyRead, "admin", "operator", "viewer") {
		return
	}
	tenantID, ok := s.edgeTenantFromRequest(w, r, "")
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !edgeCalibrationReportIDPattern.MatchString(id) {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "calibration report not found", nil)
		return
	}
	client := s.redisClient()
	if client == nil {
		writeEdgeCalibrationUnavailable(w, r)
		return
	}
	payload, err := client.Get(r.Context(), edgeCalibrationReportKey(tenantID, id)).Bytes()
	if errors.Is(err, redis.Nil) {
		writeEdgeError(w, r, http.StatusNotFound, edgeErrCodeNotFound, "calibration report not found", nil)
		return
	}
	if err != nil {
		writeEdgeInternalError(w, r, "load calibration report", err)
		return
	}
	writeEdgeCalibrationReport(w, http.StatusOK, payload)
}

// activePolicyRuleIDs returns the rule ids of the merged active policy
// bundles; no bundles means no rules.
func (s *server) activePolicyRuleIDs(ctx context.Context) ([]string, error) {
	bundles, _, err := s.loadPolicyBundles(ctx)
	if err != nil {
		return nil, err
	}
	policy, _, err := policybundles.BuildPolicyFromBundles(bundles)
	if err != nil || policy == nil {
		return nil, err
	}
	ids := make([]string, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		if rule.ID != "" {
			ids = append(ids, rule.ID)
		}
	}
	return ids, nil
}

// edgeCalibrationShadowVerdicts reads the tenant's shadow_eval events of
// every bundle in the window. They carry no session or repository, so the
// report counts them for the whole tenant.
func edgeCalibrationShadowVerdicts(
	ctx context.Context, client redis.UniversalClient, tenantID string, since, until time.Time,
) ([]calibration.ShadowVerdict, bool, error) {
	events, truncated, err := scanShadowEvents(ctx, client, shadowStreamKey(tenantID), "", since.UnixMilli(), until.UnixMilli(), 0)
	if err != nil {
		return nil, false, err
	}
	verdicts := make([]calibration.ShadowVerdict, 0, len(events))
	for _, ev := range events {
		verdicts = append(verdicts, calibration.ShadowVerdict{
			BundleID: ev.Extra["bundle_id"], RuleID: ev.Extra["shadow_rule_id"], Verdict: ev.Extra["shadow_verdict"], At: ev.Timestamp,
		})
	}
	return verdicts, truncated, nil
}

func writeEdgeCalibrationUnavailable(w http.ResponseWriter, r *http.Request) {
	slog.Error("calibration report store unavailable", "method", r.Method, "path", r.URL.Path)
	writeEdgeError(w, r, http.StatusServiceUnavailable, edgeErrCodeStoreUnavailable, "calibration report store unavailable", nil)
}

// writeEdgeCalibrationReport writes the stored bytes as-is, so the 201 body
// of a POST equals what a later GET returns.
func writeEdgeCalibrationReport(w http.ResponseWriter, status int, payload []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(payload); err != nil {
		slog.Warn("write calibration report response failed", "error", err)
	}
}
