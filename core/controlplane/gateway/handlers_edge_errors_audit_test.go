package gateway

import (
	"strings"
	"testing"
)

// TestEdgeErrCodeNaming_AllLowerSnake locks the stable-code naming
// convention rename: edgeErrCodeReplayWindowFull was the only odd
// UPPER_SNAKE outlier among the 24 sibling /api/v1/edge/* codes (all
// lower_snake). The rename to "replay_window_full" closes the
// cross-finding listed alongside the audit's MEDIUM items.
//
// This test asserts the constant value is the unified lower_snake form;
// the audit fix also renames the OpenAPI enum + dashboard codegen entry
// to match (regression evidence captured in the final task note).
func TestEdgeErrCodeNaming_AllLowerSnake(t *testing.T) {
	t.Parallel()
	codes := []string{
		edgeErrCodeUnauthorized,
		edgeErrCodeAccessDenied,
		edgeErrCodeTenantRequired,
		edgeErrCodeTenantMismatch,
		edgeErrCodeTenantAccessDenied,
		edgeErrCodeMissingPathParam,
		edgeErrCodeInvalidRequest,
		edgeErrCodeInvalidJSON,
		edgeErrCodeMissingField,
		edgeErrCodeNotFound,
		edgeErrCodeRequestTooLarge,
		edgeErrCodeServiceUnavailable,
		edgeErrCodeStoreUnavailable,
		edgeErrCodeInternalError,
		edgeErrCodeConflict,
		edgeErrCodeLimitExceeded,
		edgeErrCodeSessionTerminal,
		edgeErrCodeExecutionTerminal,
		edgeErrCodeExecutionMismatch,
		edgeErrCodeRawPayloadRejected,
		edgeErrCodeArtifactPointerInvalid,
		edgeErrCodeApprovalConflict,
		edgeErrCodeSelfApprovalDenied,
		edgeErrCodeIdempotencyConflict,
		edgeErrCodeIdempotencyKeyTooLong,
		edgeErrCodeIdempotencyWindowExpired,
		edgeErrCodeMaxExecutionsExceeded,
		edgeErrCodeEventCapExceeded,
		edgeErrCodeReplayWindowFull,
		edgeErrCodeMaxEventsTooLarge,
		edgeErrCodeEventListTooLarge,
		edgeErrCodeStepUpRequired,
	}
	for _, code := range codes {
		if code != strings.ToLower(code) {
			t.Errorf("edge error code %q is not lower_snake — code-style outlier", code)
		}
	}
	if edgeErrCodeReplayWindowFull != "replay_window_full" {
		t.Errorf("edgeErrCodeReplayWindowFull = %q, want %q (lower_snake-naming fix)",
			edgeErrCodeReplayWindowFull, "replay_window_full")
	}
}
