package gateway

import (
	"net/http"
	"strings"
)

type AuthConfig struct {
	PasswordEnabled  bool   `json:"password_enabled"`
	UserAuthEnabled  bool   `json:"user_auth_enabled"`
	SAMLEnabled      bool   `json:"saml_enabled"`
	SAMLEnterprise   bool   `json:"saml_enterprise"`
	SAMLLoginURL     string `json:"saml_login_url,omitempty"`
	SAMLMetadataURL  string `json:"saml_metadata_url,omitempty"`
	SessionTTL       string `json:"session_ttl"`
	RequireRBAC      bool   `json:"require_rbac"`
	RequirePrincipal bool   `json:"require_principal"`
	DefaultTenant    string `json:"default_tenant"`
}

func (s *server) handleAuthConfig(w http.ResponseWriter, _ *http.Request) {
	defaultTenant := strings.TrimSpace(s.tenant)
	if defaultTenant == "" {
		defaultTenant = "default"
	}
	resp := AuthConfig{
		PasswordEnabled:  false,
		SAMLEnabled:      false,
		SessionTTL:       "0s",
		RequireRBAC:      false,
		RequirePrincipal: false,
		DefaultTenant:    defaultTenant,
	}
	if provider, ok := s.auth.(AuthConfigProvider); ok {
		resp = provider.AuthConfig()
	}
	if strings.TrimSpace(resp.DefaultTenant) == "" {
		resp.DefaultTenant = defaultTenant
	}
	if strings.TrimSpace(resp.SessionTTL) == "" {
		resp.SessionTTL = "0s"
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w,resp)
}
