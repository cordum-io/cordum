package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/capprofile"
	"github.com/cordum/cordum/core/infra/store"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

func seedProductionDLQRetry(t *testing.T, s *server, req *pb.JobRequest) {
	t.Helper()
	ctx := context.Background()
	jobID := req.GetJobId()
	entry := store.DLQEntry{JobID: jobID, Topic: req.GetTopic(), CreatedAt: time.Now().UTC()}
	if err := s.dlqStore.Add(ctx, entry); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	for name, err := range map[string]error{
		"topic":     s.jobStore.SetTopic(ctx, jobID, req.GetTopic()),
		"tenant":    s.jobStore.SetTenant(ctx, jobID, "default"),
		"team":      s.jobStore.SetTeam(ctx, jobID, "team-a"),
		"principal": s.jobStore.SetPrincipal(ctx, jobID, "principal-a"),
		"request":   s.jobStore.SetJobRequest(ctx, req),
		"context":   s.memStore.PutContext(ctx, store.MakeContextKey(jobID), []byte(`{"prompt":"hello"}`)),
	} {
		if err != nil {
			t.Fatalf("seed %s error = %v", name, err)
		}
	}
}

func runProductionDLQRetry(t *testing.T, s *server, jobID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dlq/"+jobID+"/retry", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.SetPathValue("job_id", jobID)
	rec := httptest.NewRecorder()
	s.handleRetryDLQ(rec, req)
	return rec
}

func TestHandleRetryDLQProductionEchoesStoredCanonicalIdentity(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.capProfile = capprofile.Production
	authority := &pb.IdentityBinding{
		TenantId: "default", PrincipalId: "principal-a", ActorId: "actor-a",
	}
	original, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-retry-identity", Topic: "job.test"}, authority,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	seedProductionDLQRetry(t, s, original)

	rec := runProductionDLQRetry(t, s, original.GetJobId())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	packet := bus.published[len(bus.published)-1].packet
	if !proto.Equal(packet.GetIdentity(), authority) ||
		!proto.Equal(packet.GetJobRequest().GetIdentity(), authority) {
		t.Fatalf("retry identity = envelope:%v request:%v", packet.GetIdentity(), packet.GetJobRequest().GetIdentity())
	}
}

func TestHandleRetryDLQProductionRejectsConflictingStoredIdentity(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	s.capProfile = capprofile.Production
	req := &pb.JobRequest{
		JobId: "job-retry-conflict", Topic: "job.test", TenantId: "tenant-attacker",
		Identity: &pb.IdentityBinding{
			TenantId: "default", PrincipalId: "principal-a", ActorId: "actor-a",
		},
	}
	seedProductionDLQRetry(t, s, req)

	rec := runProductionDLQRetry(t, s, req.GetJobId())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	if len(bus.published) != 0 {
		t.Fatalf("conflicting retry published %d packets", len(bus.published))
	}
}
