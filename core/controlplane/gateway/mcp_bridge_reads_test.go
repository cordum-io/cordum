package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
)

const mcpReadsKeyA = "mcpreads-tenant-a-admin-key-0000000001"

// newMCPReadsBridge seeds one object of each kind the nine GetJSONFunc reads
// list for tenant-a and for tenant-b, and returns the production HTTP MCP
// service bridge with tenant-a's MCP context: a real BasicAuthProvider
// authenticated tenant-a's admin key, as mcpAuth does.
func newMCPReadsBridge(t *testing.T) (mcp.ServiceBridge, context.Context) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	enableAgentIdentityEntitlement(t, s)
	s.auth = newBasicAuthForTest(t, map[string]string{"CORDUM_API_KEYS": `[{"key":"` + mcpReadsKeyA +
		`","tenant":"tenant-a","role":"admin","principal_id":"mcpreads-a"}]`})
	req := httptest.NewRequest(http.MethodPost, "/mcp/message", nil)
	req.Header.Set("X-API-Key", mcpReadsKeyA)
	req.Header.Set("X-Tenant-ID", "tenant-a")
	ac, err := s.auth.AuthenticateHTTP(req)
	if err != nil || ac.Tenant != "tenant-a" || ac.AllowCrossTenant {
		t.Fatalf("authenticate tenant-a key = %+v, %v; want a tenant-a principal", ac, err)
	}
	seedMCPReads(t, s, "tenant-a", 2)
	seedMCPReads(t, s, "tenant-b", 3)
	return s.newMCPServiceBridge(), context.WithValue(context.Background(), auth.ContextKey{}, ac)
}

// seedMCPReads stores, for tenant, a pending job, an approval-required job,
// a workflow with one run and one timeline event, a topic, an agent and
// auditEvents chained audit events.
func seedMCPReads(t *testing.T, s *server, tenant string, auditEvents int) {
	t.Helper()
	ctx := context.Background()
	for id, state := range map[string]model.JobState{"mcpreads-job-" + tenant: model.JobStatePending, "mcpreads-approval-" + tenant: model.JobStateApproval} {
		if err := s.jobStore.SetJobMeta(ctx, &pb.JobRequest{JobId: id, Topic: "job.mcpreads", TenantId: tenant}); err != nil {
			t.Fatalf("SetJobMeta %s: %v", id, err)
		}
		if err := s.jobStore.SetState(ctx, id, state); err != nil {
			t.Fatalf("SetState %s: %v", id, err)
		}
		if err := s.jobStore.SetSafetyDecision(ctx, id, model.SafetyDecisionRecord{Decision: model.SafetyRequireApproval, ApprovalRequired: true}); err != nil {
			t.Fatalf("SetSafetyDecision %s: %v", id, err)
		}
	}
	steps := map[string]*wf.Step{"s1": {ID: "s1", Type: wf.StepTypeApproval}}
	if err := s.workflowStore.SaveWorkflow(ctx, &wf.Workflow{ID: "mcpreads-wf-" + tenant, OrgID: tenant, Name: "mcpreads", Steps: steps}); err != nil {
		t.Fatalf("SaveWorkflow %s: %v", tenant, err)
	}
	runID := "mcpreads-run-" + tenant
	if err := s.workflowStore.CreateRun(ctx, &wf.WorkflowRun{ID: runID, WorkflowID: "mcpreads-wf-" + tenant, OrgID: tenant}); err != nil {
		t.Fatalf("CreateRun %s: %v", runID, err)
	}
	if err := s.workflowStore.AppendTimelineEvent(ctx, runID, &wf.TimelineEvent{Type: "step", RunID: runID, Message: "mcpreads-event-" + tenant}); err != nil {
		t.Fatalf("AppendTimelineEvent %s: %v", runID, err)
	}
	if err := s.topicRegistry.Set(ctx, topicregistry.Registration{Name: "job.mcpreads." + tenant, TenantID: tenant, Pool: "mcpreads", Status: "active"}); err != nil {
		t.Fatalf("topic %s: %v", tenant, err)
	}
	if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{ID: "mcpreads-agent-" + tenant, TenantID: tenant,
		Name: "mcpreads-agent-" + tenant, Owner: "alice@example.com", RiskTier: "low", Status: "active"}); err != nil {
		t.Fatalf("agent %s: %v", tenant, err)
	}
	seedChain(t, s, tenant, auditEvents)
}

// TestMCPBridgeReads_TenantScoped drives each of the nine reads that the
// gateway's HTTP MCP bridge serves in-process. tenant-a's object is
// returned and tenant-b's is not; a read of tenant-b's run, or of its audit
// chain, is refused with a 403 BridgeError and no data.
func TestMCPBridgeReads_TenantScoped(t *testing.T) {
	b, ctx := newMCPReadsBridge(t)
	in := mcp.ListInput{}
	lists := []struct {
		name, key string
		read      func() (*mcp.ListPage, error)
	}{
		{"ListJobs", "jobs", func() (*mcp.ListPage, error) { return b.ListJobs(ctx, in) }},
		{"ListRuns", "runs", func() (*mcp.ListPage, error) { return b.ListRuns(ctx, in) }},
		{"ListWorkflows", "wfs", func() (*mcp.ListPage, error) { return b.ListWorkflows(ctx, in) }},
		{"ListTopics", "topics", func() (*mcp.ListPage, error) { return b.ListTopics(ctx, in) }},
		{"ListAgents", "agents", func() (*mcp.ListPage, error) { return b.ListAgents(ctx, in) }},
		{"ListPendingApprovals", "approvals", func() (*mcp.ListPage, error) { return b.ListPendingApprovals(ctx, in) }},
	}
	for _, l := range lists {
		t.Run(l.name, func(t *testing.T) {
			page, err := l.read()
			if err != nil || page == nil {
				t.Fatalf("%s = %+v, %v; want a page", l.name, page, err)
			}
			got := mcpReadsIDs(page)
			want, other := mcpReadsID(l.key, "tenant-a"), mcpReadsID(l.key, "tenant-b")
			if !slices.Contains(got, want) || slices.Contains(got, other) {
				t.Fatalf("%s ids = %q, want %q and not %q", l.name, got, want, other)
			}
		})
	}
	t.Run("GetRun", func(t *testing.T) {
		item, err := b.GetRun(ctx, "mcpreads-run-tenant-a")
		if err != nil || item == nil || item.Kind != "run" || item.Data["id"] != "mcpreads-run-tenant-a" {
			t.Fatalf("GetRun(tenant-a run) = %+v, %v; want the run", item, err)
		}
		requireMCPReadDenied(t, "GetRun(tenant-b run)")(b.GetRun(ctx, "mcpreads-run-tenant-b"))
	})
	t.Run("GetRunTimeline", func(t *testing.T) {
		item, err := b.GetRunTimeline(ctx, "mcpreads-run-tenant-a")
		events, _ := mcpReadsData(item)["events"].([]any)
		first, _ := firstOf(events).(map[string]any)
		if err != nil || item == nil || item.Kind != "run_timeline" || len(events) != 1 || first["message"] != "mcpreads-event-tenant-a" {
			t.Fatalf("GetRunTimeline(tenant-a run) = %+v, %v; want its one event", item, err)
		}
		requireMCPReadDenied(t, "GetRunTimeline(tenant-b run)")(b.GetRunTimeline(ctx, "mcpreads-run-tenant-b"))
	})
	t.Run("VerifyAudit", func(t *testing.T) {
		item, err := b.VerifyAudit(ctx, "tenant-a")
		if data := mcpReadsData(item); err != nil || item == nil || item.Kind != "audit_verify" || data["total_events"] != float64(2) {
			t.Fatalf("VerifyAudit(tenant-a) = %+v, %v; want tenant-a's 2 events", item, err)
		}
		requireMCPReadDenied(t, "VerifyAudit(tenant-b)")(b.VerifyAudit(ctx, "tenant-b"))
	})
}

func requireMCPReadDenied(t *testing.T, name string) func(*mcp.ResourceItem, error) {
	return func(item *mcp.ResourceItem, err error) {
		t.Helper()
		var berr *mcp.BridgeError
		if item != nil || !errors.As(err, &berr) || berr.StatusCode != http.StatusForbidden {
			t.Fatalf("%s = %+v, %v; want no data and a 403 BridgeError", name, item, err)
		}
	}
}

// mcpReadsID names the seeded object of kind for tenant.
func mcpReadsID(kind, tenant string) string {
	switch kind {
	case "jobs":
		return "mcpreads-job-" + tenant
	case "runs":
		return "mcpreads-run-" + tenant
	case "wfs":
		return "mcpreads-wf-" + tenant
	case "topics":
		return "job.mcpreads." + tenant
	case "agents":
		return "mcpreads-agent-" + tenant
	}
	return "mcpreads-approval-" + tenant
}

// mcpReadsIDs collects each item's id, a topic's name, or an approval's
// job id.
func mcpReadsIDs(page *mcp.ListPage) []string {
	var ids []string
	for _, item := range page.Items {
		if job, ok := item["job"].(map[string]any); ok {
			item = job
		}
		for _, key := range []string{"id", "name"} {
			if id, ok := item[key].(string); ok {
				ids = append(ids, id)
				break
			}
		}
	}
	return ids
}

func mcpReadsData(item *mcp.ResourceItem) map[string]any {
	if item == nil {
		return nil
	}
	return item.Data
}

func firstOf(items []any) any {
	if len(items) == 0 {
		return nil
	}
	return items[0]
}
