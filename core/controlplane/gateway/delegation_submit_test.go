package gateway

import (
	"errors"
	"net/http"
	"testing"

	"github.com/cordum/cordum/core/auth/delegation"
)

func TestSubmitDelegationErrorStatusMapsDelegationTaxonomy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "missing_agent", err: errDelegationAgentRequired, wantStatus: http.StatusBadRequest, wantCode: errDelegationAgentRequired.Error()},
		{name: "malformed", err: delegation.ErrMalformed, wantStatus: http.StatusUnauthorized, wantCode: "malformed"},
		{name: "bad_signature", err: delegation.ErrBadSignature, wantStatus: http.StatusUnauthorized, wantCode: "bad_signature"},
		{name: "unknown_kid", err: delegation.ErrUnknownKeyId, wantStatus: http.StatusUnauthorized, wantCode: "unknown_kid"},
		{name: "expired", err: delegation.ErrExpired, wantStatus: http.StatusForbidden, wantCode: "expired"},
		{name: "revoked", err: delegation.ErrRevoked, wantStatus: http.StatusForbidden, wantCode: "revoked"},
		{name: "audience_mismatch", err: delegation.ErrAudienceMismatch, wantStatus: http.StatusForbidden, wantCode: "audience_mismatch"},
		{name: "chain_too_deep", err: delegation.ErrChainTooDeep, wantStatus: http.StatusUnprocessableEntity, wantCode: "chain_too_deep"},
		{name: "scope_exceeded", err: delegation.ErrScopeExceeded, wantStatus: http.StatusUnprocessableEntity, wantCode: "scope_exceeded"},
		{name: "tenant_mismatch", err: errors.New("delegation token tenant mismatch"), wantStatus: http.StatusForbidden, wantCode: "delegation token tenant mismatch"},
		{name: "service_unavailable", err: errors.New("backend offline"), wantStatus: http.StatusServiceUnavailable, wantCode: "delegation token service unavailable"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotCode := submitDelegationErrorStatus(tc.err)
			if gotStatus != tc.wantStatus || gotCode != tc.wantCode {
				t.Fatalf("submitDelegationErrorStatus(%v) = (%d, %q), want (%d, %q)", tc.err, gotStatus, gotCode, tc.wantStatus, tc.wantCode)
			}
		})
	}
}
