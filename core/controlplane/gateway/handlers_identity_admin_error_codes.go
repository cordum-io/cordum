package gateway

const (
	errorCodeAuthInvalidCredentials = "AUTH_INVALID_CREDENTIALS" // #nosec G101 -- API error-code identifier returned to clients, not a credential
	errorCodeAuthTokenInvalid       = "AUTH_TOKEN_INVALID"       // #nosec G101 -- API error-code identifier returned to clients, not a credential
	errorCodeAuthOIDCCallbackFailed = "AUTH_OIDC_CALLBACK_FAILED"
	errorCodeAuthRequestInvalid     = "AUTH_REQUEST_INVALID"
	errorCodeAuthPasswordInvalid    = "AUTH_PASSWORD_INVALID"
	errorCodeAuthUserNotFound       = "AUTH_USER_NOT_FOUND"
	errorCodeAuthUserConflict       = "AUTH_USER_CONFLICT"
	errorCodeAuthKeyInvalid         = "AUTH_KEY_INVALID"
	errorCodeAuthKeyNotFound        = "AUTH_KEY_NOT_FOUND"

	errorCodeAgentRequestInvalid = "AGENT_REQUEST_INVALID"
	errorCodeAgentNotFound       = "AGENT_NOT_FOUND"

	errorCodeConfigRequestInvalid  = "CONFIG_REQUEST_INVALID"
	errorCodeConfigKeyForbidden    = "CONFIG_KEY_FORBIDDEN"
	errorCodeConfigVersionConflict = "CONFIG_VERSION_CONFLICT"
	errorCodeConfigSchemaViolation = "CONFIG_SCHEMA_VIOLATION"
	errorCodeConfigNotFound        = "CONFIG_NOT_FOUND"
	errorCodeConfigSchemaNotFound  = "CONFIG_SCHEMA_NOT_FOUND"

	errorCodePoolInvalidConfig   = "POOL_INVALID_CONFIG"
	errorCodePoolNotFound        = "POOL_NOT_FOUND"
	errorCodePoolInUse           = "POOL_IN_USE"
	errorCodePoolNameConflict    = "POOL_NAME_CONFLICT"
	errorCodePoolVersionConflict = "POOL_VERSION_CONFLICT"

	errorCodeRBACRequestInvalid    = "RBAC_REQUEST_INVALID"
	errorCodeRBACPermissionInvalid = "RBAC_PERMISSION_INVALID"
	errorCodeRBACRoleNotFound      = "RBAC_ROLE_NOT_FOUND"
	errorCodeRBACRoleInUse         = "RBAC_ROLE_IN_USE"
	errorCodeRBACRoleProtected     = "RBAC_ROLE_PROTECTED"

	errorCodeTopicSchemaViolation = "TOPIC_SCHEMA_VIOLATION"
	errorCodeTopicNotFound        = "TOPIC_NOT_FOUND"

	errorCodeWorkerCredBindingInvalid = "WORKER_CRED_BINDING_INVALID" // #nosec G101 -- API error-code identifier returned to clients, not a credential
	errorCodeWorkerCredNotFound       = "WORKER_CRED_NOT_FOUND"       // #nosec G101 -- API error-code identifier returned to clients, not a credential

	errorCodeWorkerSessionInvalid = "WORKER_SESSION_INVALID"
	errorCodeWorkerNotFound       = "WORKER_NOT_FOUND"
)
