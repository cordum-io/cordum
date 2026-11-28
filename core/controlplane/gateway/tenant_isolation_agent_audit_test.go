package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestTenantIsolationGatewayAgentAuditEnrichment(t *testing.T) {
	for _, action := range []string{"approve", "reject", "http_submit", "grpc_submit"} {
		t.Run(action, func(t *testing.T) {
			runAgentAuditCase(t, action, "tenant-b")
		})
	}
}

func TestGatewayAgentAuditSameTenant(t *testing.T) {
	for _, action := range []string{"approve", "reject", "http_submit", "grpc_submit"} {
		t.Run(action, func(t *testing.T) {
			runAgentAuditCase(t, action, "tenant-a")
		})
	}
}

func runAgentAuditCase(t *testing.T, action, agentTenant string) {
	t.Helper()
	s, bus, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	agent, err := s.agentIdentityStore.Create(t.Context(), store.AgentIdentity{
		TenantID: agentTenant, Name: "audit-marker-" + agentTenant, Owner: "reader", RiskTier: "critical",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.agentIdentityStore.LinkWorker(t.Context(), agent.ID, "reader"); err != nil {
		t.Fatal(err)
	}
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	r := tenantObjectRequest(http.MethodPost, "/api/v1/jobs", "tenant-a", "")
	ctx, span := tp.Tracer("agent-audit-test").Start(r.Context(), action)
	r = r.WithContext(ctx)
	if action == "approve" || action == "reject" {
		id := seedAgentAuditJob(t, s, agent.ID)
		r.SetPathValue("job_id", id)
		rr := httptest.NewRecorder()
		approvalHandlers(s)[action](rr, r)
		if rr.Code != http.StatusOK {
			t.Fatalf("approval status = %d: %s", rr.Code, rr.Body.String())
		}
	} else {
		submitAgentAuditJob(t, s, bus, r, action, agent)
	}
	span.End()
	assertAgentAuditIdentity(t, s, exporter, action, agent)
}

func seedAgentAuditJob(t *testing.T, s *server, agentID string) string {
	t.Helper()
	ctx := t.Context()
	id := seedApprovalJob(t, s, "")
	req, err := s.jobStore.GetJobRequest(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	req.TenantId = "tenant-a"
	req.Labels = map[string]string{"agent_id": agentID}
	if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	hash, err := scheduler.HashJobRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.jobStore.SetSafetyDecision(ctx, id, model.SafetyDecisionRecord{
		Decision: model.SafetyRequireApproval, ApprovalRequired: true, PolicySnapshot: "snap-test", JobHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func submitAgentAuditJob(t *testing.T, s *server, bus *stubBus, r *http.Request, action string, agent *store.AgentIdentity) {
	t.Helper()
	before := tenantObjectSnapshot(t, s)
	labels := map[string]string{"agent_id": agent.ID}
	sameTenant := agent.TenantID == "tenant-a"
	if action == "grpc_submit" {
		_, err := s.SubmitJob(r.Context(), &pb.SubmitJobRequest{
			Prompt: "hello", Topic: "job.test", OrgId: "tenant-a", Labels: labels,
		})
		want := codes.PermissionDenied
		if sameTenant {
			want = codes.OK
		}
		if status.Code(err) != want {
			t.Fatalf("submit code = %v, want %v: %v", status.Code(err), want, err)
		}
	} else {
		body, err := json.Marshal(submitJobRequest{Prompt: "hello", Topic: "job.test", OrgId: "tenant-a", Labels: labels})
		if err != nil {
			t.Fatal(err)
		}
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		rr := httptest.NewRecorder()
		s.handleSubmitJobHTTP(rr, r)
		want := http.StatusForbidden
		if sameTenant {
			want = http.StatusOK
		}
		if rr.Code != want {
			t.Fatalf("submit status = %d, want %d: %s", rr.Code, want, rr.Body.String())
		}
	}
	if !sameTenant && (len(bus.published) != 0 || !reflect.DeepEqual(before, tenantObjectSnapshot(t, s))) {
		t.Fatal("refused submit changed stored state or published a message")
	}
}

func assertAgentAuditIdentity(t *testing.T, s *server, exporter *tracetest.InMemoryExporter, action string, agent *store.AgentIdentity) {
	t.Helper()
	entries, err := s.loadPolicyAudit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	sameTenant := agent.TenantID == "tenant-a"
	wantName, wantRisk := agent.ID, ""
	if sameTenant {
		wantName, wantRisk = agent.Name, agent.RiskTier
	}
	wantAction := action
	if strings.HasSuffix(action, "submit") {
		wantAction = "submit"
	}
	n := 0
	for _, entry := range entries {
		if entry.Action == wantAction {
			n++
			if entry.AgentID != agent.ID || entry.AgentName != wantName || entry.AgentRiskTier != wantRisk {
				t.Errorf("audit identity = (%q, %q, %q), want (%q, %q, %q)", entry.AgentID, entry.AgentName, entry.AgentRiskTier, agent.ID, wantName, wantRisk)
			}
		}
	}
	wantCount := 1
	if !sameTenant && wantAction == "submit" {
		wantCount = 0
	}
	if n != wantCount {
		t.Errorf("audit count = %d, want %d", n, wantCount)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("span count = %d, want 1", len(spans))
	}
	if !sameTenant {
		assertAgentAuditMarkersAbsent(t, entries, spans, agent)
	}
	if sameTenant && action == "http_submit" {
		assertAgentAuditSpan(t, spans[0], agent)
	}
}

func assertAgentAuditMarkersAbsent(t *testing.T, entries []policybundles.PolicyAuditEntry, spans tracetest.SpanStubs, agent *store.AgentIdentity) {
	t.Helper()
	data, err := json.Marshal(struct {
		Audit []policybundles.PolicyAuditEntry
		Spans tracetest.SpanStubs
	}{entries, spans})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), agent.Name) || strings.Contains(string(data), agent.RiskTier) {
		t.Error("audit or span contains identity markers outside the job tenant")
	}
}

func assertAgentAuditSpan(t *testing.T, span tracetest.SpanStub, agent *store.AgentIdentity) {
	t.Helper()
	attrs := make(map[string]string)
	for _, attr := range span.Attributes {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	for key, want := range map[string]string{
		"cordum.agent_id": agent.ID, "cordum.agent_name": agent.Name, "cordum.agent_risk_tier": agent.RiskTier,
	} {
		if attrs[key] != want {
			t.Errorf("span %s = %q, want %q", key, attrs[key], want)
		}
	}
}
