package scheduler

// RED coverage for task-5c18f890: the scheduler session-token trust gate.
//
//   (b) Both extractors read the token only from the protobuf *unknown*
//       fields, but cap/v2 v2.13.x ships a TYPED BusPacket.auth_token
//       (field 18) that the SDK sets. After a real marshal/reparse the
//       token lives in the typed field and the unknown set is empty, so
//       the extractors return "" and the gate sees every packet as
//       token-less.
//   (a) Because the middleware is wired, an enforce-mode JobResult that
//       carries a VALID typed token must still be admitted; today it is
//       rejected (token invisible) and never reaches SUCCEEDED.
//
// These exercise the REAL typed wire format (proto.Marshal +
// proto.Unmarshal), not SetUnknown — that is what a live SDK worker puts
// on the bus.

import (
	"context"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// reparseWithTypedAuthToken sets the typed BusPacket.auth_token field,
// marshals, and re-unmarshals — reproducing exactly what the scheduler
// receives off the NATS bus from a cap/v2 SDK worker. After this round
// trip GetAuthToken() holds the token and GetUnknown() is empty.
func reparseWithTypedAuthToken(t *testing.T, packet *pb.BusPacket, token string) *pb.BusPacket {
	t.Helper()
	packet.AuthToken = token
	raw, err := proto.Marshal(packet)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := &pb.BusPacket{}
	if err := proto.Unmarshal(raw, out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.GetAuthToken() != token {
		t.Fatalf("precondition: typed auth_token not preserved across marshal, got %q", out.GetAuthToken())
	}
	if len(out.ProtoReflect().GetUnknown()) != 0 {
		t.Fatalf("precondition: typed auth_token must not land in unknown fields")
	}
	return out
}

func TestExtractSessionToken_RecoversTypedAuthTokenAfterMarshal(t *testing.T) {
	t.Parallel()
	const token = "sess.tok.extract"
	pkt := reparseWithTypedAuthToken(t, &pb.BusPacket{SenderId: "w1"}, token)
	if got := extractSessionToken(pkt); got != token {
		t.Fatalf("extractSessionToken = %q, want %q (typed BusPacket.auth_token must be read first)", got, token)
	}
}

func TestAuthTokenFromPacket_RecoversTypedAuthTokenAfterMarshal(t *testing.T) {
	t.Parallel()
	const token = "sess.tok.attest"
	pkt := reparseWithTypedAuthToken(t, &pb.BusPacket{SenderId: "w1"}, token)
	if got := authTokenFromPacket(pkt); got != token {
		t.Fatalf("authTokenFromPacket = %q, want %q (typed BusPacket.auth_token must be read first)", got, token)
	}
}

// A live JobResult carrying a VALID session token must drive the job to
// SUCCEEDED when handshake enforcement is on. Today the typed token is
// invisible to the extractor, so the enforce gate treats it as missing
// and drops the packet — the job never leaves RUNNING.
func TestHandlePacket_JobResult_ValidTypedTokenReachesSucceeded(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.Issue(context.Background(), "w-live", "tenant-live", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-live"] = JobStateRunning
	store.topics["job-live"] = "job.default"
	store.mu.Unlock()

	engine := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	engine.sessionMiddleware = NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())

	pkt := reparseWithTypedAuthToken(t, &pb.BusPacket{
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId:    "job-live",
			WorkerId: "w-live",
			Status:   pb.JobStatus_JOB_STATUS_SUCCEEDED,
		}},
	}, token)
	if err := engine.HandlePacket(pkt); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}

	store.mu.RLock()
	got := store.states["job-live"]
	store.mu.RUnlock()
	if got != JobStateSucceeded {
		t.Fatalf("valid typed session token must admit job_result to SUCCEEDED; got %q", got)
	}
}

// A forged token must never drive a job to SUCCEEDED under enforcement.
// (Green both before and after the fix — a regression guard that the
// gate stays fail-closed, complementing the valid-token RED above.)
func TestHandlePacket_JobResult_ForgedTokenRejected(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-forge"] = JobStateRunning
	store.topics["job-forge"] = "job.default"
	store.mu.Unlock()

	engine := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	engine.sessionMiddleware = NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())

	pkt := reparseWithTypedAuthToken(t, &pb.BusPacket{
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId:    "job-forge",
			WorkerId: "w-attacker",
			Status:   pb.JobStatus_JOB_STATUS_SUCCEEDED,
		}},
	}, "forged.invalid.token")
	if err := engine.HandlePacket(pkt); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}

	store.mu.RLock()
	got := store.states["job-forge"]
	store.mu.RUnlock()
	if got == JobStateSucceeded {
		t.Fatalf("forged session token must not drive job to SUCCEEDED; got %q", got)
	}
}

// attachLegacyUnknownToken encodes the token as a protobuf unknown field
// (the pre-typed-field wire format). The typed BusPacket.auth_token stays
// empty so the extractor must use its legacy fallback to recover it.
func attachLegacyUnknownToken(t *testing.T, packet *pb.BusPacket, token string) *pb.BusPacket {
	t.Helper()
	raw := packet.ProtoReflect().GetUnknown()
	buf := append([]byte{}, raw...)
	buf = protowire.AppendTag(buf, sessionTokenPacketField, protowire.BytesType)
	buf = protowire.AppendString(buf, token)
	packet.ProtoReflect().SetUnknown(buf)
	if packet.GetAuthToken() != "" {
		t.Fatalf("precondition: legacy packet must leave the typed field empty")
	}
	return packet
}

func TestExtractSessionToken_LegacyUnknownFieldFallback(t *testing.T) {
	t.Parallel()
	const token = "legacy.extract.tok"
	pkt := attachLegacyUnknownToken(t, &pb.BusPacket{SenderId: "w1"}, token)
	if got := extractSessionToken(pkt); got != token {
		t.Fatalf("extractSessionToken legacy fallback = %q, want %q", got, token)
	}
}

func TestAuthTokenFromPacket_LegacyUnknownFieldFallback(t *testing.T) {
	t.Parallel()
	const token = "legacy.attest.tok"
	pkt := attachLegacyUnknownToken(t, &pb.BusPacket{SenderId: "w1"}, token)
	if got := authTokenFromPacket(pkt); got != token {
		t.Fatalf("authTokenFromPacket legacy fallback = %q, want %q", got, token)
	}
}

// The exported WithSessionMiddleware builder (the production wiring API,
// previously missing) must actually wire the gate end-to-end.
func TestWithSessionMiddleware_BuilderEnforcesThroughHandlePacket(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.Issue(context.Background(), "w-bld", "tenant-bld", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-bld"] = JobStateRunning
	store.topics["job-bld"] = "job.default"
	store.mu.Unlock()

	engine := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil).
		WithSessionMiddleware(NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker()))

	pkt := reparseWithTypedAuthToken(t, &pb.BusPacket{
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId: "job-bld", WorkerId: "w-bld", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
		}},
	}, token)
	if err := engine.HandlePacket(pkt); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}
	store.mu.RLock()
	got := store.states["job-bld"]
	store.mu.RUnlock()
	if got != JobStateSucceeded {
		t.Fatalf("WithSessionMiddleware must wire the gate; valid-token job not admitted, got %q", got)
	}
}

func TestHandlePacket_JobResult_MissingTokenRejected(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-miss"] = JobStateRunning
	store.topics["job-miss"] = "job.default"
	store.mu.Unlock()

	engine := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	engine.sessionMiddleware = NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())

	// No token attached at all.
	pkt := &pb.BusPacket{Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
		JobId: "job-miss", WorkerId: "w-miss", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}}}
	if err := engine.HandlePacket(pkt); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}
	store.mu.RLock()
	got := store.states["job-miss"]
	store.mu.RUnlock()
	if got == JobStateSucceeded {
		t.Fatalf("enforce + missing token must reject job_result; got SUCCEEDED")
	}
}

// Back-compat: a scheduler that never opted into CORDUM_SDK_HANDSHAKE
// (nil middleware) must keep admitting, so an upgrade does not DoS.
func TestHandlePacket_JobResult_NoMiddlewareAdmits(t *testing.T) {
	t.Parallel()
	store := newFakeJobStore()
	store.mu.Lock()
	store.states["job-nomw"] = JobStateRunning
	store.topics["job-nomw"] = "job.default"
	store.mu.Unlock()

	engine := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, nil)
	// Intentionally no WithSessionMiddleware.
	pkt := &pb.BusPacket{Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
		JobId: "job-nomw", WorkerId: "w-any", Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
	}}}
	if err := engine.HandlePacket(pkt); err != nil {
		t.Fatalf("HandlePacket: %v", err)
	}
	store.mu.RLock()
	got := store.states["job-nomw"]
	store.mu.RUnlock()
	if got != JobStateSucceeded {
		t.Fatalf("no session middleware must admit (back-compat); got %q", got)
	}
}

// The job_cancel ingest path is gated too: a missing token must not let an
// unauthenticated sender cancel a job, while a valid token cancels.
func TestHandlePacket_JobCancel_TokenGated(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.Issue(context.Background(), "w-cxl", "tenant-cxl", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	storeMiss := newFakeJobStore()
	storeMiss.mu.Lock()
	storeMiss.states["job-cxl-miss"] = JobStateRunning
	storeMiss.mu.Unlock()
	engMiss := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), storeMiss, nil)
	engMiss.sessionMiddleware = NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	missPkt := &pb.BusPacket{Payload: &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{
		JobId: "job-cxl-miss", RequestedBy: "w-cxl", Reason: "x",
	}}}
	if err := engMiss.HandlePacket(missPkt); err != nil {
		t.Fatalf("HandlePacket(miss): %v", err)
	}
	storeMiss.mu.RLock()
	gotMiss := storeMiss.states["job-cxl-miss"]
	storeMiss.mu.RUnlock()
	if gotMiss == JobStateCancelled {
		t.Fatalf("enforce + missing token must not cancel job; got CANCELLED")
	}

	storeOK := newFakeJobStore()
	storeOK.mu.Lock()
	storeOK.states["job-cxl-ok"] = JobStateRunning
	storeOK.mu.Unlock()
	engOK := NewEngine(newCountingBus(), NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), storeOK, nil)
	engOK.sessionMiddleware = NewSessionTokenMiddleware(issuer, HandshakeModeEnforce, NewHandshakeMissingTracker())
	okPkt := reparseWithTypedAuthToken(t, &pb.BusPacket{Payload: &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{
		JobId: "job-cxl-ok", RequestedBy: "w-cxl", Reason: "x",
	}}}, token)
	if err := engOK.HandlePacket(okPkt); err != nil {
		t.Fatalf("HandlePacket(ok): %v", err)
	}
	storeOK.mu.RLock()
	gotOK := storeOK.states["job-cxl-ok"]
	storeOK.mu.RUnlock()
	if gotOK != JobStateCancelled {
		t.Fatalf("enforce + valid token must cancel job; got %q", gotOK)
	}
}
