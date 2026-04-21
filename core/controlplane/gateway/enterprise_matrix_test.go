package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
)

func TestEnterpriseEntitlementMatrixProjectsLicenseSurface(t *testing.T) {
	s, _, _ := newTestGateway(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.SSO = true
		entitlements.SAML = false
		entitlements.SCIM = true
		entitlements.RBAC = true
		entitlements.AuditExport = true
		entitlements.SIEMExport = false
		entitlements.LegalHold = true
		entitlements.VelocityRules = true
		entitlements.BreakGlassAdmin = true
		entitlements.AgentIdentity = true
	})

	req := adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/license", nil))
	rec := httptest.NewRecorder()
	s.handleGetLicense(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get license: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Entitlements struct {
			SSO             bool `json:"sso"`
			SAML            bool `json:"saml"`
			SCIM            bool `json:"scim"`
			RBAC            bool `json:"rbac"`
			AuditExport     bool `json:"audit_export"`
			SIEMExport      bool `json:"siem_export"`
			LegalHold       bool `json:"legal_hold"`
			VelocityRules   bool `json:"velocity_rules"`
			BreakGlassAdmin bool `json:"break_glass_admin"`
			AgentIdentity   bool `json:"agent_identity"`
		} `json:"entitlements"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode license response: %v", err)
	}

	if !payload.Entitlements.SSO {
		t.Fatal("expected sso entitlement in /api/v1/license response")
	}
	if payload.Entitlements.SAML {
		t.Fatal("expected saml entitlement to remain false in /api/v1/license response")
	}
	if !payload.Entitlements.SCIM {
		t.Fatal("expected scim entitlement in /api/v1/license response")
	}
	if !payload.Entitlements.RBAC {
		t.Fatal("expected rbac entitlement in /api/v1/license response")
	}
	if !payload.Entitlements.AuditExport {
		t.Fatal("expected audit_export entitlement in /api/v1/license response")
	}
	if payload.Entitlements.SIEMExport {
		t.Fatal("expected siem_export entitlement to remain false in /api/v1/license response")
	}
	if !payload.Entitlements.LegalHold {
		t.Fatal("expected legal_hold entitlement in /api/v1/license response")
	}
	if !payload.Entitlements.VelocityRules {
		t.Fatal("expected velocity_rules entitlement in /api/v1/license response")
	}
	if !payload.Entitlements.BreakGlassAdmin {
		t.Fatal("expected break_glass_admin entitlement in /api/v1/license response")
	}
	if !payload.Entitlements.AgentIdentity {
		t.Fatal("expected agent_identity entitlement in /api/v1/license response")
	}
}

func TestEnterpriseEntitlementMatrixRepresentativeGatewayFeatureGates(t *testing.T) {
	rangeQuery := func() string {
		now := time.Now().UTC()
		return "?format=json&from=" + now.Add(-time.Hour).Format(time.RFC3339) + "&to=" + now.Format(time.RFC3339)
	}

	type testCase struct {
		name     string
		mutate   func(*licensing.Entitlements)
		prepare  func(*server)
		request  func() *http.Request
		handler  func(*server, http.ResponseWriter, *http.Request)
		wantCode int
		wantText string
	}

	cases := []testCase{
		{
			name: "custom roles require rbac entitlement",
			mutate: func(entitlements *licensing.Entitlements) {
				entitlements.RBAC = false
			},
			request: func() *http.Request {
				req := adminCtx(httptest.NewRequest(
					http.MethodPut,
					"/api/v1/auth/roles/platform_editor",
					strings.NewReader(`{"description":"Platform editor","permissions":["config.read"]}`),
				))
				req.Header.Set("Content-Type", "application/json")
				req.SetPathValue("name", "platform_editor")
				return req
			},
			handler:  (*server).handlePutRole,
			wantCode: http.StatusForbidden,
			wantText: `"limit":"rbac"`,
		},
		{
			name: "audit export requires export entitlement",
			mutate: func(entitlements *licensing.Entitlements) {
				entitlements.AuditExport = false
				entitlements.SIEMExport = false
			},
			request: func() *http.Request {
				return adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/audit/export"+rangeQuery(), nil))
			},
			handler:  (*server).handleAuditExport,
			wantCode: http.StatusForbidden,
			wantText: `"limit":"siem_export"`,
		},
		{
			name: "legal hold requires legal_hold entitlement",
			mutate: func(entitlements *licensing.Entitlements) {
				entitlements.LegalHold = false
			},
			prepare: func(s *server) {
				if s.jobStore != nil {
					if client, ok := s.jobStore.Client().(*redis.Client); ok {
						s.legalHoldStore = audit.NewLegalHoldStoreFromClient(client)
					}
				}
			},
			request: func() *http.Request {
				req := adminCtx(httptest.NewRequest(
					http.MethodPost,
					"/api/v1/audit/legal-hold",
					strings.NewReader(`{"tenant_id":"default","reason":"retain"}`),
				))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
			handler:  (*server).handleCreateLegalHold,
			wantCode: http.StatusForbidden,
			wantText: `"limit":"legal_hold"`,
		},
		{
			name: "velocity rules require velocity_rules entitlement",
			mutate: func(entitlements *licensing.Entitlements) {
				entitlements.VelocityRules = false
			},
			request: func() *http.Request {
				return adminCtx(httptest.NewRequest(http.MethodGet, "/api/v1/policy/velocity-rules", nil))
			},
			handler:  (*server).handleVelocityRules,
			wantCode: http.StatusForbidden,
			wantText: `"limit":"velocity_rules"`,
		},
		{
			name: "agent identities require agent_identity entitlement",
			mutate: func(entitlements *licensing.Entitlements) {
				entitlements.AgentIdentity = false
			},
			request: func() *http.Request {
				return withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil), &auth.AuthContext{
					Tenant:      "default",
					Role:        "admin",
					PrincipalID: "admin-user",
				})
			},
			handler:  (*server).handleListAgents,
			wantCode: http.StatusForbidden,
			wantText: `"limit":"agent_identity"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			setTestEntitlements(t, s, licensing.PlanEnterprise, tc.mutate)
			if tc.prepare != nil {
				tc.prepare(s)
			}

			req := tc.request()
			rec := httptest.NewRecorder()
			tc.handler(s, rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if !bytes.Contains(rec.Body.Bytes(), []byte(`"code":"tier_limit_exceeded"`)) {
				t.Fatalf("expected tier_limit_exceeded response, got %s", rec.Body.String())
			}
			if !bytes.Contains(rec.Body.Bytes(), []byte(tc.wantText)) {
				t.Fatalf("expected %s in response, got %s", tc.wantText, rec.Body.String())
			}
		})
	}
}
