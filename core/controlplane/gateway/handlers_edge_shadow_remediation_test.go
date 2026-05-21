// EDGE-142 — Tests for the Gateway shadow remediation handler.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge/shadow"
)

// createSeedFinding writes one ShadowAgentFinding into the gateway's
// store so remediation tests have something to fetch.
func createSeedFinding(t *testing.T, s *server, tenant string) *shadow.ShadowAgentFinding {
	t.Helper()
	f, err := s.shadowFindingStore.CreateFinding(context.Background(), shadow.CreateFindingRequest{
		TenantID:         tenant,
		OwnerPrincipalID: "owner-alice",
		PrincipalID:      "scanner-svc",
		AgentProduct:     "claude-code",
		Risk:             shadow.FindingRiskHigh,
		EvidenceType:     shadow.EvidenceConfigFile,
		EvidenceSummary:  "1 mcp servers configured (transports: stdio)",
		RedactedPath:     "~/.claude/settings.json",
		DetectedAt:       time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
		SignalSet:        []string{"unmanaged_claude_settings"},
	})
	if err != nil {
		t.Fatalf("seed CreateFinding: %v", err)
	}
	return f
}

func TestShadowRemediation_HappyPath(t *testing.T) {
	s := newShadowGateway(t)
	seeded := createSeedFinding(t, s, "tenant-alpha")

	rec := postShadow(t, s, "tenant-alpha",
		"/api/v1/edge/shadow-agents/"+seeded.FindingID+"/remediation",
		map[string]any{"audience": "dev"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp shadowRemediationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rec.Body.String())
	}
	if resp.FindingID != seeded.FindingID {
		t.Errorf("FindingID: want %q, got %q", seeded.FindingID, resp.FindingID)
	}
	if resp.TenantID != "tenant-alpha" {
		t.Errorf("TenantID: want tenant-alpha, got %q", resp.TenantID)
	}
	if resp.Remediation == nil {
		t.Fatal("remediation missing")
	}
	if resp.Remediation.ActionKind != shadow.RemediationUseCordumctlEdgeClaude {
		t.Errorf("ActionKind: want %q, got %q", shadow.RemediationUseCordumctlEdgeClaude, resp.Remediation.ActionKind)
	}
	if resp.Remediation.Audience != shadow.RemediationAudienceDev {
		t.Errorf("Audience: want dev, got %q", resp.Remediation.Audience)
	}
	if !resp.Remediation.AdvisoryOnly {
		t.Error("AdvisoryOnly must be true")
	}
}

func TestShadowRemediation_EmptyBodyDefaultsToBoth(t *testing.T) {
	s := newShadowGateway(t)
	seeded := createSeedFinding(t, s, "tenant-alpha")

	rec := postShadowNoBody(t, s, "tenant-alpha",
		"/api/v1/edge/shadow-agents/"+seeded.FindingID+"/remediation")
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp shadowRemediationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Remediation == nil || resp.Remediation.Audience != shadow.RemediationAudienceBoth {
		t.Errorf("empty body must default audience=both, got %+v", resp.Remediation)
	}
}

func TestShadowRemediation_InvalidAudience(t *testing.T) {
	s := newShadowGateway(t)
	seeded := createSeedFinding(t, s, "tenant-alpha")

	rec := postShadow(t, s, "tenant-alpha",
		"/api/v1/edge/shadow-agents/"+seeded.FindingID+"/remediation",
		map[string]any{"audience": "bogus"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestShadowRemediation_TenantIsolation(t *testing.T) {
	s := newShadowGateway(t)
	seeded := createSeedFinding(t, s, "tenant-alpha")

	rec := postShadow(t, s, "tenant-bravo",
		"/api/v1/edge/shadow-agents/"+seeded.FindingID+"/remediation",
		map[string]any{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404 (cross-tenant becomes not-found), got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestShadowRemediation_MissingFinding(t *testing.T) {
	s := newShadowGateway(t)
	rec := postShadow(t, s, "tenant-alpha",
		"/api/v1/edge/shadow-agents/edge_shadow_ghost/remediation",
		map[string]any{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestShadowRemediation_StoreUnavailable(t *testing.T) {
	s := newShadowGateway(t)
	s.shadowFindingStore = nil
	rec := postShadow(t, s, "tenant-alpha",
		"/api/v1/edge/shadow-agents/edge_shadow_any/remediation",
		map[string]any{})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: want 503, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestShadowRemediation_NoRawEvidenceLeakage(t *testing.T) {
	s := newShadowGateway(t)
	// Seed a finding whose evidence summary contains a secret marker.
	leakingSummary := "leaked cordum_fake_sk-ant-realsecret0123456789 in summary"
	if _, err := s.shadowFindingStore.CreateFinding(context.Background(), shadow.CreateFindingRequest{
		TenantID:         "tenant-leak",
		OwnerPrincipalID: "owner",
		PrincipalID:      "scanner",
		AgentProduct:     "claude-code",
		Risk:             shadow.FindingRiskMedium,
		EvidenceType:     shadow.EvidenceConfigFile,
		EvidenceSummary:  leakingSummary,
		RedactedPath:     "~/.claude/settings.json",
		DetectedAt:       time.Now().UTC(),
		SignalSet:        []string{"unmanaged_claude_settings"},
	}); err != nil {
		t.Fatalf("seed CreateFinding: %v", err)
	}

	// Find the seeded record so we have the ID.
	page, err := s.shadowFindingStore.ListFindings(context.Background(), shadow.ListFindingsQuery{TenantID: "tenant-leak"})
	if err != nil || len(page.Findings) == 0 {
		t.Fatalf("ListFindings: %v page=%+v", err, page)
	}
	id := page.Findings[0].FindingID

	rec := postShadow(t, s, "tenant-leak",
		"/api/v1/edge/shadow-agents/"+id+"/remediation", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-ant-realsecret") {
		t.Errorf("response leaked secret marker; body=%s", rec.Body.String())
	}
}

func TestShadowRemediation_OmitCommandsStripsCommandText(t *testing.T) {
	s := newShadowGateway(t)
	seeded := createSeedFinding(t, s, "tenant-alpha")

	rec := postShadow(t, s, "tenant-alpha",
		"/api/v1/edge/shadow-agents/"+seeded.FindingID+"/remediation",
		map[string]any{"audience": "dev", "omit_commands": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp shadowRemediationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, step := range resp.Remediation.Steps {
		if step.Command != "" {
			t.Errorf("step %q must have empty command when omit_commands=true; got %q", step.ID, step.Command)
		}
	}
}

func TestShadowRemediation_NoTenantHeader(t *testing.T) {
	s := newShadowGateway(t)
	seeded := createSeedFinding(t, s, "tenant-alpha")

	rec := postShadowNoTenant(t, s,
		"/api/v1/edge/shadow-agents/"+seeded.FindingID+"/remediation",
		map[string]any{})
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusForbidden {
		t.Fatalf("missing X-Tenant-ID must be rejected; got %d body=%s", rec.Code, rec.Body.String())
	}
}

// postShadowNoBody issues a POST with Content-Length: 0 so the handler
// exercises the "body is optional" branch.
func postShadowNoBody(t *testing.T, s *server, tenant, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := shadowAdminCtx(httptest.NewRequest(http.MethodPost, path, bytes.NewReader(nil)))
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	mux.ServeHTTP(rec, req)
	return rec
}

// postShadowNoTenant omits the X-Tenant-ID header entirely.
func postShadowNoTenant(t *testing.T, s *server, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := shadowAdminCtx(httptest.NewRequest(http.MethodPost, path, reader))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	mux.ServeHTTP(rec, req)
	return rec
}
