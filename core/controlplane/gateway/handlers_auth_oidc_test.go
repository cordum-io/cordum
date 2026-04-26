package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
)

type fakeOIDCGroupMappingAuth struct {
	role string
	cfg  auth.AuthConfig
}

func (f *fakeOIDCGroupMappingAuth) AuthenticateHTTP(*http.Request) (*auth.AuthContext, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOIDCGroupMappingAuth) AuthenticateGRPC(context.Context) (*auth.AuthContext, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOIDCGroupMappingAuth) RequireRole(r *http.Request, roles ...string) error {
	authCtx := auth.FromRequest(r)
	if authCtx == nil {
		return errors.New("authentication required")
	}
	role := strings.TrimSpace(authCtx.Role)
	if role == "" {
		role = strings.TrimSpace(f.role)
	}
	for _, candidate := range roles {
		if strings.TrimSpace(candidate) == role {
			return nil
		}
	}
	return fmt.Errorf("role %s not permitted", role)
}

func (f *fakeOIDCGroupMappingAuth) ResolveTenant(*http.Request, string, string) (string, error) {
	return "", nil
}

func (f *fakeOIDCGroupMappingAuth) RequireTenantAccess(*http.Request, string) error {
	return nil
}

func (f *fakeOIDCGroupMappingAuth) ResolvePrincipal(*http.Request, string) (string, error) {
	return "", nil
}

func (f *fakeOIDCGroupMappingAuth) AuthConfig() auth.AuthConfig {
	return f.cfg
}

func (f *fakeOIDCGroupMappingAuth) UpdateOIDCGroupRoleMapping(groupsClaim string, mapping map[string]string) (auth.AuthConfig, error) {
	groupsClaim = strings.TrimSpace(groupsClaim)
	if groupsClaim == "" {
		groupsClaim = "groups"
	}
	normalized, err := normalizeTestOIDCGroupRoleMapping(mapping)
	if err != nil {
		return auth.AuthConfig{}, err
	}
	f.cfg.OIDCEnabled = true
	f.cfg.OIDCGroupsClaim = groupsClaim
	f.cfg.OIDCGroupRoleMapping = normalized
	return f.cfg, nil
}

func normalizeTestOIDCGroupRoleMapping(mapping map[string]string) (map[string]string, error) {
	if len(mapping) == 0 {
		return nil, nil
	}
	normalized := make(map[string]string, len(mapping))
	for group, role := range mapping {
		groupKey := strings.ToLower(strings.TrimSpace(group))
		if groupKey == "" {
			return nil, errors.New("empty group")
		}
		roleKey := strings.ToLower(strings.TrimSpace(role))
		switch roleKey {
		case "admin", "operator", "viewer":
		default:
			return nil, fmt.Errorf("invalid role %q", role)
		}
		if _, exists := normalized[groupKey]; exists {
			return nil, fmt.Errorf("duplicate group %q", groupKey)
		}
		normalized[groupKey] = roleKey
	}
	return normalized, nil
}

func TestHandleUpdateOIDCGroupRoleMapping_AdminSuccessPersistsAndSanitizes(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.auth = &fakeOIDCGroupMappingAuth{
		role: "admin",
		cfg: auth.AuthConfig{
			OIDCEnabled:            true,
			OIDCClientID:           "cordum-dashboard",
			OIDCClientSecretMasked: "supe********alue",
		},
	}

	body := `{"oidc_groups_claim":"okta_groups","oidc_group_role_mapping":{"Cordum-Admins":"admin","cordum-operators":"operator"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/oidc/group-role-mapping", strings.NewReader(body))
	req = withOIDCGroupMappingRole(req, "admin")
	rr := httptest.NewRecorder()

	s.handleUpdateOIDCGroupRoleMapping(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusOK, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `"client_secret"`) || strings.Contains(rr.Body.String(), `"oidc_client_secret"`) || strings.Contains(rr.Body.String(), "super-secret-value") {
		t.Fatalf("response leaked client secret: %s", rr.Body.String())
	}
	var resp auth.AuthConfig
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.OIDCGroupsClaim != "okta_groups" {
		t.Fatalf("OIDCGroupsClaim = %q, want okta_groups", resp.OIDCGroupsClaim)
	}
	if got := resp.OIDCGroupRoleMapping["cordum-admins"]; got != "admin" {
		t.Fatalf("OIDCGroupRoleMapping[cordum-admins] = %q, want admin", got)
	}
	if got := resp.OIDCGroupRoleMapping["cordum-operators"]; got != "operator" {
		t.Fatalf("OIDCGroupRoleMapping[cordum-operators] = %q, want operator", got)
	}

	doc, err := s.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatalf("config get: %v", err)
	}
	if got := doc.Data["CORDUM_OIDC_GROUPS_CLAIM"]; got != "okta_groups" {
		t.Fatalf("CORDUM_OIDC_GROUPS_CLAIM = %#v, want okta_groups", got)
	}
	rawMapping, ok := doc.Data["CORDUM_OIDC_GROUP_ROLE_MAPPING"].(string)
	if !ok {
		t.Fatalf("CORDUM_OIDC_GROUP_ROLE_MAPPING type = %T, want string", doc.Data["CORDUM_OIDC_GROUP_ROLE_MAPPING"])
	}
	var persisted map[string]string
	if err := json.Unmarshal([]byte(rawMapping), &persisted); err != nil {
		t.Fatalf("unmarshal persisted mapping: %v", err)
	}
	if got := persisted["cordum-operators"]; got != "operator" {
		t.Fatalf("persisted cordum-operators = %q, want operator", got)
	}
	if len(bus.published) != 1 || bus.published[0].subject != capsdk.SubjectConfigChanged {
		t.Fatalf("config changed publish = %#v, want one %s", bus.published, capsdk.SubjectConfigChanged)
	}
}

func TestHandleUpdateOIDCGroupRoleMapping_RejectsBadRequests(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed json", body: `{"oidc_groups_claim":`},
		{name: "invalid role", body: `{"oidc_groups_claim":"groups","oidc_group_role_mapping":{"cordum-admins":"owner"}}`},
		{name: "duplicate case-colliding group", body: `{"oidc_groups_claim":"groups","oidc_group_role_mapping":{"Cordum-Admins":"admin","cordum-admins":"viewer"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = &fakeOIDCGroupMappingAuth{role: "admin"}
			req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/oidc/group-role-mapping", strings.NewReader(tt.body))
			req = withOIDCGroupMappingRole(req, "admin")
			rr := httptest.NewRecorder()

			s.handleUpdateOIDCGroupRoleMapping(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusBadRequest, rr.Body.String())
			}
		})
	}
}

func TestHandleUpdateOIDCGroupRoleMapping_DeniesViewerAndOperator(t *testing.T) {
	for _, role := range []string{"viewer", "operator"} {
		t.Run(role, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = &fakeOIDCGroupMappingAuth{role: role}
			req := httptest.NewRequest(http.MethodPut, "/api/v1/auth/oidc/group-role-mapping", strings.NewReader(`{"oidc_groups_claim":"groups","oidc_group_role_mapping":{"cordum-admins":"admin"}}`))
			req = withOIDCGroupMappingRole(req, role)
			rr := httptest.NewRecorder()

			s.handleUpdateOIDCGroupRoleMapping(rr, req)

			if rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
			}
		})
	}
}

func withOIDCGroupMappingRole(req *http.Request, role string) *http.Request {
	authCtx := &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: role + "-user",
		Role:        role,
		AuthSource:  auth.AuthSourceAPIKey,
	}
	return req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, authCtx))
}
