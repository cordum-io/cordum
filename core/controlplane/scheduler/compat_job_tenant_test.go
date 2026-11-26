package scheduler

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/cordum/cordum/core/auth/servicetoken"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type compatTenantBus struct {
	fakeBus
	handlers map[string]func(*pb.BusPacket) error
}

func (b *compatTenantBus) Subscribe(subject, _ string, handler func(*pb.BusPacket) error) error {
	if b.handlers[subject] != nil {
		return fmt.Errorf("duplicate subscription: %s", subject)
	}
	b.handlers[subject] = handler
	return nil
}

type compatTenantStore struct {
	*fakeJobStore
	lookupErr error
}

func (s *compatTenantStore) GetTenant(ctx context.Context, id string) (string, error) {
	if s.lookupErr != nil {
		return "", s.lookupErr
	}
	return s.fakeJobStore.GetTenant(ctx, id)
}

type compatTenantMetrics struct {
	cancelMetricsSpy
	rejected int
}

func (m *compatTenantMetrics) IncValidationRejections() { m.rejected++ }

func compatTenantJob(t *testing.T, tenant string) *fakeJobStore {
	t.Helper()
	s := newFakeJobStore()
	s.states["job-b"], s.tenants["job-b"] = JobStateRunning, tenant
	if err := s.SetJobRequest(context.Background(), &pb.JobRequest{
		JobId: "job-b", Topic: "job.test", TenantId: tenant,
		PrincipalId: "requester-b", Identity: &pb.IdentityBinding{
			TenantId: tenant, PrincipalId: "requester-b", ActorId: "requester-b",
		},
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

type compatTenantCase struct {
	name, tenant, stored string
	jobID                string
	result, service, off bool
	production, retry    bool
	admitted             bool
}

func compatTenantPacket(t *testing.T, issuer *SessionTokenIssuer, tc compatTenantCase) *pb.BusPacket {
	t.Helper()
	worker, token := "worker-"+tc.tenant, ""
	var err error
	if tc.service {
		worker = servicetoken.IdentityGateway
		token, err = issuer.MintServiceToken(worker)
	} else if !tc.off {
		token, _, err = issuer.IssueBound(context.Background(), boundTestBinding(worker, tc.tenant, "go/test"))
	}
	if err != nil {
		t.Fatal(err)
	}
	identity := &pb.IdentityBinding{TenantId: tc.tenant, PrincipalId: worker, ActorId: worker}
	p := &pb.BusPacket{SenderId: worker, Identity: identity}
	jobID := tc.jobID
	if jobID == "" {
		jobID = "job-b"
	}
	if tc.result {
		p.Payload = &pb.BusPacket_JobResult{JobResult: &pb.JobResult{
			JobId: jobID, WorkerId: worker, Identity: identity,
			Status: pb.JobStatus_JOB_STATUS_SUCCEEDED, ResultPtr: "redis://result-b",
		}}
	} else {
		p.Payload = &pb.BusPacket_JobCancel{JobCancel: &pb.JobCancel{
			JobId: jobID, RequestedBy: worker, Identity: identity,
		}}
	}
	return reparseWithTypedAuthToken(t, completeSecurityTestEnvelope(p), token)
}

func TestCompatJobPacketsStayInTheSendersTenant(t *testing.T) {
	cases := []compatTenantCase{
		{name: "cancel-other", tenant: "tenant-a", stored: "tenant-b"},
		{name: "result-other", tenant: "tenant-a", stored: "tenant-b", result: true},
		{name: "cancel-own", tenant: "tenant-b", stored: "tenant-b", admitted: true},
		{name: "result-own", tenant: "tenant-b", stored: "tenant-b", result: true, admitted: true},
		{name: "result-own-padded-id", tenant: "tenant-b", stored: "tenant-b", jobID: " job-b ", result: true, admitted: true},
		{name: "cancel-service", tenant: "tenant-a", stored: "tenant-b", service: true, admitted: true},
		{name: "result-service", tenant: "tenant-a", stored: "tenant-b", service: true, result: true, admitted: true},
		{name: "cancel-off", tenant: "tenant-a", stored: "tenant-b", off: true, admitted: true},
		{name: "result-off", tenant: "tenant-a", stored: "tenant-b", off: true, result: true, admitted: true},
		{name: "production-cancel-other", tenant: "tenant-a", stored: "tenant-b", production: true},
		{name: "cancel-empty-stored", tenant: "tenant-b", stored: " "},
		{name: "result-empty-stored", tenant: "tenant-b", result: true},
		{name: "cancel-trimmed", tenant: "tenant-b", stored: " tenant-b ", admitted: true},
		{name: "cancel-retry", tenant: "tenant-b", stored: "tenant-b", retry: true},
		{name: "result-retry", tenant: "tenant-b", stored: "tenant-b", retry: true, result: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { compatTenantRun(t, tc) })
	}
}

func compatTenantRun(t *testing.T, tc compatTenantCase) {
	t.Helper()
	t.Setenv("CORDUM_CAP_PROFILE", "compat")
	t.Setenv("CORDUM_SDK_HANDSHAKE", "enforce")
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	t.Cleanup(cleanup)
	s, expected := compatTenantJob(t, tc.stored), compatTenantJob(t, tc.stored)
	store := &compatTenantStore{fakeJobStore: s}
	if tc.retry {
		store.lookupErr = errors.New("tenant store unavailable")
	}
	b := &compatTenantBus{handlers: make(map[string]func(*pb.BusPacket) error)}
	m := &compatTenantMetrics{}
	mode := HandshakeModeEnforce
	if tc.off {
		mode = HandshakeModeOff
	}
	e := NewEngine(b, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), store, m).
		WithSessionMiddleware(NewSessionTokenMiddleware(issuer, mode, NewHandshakeMissingTracker()))
	t.Cleanup(e.Stop)
	if err := e.Start(); err != nil {
		t.Fatal(err)
	}
	// Exercise the production identity branch through the registered handler.
	e.WithProductionIdentityEnforcement(tc.production)
	subject := capsdk.SubjectCancel
	if tc.result {
		subject = capsdk.SubjectResult
	}
	handler := b.handlers[subject]
	if handler == nil || !reflect.DeepEqual(s, expected) {
		t.Fatal("invalid initial fixture or missing subscription")
	}
	err := handler(compatTenantPacket(t, issuer, tc))
	if tc.retry {
		var retry *retryableError
		if !errors.As(err, &retry) || retry.RetryDelay() != retryDelayStore || !errors.Is(err, store.lookupErr) {
			t.Fatalf("tenant lookup must request store retry, got %v", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	compatTenantAssert(t, tc, s, expected, b, m)
}

func compatTenantAssert(t *testing.T, tc compatTenantCase, s, expected *fakeJobStore, b *compatTenantBus, m *compatTenantMetrics) {
	t.Helper()
	publications, rejected := 0, 0
	if tc.admitted {
		expected.states["job-b"] = JobStateCancelled
		if tc.result {
			expected.states["job-b"] = JobStateSucceeded
			expected.ptrs["job-b"] = "redis://result-b"
			publications = 1
		}
	} else if !tc.retry {
		rejected = 1
	}
	if !reflect.DeepEqual(s, expected) {
		t.Errorf("job store differs: state=%q want=%q", s.states["job-b"], expected.states["job-b"])
	}
	if got := b.snapshotPublished(); len(got) != publications {
		t.Errorf("publications=%d want=%d", len(got), publications)
	} else if publications == 1 && (got[0].subject != capsdk.SubjectAcceptedResult || got[0].packet.GetJobResult().GetStatus() != pb.JobStatus_JOB_STATUS_SUCCEEDED) {
		t.Error("result publication does not carry the accepted outcome")
	}
	if m.rejected != rejected {
		t.Errorf("validation rejections=%d want=%d", m.rejected, rejected)
	}
}
