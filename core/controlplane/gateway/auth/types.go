package auth

import (
	"context"
	"net/http"
	"time"
)

// ---------------------------------------------------------------------------
// AuthSource type
// ---------------------------------------------------------------------------

// AuthSource identifies the authentication mechanism that validated a request.
type AuthSource string

const (
	AuthSourceAPIKey  AuthSource = "api_key"
	AuthSourceJWT     AuthSource = "jwt"
	AuthSourceOIDC    AuthSource = "oidc"
	AuthSourceSession AuthSource = "session"
)

// ---------------------------------------------------------------------------
// AuthContext & context helpers
// ---------------------------------------------------------------------------

// AuthContext captures request identity for auditing and tenant routing.
type AuthContext struct {
	// #nosec G117 -- runtime credential in request context, not a hardcoded secret.
	APIKey           string
	Tenant           string
	PrincipalID      string
	Role             string
	AllowCrossTenant bool
	AuthSource       AuthSource
}

// ContextKey is the context key for AuthContext values.
type ContextKey struct{}

// FromContext extracts an AuthContext from a context.
func FromContext(ctx context.Context) *AuthContext {
	if ctx == nil {
		return nil
	}
	if raw := ctx.Value(ContextKey{}); raw != nil {
		if auth, ok := raw.(*AuthContext); ok {
			return auth
		}
	}
	return nil
}

// FromRequest extracts an AuthContext from an HTTP request.
func FromRequest(r *http.Request) *AuthContext {
	if r == nil {
		return nil
	}
	return FromContext(r.Context())
}

// ---------------------------------------------------------------------------
// Internal API key types (used by BasicAuthProvider and helpers)
// ---------------------------------------------------------------------------

type apiKeyEntry struct {
	Key              string `json:"key"`
	Tenant           string `json:"tenant,omitempty"`
	Role             string `json:"role,omitempty"`
	PrincipalID      string `json:"principal_id,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	AllowCrossTenant bool   `json:"allow_cross_tenant,omitempty"`
}

type apiKeyMeta struct {
	Tenant           string
	Role             string
	PrincipalID      string
	AllowCrossTenant bool
	ExpiresAt        time.Time
}

// ---------------------------------------------------------------------------
// AuthConfig
// ---------------------------------------------------------------------------

// AuthConfig describes authentication capabilities for the dashboard.
type AuthConfig struct {
	PasswordEnabled        bool              `json:"password_enabled"`
	UserAuthEnabled        bool              `json:"user_auth_enabled"`
	SAMLEnabled            bool              `json:"saml_enabled"`
	SAMLEnterprise         bool              `json:"saml_enterprise"`
	SAMLLoginURL           string            `json:"saml_login_url,omitempty"`
	SAMLMetadataURL        string            `json:"saml_metadata_url,omitempty"`
	SessionTTL             string            `json:"session_ttl"`
	RequireRBAC            bool              `json:"require_rbac"`
	RequirePrincipal       bool              `json:"require_principal"`
	DefaultTenant          string            `json:"default_tenant"`
	OIDCEnabled            bool              `json:"oidc_enabled,omitempty"`
	OIDCIssuer             string            `json:"oidc_issuer,omitempty"`
	OIDCLoginURL           string            `json:"oidc_login_url,omitempty"`
	OIDCClientID           string            `json:"oidc_client_id,omitempty"`
	OIDCRedirectURI        string            `json:"oidc_redirect_uri,omitempty"`
	OIDCScopes             []string          `json:"oidc_scopes,omitempty"`
	OIDCGroupsClaim        string            `json:"oidc_groups_claim,omitempty"`
	OIDCGroupRoleMapping   map[string]string `json:"oidc_group_role_mapping,omitempty"`
	OIDCClientSecretMasked string            `json:"oidc_client_secret_masked,omitempty"`
}
