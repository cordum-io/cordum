package scheduler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	businfra "github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

type crossTenantSubjectBus struct {
	fakeBus
	recording recordingBus
	handlers  map[string]func(context.Context, *pb.BusPacket) error
}

func (b *crossTenantSubjectBus) Subscribe(subject, queue string, handler func(*pb.BusPacket) error) error {
	return b.SubscribeWithContext(subject, queue, func(_ context.Context, p *pb.BusPacket) error { return handler(p) })
}

func (b *crossTenantSubjectBus) SubscribeWithContext(subject, queue string, handler func(context.Context, *pb.BusPacket) error) error {
	if b.handlers[subject] != nil {
		return fmt.Errorf("duplicate subscription: %s", subject)
	}
	b.handlers[subject] = handler
	return b.recording.Subscribe(subject, queue, nil)
}

func crossTenantWorkerCache() *WorkerCredentialCache {
	cache := NewWorkerCredentialCache(nil)
	cache.list = func(context.Context) ([]workercredentials.Credential, error) {
		var records []workercredentials.Credential
		for _, tenant := range []string{"tenant-a", "tenant-b"} {
			worker := "worker-" + tenant
			records = append(records, workercredentials.Credential{
				WorkerID: worker, TenantID: tenant, AgentID: worker + "-agent", ProofKeyID: worker + "-proof",
				AllowedPools: []string{"default"}, AllowedTopics: []string{"job.test"},
			})
		}
		return records, nil
	}
	return cache
}

func crossTenantStartScheduler(t *testing.T, production bool) (*crossTenantSubjectBus, *MemoryRegistry, *SessionTokenIssuer) {
	t.Helper()
	var jobStore model.JobStore = newFakeJobStore()
	if production {
		_, jobStore, _, _, _, _ = productionResultFixture(t)
	}
	return crossTenantScheduler(t, production, jobStore)
}

func crossTenantScheduler(t *testing.T, production bool, jobStore model.JobStore) (*crossTenantSubjectBus, *MemoryRegistry, *SessionTokenIssuer) {
	t.Helper()
	profile := "compat"
	if production {
		profile = "production"
	}
	t.Setenv("CORDUM_CAP_PROFILE", profile)
	t.Setenv("CORDUM_SDK_HANDSHAKE", "enforce")
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	t.Cleanup(cleanup)
	registry := newTestRegistry(t)
	b := &crossTenantSubjectBus{handlers: make(map[string]func(context.Context, *pb.BusPacket) error)}
	e := NewEngine(b, NewSafetyBasic(), registry, NewNaiveStrategy(), jobStore, nil).
		WithWorkerCredentialCache(crossTenantWorkerCache()).
		WithSessionMiddleware(NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())).
		WithProductionIdentityEnforcement(production)
	t.Cleanup(e.Stop)
	if err := e.Start(); err != nil {
		t.Fatal(err)
	}
	return b, registry, issuer
}

// docs/sdk/handshake.md: off has no authenticated responder/session authority.
// These cases instead exercise compat/enforce and production/enforce.
func TestCrossTenantBusSchedulerSubjectManifest(t *testing.T) {
	manifest := map[string]string{
		capsdk.SubjectSubmit:        "covered TestCrossTenantBusSubmitCompat TestCrossTenantBusSubmitProduction: reserved-service submission only; all tenant sessions refused",
		capsdk.SubjectResult:        "covered TestCompatJobPacketsStayInTheSendersTenant TestCrossTenantBusResultProduction",
		capsdk.SubjectCancel:        "pending PR1-02-BUS",
		capsdk.SubjectProgress:      "covered TestCrossTenantBusProgressCompat (no-op) TestCrossTenantBusProgressProduction",
		capsdk.SubjectHeartbeat:     "covered TestCrossTenantBusHeartbeatCompat TestCrossTenantBusHeartbeatProduction",
		capsdk.SubjectHandshake:     "covered TestCrossTenantBusHandshakeCompat TestCrossTenantBusHandshakeProduction",
		capsdk.SubjectConfigChanged: "global: only system/workers invalidation is consumed; payload supplies no tenant record or config value",
	}
	seen := make(map[string]bool)
	for _, production := range []bool{false, true} {
		b, _, _ := crossTenantStartScheduler(t, production)
		for _, subscription := range b.recording.subs {
			seen[subscription.subject] = true
			if manifest[subscription.subject] == "" {
				t.Errorf("unclassified subject: %s", subscription.subject)
			}
		}
	}
	for subject, class := range manifest {
		if !seen[subject] {
			t.Errorf("stale subject: %s (%s)", subject, class)
		}
	}
	t.Logf("subjects=%d covered=5 global=1 pending=1", len(seen))
}

func crossTenantRegistryEntry(registry *MemoryRegistry) *workerEntry {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	entry := *registry.workers["worker-tenant-b"]
	if entry.hb != nil {
		entry.hb = proto.Clone(entry.hb).(*pb.Heartbeat)
	}
	if entry.handshake != nil {
		entry.handshake = proto.Clone(entry.handshake).(*pb.Handshake)
	}
	entry.readyTopics = append([]string(nil), entry.readyTopics...)
	return &entry
}

func crossTenantRegistryPacket(t *testing.T, issuer *SessionTokenIssuer, tenant, subject string) *pb.BusPacket {
	t.Helper()
	worker := "worker-" + tenant
	token, _, err := issuer.IssueBound(context.Background(), boundTestBinding(worker, tenant, "go/test"))
	if err != nil {
		t.Fatal(err)
	}
	p := &pb.BusPacket{SenderId: "worker-tenant-b"}
	if subject == capsdk.SubjectHeartbeat {
		p.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{
			WorkerId: "worker-tenant-b", Pool: "default", ActiveJobs: 7,
		}}
	} else {
		p.Payload = &pb.BusPacket_Handshake{Handshake: capabilityHandshake("worker-tenant-b", "go/test", "job.test")}
	}
	p = reparseWithTypedAuthToken(t, completeSecurityTestEnvelope(p), token)
	claims, err := issuer.VerifyBound(context.Background(), p.GetAuthToken(), true)
	if err != nil || claims.Tenant != tenant || claims.Subject != worker {
		t.Fatalf("invalid session fixture: %v", err)
	}
	return p
}

func crossTenantRegistryCase(t *testing.T, production bool, subject string) {
	t.Helper()
	b, registry, issuer := crossTenantStartScheduler(t, production)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-tenant-b", Pool: "default", ActiveJobs: 1})
	registry.UpdateHandshakeTrust(capabilityHandshake("worker-tenant-b", "go/test", "job.before"), true)
	before := crossTenantRegistryEntry(registry)
	handler := b.handlers[subject]
	if handler == nil {
		t.Fatal("subject not subscribed")
	}
	if err := handler(context.Background(), crossTenantRegistryPacket(t, issuer, "tenant-a", subject)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, crossTenantRegistryEntry(registry)) {
		t.Error("other tenant changed the victim registry entry")
	}
	if len(b.snapshotPublished()) != 0 {
		t.Error("other tenant published an effect")
	}
	if err := handler(context.Background(), crossTenantRegistryPacket(t, issuer, "tenant-b", subject)); err != nil {
		t.Fatal(err)
	}
	after := crossTenantRegistryEntry(registry)
	if subject == capsdk.SubjectHeartbeat {
		if after.hb.GetActiveJobs() != 7 {
			t.Error("owner heartbeat did not update the registry")
		}
	} else if !after.handshakeTrusted || !reflect.DeepEqual(after.readyTopics, []string{"job.test"}) {
		t.Error("owner handshake did not update trusted readiness")
	}
}

func TestCrossTenantBusHeartbeatCompat(t *testing.T) {
	crossTenantRegistryCase(t, false, capsdk.SubjectHeartbeat)
}

func TestCrossTenantBusHeartbeatProduction(t *testing.T) {
	crossTenantRegistryCase(t, true, capsdk.SubjectHeartbeat)
}

func TestCrossTenantBusHandshakeCompat(t *testing.T) {
	crossTenantRegistryCase(t, false, capsdk.SubjectHandshake)
}

func TestCrossTenantBusHandshakeProduction(t *testing.T) {
	crossTenantRegistryCase(t, true, capsdk.SubjectHandshake)
}

func crossTenantProductionJob(t *testing.T) (*store.RedisJobStore, *miniredis.Miniredis, *pb.IdentityBinding, *pb.DispatchIdentity) {
	t.Helper()
	srv := miniredis.RunT(t)
	s, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx := context.Background()
	identity := &pb.IdentityBinding{TenantId: "tenant-b", PrincipalId: "requester-b", ActorId: "requester-b"}
	for _, err := range []error{
		s.SetState(ctx, "job-b", JobStatePending), s.SetTopic(ctx, "job-b", "job.test"), s.SetTenant(ctx, "job-b", "tenant-b"),
		s.SetJobRequest(ctx, &pb.JobRequest{JobId: "job-b", Topic: "job.test", TenantId: "tenant-b", PrincipalId: "requester-b", Identity: identity}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	id, attempt, err := s.BeginDispatch(ctx, "job-b", "worker-tenant-b", "tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(ctx, "job-b", JobStateRunning); err != nil {
		t.Fatal(err)
	}
	return s, srv, identity, &pb.DispatchIdentity{DispatchId: id, Attempt: uint64(attempt), AssignedWorkerId: "worker-tenant-b"}
}

func crossTenantJobPacket(t *testing.T, issuer *SessionTokenIssuer, tenant, subject string, identity *pb.IdentityBinding, dispatch *pb.DispatchIdentity) *pb.BusPacket {
	t.Helper()
	worker := "worker-" + tenant
	token, _, err := issuer.IssueBound(context.Background(), boundTestBinding(worker, tenant, "go/test"))
	if err != nil {
		t.Fatal(err)
	}
	p := &pb.BusPacket{SenderId: worker, Identity: identity,
		SignatureMetadata: &pb.SignatureMetadata{MessageId: []byte(tenant + "-event01")}}
	switch subject {
	case capsdk.SubjectResult:
		p.Payload = &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: "job-b", WorkerId: worker,
			Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "redis://result-b", Identity: identity, Dispatch: dispatch}}
	case capsdk.SubjectCancel:
		p.Payload = &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{JobId: "job-b", RequestedBy: worker, Identity: identity, Dispatch: dispatch}}
	case capsdk.SubjectProgress:
		p.Payload = &pb.BusPacket_JobProgress{JobProgress: &pb.JobProgress{JobId: "job-b", Identity: identity, Dispatch: dispatch}}
	default:
		t.Fatalf("unsupported subject: %s", subject)
	}
	return reparseWithTypedAuthToken(t, completeSecurityTestEnvelope(p), token)
}

// Supply transport evidence through the real bus context carrier, not a forged
// context key. The engine's session/tenant checks are the boundary under test;
// raw signature validation has its own production admission tests.
func crossTenantJobTransport(t *testing.T, issuer *SessionTokenIssuer, subject string, handler func(context.Context, *pb.BusPacket) error) (*businfra.NatsBus, <-chan error) {
	t.Helper()
	t.Setenv("CORDUM_ENV", "development")
	t.Setenv("CORDUM_PRODUCTION", "false")
	t.Setenv("NATS_USE_JETSTREAM", "false")
	ns := startProductionAdmissionNATS(t)
	transport, err := businfra.NewNatsBus(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(transport.Close)
	if err := transport.SetRawPacketAdmission(func(ctx context.Context, actual string, raw []byte) businfra.RawAdmissionResult {
		p := &pb.BusPacket{}
		if err := proto.Unmarshal(raw, p); err != nil {
			return businfra.RawAdmissionResult{Disposition: businfra.RawAdmissionRejected}
		}
		claims, err := issuer.VerifyBound(ctx, p.GetAuthToken(), true)
		if err != nil {
			return businfra.RawAdmissionResult{Disposition: businfra.RawAdmissionRejected}
		}
		digest := sha256.Sum256(raw)
		authority := &businfra.RawAdmissionAuthority{ActualSubject: actual, TenantID: claims.Tenant,
			SessionSubject: claims.Subject, Identity: p.GetIdentity(),
			MessageID: p.GetSignatureMetadata().GetMessageId(), UnsignedDigest: digest[:]}
		return businfra.RawAdmissionResult{Disposition: businfra.RawAdmissionAccepted, Packet: p, Authority: authority}
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	if err := transport.SubscribeWithContext(subject, "", func(ctx context.Context, p *pb.BusPacket) error {
		err := handler(ctx, p)
		done <- err
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return transport, done
}

func crossTenantDeliverJob(t *testing.T, transport *businfra.NatsBus, done <-chan error, subject string, p *pb.BusPacket) {
	t.Helper()
	if err := transport.Publish(subject, p); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("registered handler did not finish")
	}
}

func crossTenantProductionCase(t *testing.T, subject string, state JobState) {
	t.Helper()
	t.Setenv("CORDUM_CAP_PROFILE", "production")
	t.Setenv("CORDUM_SDK_HANDSHAKE", "enforce")
	s, srv, identity, dispatch := crossTenantProductionJob(t)
	b, _, issuer := crossTenantScheduler(t, true, s)
	handler := b.handlers[subject]
	if handler == nil {
		t.Fatal("subject not subscribed")
	}
	transport, done := crossTenantJobTransport(t, issuer, subject, handler)
	before := srv.Dump()
	crossTenantDeliverJob(t, transport, done, subject, crossTenantJobPacket(t, issuer, "tenant-a", subject, identity, dispatch))
	if got := srv.Dump(); got != before {
		t.Error("other tenant changed the victim store")
	}
	if len(b.snapshotPublished()) != 0 {
		t.Error("other tenant published an effect")
	}
	crossTenantDeliverJob(t, transport, done, subject, crossTenantJobPacket(t, issuer, "tenant-b", subject, identity, dispatch))
	if got, err := s.GetState(context.Background(), "job-b"); err != nil || got != state {
		t.Errorf("owner state=%s want=%s err=%v", got, state, err)
	}
	crossTenantOwnerEffects(t, b.snapshotPublished(), subject)
}

func crossTenantOwnerEffects(t *testing.T, published []publishedMsg, subject string) {
	t.Helper()
	count, accepted := 1, capsdk.SubjectAcceptedResult
	if subject == capsdk.SubjectCancel {
		count = 2 // processDurableFailure also emits the cancelled job to DLQ.
	}
	if subject == capsdk.SubjectProgress {
		accepted = capsdk.SubjectAcceptedProgress
	}
	if len(published) != count {
		t.Fatalf("owner publications=%d want=%d", len(published), count)
	}
	if published[0].subject != accepted || published[0].packet.GetIdentity().GetTenantId() != "tenant-b" {
		t.Error("owner did not publish the tenant-B accepted event")
	}
	if subject == capsdk.SubjectCancel && published[1].subject != capsdk.SubjectDLQ {
		t.Error("owner cancel did not publish DLQ event")
	}
}

func TestCrossTenantBusResultProduction(t *testing.T) {
	crossTenantProductionCase(t, capsdk.SubjectResult, JobStateSucceeded)
}

func TestCrossTenantBusCancelProduction(t *testing.T) {
	crossTenantProductionCase(t, capsdk.SubjectCancel, JobStateCancelled)
}

func TestCrossTenantBusProgressProduction(t *testing.T) {
	crossTenantProductionCase(t, capsdk.SubjectProgress, JobStateRunning)
}

func TestCrossTenantBusProgressCompat(t *testing.T) {
	s, expected := compatTenantJob(t, "tenant-b"), compatTenantJob(t, "tenant-b")
	b, _, issuer := crossTenantScheduler(t, false, s)
	// engine.go's JobProgress branch returns without effects outside production.
	// This is a no-op control for both tenants, not an isolation-enforcement claim.
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		identity := &pb.IdentityBinding{TenantId: "tenant-b", PrincipalId: "requester-b", ActorId: "requester-b"}
		p := crossTenantJobPacket(t, issuer, tenant, capsdk.SubjectProgress, identity, nil)
		if !reflect.DeepEqual(s, expected) {
			t.Fatal("fixture differs before handler delivery")
		}
		if err := b.handlers[capsdk.SubjectProgress](context.Background(), p); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(s, expected) || len(b.snapshotPublished()) != 0 {
			t.Errorf("compat progress from %s had an effect", tenant)
		}
	}
}

func crossTenantSubmitPacket(t *testing.T, issuer *SessionTokenIssuer, identity *pb.IdentityBinding, owner bool) *pb.BusPacket {
	t.Helper()
	sender := "worker-tenant-a"
	token, _, err := issuer.IssueBound(context.Background(), boundTestBinding(sender, "tenant-a", "go/test"))
	if owner {
		sender = servicetoken.IdentityGateway
		token, err = issuer.MintServiceToken(sender)
	}
	if err != nil {
		t.Fatal(err)
	}
	p := &pb.BusPacket{SenderId: sender, Identity: identity, Payload: &pb.BusPacket_JobRequest{JobRequest: &pb.JobRequest{
		JobId: "submit-b", Topic: "job.test", TenantId: "tenant-b", PrincipalId: "requester-b", Identity: identity,
	}}}
	return reparseWithTypedAuthToken(t, completeSecurityTestEnvelope(p), token)
}

func crossTenantSubmitCase(t *testing.T, production bool) {
	t.Helper()
	s, srv, identity, _ := crossTenantProductionJob(t)
	b, registry, issuer := crossTenantScheduler(t, production, s)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-tenant-b", Pool: "default"})
	registry.UpdateHandshakeTrust(capabilityHandshake("worker-tenant-b", "go/test", "job.test"), true)
	handler := b.handlers[capsdk.SubjectSubmit]
	if handler == nil {
		t.Fatal("submit subject not subscribed")
	}
	before := srv.Dump()
	if err := handler(context.Background(), crossTenantSubmitPacket(t, issuer, identity, false)); err != nil {
		t.Fatal(err)
	}
	if srv.Dump() != before || len(b.snapshotPublished()) != 0 {
		t.Error("tenant-bound submit changed B's store or published an effect")
	}
	// Only reserved services can submit in enforce; the gateway carries B's identity.
	if err := handler(context.Background(), crossTenantSubmitPacket(t, issuer, identity, true)); err != nil {
		t.Fatal(err)
	}
	req, err := s.GetJobRequest(context.Background(), "submit-b")
	if err != nil || req.GetTenantId() != "tenant-b" || req.GetJobId() != "submit-b" {
		t.Fatalf("gateway owner control did not create B's job: %v", err)
	}
	if got, err := s.GetState(context.Background(), "submit-b"); err != nil || got != JobStateRunning {
		t.Errorf("owner submit state=%s want=RUNNING err=%v", got, err)
	}
	published := b.snapshotPublished()
	if len(published) != 1 || published[0].packet.GetJobRequest().GetJobId() != "submit-b" {
		t.Error("gateway owner control did not dispatch B's job")
	}
}

func TestCrossTenantBusSubmitCompat(t *testing.T) {
	crossTenantSubmitCase(t, false)
}

func TestCrossTenantBusSubmitProduction(t *testing.T) {
	crossTenantSubmitCase(t, true)
}
