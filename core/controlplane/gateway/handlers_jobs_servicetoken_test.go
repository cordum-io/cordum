package gateway

// Coverage for task-948d913b: the gateway stamps a verifiable control-plane
// service token on its cancel broadcasts and sets the cancel-ack JobResult
// WorkerId to "api-gateway" so the scheduler's subject-binding (Subject ==
// SenderId == WorkerId) admits it under CORDUM_SDK_HANDSHAKE=enforce.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/capprofile"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policysign"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func gatewayTestIssuer(t *testing.T, s *server) *scheduler.SessionTokenIssuer {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	trust := policysign.NewTrustStore()
	if err := trust.Add("primary", pub); err != nil {
		t.Fatalf("trust add: %v", err)
	}
	issuer, err := scheduler.NewSessionTokenIssuer(priv, "primary", trust, s.jobStore.Client(), scheduler.SessionTokenIssuerOptions{})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	return issuer
}

func assertGatewayServiceToken(t *testing.T, issuer *scheduler.SessionTokenIssuer, pkt *pb.BusPacket) {
	t.Helper()
	tok := pkt.GetAuthToken()
	if tok == "" {
		t.Fatalf("internal broadcast (sender %q) carries no service token", pkt.GetSenderId())
	}
	claims, err := issuer.VerifyService(tok)
	if err != nil {
		t.Fatalf("attached token must verify as a service token: %v", err)
	}
	if claims.Subject != servicetoken.IdentityGateway {
		t.Fatalf("service token subject = %q, want %q", claims.Subject, servicetoken.IdentityGateway)
	}
	// Subject-binding contract: SenderId must equal the service identity too.
	if pkt.GetSenderId() != servicetoken.IdentityGateway {
		t.Fatalf("SenderId = %q, want %q", pkt.GetSenderId(), servicetoken.IdentityGateway)
	}
}

func TestHandleCancelJob_AttachesGatewayServiceTokenAndSetsWorkerId(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	issuer := gatewayTestIssuer(t, s)
	s.WithSessionIssuer(issuer)

	ctx := context.Background()
	const jobID = "job-cxl-gw"
	if err := s.jobStore.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/"+jobID, nil)
	r.SetPathValue("id", jobID)
	r = withAuth(r, &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "tester"})
	w := httptest.NewRecorder()
	s.handleCancelJob(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body=%s", w.Code, w.Body.String())
	}

	var sawResult, sawCancel bool
	for _, m := range bus.published {
		switch m.subject {
		case capsdk.SubjectResult:
			sawResult = true
			res := m.packet.GetJobResult()
			if res == nil || res.GetWorkerId() != servicetoken.IdentityGateway {
				t.Fatalf("cancel-ack JobResult WorkerId = %q, want %q", res.GetWorkerId(), servicetoken.IdentityGateway)
			}
			assertGatewayServiceToken(t, issuer, m.packet)
		case capsdk.SubjectCancel:
			sawCancel = true
			cancel := m.packet.GetJobCancel()
			if cancel == nil || cancel.GetRequestedBy() != servicetoken.IdentityGateway {
				t.Fatalf("cancel RequestedBy = %q, want %q", cancel.GetRequestedBy(), servicetoken.IdentityGateway)
			}
			assertGatewayServiceToken(t, issuer, m.packet)
		}
	}
	if !sawResult {
		t.Fatal("expected a SubjectResult cancel-ack broadcast")
	}
	if !sawCancel {
		t.Fatal("expected a SubjectCancel broadcast")
	}
}

func TestServerAttachServiceToken_NoIssuerNoToken(t *testing.T) {
	s, _, _ := newTestGateway(t)
	// No issuer wired (back-compat / gate disabled): attach nothing, no panic.
	pkt := &pb.BusPacket{}
	s.attachServiceToken(pkt)
	if pkt.GetAuthToken() != "" {
		t.Fatalf("no issuer must attach no token; got %q", pkt.GetAuthToken())
	}
}

func TestHandleCancelJobProductionEchoesStoredIdentity(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.capProfile = capprofile.Production
	identity := &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "principal-a",
	}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-cxl-production", Topic: "job.test"}, identity,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	ctx := context.Background()
	if err := s.jobStore.SetJobRequest(ctx, req); err != nil {
		t.Fatalf("SetJobRequest() error = %v", err)
	}
	if err := s.jobStore.SetState(ctx, req.GetJobId(), model.JobStateRunning); err != nil {
		t.Fatalf("SetState() error = %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/"+req.GetJobId(), nil)
	r.SetPathValue("id", req.GetJobId())
	r = withAuth(r, &auth.AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "operator-a"})
	w := httptest.NewRecorder()
	s.handleCancelJob(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, body=%s", w.Code, w.Body.String())
	}

	for _, published := range bus.published {
		if published.subject != capsdk.SubjectResult && published.subject != capsdk.SubjectCancel {
			continue
		}
		if !proto.Equal(published.packet.GetIdentity(), identity) {
			t.Fatalf("%s envelope identity = %v", published.subject, published.packet.GetIdentity())
		}
		var payloadIdentity *pb.IdentityBinding
		if published.subject == capsdk.SubjectResult {
			payloadIdentity = published.packet.GetJobResult().GetIdentity()
		} else {
			payloadIdentity = published.packet.GetJobCancel().GetIdentity()
		}
		if !proto.Equal(payloadIdentity, identity) {
			t.Fatalf("%s payload identity = %v", published.subject, payloadIdentity)
		}
	}
}

// gatewayPublishCapture snapshots each packet at Publish entry. The workflow
// gate reuses the DLQ packet pointer for its result publish, so a
// pointer-retaining fake would show the later result token on the DLQ.
type gatewayPublishCapture struct {
	mu        sync.Mutex
	published []publishedMessage
}

func (b *gatewayPublishCapture) Publish(subject string, packet *pb.BusPacket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, publishedMessage{subject: subject, packet: proto.Clone(packet).(*pb.BusPacket)})
	return nil
}

func (b *gatewayPublishCapture) Subscribe(string, string, func(*pb.BusPacket) error) error {
	return nil
}

func rejectViaHandler(topic string) func(*testing.T, *server) {
	return func(t *testing.T, s *server) {
		const jobID = "job-dlq-reject"
		req := &pb.JobRequest{JobId: jobID, Topic: topic, TenantId: "default"}
		ctx := context.Background()
		hash, err := scheduler.HashJobRequest(req)
		if err != nil {
			t.Fatalf("hash job: %v", err)
		}
		if err := errors.Join(
			s.jobStore.SetJobMeta(ctx, req),
			s.jobStore.SetJobRequest(ctx, req),
			s.jobStore.SetState(ctx, jobID, model.JobStateApproval),
			s.jobStore.SetSafetyDecision(ctx, jobID, model.SafetyDecisionRecord{
				Decision: model.SafetyRequireApproval, ApprovalRequired: true, PolicySnapshot: "snap-test", JobHash: hash,
			}),
		); err != nil {
			t.Fatalf("seed approval job: %v", err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+jobID+"/reject", strings.NewReader(`{"reason":"denied"}`))
		r.SetPathValue("job_id", jobID)
		r = withAuth(r, &auth.AuthContext{Tenant: "default", PrincipalID: "bob", Role: "admin"})
		w := httptest.NewRecorder()
		s.handleRejectJob(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("reject status = %d, body=%s", w.Code, w.Body.String())
		}
	}
}

func repairApproval(target model.ApprovalPublishTarget) func(*testing.T, *server) {
	return func(t *testing.T, s *server) {
		err := s.publishApprovalRepair(context.Background(), &store.ApprovalRepairResult{
			JobID: "job-dlq-repair", TraceID: "trace-dlq-repair",
			Request:        &pb.JobRequest{JobId: "job-dlq-repair", Topic: "job.test"},
			ApprovalRecord: model.ApprovalRecord{PublishTarget: target, Reason: "rejected"},
		})
		if err != nil {
			t.Fatalf("publishApprovalRepair: %v", err)
		}
	}
}

func denySubmit(t *testing.T, s *server) {
	r := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil),
		&auth.AuthContext{Tenant: "default", PrincipalID: "principal-a", Role: "user"})
	if err := s.persistSubmitDeniedJob(
		context.Background(), r, submitJobRequest{Topic: "job.test"},
		&pb.JobMetadata{TenantId: "default"}, "job-dlq-denied", "trace-dlq-denied",
		"default", "principal-a", "", "", "", "", submitPolicyDecision{}, "denied",
	); err != nil {
		t.Fatalf("persistSubmitDeniedJob: %v", err)
	}
}

// drainStream empties eventsCh and returns what was queued.
func drainStream(s *server) [][]byte {
	var out [][]byte
	for {
		select {
		case evt := <-s.eventsCh:
			out = append(out, evt.data)
		default:
			return out
		}
	}
}

func TestGatewayDLQServiceTokenPublishers(t *testing.T) {
	dlqResult := []string{capsdk.SubjectDLQ, capsdk.SubjectResult}
	sites := []struct {
		name    string
		emits   []string // every subject published, in order; the first is the DLQ
		publish func(*testing.T, *server)
	}{
		{"publishApprovalRepair_dlq", []string{capsdk.SubjectDLQ}, repairApproval(model.ApprovalPublishTargetDLQ)},
		{"publishApprovalRepair_dlq_and_result", dlqResult, repairApproval(model.ApprovalPublishTargetDLQAndResult)},
		{"handleRejectJob_dlq", []string{capsdk.SubjectDLQ}, rejectViaHandler("job.test")},
		{"handleRejectJob_workflow_gate", dlqResult, rejectViaHandler(capsdk.SubjectWorkflowApprovalGate)},
		{"persistSubmitDeniedJob", []string{capsdk.SubjectDLQ}, denySubmit},
	}
	modes := []gatewayDLQMode{
		{"warn", scheduler.HandshakeModeWarn, scheduler.HeartbeatModeWarn, true},
		{"enforce", scheduler.HandshakeModeEnforce, scheduler.HeartbeatModeAuthority, true},
		// Production wiring for off: no issuer is loaded.
		{"off", scheduler.HandshakeModeOff, scheduler.HeartbeatModeTelemetry, false},
	}
	for _, site := range sites {
		t.Run(site.name, func(t *testing.T) {
			var firstPayload *pb.BusPacket
			for _, mode := range modes {
				payload := runGatewayDLQSite(t, site.emits, site.publish, mode)
				if firstPayload == nil {
					firstPayload = payload
				} else if !proto.Equal(payload, firstPayload) {
					t.Fatalf("%s DLQ payload changed: got %v, want %v", mode.name, payload, firstPayload)
				}
			}
		})
	}
}

type gatewayDLQMode struct {
	name      string
	handshake scheduler.HandshakeMode
	heartbeat scheduler.HeartbeatMode
	signed    bool
}

// runGatewayDLQSite runs one producer on a fresh gateway and returns its DLQ
// packet without the per-publish token, trace and timestamp.
func runGatewayDLQSite(t *testing.T, emits []string, publish func(*testing.T, *server), mode gatewayDLQMode) *pb.BusPacket {
	t.Helper()
	s, _, _ := newTestGateway(t)
	bus := &gatewayPublishCapture{}
	s.bus = bus
	var issuer *scheduler.SessionTokenIssuer
	if mode.signed {
		issuer = gatewayTestIssuer(t, s)
	}
	s.WithGatewayHandshakeSecurity(mode.handshake, mode.heartbeat, issuer)
	s.eventsCh = make(chan wsEvent, 64)
	s.enqueueBusPacket(&pb.BusPacket{TraceId: "stream-control", SenderId: servicetoken.IdentityGateway,
		Payload: &pb.BusPacket_JobResult{JobResult: &pb.JobResult{JobId: "stream-control"}}})
	if got := len(drainStream(s)); got != 1 {
		t.Fatalf("%s stream control queued %d events, want 1", mode.name, got)
	}

	publish(t, s)

	var subjects []string
	for _, m := range bus.published {
		subjects = append(subjects, m.subject)
	}
	if !slices.Equal(subjects, emits) {
		t.Fatalf("%s published %v, want %v", mode.name, subjects, emits)
	}
	dlq := bus.published[0].packet
	if mode.signed {
		assertGatewayServiceToken(t, issuer, dlq)
	} else if dlq.GetAuthToken() != "" || dlq.GetSenderId() != servicetoken.IdentityGateway {
		t.Fatalf("%s token=%q sender=%q, want no token", mode.name, dlq.GetAuthToken(), dlq.GetSenderId())
	}
	for _, data := range drainStream(s) {
		if bytes.Contains(data, []byte(`"authToken"`)) ||
			(dlq.GetAuthToken() != "" && bytes.Contains(data, []byte(dlq.GetAuthToken()))) {
			t.Fatalf("%s queued a token-bearing packet for the stream: %s", mode.name, data)
		}
	}
	payload := proto.Clone(dlq).(*pb.BusPacket)
	payload.AuthToken, payload.TraceId, payload.CreatedAt = "", "", nil
	return payload
}
