package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/capprofile"
	"github.com/cordum/cordum/core/infra/store"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

// busTapGateway starts the bus taps of a fresh gateway with a wired session
// issuer, in handshake mode mode or under the production profile.
func busTapGateway(t *testing.T, mode scheduler.HandshakeMode, production bool) (*server, *stubBus) {
	t.Helper()
	s, bus, _ := newTestGateway(t)
	wireSessionIssuer(t, s)
	s.handshakeMode = mode
	if production {
		s.capProfile = capprofile.Production
	}
	if err := s.startBusTaps(); err != nil {
		t.Fatalf("start bus taps: %v", err)
	}
	t.Cleanup(func() {
		s.stopBusTaps()
		s.stopWorkerExpiry()
	})
	return s, bus
}

// busTapToken mints the token a test packet carries.
type busTapToken func(t *testing.T, s *server) string

func busTapFixed(token string) busTapToken {
	return func(*testing.T, *server) string { return token }
}

// busTapSession is a bound session of worker for audience aud.
func busTapSession(worker, aud string) busTapToken {
	return func(t *testing.T, s *server) string {
		t.Helper()
		token, _, err := s.sessionIssuer.IssueBound(context.Background(), scheduler.SessionBinding{
			WorkerID: worker, AgentID: "agent-" + worker, Tenant: matrixTenant,
			Audience: aud, ProofKeyID: "proof-" + worker, SDKVersion: "test-sdk",
		})
		if err != nil {
			t.Fatalf("session for %s: %v", worker, err)
		}
		return token
	}
}

// busTapRevoked is worker's own session, revoked once issued.
func busTapRevoked(worker string) busTapToken {
	return func(t *testing.T, s *server) string {
		t.Helper()
		token := busTapSession(worker, scheduler.WorkerHandshakeAudience)(t, s)
		if err := s.sessionIssuer.RevokeByWorker(context.Background(), matrixTenant, worker); err != nil {
			t.Fatalf("revoke %s: %v", worker, err)
		}
		return token
	}
}

// busTapService is the control-plane service token of subject.
func busTapService(subject string) busTapToken {
	return func(t *testing.T, s *server) string {
		t.Helper()
		token, err := s.sessionIssuer.MintServiceToken(subject)
		if err != nil {
			t.Fatalf("service token %s: %v", subject, err)
		}
		return token
	}
}

func busTapHeartbeatPacket(worker, sender, token string) *pb.BusPacket {
	return &pb.BusPacket{SenderId: sender, AuthToken: token, Payload: &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: worker}}}
}

// busTapDLQPacket is the scheduler's DLQ result for jobID.
func busTapDLQPacket(jobID, message, token string) *pb.BusPacket {
	return &pb.BusPacket{SenderId: servicetoken.IdentityScheduler, AuthToken: token, Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
		JobId: jobID, WorkerId: servicetoken.IdentityScheduler, Status: pb.JobStatus_JOB_STATUS_DENIED, ErrorMessage: message,
	}}}
}

// TestBusTapAuthHeartbeat checks which heartbeats register their worker and
// reach stream clients; a refused one leaves no entry and no relay.
func TestBusTapAuthHeartbeat(t *testing.T) {
	const worker, other = "worker-bus-tap", "worker-bus-tap-other"
	aud, warn, enforce := scheduler.WorkerHandshakeAudience, scheduler.HandshakeModeWarn, scheduler.HandshakeModeEnforce
	cases := []struct {
		name           string
		mode           scheduler.HandshakeMode
		production     bool
		worker, sender string
		token          busTapToken
		admitted       bool
	}{
		{name: "unset_mode_admits_tokenless", worker: worker, sender: worker, admitted: true},
		{name: "warn_admits_tokenless", mode: warn, worker: worker, sender: worker, admitted: true},
		{name: "warn_refuses_invalid_token", mode: warn, worker: worker, sender: worker, token: busTapFixed("not-a-session-token")},
		{name: "enforce_refuses_tokenless", mode: enforce, worker: worker, sender: worker},
		{name: "production_refuses_tokenless", production: true, worker: worker, sender: worker},
		{name: "own_session_admitted", mode: enforce, worker: worker, sender: worker, token: busTapSession(worker, aud), admitted: true},
		{name: "subject_is_not_worker_id", mode: enforce, worker: worker, sender: other, token: busTapSession(other, aud)},
		{name: "subject_is_not_sender", mode: enforce, worker: worker, sender: other, token: busTapSession(worker, aud)},
		{name: "wrong_audience_session", mode: enforce, worker: worker, sender: worker, token: busTapSession(worker, matrixWrongAudience)},
		{name: "service_token", mode: enforce, worker: servicetoken.IdentityScheduler, sender: servicetoken.IdentityScheduler,
			token: busTapService(servicetoken.IdentityScheduler)},
		{name: "revoked_session", mode: enforce, worker: worker, sender: worker, token: busTapRevoked(worker)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, bus := busTapGateway(t, c.mode, c.production)
			client := registerGatewayEdgeStreamClient(t, s, "", true)
			token := ""
			if c.token != nil {
				token = c.token(t, s)
			}
			effects := func() string {
				s.workerMu.RLock()
				_, registered := s.workers[c.worker]
				_, seen := s.workerSeen[c.worker]
				s.workerMu.RUnlock()
				relayed := (&audienceMatrix{s: s}).matrixRelayed(t, client, "")
				return fmt.Sprintf("registered=%t seen=%t relayed=%d", registered, seen, relayed)
			}
			before := effects()
			bus.emit(capsdk.SubjectHeartbeat, busTapHeartbeatPacket(c.worker, c.sender, token))
			after, want := effects(), "registered=false seen=false relayed=0"
			if c.admitted {
				want = "registered=true seen=true relayed=1"
			}
			if before != "registered=false seen=false relayed=0" || after != want {
				t.Fatalf("effects before %q, after %q; want after %q", before, after, want)
			}
		})
	}
}

// TestBusTapAuthDLQ checks which DLQ results the gateway stores; a refused one
// leaves no DLQ entry, no res:<job> and no result pointer.
func TestBusTapAuthDLQ(t *testing.T) {
	const jobID = "job-bus-tap-dlq"
	warn, enforce := scheduler.HandshakeModeWarn, scheduler.HandshakeModeEnforce
	cases := []struct {
		name       string
		mode       scheduler.HandshakeMode
		production bool
		token      busTapToken
		stored     bool
	}{
		{name: "unset_mode_stores_tokenless", stored: true},
		{name: "warn_stores_tokenless", mode: warn, stored: true},
		{name: "warn_refuses_invalid_token", mode: warn, token: busTapFixed("not-a-service-token")},
		{name: "enforce_refuses_tokenless", mode: enforce},
		{name: "production_refuses_tokenless", production: true},
		{name: "scheduler_token_stored", mode: enforce, token: busTapService(servicetoken.IdentityScheduler), stored: true},
		{name: "gateway_token_stored", mode: enforce, token: busTapService(servicetoken.IdentityGateway), stored: true},
		{name: "workflow_token_refused", mode: enforce, token: busTapService(servicetoken.IdentityWorkflow)},
		{name: "worker_session_refused", mode: enforce, token: busTapSession("worker-bus-tap-dlq", scheduler.WorkerHandshakeAudience)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, bus := busTapGateway(t, c.mode, c.production)
			token := ""
			if c.token != nil {
				token = c.token(t, s)
			}
			effects := func() string {
				ctx := context.Background()
				_, dlqErr := s.dlqStore.Get(ctx, jobID)
				_, resErr := s.memStore.GetResult(ctx, store.MakeResultKey(jobID))
				_, ptrErr := s.jobStore.GetResultPtr(ctx, jobID)
				return fmt.Sprintf("dlq=%t res=%t ptr=%t", busTapStored(t, dlqErr), busTapStored(t, resErr), busTapStored(t, ptrErr))
			}
			before := effects()
			bus.emit(capsdk.SubjectDLQ, busTapDLQPacket(jobID, "approval rejected", token))
			after, want := effects(), "dlq=false res=false ptr=false"
			if c.stored {
				want = "dlq=true res=true ptr=true"
			}
			if before != "dlq=false res=false ptr=false" || after != want {
				t.Fatalf("effects before %q, after %q; want after %q", before, after, want)
			}
		})
	}
}

// busTapStored reports whether a store read found its key.
func busTapStored(t *testing.T, err error) bool {
	t.Helper()
	if errors.Is(err, redis.Nil) {
		return false
	}
	if err != nil {
		t.Fatalf("store read: %v", err)
	}
	return true
}

// TestBusTapAuthRefusalLog checks that a refusal is logged by tap and reason
// only, and that warn mode logs a token-less sender once. Not parallel: it
// swaps the default logger.
func TestBusTapAuthRefusalLog(t *testing.T) {
	var logs syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	const worker, sender, job, message = "worker-bus-tap-log", "sender-bus-tap-log", "job-bus-tap-log", "payload-bus-tap-log"
	const invalid = "invalid-bus-tap-log-token"
	s, bus := busTapGateway(t, scheduler.HandshakeModeEnforce, false)
	hbToken := busTapSession(worker, scheduler.WorkerHandshakeAudience)(t, s)
	dlqToken := busTapSession(sender, scheduler.WorkerHandshakeAudience)(t, s)
	bus.emit(capsdk.SubjectHeartbeat, busTapHeartbeatPacket(worker, sender, hbToken))
	bus.emit(capsdk.SubjectDLQ, busTapDLQPacket(job, message, dlqToken))
	bus.emit(capsdk.SubjectDLQ, busTapDLQPacket(job, message, invalid))
	_, warnBus := busTapGateway(t, scheduler.HandshakeModeWarn, false)
	for range 2 {
		warnBus.emit(capsdk.SubjectHeartbeat, busTapHeartbeatPacket(sender, sender, ""))
	}

	for _, secret := range []string{hbToken, dlqToken, invalid, worker, sender, job, message} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("log output carries %q", secret)
		}
	}
	var got []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, `msg="bus tap `) {
			got = append(got, line[strings.Index(line, "level="):])
		}
	}
	want := []string{
		`level=WARN msg="bus tap packet refused" tap=heartbeat reason=identity`,
		`level=WARN msg="bus tap packet refused" tap=dlq reason=identity`,
		`level=WARN msg="bus tap packet refused" tap=dlq reason=reject_invalid`,
		`level=WARN msg="bus tap admitted a packet without a session token" tap=heartbeat`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("bus tap log lines:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
