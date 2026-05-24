package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
)

// TestGRPCHTTPActorIdentityParity locks DoD#3: the same credential must resolve
// to an identical (identity, source, label) on the gRPC and HTTP paths, both
// routed through the shared policybundles.ActorIdentity resolver.
func TestGRPCHTTPActorIdentityParity(t *testing.T) {
	ac := auth.AuthContext{KeyID: "mk_x", KeyName: "ci", Role: "operator"}

	// gRPC path: actor derived from the context auth value.
	ctx := context.WithValue(context.Background(), auth.ContextKey{}, &ac)
	gID, gSource, gLabel, gRole := grpcAuditActor(ctx)
	if gID != "mk_x" || gSource != "api_key:mk_x" || gLabel != "ci" {
		t.Fatalf("gRPC actor = (%q,%q,%q), want (mk_x, api_key:mk_x, ci)", gID, gSource, gLabel)
	}
	if gRole != "operator" {
		t.Fatalf("gRPC role = %q, want operator", gRole)
	}

	// HTTP path: actor derived from the request's auth context (same credential).
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.ContextKey{}, &ac))
	hID, hSource, hLabel := policybundles.PolicyActorIdentity(req)

	// Parity: HTTP and gRPC must agree on identity/source/label for one credential.
	if hID != gID || hSource != gSource || hLabel != gLabel {
		t.Fatalf("parity mismatch: gRPC=(%q,%q,%q) HTTP=(%q,%q,%q)", gID, gSource, gLabel, hID, hSource, hLabel)
	}
	if hID != "mk_x" || hSource != "api_key:mk_x" || hLabel != "ci" {
		t.Fatalf("HTTP actor = (%q,%q,%q), want (mk_x, api_key:mk_x, ci)", hID, hSource, hLabel)
	}
}

// TestGRPCAuditActorAnonymousWhenUnauthenticated verifies the gRPC audit actor
// falls back to the "anonymous"/"none" sentinel only when there is no auth
// context (truly unauthenticated), per the plan's parity requirement.
func TestGRPCAuditActorAnonymousWhenUnauthenticated(t *testing.T) {
	id, source, label, role := grpcAuditActor(context.Background())
	if id != "anonymous" || role != "none" {
		t.Fatalf("unauthenticated gRPC actor = (id=%q, role=%q), want (anonymous, none)", id, role)
	}
	if source != "" || label != "" {
		t.Fatalf("unauthenticated source/label = (%q,%q), want empty", source, label)
	}
}
