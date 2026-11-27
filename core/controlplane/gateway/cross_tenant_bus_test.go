package gateway

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	wf "github.com/cordum/cordum/core/workflow"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func crossTenantGatewayBus(t *testing.T) (*server, *stubBus, *scheduler.SessionTokenIssuer) {
	t.Helper()
	t.Setenv("CORDUM_CAP_PROFILE", "compat")
	t.Setenv("CORDUM_SDK_HANDSHAKE", "enforce")
	s, b, _ := newTestGateway(t)
	issuer, resolver := newTrustResolverForTest(t, s)
	s.sessionIssuer = issuer
	s.handshakeMode = scheduler.HandshakeModeEnforce
	s.WithTrustResolver(resolver)
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh); s.stopWorkerExpiry() })
	if err := s.startBusTaps(); err != nil {
		t.Fatal(err)
	}
	return s, b, issuer
}

// docs/sdk/handshake.md: off has no authenticated responder/session authority.
// Gateway has no CAP-PRODUCTION profile; these subscriptions are compat/enforce.
func TestCrossTenantBusGatewaySubjectManifest(t *testing.T) {
	_, b, _ := crossTenantGatewayBus(t)
	manifest := map[string]string{
		capsdk.SubjectHeartbeat: "pending PR1-02-BUS",
		capsdk.SubjectDLQ:       "pending PR1-02-BUS",
		"sys.job.>":             "pending PR1-02-BUS",
		"sys.audit.>":           "covered TestCrossTenantBusGatewayAuditStreamCompat",
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for subject := range b.subs {
		if manifest[subject] == "" {
			t.Errorf("unclassified subject: %s", subject)
		}
	}
	counts := map[string]int{}
	for subject, class := range manifest {
		kind, reason, _ := strings.Cut(class, " ")
		if !slices.Contains([]string{"covered", "global", "pending"}, kind) || reason == "" {
			t.Errorf("subject %s has no class with a reason or row: %q", subject, class)
		}
		counts[kind]++
		if len(b.subs[subject]) != 1 {
			t.Errorf("subject %s handlers=%d want=1", subject, len(b.subs[subject]))
		}
	}
	t.Logf("subjects=%d covered=%d global=%d pending=%d", len(b.subs), counts["covered"], counts["global"], counts["pending"])
}

// crossTenantGatewayHandler returns the one handler startBusTaps recorded for subject.
func crossTenantGatewayHandler(t *testing.T, b *stubBus, subject string) func(*pb.BusPacket) error {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs[subject]) != 1 {
		t.Fatalf("subject %s handlers=%d want=1", subject, len(b.subs[subject]))
	}
	return b.subs[subject][0]
}

func crossTenantGatewayPublished(b *stubBus) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

// crossTenantGatewayKeyspace reads every key of the gateway's Redis DB (the job,
// workflow, DLQ and result stores share it), so a case proves that nothing
// changed rather than only the keys it expects.
func crossTenantGatewayKeyspace(t *testing.T, s *server) map[string]string {
	t.Helper()
	ctx := context.Background()
	c := s.jobStore.Client()
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		kind, err := c.Type(ctx, key).Result()
		var value any
		switch {
		case err != nil:
		case kind == "string":
			value, err = c.Get(ctx, key).Result()
		case kind == "hash":
			value, err = c.HGetAll(ctx, key).Result()
		case kind == "list":
			value, err = c.LRange(ctx, key, 0, -1).Result()
		case kind == "set":
			var members []string
			members, err = c.SMembers(ctx, key).Result()
			slices.Sort(members)
			value = members
		case kind == "zset":
			value, err = c.ZRangeWithScores(ctx, key, 0, -1).Result()
		default:
			err = fmt.Errorf("unhandled redis type %s", kind)
		}
		if err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		out[key] = kind + "=" + fmt.Sprint(value)
	}
	return out
}

// crossTenantGatewayVictim seeds tenant B's workflow run with its worker step
// dispatched, records the step's job as tenant B's, and returns the job id.
func crossTenantGatewayVictim(t *testing.T, s *server, b *stubBus) string {
	t.Helper()
	ctx := context.Background()
	s.workflowEng = wf.NewEngine(s.workflowStore, b).WithMemory(s.memStore)
	def := &wf.Workflow{ID: "wf-tenant-b", OrgID: "tenant-b", Steps: map[string]*wf.Step{
		"step": {ID: "step", Type: wf.StepTypeWorker, Topic: "job.default"},
	}}
	if err := s.workflowStore.SaveWorkflow(ctx, def); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	run := &wf.WorkflowRun{ID: "run-tenant-b", WorkflowID: def.ID, OrgID: "tenant-b",
		Steps: map[string]*wf.StepRun{}, Status: wf.RunStatusPending, CreatedAt: now, UpdatedAt: now}
	if err := s.workflowStore.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if err := s.workflowEng.StartRun(ctx, def.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	run, err := s.workflowStore.GetRun(ctx, run.ID)
	if err != nil || run == nil || run.Steps["step"] == nil || run.Steps["step"].JobID == "" {
		t.Fatalf("tenant B's step was not dispatched: %v", err)
	}
	job := run.Steps["step"].JobID
	if err := s.jobStore.SetJobMeta(ctx, &pb.JobRequest{JobId: job, Topic: "job.default", TenantId: "tenant-b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.jobStore.SetTopic(ctx, job, "job.default"); err != nil {
		t.Fatal(err)
	}
	if err := s.jobStore.SetState(ctx, job, model.JobStateRunning); err != nil {
		t.Fatal(err)
	}
	return job
}

// crossTenantGatewayEnvelope attaches token and returns p as the bus would
// deliver it: marshalled, unmarshalled and valid under the CAP envelope rules.
func crossTenantGatewayEnvelope(t *testing.T, p *pb.BusPacket, token string) *pb.BusPacket {
	t.Helper()
	p.AuthToken, p.ProtocolVersion, p.CreatedAt = token, 1, timestamppb.Now()
	raw, err := proto.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	out := &pb.BusPacket{}
	if err := proto.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
	if err := capsdk.ValidateBusPacket(out); err != nil {
		t.Fatalf("invalid envelope fixture: %v", err)
	}
	return out
}

// crossTenantGatewaySigned gives p the real bound worker session of tenant's
// worker and checks that the token verifies to that worker and tenant.
func crossTenantGatewaySigned(t *testing.T, issuer *scheduler.SessionTokenIssuer, tenant string, p *pb.BusPacket) *pb.BusPacket {
	t.Helper()
	worker := "worker-" + tenant
	token, _, err := issuer.IssueBound(context.Background(), scheduler.SessionBinding{
		WorkerID: worker, Tenant: tenant, AgentID: worker + "-agent", ProofKeyID: worker + "-proof",
		Audience: scheduler.WorkerHandshakeAudience, SDKVersion: "go/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := crossTenantGatewayEnvelope(t, p, token)
	claims, err := issuer.VerifyBound(context.Background(), out.GetAuthToken(), true)
	if err != nil || claims.Subject != worker || claims.Tenant != tenant {
		t.Fatalf("invalid bound session fixture: %v", err)
	}
	return out
}

// crossTenantGatewayService gives p the control-plane service token of identity.
func crossTenantGatewayService(t *testing.T, issuer *scheduler.SessionTokenIssuer, identity string, p *pb.BusPacket) *pb.BusPacket {
	t.Helper()
	token, err := issuer.MintServiceToken(identity)
	if err != nil {
		t.Fatal(err)
	}
	return crossTenantGatewayEnvelope(t, p, token)
}

// crossTenantGatewayAlert has the shape core/audit/nats.go publishes on
// sys.audit.export: a system alert whose details name the event's tenant.
func crossTenantGatewayAlert(sender, marker string) *pb.BusPacket {
	return &pb.BusPacket{SenderId: sender, TraceId: "cross-tenant-gateway-" + marker,
		Payload: &pb.BusPacket_Alert{Alert: &pb.SystemAlert{
			Severity: pb.AlertSeverity_ALERT_SEVERITY_INFO, Message: marker, SourceComponent: "audit-export",
			Details: map[string]string{"event_type": "cross_tenant_probe", "tenant_id": "tenant-b"},
		}}}
}

// crossTenantGatewayNoEffect delivers tenant A's packet through the recorded
// handler and checks effects, not the returned error: every Redis key is
// unchanged, nothing is published, and no listed listener gets a stream event.
func crossTenantGatewayNoEffect(t *testing.T, s *server, b *stubBus, handler func(*pb.BusPacket) error, p *pb.BusPacket, listeners ...*wsClient) {
	t.Helper()
	before, published := crossTenantGatewayKeyspace(t, s), crossTenantGatewayPublished(b)
	_ = handler(p)
	if !maps.Equal(before, crossTenantGatewayKeyspace(t, s)) {
		t.Error("tenant A's packet changed stored state of tenant B")
	}
	if got := crossTenantGatewayPublished(b); got != published {
		t.Errorf("tenant A's packet published %d messages", got-published)
	}
	for _, listener := range listeners {
		assertNoGatewayEdgeStreamEvent(t, listener, "tenant A's packet reached a tenant stream listener")
	}
}

// crossTenantGatewayAwait waits for a stream event carrying marker.
func crossTenantGatewayAwait(t *testing.T, client *wsClient, marker string) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-client.ch:
			if strings.Contains(string(event.data), marker) {
				return
			}
		case <-deadline:
			t.Fatalf("owner control: no stream event carrying %q", marker)
		}
	}
}

// TestCrossTenantBusGatewayAuditStreamCompat: tenantForBusPacket (helpers.go)
// resolves no tenant for an audit alert, and the broadcast loop gives a
// tenantless event to no tenant-scoped listener. Tenant A's alert naming tenant
// B reaches neither tenant's listener and changes nothing; the control-plane
// alert then reaches the cross-tenant listener, so the relay is live.
func TestCrossTenantBusGatewayAuditStreamCompat(t *testing.T) {
	s, b, issuer := crossTenantGatewayBus(t)
	crossTenantGatewayVictim(t, s, b)
	handler := crossTenantGatewayHandler(t, b, "sys.audit.>")
	listenA := registerGatewayEdgeStreamClient(t, s, "tenant-a", false)
	listenB := registerGatewayEdgeStreamClient(t, s, "tenant-b", false)
	admin := registerGatewayEdgeStreamClient(t, s, "", true)
	forged := crossTenantGatewaySigned(t, issuer, "tenant-a", crossTenantGatewayAlert("worker-tenant-a", "tenant-a-alert"))
	crossTenantGatewayNoEffect(t, s, b, handler, forged, listenA, listenB)
	owner := crossTenantGatewayService(t, issuer, servicetoken.IdentityGateway,
		crossTenantGatewayAlert(servicetoken.IdentityGateway, "owner-control-alert"))
	if err := handler(owner); err != nil {
		t.Fatal(err)
	}
	crossTenantGatewayAwait(t, admin, "owner-control-alert")
	assertNoGatewayEdgeStreamEvent(t, listenA, "tenant A got a tenantless audit alert")
	assertNoGatewayEdgeStreamEvent(t, listenB, "tenant B got a tenantless audit alert")
}
