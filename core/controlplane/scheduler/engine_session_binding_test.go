package scheduler

// RED coverage for task-948d913b: subject-binding + SenderId defense-in-depth.
//
// Before this change verifySessionToken switched on the verdict but DISCARDED
// result.Claims, so a provisioned worker A holding its OWN valid token could
// publish JobResult{WorkerId:"w-victim"} / JobCancel{RequestedBy:"w-victim"}
// and drive a victim job. The gate now requires Claims.Subject == claimed
// identity AND BusPacket.SenderId == claimed identity (both owner-approved).

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func enforceVerifyEngine(t *testing.T, issuer *SessionTokenIssuer) *Engine {
	t.Helper()
	e := &Engine{sessionMiddleware: NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())}
	e.ctx, e.cancel = context.Background(), func() {}
	return e
}

func TestVerifySessionToken_SubjectMismatchRejected(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.Issue(context.Background(), "w-a", "tenant-a", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	e := enforceVerifyEngine(t, issuer)
	// SenderId matches the claimed victim id, so ONLY the token subject (w-a)
	// differs from the claimed id — isolates the Claims.Subject binding.
	packet := &pb.BusPacket{SenderId: "w-victim"}
	attachTokenForVerify(packet, token)
	if e.verifySessionToken(packet, "w-victim", "job_result") {
		t.Fatal("worker A's valid token must NOT drive w-victim's job (subject binding)")
	}
}

func TestVerifySessionToken_SenderIdMismatchRejected(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.Issue(context.Background(), "w-a", "tenant-a", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	e := enforceVerifyEngine(t, issuer)
	// Subject (w-a) matches the claimed id, but SenderId is forged.
	packet := &pb.BusPacket{SenderId: "w-evil"}
	attachTokenForVerify(packet, token)
	if e.verifySessionToken(packet, "w-a", "job_result") {
		t.Fatal("a packet whose SenderId != claimed id must be rejected (sender binding)")
	}
}

func TestVerifySessionToken_MatchingIdentityPasses(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.Issue(context.Background(), "w-ok", "tenant-ok", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	e := enforceVerifyEngine(t, issuer)
	packet := &pb.BusPacket{SenderId: "w-ok"}
	attachTokenForVerify(packet, token)
	if !e.verifySessionToken(packet, "w-ok", "job_result") {
		t.Fatal("matching Subject==SenderId==claimed id must pass")
	}
}

func TestVerifySessionToken_ServiceTokenAdmittedAndBound(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	tok, err := issuer.MintServiceToken(servicetoken.IdentityScheduler)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	e := enforceVerifyEngine(t, issuer)
	packet := &pb.BusPacket{SenderId: servicetoken.IdentityScheduler}
	attachTokenForVerify(packet, tok)
	if !e.verifySessionToken(packet, servicetoken.IdentityScheduler, "job_cancel") {
		t.Fatal("a scheduler service token on an internal broadcast must be admitted under enforce")
	}
}

func TestHandlePacket_JobResult_ForgedSubjectDoesNotComplete(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.Issue(context.Background(), "w-a", "tenant-a", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-victim"] = JobStateRunning
	store.mu.Unlock()
	eng := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	eng.sessionMiddleware = NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	// Worker A forges a SUCCEEDED result for w-victim's job using its own token.
	pkt := reparseWithTypedAuthToken(t, &pb.BusPacket{
		SenderId: "w-a",
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId: "job-victim", WorkerId: "w-victim", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
		}},
	}, token)
	if err := eng.HandlePacket(pkt); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}
	store.mu.RLock()
	got := store.states["job-victim"]
	store.mu.RUnlock()
	if got == JobStateSucceeded {
		t.Fatal("worker A's token must not drive w-victim's job to SUCCEEDED")
	}
}

func TestHandlePacket_JobCancel_SchedulerServiceTokenAdmitted(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	tok, err := issuer.MintServiceToken(servicetoken.IdentityScheduler)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-svc"] = JobStateRunning
	store.mu.Unlock()
	eng := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	eng.sessionMiddleware = NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	pkt := reparseWithTypedAuthToken(t, &pb.BusPacket{
		SenderId: servicetoken.IdentityScheduler,
		Payload:  &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{JobId: "job-svc", RequestedBy: servicetoken.IdentityScheduler, Reason: "x"}},
	}, tok)
	if err := eng.HandlePacket(pkt); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}
	store.mu.RLock()
	got := store.states["job-svc"]
	store.mu.RUnlock()
	if got != JobStateCancelled {
		t.Fatalf("scheduler service token must admit the internal cancel under enforce; got %q", got)
	}
}

func TestHandlePacket_JobCancel_TokenlessInternalRejectedUnderEnforce(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-noauth"] = JobStateRunning
	store.mu.Unlock()
	eng := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	eng.sessionMiddleware = NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	// Token-less internal broadcast must be rejected under enforce — proves we
	// did NOT add a SenderId==cordum-scheduler exemption (worker-forgeable).
	pkt := &pb.BusPacket{
		SenderId: servicetoken.IdentityScheduler,
		Payload:  &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{JobId: "job-noauth", RequestedBy: servicetoken.IdentityScheduler, Reason: "x"}},
	}
	if err := eng.HandlePacket(pkt); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}
	store.mu.RLock()
	got := store.states["job-noauth"]
	store.mu.RUnlock()
	if got == JobStateCancelled {
		t.Fatal("token-less internal cancel must be rejected under enforce (no SenderId exemption)")
	}
}

// TestReservedSchedulerIdentityMatchesDefaultSenderID is the single-source-of-
// truth guard: the scheduler stamps defaultSenderID on its internal broadcasts
// and mints a service token for that same subject, while VerifyService accepts
// only servicetoken.ReservedServiceIdentities. If the two ever diverge the
// scheduler's own broadcasts would be rejected under enforce, so pin them.
func TestReservedSchedulerIdentityMatchesDefaultSenderID(t *testing.T) {
	t.Parallel()
	if servicetoken.IdentityScheduler != defaultSenderID {
		t.Fatalf("servicetoken.IdentityScheduler %q must equal scheduler.defaultSenderID %q",
			servicetoken.IdentityScheduler, defaultSenderID)
	}
	if !servicetoken.IsReservedIdentity(defaultSenderID) {
		t.Fatalf("defaultSenderID %q must be a reserved service identity", defaultSenderID)
	}
}
