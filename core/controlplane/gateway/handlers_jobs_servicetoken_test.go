package gateway

// Coverage for task-948d913b: the gateway stamps a verifiable control-plane
// service token on its cancel broadcasts and sets the cancel-ack JobResult
// WorkerId to "api-gateway" so the scheduler's subject-binding (Subject ==
// SenderId == WorkerId) admits it under CORDUM_SDK_HANDSHAKE=enforce.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	"github.com/cordum/cordum/core/infra/capprofile"
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
