package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	wf "github.com/cordum/cordum/core/workflow"
)

func checkTenantObjectRead(t *testing.T, handler http.HandlerFunc, path, id, marker string) {
	t.Helper()
	owner := httptest.NewRecorder()
	handler(owner, tenantObjectRequest(http.MethodGet, path, "tenant-b", id))
	if owner.Code != http.StatusOK || !strings.Contains(owner.Body.String(), marker) {
		t.Fatalf("owner did not read seeded object: %d %s", owner.Code, owner.Body.String())
	}
	other := httptest.NewRecorder()
	handler(other, tenantObjectRequest(http.MethodGet, path, "tenant-a", id))
	if strings.Contains(other.Body.String(), "private-") || strings.Contains(other.Body.String(), marker) || strings.Contains(other.Body.String(), "tenant-b") {
		t.Errorf("response released tenant B data: %s", other.Body.String())
	}
	if other.Code >= http.StatusInternalServerError {
		t.Errorf("infrastructure error is not an isolation result: %d", other.Code)
	}
}

func TestTenantIsolationGatewayConfigScopes(t *testing.T) {
	for _, scope := range []string{"org", "team", "workflow", "step"} {
		for _, effective := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/effective=%t", scope, effective), func(t *testing.T) {
				s, _, _ := newTestGateway(t)
				s.auth = newBasicAuthForTest(t, nil)
				id := "private-config-b"
				if scope == "org" {
					id = "tenant-b"
				}
				r := tenantObjectRequest(http.MethodPost, "/api/v1/config", "tenant-b", id)
				r.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"scope":%q,"scope_id":%q,"data":{"secret":"private-config-b"}}`, scope, id)))
				seed := httptest.NewRecorder()
				s.handleSetConfig(seed, r)
				if seed.Code != http.StatusOK && seed.Code != http.StatusNoContent {
					t.Fatalf("seed config: %d %s", seed.Code, seed.Body.String())
				}
				tenantObjectUnchanged(t, s)
				path := "/api/v1/config?scope=" + scope + "&scope_id=" + id
				handler := s.handleGetConfig
				if effective {
					path = "/api/v1/config/effective?" + scope + "_id=" + id
					handler = s.handleGetEffectiveConfig
				}
				checkTenantObjectRead(t, handler, path, id, "private-config-b")
			})
		}
	}
}

func checkTenantConfig(t *testing.T, s *server, scope, method, tenant, target, value string, cross bool, status int) {
	t.Helper()
	r := tenantObjectRequest(method, "/api/v1/config?scope="+scope+"&scope_id=shared", tenant, "shared")
	r.Header.Set("X-Tenant-ID", target)
	auth.FromRequest(r).AllowCrossTenant = cross
	r.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"scope":%q,"scope_id":"shared","data":{"value":%q}}`, scope, value)))
	rr := httptest.NewRecorder()
	if method == http.MethodGet {
		s.handleGetConfig(rr, r)
	} else {
		s.handleSetConfig(rr, r)
	}
	if rr.Code != status {
		t.Fatalf("%s %s as %s: status %d, want %d: %s", method, target, tenant, rr.Code, status, rr.Body.String())
	}
	if method == http.MethodGet && status == http.StatusOK && strings.TrimSpace(rr.Body.String()) != fmt.Sprintf(`{"value":%q}`, value) {
		t.Errorf("config data = %s, want value %q", rr.Body.String(), value)
	}
	if status == http.StatusForbidden && strings.Contains(rr.Body.String(), "private-") {
		t.Errorf("refusal contains config data: %s", rr.Body.String())
	}
}

func TestTenantIsolationGatewayConfigScopeWrites(t *testing.T) {
	for _, scope := range []string{"team", "workflow", "step"} {
		t.Run(scope, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = newBasicAuthForTest(t, nil)
			for _, tenant := range []string{"tenant-b", "tenant-a"} {
				checkTenantConfig(t, s, scope, http.MethodPut, tenant, tenant, "private-"+tenant, false, http.StatusNoContent)
			}
			for _, tenant := range []string{"tenant-b", "tenant-a"} {
				checkTenantConfig(t, s, scope, http.MethodGet, tenant, tenant, "private-"+tenant, false, http.StatusOK)
			}
			t.Run("tenant-header-refused", func(t *testing.T) {
				tenantObjectUnchanged(t, s)
				for _, method := range []string{http.MethodGet, http.MethodPut} {
					checkTenantConfig(t, s, scope, method, "tenant-a", "tenant-b", "private-denied", false, http.StatusForbidden)
				}
			})
			checkTenantConfig(t, s, scope, http.MethodGet, "tenant-a", "tenant-b", "private-tenant-b", true, http.StatusOK)
			checkTenantConfig(t, s, scope, http.MethodPut, "tenant-a", "tenant-b", "private-selected", true, http.StatusNoContent)
			checkTenantConfig(t, s, scope, http.MethodGet, "tenant-b", "tenant-b", "private-selected", false, http.StatusOK)
			checkTenantConfig(t, s, scope, http.MethodGet, "tenant-a", "tenant-a", "private-tenant-a", false, http.StatusOK)
		})
	}
}

// seedUnownedConfig stores, at tenant-a's key for scope_id shared, a document
// that records no tenant, and returns its key and bytes.
func seedUnownedConfig(t *testing.T, s *server, scope string) (string, string) {
	t.Helper()
	key := "cfg:" + scope + ":tenant-a/shared"
	raw, err := json.Marshal(configsvc.Document{Scope: configsvc.Scope(scope), ScopeID: "tenant-a/shared", Revision: 1,
		Data: map[string]any{"value": "private-legacy-b"}, Meta: map[string]string{"tenant": "tenant-a"}})
	if err == nil {
		err = s.jobStore.Client().Set(context.Background(), key, raw, 0).Err()
	}
	if err != nil {
		t.Fatal(err)
	}
	return key, string(raw)
}

func TestTenantIsolationGatewayConfigUnownedDocuments(t *testing.T) {
	cross := configCaller{tenant: "tenant-b", target: "tenant-a", cross: true}
	for _, scope := range []string{"team", "workflow", "step"} {
		effective := "/api/v1/config/effective?" + scope + "_id=shared"
		t.Run(scope+"/raw", func(t *testing.T) {
			s, _, mux := newConfigAdmissionGateway(t)
			seedUnownedConfig(t, s, scope)
			tenantObjectUnchanged(t, s)
			for _, c := range []configCaller{configOther, cross} {
				rr := c.do(mux, http.MethodGet, "/api/v1/config?envelope=true&scope="+scope+"&scope_id=shared", "")
				if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), "private-") {
					t.Errorf("GET as %s for tenant-a: %d %s", c.tenant, rr.Code, rr.Body.String())
				}
			}
		})
		t.Run(scope+"/effective", func(t *testing.T) {
			s, _, mux := newConfigAdmissionGateway(t)
			before := configOther.do(mux, http.MethodGet, effective, "")
			seedUnownedConfig(t, s, scope)
			after := configOther.do(mux, http.MethodGet, effective, "")
			if before.Code != http.StatusOK || after.Code != http.StatusOK || after.Body.String() != before.Body.String() {
				t.Errorf("effective config: %d %s, then %d %s", before.Code, before.Body.String(), after.Code, after.Body.String())
			}
		})
		t.Run(scope+"/write", func(t *testing.T) {
			s, bus, mux := newConfigAdmissionGateway(t)
			key, stored := seedUnownedConfig(t, s, scope)
			tenantObjectUnchanged(t, s)
			published := publishedCount(bus)
			rr := configOther.write(mux, http.MethodPut, scope, "shared", "private-a")
			var body struct{ Code string }
			_ = json.Unmarshal(rr.Body.Bytes(), &body)
			if rr.Code != http.StatusForbidden || body.Code != errorCodeConfigKeyForbidden || strings.Contains(rr.Body.String(), "private-") {
				t.Errorf("PUT: %d %s", rr.Code, rr.Body.String())
			}
			if got, err := s.jobStore.Client().Get(context.Background(), key).Result(); err != nil || got != stored {
				t.Errorf("stored document = %s, %v; want %s", got, err, stored)
			}
			if n := publishedCount(bus); n != published {
				t.Errorf("config-changed published %d messages", n-published)
			}
		})
		t.Run(scope+"/current", func(t *testing.T) { checkCurrentConfig(t, scope) })
	}
}

// checkCurrentConfig writes and reads tenant-a's document at scope_id own, then
// at scope_id shared once the document stored at that key is deleted.
func checkCurrentConfig(t *testing.T, scope string) {
	s, _, mux := newConfigAdmissionGateway(t)
	key, _ := seedUnownedConfig(t, s, scope)
	for _, id := range []string{"own", "shared"} {
		if id == "shared" {
			if err := s.jobStore.Client().Del(context.Background(), key).Err(); err != nil {
				t.Fatal(err)
			}
		}
		value := "private-a-" + id
		if rr := configOther.write(mux, http.MethodPut, scope, id, value); rr.Code != http.StatusNoContent {
			t.Fatalf("PUT %s: %d %s", id, rr.Code, rr.Body.String())
		}
		for _, path := range []string{"/api/v1/config?envelope=true&scope=" + scope + "&scope_id=" + id, "/api/v1/config/effective?" + scope + "_id=" + id} {
			if rr := configOther.do(mux, http.MethodGet, path, ""); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), value) {
				t.Errorf("GET %s: %d %s", path, rr.Code, rr.Body.String())
			}
		}
	}
}

func seedTenantObjectWorkflow(t *testing.T, s *server) {
	t.Helper()
	ctx := context.Background()
	def := &wf.Workflow{ID: "private-workflow-b", OrgID: "tenant-b", Name: "private-workflow-b"}
	run := &wf.WorkflowRun{ID: "private-run-b", WorkflowID: def.ID, OrgID: def.OrgID,
		Steps: map[string]*wf.StepRun{"step": {StepID: "step", JobID: "private-run-b:step"}}}
	for _, err := range []error{
		s.workflowStore.SaveWorkflow(ctx, def),
		s.workflowStore.UpdateAuditHash(ctx, "private-run-b:step", strings.Repeat("b", 64)),
		s.workflowStore.CreateRun(ctx, run),
		s.workflowStore.UpdateAuditHash(ctx, "private-run-b:pending", strings.Repeat("c", 64)),
		s.workflowStore.AppendTimelineEvent(ctx, run.ID, &wf.TimelineEvent{Time: time.Now().UTC(), Type: "job", JobID: "private-run-b:step", Message: "private-timeline-b"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	stored, err := s.workflowStore.GetRun(ctx, run.ID)
	if err != nil || stored == nil || stored.Steps["step"] == nil || stored.Steps["step"].AuditHash != strings.Repeat("b", 64) {
		t.Fatalf("pending audit hash was not joined to run: %v", err)
	}
	for _, key := range []string{"wf:run:job:private-run-b:step", "wf:run:pending_audit_hash:private-run-b:pending"} {
		if n, err := s.jobStore.Client().Exists(ctx, key).Result(); err != nil || n != 1 {
			t.Fatalf("missing child %s: %d, %v", key, n, err)
		}
	}
}

func TestTenantIsolationGatewayWorkflowObjects(t *testing.T) {
	for _, surface := range []string{"definition", "run", "timeline", "workflow-runs"} {
		t.Run(surface, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = newBasicAuthForTest(t, nil)
			seedTenantObjectWorkflow(t, s)
			tenantObjectUnchanged(t, s)
			id, marker, handler := "private-run-b", "private-run-b", s.handleGetRun
			switch surface {
			case "definition":
				id, marker, handler = "private-workflow-b", "private-workflow-b", s.handleGetWorkflow
			case "timeline":
				marker, handler = "private-timeline-b", s.handleGetRunTimeline
			case "workflow-runs":
				id, handler = "private-workflow-b", s.handleListRuns
			}
			checkTenantObjectRead(t, handler, "/api/v1/workflows/"+id, id, marker)
		})
	}
}

func TestTenantIsolationGatewayWorkerCredential(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	ctx := context.Background()
	const id = "private-worker-b"
	if _, err := s.workerCredentialStore.Create(ctx, workercredentials.IssueInput{TenantID: "tenant-b", WorkerID: id, CreatedBy: "private-owner-b"}); err != nil {
		t.Fatal(err)
	}
	owner, err := s.workerCredentialStore.GetByWorkerID(ctx, id)
	if err != nil || owner == nil || owner.TenantID != "tenant-b" {
		t.Fatalf("internal lookup control: %v, %v", owner, err)
	}
	tenantObjectUnchanged(t, s)
	// The public Get wrapper is the tenant boundary around GetByWorkerID.
	got, err := s.workerCredentialStore.Get(ctx, "tenant-a", id)
	if err != nil || got != nil {
		t.Errorf("tenant-qualified lookup: %v, %v", got, err)
	}
	checkTenantObjectRead(t, s.handleListWorkerCredentials, "/api/v1/workers/credentials", id, id)
}

func TestTenantIsolationGatewayDelegationObject(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		t.Run(fmt.Sprintf("revoked=%t", revoked), func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = newBasicAuthForTest(t, nil)
			ctx := context.Background()
			const id = "private-delegation-b"
			view := delegation.DelegationView{JTI: id, Tenant: "tenant-b", Subject: "private-agent-b",
				Audience: "private-target-b", IssuedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour), Revoked: revoked}
			if err := s.delegationListStore().RecordIssuedToken(ctx, view); err != nil {
				t.Fatal(err)
			}
			if revoked {
				if err := s.jobStore.Client().Set(ctx, "delegation:revoked:"+id, "1", time.Hour).Err(); err != nil {
					t.Fatal(err)
				}
			}
			tenantObjectUnchanged(t, s)
			checkTenantObjectRead(t, s.handleListDelegations, "/api/v1/delegations?status=all", id, id)
			// Revoke is the gateway's only id-addressed lookup of the token record.
			r := tenantObjectRequest(http.MethodPost, "/api/v1/delegations/revoke", "tenant-a", id)
			r.Body = io.NopCloser(strings.NewReader(fmt.Sprintf(`{"jti":%q}`, id)))
			rr := httptest.NewRecorder()
			s.handleRevokeDelegation(rr, r)
			if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), id) {
				t.Errorf("foreign delegation lookup was not refused: %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestTenantIsolationGatewayMCPApprovalObject(t *testing.T) {
	s, _, _ := newTestGateway(t)
	store := NewMCPApprovalStore(s.jobStore.Client())
	stored, err := store.EnqueueMCPApproval(context.Background(), &MCPApprovalRequest{
		Tenant: "tenant-b", AgentID: "private-agent-b", ToolName: "private-tool-b", ArgsHash: "private-hash-b", Requester: "private-owner-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	tenantObjectUnchanged(t, s)
	h := &mcpApprovalHandler{store: store}
	checkTenantObjectRead(t, func(w http.ResponseWriter, r *http.Request) {
		h.Get(w, r, stored.ID)
	}, "/api/v1/approvals/mcp/"+stored.ID, stored.ID, "private-tool-b")
}

func TestTenantIsolationGatewayEvalRunPending(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	ctx := context.Background()
	const id = "private-pending-run-b"
	pending := evalRunPendingRecord{RunID: id, Tenant: "tenant-b", Status: "running",
		DatasetID: "private-dataset-b", DatasetName: "private-pending-marker-b", DatasetVersion: 1}
	if err := s.saveEvalRunPending(ctx, pending); err != nil {
		t.Fatal(err)
	}
	key := "eval:run:pending:tenant-b:" + id
	before, err := s.jobStore.Client().Get(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	mux := bindEvalRunRoutes(t, s)
	path := "/api/v1/evals/runs/" + id
	owner := httptest.NewRecorder()
	mux.ServeHTTP(owner, tenantObjectRequest(http.MethodGet, path, "tenant-b", id))
	var got evalRunPendingRecord
	if err := json.Unmarshal(owner.Body.Bytes(), &got); err != nil || owner.Code != http.StatusAccepted || got != pending {
		t.Fatalf("owner did not read pending record: %d %s, %v", owner.Code, owner.Body.String(), err)
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, tenantObjectRequest(method, path+"?force=true", "tenant-a", id))
			if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), "private-") || strings.Contains(rr.Body.String(), "tenant-b") {
				t.Errorf("foreign pending record was not refused: %d %s", rr.Code, rr.Body.String())
			}
			if after, err := s.jobStore.Client().Get(ctx, key).Result(); err != nil || after != before {
				t.Errorf("foreign request changed pending record: %v", err)
			}
		})
	}
}
