package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/tests/identity"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	matrixSchedulerSender = "cordum-scheduler" // scheduler engine.go defaultSenderID
	matrixServiceSender   = "api-gateway"      // subject of the harness service token
)

// TestAudienceMatrixBusGatewayHeartbeatTap presents every credential on a
// worker's own sys.heartbeat. The protected effect is that worker's entry in
// the gateway's worker registry.
func TestAudienceMatrixBusGatewayHeartbeatTap(t *testing.T) {
	m := newAudienceMatrix(t)
	m.matrixStartTaps(t)
	identity.Run(t, identity.BusGatewayHeartbeatTap, func(t *testing.T, c identity.Credential) identity.Result {
		worker := "worker-matrix-hb-" + string(c)
		p := m.matrixCarry(t, matrixPacket(worker), c, worker)
		p.Payload = &pb.BusPacket_Heartbeat{Heartbeat: &pb.Heartbeat{WorkerId: worker}}
		registered := func(t *testing.T) int {
			m.s.workerMu.RLock()
			defer m.s.workerMu.RUnlock()
			if m.s.workers[worker] != nil {
				return 1
			}
			return 0
		}
		return matrixEmit(t, "worker registry entries", registered, func() { m.bus.emit(capsdk.SubjectHeartbeat, p) })
	})
}

// TestAudienceMatrixBusGatewayDLQ presents every credential on the scheduler's
// sys.job.dlq result. The protected effect is the job's DLQ entry. The
// baseline publishes as the scheduler does, with its service token.
func TestAudienceMatrixBusGatewayDLQ(t *testing.T) {
	m := newAudienceMatrix(t)
	m.matrixStartTaps(t)
	present := func(t *testing.T, c identity.Credential) identity.Result {
		return m.matrixDLQ(t, m.matrixCarry(t, matrixPacket(matrixSchedulerSender), c, "worker-matrix-dlq-"+string(c)), "job-matrix-dlq-"+string(c))
	}
	baseline := func(t *testing.T) identity.Result {
		token, err := m.s.sessionIssuer.MintServiceToken(servicetoken.IdentityScheduler)
		if err != nil {
			t.Fatalf("scheduler service token: %v", err)
		}
		p := matrixPacket(matrixSchedulerSender)
		p.AuthToken = token
		return m.matrixDLQ(t, p, "job-matrix-dlq-baseline")
	}
	identity.Run(t, identity.BusGatewayDLQ, present, identity.Baseline(baseline))
}

// TestAudienceMatrixBusGatewayStream presents every credential on one
// sys.job.> result and one sys.audit.> output-policy event. The protected
// effect is the events relayed to a subscribed stream client.
func TestAudienceMatrixBusGatewayStream(t *testing.T) {
	m := newAudienceMatrix(t)
	m.matrixStartTaps(t)
	client := registerGatewayEdgeStreamClient(t, m.s, "", true)
	identity.Run(t, identity.BusGatewayStream, func(t *testing.T, c identity.Credential) identity.Result {
		job, worker := "job-matrix-stream-"+string(c), "worker-matrix-stream-"+string(c)
		sender := worker
		if c == identity.ServiceToken {
			sender = matrixServiceSender
		}
		result := m.matrixCarry(t, matrixPacket(sender), c, worker)
		result.Payload = &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: job, WorkerId: sender, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED}}
		// Reuse the cell's active credential; issuing a second session replaces the first.
		audit := proto.Clone(result).(*pb.BusPacket)
		audit.Payload = &pb.BusPacket_JobProgress{JobProgress: &pb.JobProgress{
			JobId: job, StepId: "output_policy", Percent: 100, Status: pb.JobStatus_JOB_STATUS_DENIED, Message: "output policy event",
		}}
		relayed := 0
		effect := func(t *testing.T) int { relayed += m.matrixRelayed(t, client, job); return relayed }
		observed := matrixEmit(t, "relayed stream events", effect, func() {
			m.bus.emit(capsdk.SubjectResult, result)
			m.bus.emit("sys.audit.output_policy", audit)
		})
		if (c == identity.WorkerSession || c == identity.ServiceToken) && relayed != 2 {
			t.Fatalf("active credential must relay both subjects, got %d events", relayed)
		}
		return observed
	})
}

// matrixStartTaps starts the gateway's bus taps and checks each is subscribed,
// so a packet no tap received can never read as a refusal.
func (m *audienceMatrix) matrixStartTaps(t *testing.T) {
	t.Helper()
	if err := m.s.startBusTaps(); err != nil {
		t.Fatalf("start bus taps: %v", err)
	}
	t.Cleanup(func() {
		m.s.stopBusTaps()
		m.s.stopWorkerExpiry()
	})
	m.bus.mu.Lock()
	defer m.bus.mu.Unlock()
	for _, subject := range []string{capsdk.SubjectHeartbeat, capsdk.SubjectDLQ, "sys.job.>", "sys.audit.>"} {
		if len(m.bus.subs[subject]) == 0 {
			t.Fatalf("no gateway tap on %s", subject)
		}
	}
}

// matrixPacket is a bus packet from sender, as its legitimate publisher sends it.
func matrixPacket(sender string) *pb.BusPacket {
	return &pb.BusPacket{SenderId: sender, CreatedAt: timestamppb.Now(), ProtocolVersion: capsdk.DefaultProtocolVersion}
}

// matrixCarry puts credential c on p: its token in auth_token, except that
// anonymous carries none and agent_id_header names the agent as sender.
func (m *audienceMatrix) matrixCarry(t *testing.T, p *pb.BusPacket, c identity.Credential, worker string) *pb.BusPacket {
	t.Helper()
	switch c {
	case identity.Anonymous:
	case identity.AgentIDHeader:
		p.SenderId = m.tokens[c]
	default:
		p.AuthToken = m.matrixBusToken(t, c, worker)
	}
	return p
}

// matrixBusToken is c's token for worker. The worker-bound credentials are
// minted for that worker by their real issuers, one worker id per cell, so no
// two sessions of one worker can revoke each other.
func (m *audienceMatrix) matrixBusToken(t *testing.T, c identity.Credential, worker string) string {
	t.Helper()
	ctx := context.Background()
	switch c {
	case identity.WorkerCredential:
		cred, err := m.s.workerCredentialStore.Create(ctx, workercredentials.IssueInput{TenantID: matrixTenant, WorkerID: worker})
		if err != nil {
			t.Fatalf("worker credential: %v", err)
		}
		return cred.Token
	case identity.WorkerSession, identity.WorkerSessionWrongAudience:
		audience := scheduler.WorkerHandshakeAudience
		if c == identity.WorkerSessionWrongAudience {
			audience = matrixWrongAudience
		}
		token, _, err := m.s.sessionIssuer.IssueBound(ctx, scheduler.SessionBinding{
			WorkerID: worker, AgentID: matrixAgentID, Tenant: matrixTenant,
			Audience: audience, ProofKeyID: "matrix-proof-key", SDKVersion: "matrix-sdk",
		})
		if err != nil {
			t.Fatalf("worker session %s: %v", c, err)
		}
		return token
	}
	return m.tokens[c]
}

// matrixDLQ sends p as the scheduler's rejected-approval DLQ result for jobID.
// The effect is the job's DLQ entry; a read error other than absence fails.
func (m *audienceMatrix) matrixDLQ(t *testing.T, p *pb.BusPacket, jobID string) identity.Result {
	t.Helper()
	p.Payload = &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
		JobId: jobID, WorkerId: matrixSchedulerSender, Status: pb.JobStatus_JOB_STATUS_DENIED,
		ErrorCode: "approval_rejected", ErrorCodeEnum: pb.ErrorCode_ERROR_CODE_SAFETY_DENIED, ErrorMessage: "approval rejected",
	}}
	stored := func(t *testing.T) int {
		_, err := m.s.dlqStore.Get(context.Background(), jobID)
		if errors.Is(err, redis.Nil) {
			return 0
		}
		if err != nil {
			t.Fatalf("read dlq %s: %v", jobID, err)
		}
		return 1
	}
	return matrixEmit(t, "dlq entries", stored, func() { m.bus.emit(capsdk.SubjectDLQ, p) })
}

// matrixRelayed counts the events client receives for jobID up to a sentinel
// queued after them, so an event still in flight never reads as dropped.
func (m *audienceMatrix) matrixRelayed(t *testing.T, client *wsClient, jobID string) int {
	t.Helper()
	sentinel := []byte("matrix-sentinel-" + matrixRandom(t))
	deadline := time.Now().Add(5 * time.Second)
	for !m.s.enqueueWSEvent(sentinel, "", "") {
		if time.Now().After(deadline) {
			t.Fatalf("stream sentinel not queued")
		}
		time.Sleep(time.Millisecond)
	}
	timeout := time.After(time.Until(deadline))
	for n := 0; ; {
		select {
		case evt, ok := <-client.ch:
			if !ok {
				t.Fatalf("stream client evicted")
			}
			if bytes.Equal(evt.data, sentinel) {
				return n
			}
			if evt.jobID == jobID {
				n++
			}
		case <-timeout:
			t.Fatalf("stream sentinel not relayed")
		}
	}
}

// matrixEmit snapshots effect, runs emit, and snapshots it again. The taps
// return no verdict to the publisher, so Accepted is the observed effect.
func matrixEmit(t *testing.T, what string, effect func(t *testing.T) int, emit func()) identity.Result {
	t.Helper()
	before := effect(t)
	if before != 0 {
		t.Fatalf("%s present before the cell's packets", what)
	}
	emit()
	after := effect(t)
	changed := after != before
	return identity.Result{Accepted: changed, Effect: changed,
		Evidence: fmt.Sprintf("%s %d->%d (taps return no verdict; Accepted is this snapshot's effect)", what, before, after)}
}
