from enum import Enum


class EdgeErrorCode(str, Enum):
    ACCESS_DENIED = "access_denied"
    APPROVAL_CONFLICT = "approval_conflict"
    APPROVAL_NOT_ACTIONABLE = "approval_not_actionable"
    ARTIFACT_POINTER_INVALID = "artifact_pointer_invalid"
    CONFLICT = "conflict"
    EVENT_CAP_EXCEEDED = "event_cap_exceeded"
    EXECUTION_SESSION_MISMATCH = "execution_session_mismatch"
    EXECUTION_TERMINAL = "execution_terminal"
    IDEMPOTENCY_CONFLICT = "idempotency_conflict"
    IDEMPOTENCY_KEY_INVALID = "idempotency_key_invalid"
    IDEMPOTENCY_WINDOW_EXPIRED = "idempotency_window_expired"
    INTERNAL_ERROR = "internal_error"
    INVALID_JSON = "invalid_json"
    INVALID_REQUEST = "invalid_request"
    MAX_EXECUTIONS_EXCEEDED = "max_executions_exceeded"
    MISSING_PATH_PARAM = "missing_path_param"
    MISSING_REQUIRED_FIELD = "missing_required_field"
    NOT_FOUND = "not_found"
    RAW_PAYLOAD_REJECTED = "raw_payload_rejected"
    REQUEST_TOO_LARGE = "request_too_large"
    SELF_APPROVAL_DENIED = "self_approval_denied"
    SERVICE_UNAVAILABLE = "service_unavailable"
    SESSION_TERMINAL = "session_terminal"
    STORE_UNAVAILABLE = "store_unavailable"
    TENANT_ACCESS_DENIED = "tenant_access_denied"
    TENANT_MISMATCH = "tenant_mismatch"
    TENANT_REQUIRED = "tenant_required"
    UNAUTHORIZED = "unauthorized"
    UPSTREAM_ERROR = "upstream_error"

    def __str__(self) -> str:
        return str(self.value)
