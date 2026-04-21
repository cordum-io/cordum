package gateway

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestBreakGlass_GraceStateJobSubmitReturns503(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	enableTestAuth(s)
	setBreakGlassState(t, s, "grace")

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"prompt":"hello","topic":"job.test"}`)), &auth.AuthContext{
		Tenant:      "default",
		Role:        "admin",
		PrincipalID: "admin-1",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if len(bus.published) != 0 {
		t.Fatalf("expected no bus publish when break-glass blocks submit, got %d", len(bus.published))
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["state"] != string(licensing.BreakGlassStateGrace) {
		t.Fatalf("state = %#v, want %q", body["state"], licensing.BreakGlassStateGrace)
	}
}

func TestBreakGlass_GraceStateLicenseRotateSucceeds(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setBreakGlassState(t, s, "grace")
	installReloadableLicense(t, licensing.Claims{
		OrgID:     "org-breakglass",
		LicenseID: "lic-reload",
		Plan:      string(licensing.PlanEnterprise),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
	})
	sink := &testAuditSender{}
	s.auditExporter = sink
	logs, restoreLogger := captureBreakGlassLogger(t)
	defer restoreLogger()
	before := breakGlassDecisionCount(t, "allow", licensing.BreakGlassStateGrace)

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/license/reload", nil))
	rec := httptest.NewRecorder()

	s.handleReloadLicense(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["status"] != "reloaded" {
		t.Fatalf("status = %#v, want reloaded", body["status"])
	}
	if body["plan"] != string(licensing.PlanEnterprise) {
		t.Fatalf("plan = %#v, want %q", body["plan"], licensing.PlanEnterprise)
	}
	if sink.Len() != 1 {
		t.Fatalf("expected 1 break-glass audit event, got %d", sink.Len())
	}
	ev := sink.Get(0)
	if ev.EventType != audit.EventLicenseBreakglassActivated {
		t.Fatalf("event type = %q, want %q", ev.EventType, audit.EventLicenseBreakglassActivated)
	}
	if ev.Reason != string(licensing.BreakGlassStateGrace) {
		t.Fatalf("reason = %q, want %q", ev.Reason, licensing.BreakGlassStateGrace)
	}
	if got := breakGlassDecisionCount(t, "allow", licensing.BreakGlassStateGrace) - before; got != 1 {
		t.Fatalf("allow metric increment = %v, want 1", got)
	}
	logOutput := logs.String()
	for _, needle := range []string{
		`"principal":"admin"`,
		`"route":"/api/v1/license/reload"`,
		`"state":"grace"`,
		`"decision":"allow"`,
	} {
		if !strings.Contains(logOutput, needle) {
			t.Fatalf("expected log output to contain %s, got %s", needle, logOutput)
		}
	}
}

func TestBreakGlass_DegradedStateNonAllowlistRouteReturns503(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setBreakGlassState(t, s, "degraded")

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/license", nil))
	rec := httptest.NewRecorder()

	s.handleGetLicense(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["code"] != "license.degraded" {
		t.Fatalf("code = %#v, want license.degraded", body["code"])
	}
	if body["error_code"] != "license.degraded" {
		t.Fatalf("error_code = %#v, want license.degraded", body["error_code"])
	}
}

func TestBreakGlass_DegradedStateLoginSucceeds(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"emergency-key","role":"admin","principal_id":"breakglass","tenant":"default"}]`,
	})
	setBreakGlassState(t, s, "degraded")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"breakglass","password":"emergency-key"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestBreakGlass_InvalidStateRecoveryEmitsAuditEvent(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setBreakGlassState(t, s, "invalid")
	installReloadableLicense(t, licensing.Claims{
		OrgID:     "org-breakglass",
		LicenseID: "lic-recover",
		Plan:      string(licensing.PlanEnterprise),
		ExpiresAt: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
	})
	sink := &testAuditSender{}
	s.auditExporter = sink

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/license/reload", nil))
	rec := httptest.NewRecorder()

	s.handleReloadLicense(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if sink.Len() != 1 {
		t.Fatalf("expected 1 break-glass audit event, got %d", sink.Len())
	}
	ev := sink.Get(0)
	if ev.EventType != audit.EventLicenseBreakglassActivated {
		t.Fatalf("event type = %q, want %q", ev.EventType, audit.EventLicenseBreakglassActivated)
	}
	if ev.Reason != string(licensing.BreakGlassStateInvalid) {
		t.Fatalf("reason = %q, want %q", ev.Reason, licensing.BreakGlassStateInvalid)
	}
	if ev.Extra["state"] != string(licensing.BreakGlassStateInvalid) {
		t.Fatalf("extra.state = %q, want %q", ev.Extra["state"], licensing.BreakGlassStateInvalid)
	}
}

func TestBreakGlass_DegradedStateDeniedRouteRecordsDecisionMetric(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setBreakGlassState(t, s, "degraded")
	before := breakGlassDecisionCount(t, "deny", licensing.BreakGlassStateDegraded)

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/license", nil))
	rec := httptest.NewRecorder()

	s.handleGetLicense(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := breakGlassDecisionCount(t, "deny", licensing.BreakGlassStateDegraded) - before; got != 1 {
		t.Fatalf("deny metric increment = %v, want 1", got)
	}
}

func setBreakGlassState(t *testing.T, s *server, status string) {
	t.Helper()

	entitlements := licensing.DefaultEntitlements(licensing.PlanEnterprise)
	entitlements.RBAC = false

	resolver := s.entitlements
	if resolver == nil {
		resolver = licensing.NewEntitlementResolver()
		s.entitlements = resolver
	}
	resolver.ForceStateWithStatus(licensing.PlanEnterprise, entitlements, nil, status)
}

func installReloadableLicense(t *testing.T, claims licensing.Claims) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate license key: %v", err)
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	licenseJSON, err := json.Marshal(licensing.License{
		Payload:   claims,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload)),
	})
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}

	t.Setenv("CORDUM_LICENSE_FILE", "")
	t.Setenv("CORDUM_LICENSE_TOKEN", base64.StdEncoding.EncodeToString(licenseJSON))
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(pub))
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY_PATH", "")
}

func captureBreakGlassLogger(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	return &buf, func() {
		slog.SetDefault(previous)
	}
}

func breakGlassDecisionCount(t *testing.T, decision string, state licensing.BreakGlassState) float64 {
	t.Helper()
	return testutil.ToFloat64(licenseBreakGlassDecisionsTotal.WithLabelValues(decision, string(state)))
}
