package scheduler

// End-to-end (single peer-scheduler) verification for task-948d913b. Under
// CORDUM_SDK_HANDSHAKE=enforce a peer scheduler ADMITS internal broadcasts from
// all three control-plane producers (scheduler / api-gateway / workflow-engine)
// carrying a service token, and REJECTS a worker-forged result/cancel that uses
// a valid worker token to claim a victim (or a reserved-service) identity.
//
// All three producers and the verifier share one Ed25519 signing key in a
// self-hosted deployment, modeled here by minting service tokens with the test
// issuer's own key (the same key its trust store validates).

import (
	"context"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestEnforce_CrossServiceBroadcastMatrix(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	newEnforceEngine := func(jobID string) (*Engine, *fakeJobStore) {
		store := newFakeJobStore()
		store.mu.Lock()
		store.states[jobID] = JobStateRunning
		store.mu.Unlock()
		eng := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
		eng.sessionMiddleware = NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
		return eng, store
	}
	stateOf := func(s *fakeJobStore, id string) JobState {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.states[id]
	}
	mint := func(identity string) string {
		tok, err := servicetoken.MintService(issuer.privateKey, issuer.keyID, identity, time.Now())
		if err != nil {
			t.Fatalf("mint %s: %v", identity, err)
		}
		return tok
	}

	// (1) Each reserved control-plane identity's internal cancel is ADMITTED.
	for _, id := range []string{servicetoken.IdentityScheduler, servicetoken.IdentityGateway, servicetoken.IdentityWorkflow} {
		jobID := "job-" + id
		eng, store := newEnforceEngine(jobID)
		pkt := reparseWithTypedAuthToken(t, &pb.BusPacket{
			SenderId: id,
			Payload:  &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{JobId: jobID, RequestedBy: id, Reason: "control-plane cancel"}},
		}, mint(id))
		if err := eng.HandlePacket(pkt); err != nil {
			t.Fatalf("[%s] HandlePacket: %v", id, err)
		}
		if got := stateOf(store, jobID); got != JobStateCancelled {
			t.Fatalf("[%s] internal cancel must be ADMITTED under enforce; job state = %q", id, got)
		}
	}

	// (2) A worker-forged result (valid worker token for w-a, claiming w-victim)
	// is REJECTED — worker A cannot drive worker B's job.
	workerTok, _, err := issuer.Issue(context.Background(), "w-a", "tenant-a", "v1")
	if err != nil {
		t.Fatalf("issue worker token: %v", err)
	}
	engF, storeF := newEnforceEngine("job-victim")
	forged := reparseWithTypedAuthToken(t, &pb.BusPacket{
		SenderId: "w-a",
		Payload:  &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: "job-victim", WorkerId: "w-victim", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED}},
	}, workerTok)
	if err := engF.HandlePacket(forged); err != nil {
		t.Fatalf("HandlePacket(forged): %v", err)
	}
	if got := stateOf(storeF, "job-victim"); got == JobStateSucceeded {
		t.Fatal("worker A's token must NOT drive w-victim's job to SUCCEEDED under enforce")
	}

	// (3) A worker forging the scheduler identity (SenderId/RequestedBy=
	// cordum-scheduler) with its OWN worker token is STILL rejected — there is
	// no SenderId==defaultSenderID exemption; the token's Subject (w-a) does
	// not match the claimed reserved identity.
	engS, storeS := newEnforceEngine("job-spoof")
	spoof := reparseWithTypedAuthToken(t, &pb.BusPacket{
		SenderId: servicetoken.IdentityScheduler,
		Payload:  &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{JobId: "job-spoof", RequestedBy: servicetoken.IdentityScheduler, Reason: "spoof"}},
	}, workerTok)
	if err := engS.HandlePacket(spoof); err != nil {
		t.Fatalf("HandlePacket(spoof): %v", err)
	}
	if got := stateOf(storeS, "job-spoof"); got == JobStateCancelled {
		t.Fatal("a worker token claiming SenderId=cordum-scheduler must STILL be rejected (Subject w-a != cordum-scheduler)")
	}
}
