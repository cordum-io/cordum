package gateway

// auth_compat.go provides backward-compatible type aliases and function
// re-exports so that existing gateway code (handlers, middleware, tests)
// continues to compile after the auth logic moved to gateway/auth/.
//
// New code should import "gateway/auth" directly.

import (
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

// ─── Type aliases ────────────────────────────────────────────────────────────
// Go type aliases are fully compatible — callers can use either name.

type AuthSource = auth.AuthSource
type AuthContext = auth.AuthContext
type authContextKey = auth.ContextKey
type AuthProvider = auth.AuthProvider
type UserStoreProvider = auth.UserStoreProvider
type BasicAuthProvider = auth.BasicAuthProvider
type CompositeAuthProvider = auth.CompositeAuthProvider
type OIDCProvider = auth.OIDCProvider
type OIDCConfig = auth.OIDCConfig
type OIDCAuthAdapter = auth.OIDCAuthAdapter
type AuthConfig = auth.AuthConfig
type User = auth.User
type UserStore = auth.UserStore
type KeyStore = auth.KeyStore
type ManagedKey = auth.ManagedKey
type RedisUserStore = auth.RedisUserStore
type RedisKeyStore = auth.RedisKeyStore
type PublicPathProvider = auth.PublicPathProvider
type AuthConfigProvider = auth.AuthConfigProvider
type RouteRegistrar = auth.RouteRegistrar
type CreateUserRequest = auth.CreateUserRequest
type ChangePasswordRequest = auth.ChangePasswordRequest

// ─── Constant re-exports ────────────────────────────────────────────────────

const (
	AuthSourceAPIKey  = auth.AuthSourceAPIKey
	AuthSourceJWT     = auth.AuthSourceJWT
	AuthSourceOIDC    = auth.AuthSourceOIDC
	AuthSourceSession = auth.AuthSourceSession
)

// ─── Function re-exports (var = pkg.Fn preserves the original signature) ────

var (
	// Context helpers (unexported — used by gateway internals + tests).
	authFromContext = auth.FromContext
	authFromRequest = auth.FromRequest

	// Provider constructors.
	newBasicAuthProvider   = auth.NewBasicAuthProvider
	NewCompositeAuthProvider = auth.NewCompositeAuthProvider
	NewOIDCAuthAdapter     = auth.NewOIDCAuthAdapter
	NewOIDCProvider        = auth.NewOIDCProvider
	NewOIDCProviderFromEnv = auth.NewOIDCProviderFromEnv

	// Store constructors.
	NewRedisUserStore    = auth.NewRedisUserStore
	NewRedisKeyStore     = auth.NewRedisKeyStore
	seedDefaultAdminUser = auth.SeedDefaultAdminUser
	GenerateRawKey       = auth.GenerateRawKey

	// Validation helpers.
	ValidatePassword = auth.ValidatePassword

	// Auth helpers (unexported — used by gateway internals + tests).
	basicAuthProvider  = auth.ExtractBasicAuth
	normalizeAPIKey    = auth.NormalizeAPIKey
	apiKeyFromWebSocket = auth.APIKeyFromWebSocket
	bearerToken        = auth.BearerToken
	headerValue        = auth.HeaderValue
	normalizeRole      = auth.NormalizeRole
	parseAPIKeys       = auth.ParseAPIKeys
)

// ─── Error re-exports ───────────────────────────────────────────────────────

var (
	ErrUserNotFound      = auth.ErrUserNotFound
	ErrUserAlreadyExists = auth.ErrUserAlreadyExists
	ErrInvalidPassword   = auth.ErrInvalidPassword
	ErrUserDisabled      = auth.ErrUserDisabled
	ErrLoginThrottled    = auth.ErrLoginThrottled
	ErrKeyNotFound       = auth.ErrKeyNotFound
)
