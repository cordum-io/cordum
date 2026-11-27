package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/model"
)

func listMCPApprovalIDs(t *testing.T, h *mcpApprovalHandler, tenant, query string) []string {
	t.Helper()
	rr := httptest.NewRecorder()
	h.List(rr, tenantObjectRequest(http.MethodGet, "/api/v1/mcp/approvals"+query, tenant, ""))
	var body struct {
		Items []MCPApprovalRecord `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("%s list: %d %s, %v", tenant, rr.Code, rr.Body.String(), err)
	}
	ids := make([]string, 0, len(body.Items))
	for _, rec := range body.Items {
		if rec.Tenant != tenant {
			t.Errorf("%s list holds %s's approval %s", tenant, rec.Tenant, rec.ID)
		}
		ids = append(ids, rec.ID)
	}
	slices.Sort(ids)
	return ids
}

// Both tenants hold an approved and a pending approval for the same agent,
// tool and args, so they share one index tuple apart from the tenant.
func TestTenantIsolationGatewayMCPApprovalIndex(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx, client := context.Background(), s.jobStore.Client()
	store := NewMCPApprovalStore(client)
	h := &mcpApprovalHandler{store: store}
	approved, all := map[string]string{}, map[string][]string{}
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		for _, approve := range []bool{true, false} {
			rec, err := store.EnqueueMCPApproval(ctx, &MCPApprovalRequest{Tenant: tenant, AgentID: "agent-shared", ToolName: "tool-shared", ArgsHash: "hash-shared", Requester: "requester-" + tenant})
			if err != nil {
				t.Fatal(err)
			}
			if approve {
				if _, err := store.Resolve(ctx, rec.ID, model.ApprovalDecisionApprove, "resolver-"+tenant, "approve"); err != nil {
					t.Fatal(err)
				}
				approved[tenant] = rec.ID
			}
			all[tenant] = append(all[tenant], rec.ID)
		}
		slices.Sort(all[tenant])
	}
	before := tenantObjectSnapshot(t, s)
	for tenant, want := range all {
		if got := listMCPApprovalIDs(t, h, tenant, ""); !slices.Equal(got, want) {
			t.Errorf("%s list = %v; want %v", tenant, got, want)
		}
		if got := listMCPApprovalIDs(t, h, tenant, "?status=approved"); !slices.Equal(got, []string{approved[tenant]}) {
			t.Errorf("%s approved list = %v; want %s", tenant, got, approved[tenant])
		}
		if rec, err := store.FindPreApproved(ctx, tenant, "agent-shared", "tool-shared", "hash-shared"); err != nil || rec == nil || rec.ID != approved[tenant] {
			t.Errorf("%s pre-approved = %+v, %v; want %s", tenant, rec, err, approved[tenant])
		}
	}
	foreign := httptest.NewRecorder()
	h.Get(foreign, tenantObjectRequest(http.MethodGet, "/api/v1/mcp/approvals/"+approved["tenant-b"], "tenant-a", approved["tenant-b"]), approved["tenant-b"])
	if foreign.Code != http.StatusNotFound || strings.Contains(foreign.Body.String(), approved["tenant-b"]) || strings.Contains(foreign.Body.String(), "tenant-b") {
		t.Errorf("A read B's approval by id: %d %s", foreign.Code, foreign.Body.String())
	}
	if after := tenantObjectSnapshot(t, s); !reflect.DeepEqual(before, after) {
		t.Errorf("keyspace changed by reads")
	}
	bKeys := []string{mcpApprovalIndexKey("tenant-b", "agent-shared", "tool-shared", "hash-shared")}
	for _, id := range all["tenant-b"] {
		bKeys = append(bKeys, mcpApprovalKey(id))
	}
	// A's claim consumes from A's tuple index only.
	if rec, err := store.ClaimPreApproved(ctx, "tenant-a", "agent-shared", "tool-shared", "hash-shared"); err != nil || rec == nil || rec.ID != approved["tenant-a"] {
		t.Fatalf("A claim = %+v, %v; want %s", rec, err, approved["tenant-a"])
	}
	if rec, err := store.ClaimPreApproved(ctx, "tenant-a", "agent-shared", "tool-shared", "hash-shared"); err != nil || rec != nil {
		t.Errorf("A's second claim reached another record: %+v, %v", rec, err)
	}
	after := tenantObjectSnapshot(t, s)
	for _, key := range bKeys {
		if before[key] == "" || after[key] != before[key] {
			t.Errorf("B key %s changed by A's claim: %q -> %q", key, before[key], after[key])
		}
	}
	if rec, err := store.FindPreApproved(ctx, "tenant-b", "agent-shared", "tool-shared", "hash-shared"); err != nil || rec == nil || rec.ID != approved["tenant-b"] || rec.ConsumedAt != 0 {
		t.Errorf("B pre-approved after A's claim = %+v, %v", rec, err)
	}
}
