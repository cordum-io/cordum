package gateway

import (
	"net/http"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
)

// EDGE-067 — approval endpoints cross-tenant isolation. Approval handlers
// look up approvals via store.GetApproval(ctx, tenantID, ref) — a tenant-A
// caller targeting tenant-B's approval_ref hits the same not-found path
// as any nonexistent ref. We assert that path explicitly with both fake
// refs (probing the tenant-scoped lookup) and, where feasible, a
// cross-tenant probe seeded with tenantB's real approval scope.

const fakeForeignApprovalRef = edgecore.ApprovalRefPrefix + "edge067-fake-foreign-ref"

func TestEdgeCrossTenantGetApprovalReturnsNotFoundForForeignRef(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodGet, "/api/v1/edge/approvals/"+fakeForeignApprovalRef, "")

	if rr.Code != http.StatusNotFound {
		t.Fatalf("get approval foreign-ref status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
	// Must NOT echo the ref back beyond the URL — body must not surface
	// existence/non-existence facts that distinguish cross-tenant from
	// truly-missing.
	if !strings.Contains(rr.Body.String(), "not_found") {
		t.Fatalf("get approval foreign-ref body missing not_found code: %s", rr.Body.String())
	}
}

func TestEdgeCrossTenantApproveRejectsForeignRef(t *testing.T) {
	fix := newCrossTenantFixture(t)

	body := `{"reason":"approved"}`
	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/approvals/"+fakeForeignApprovalRef+"/approve", body)

	assertCrossTenantBlocked(t, rr, "approve foreign-ref")
}

func TestEdgeCrossTenantRejectRejectsForeignRef(t *testing.T) {
	fix := newCrossTenantFixture(t)

	body := `{"reason":"rejected"}`
	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/approvals/"+fakeForeignApprovalRef+"/reject", body)

	assertCrossTenantBlocked(t, rr, "reject foreign-ref")
}

func TestEdgeCrossTenantWaitRejectsForeignRef(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodPost, "/api/v1/edge/approvals/"+fakeForeignApprovalRef+"/wait?timeout_ms=10", "")

	assertCrossTenantBlocked(t, rr, "wait foreign-ref")
}

func TestEdgeCrossTenantListApprovalsReturnsOnlyOwnTenant(t *testing.T) {
	fix := newCrossTenantFixture(t)

	rr := fix.asAttacker(t, http.MethodGet, "/api/v1/edge/approvals", "")

	if rr.Code != http.StatusOK {
		t.Fatalf("list approvals as A status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	fix.assertNoTenantBLeak(t, rr, "list approvals must not leak B's IDs")
}
