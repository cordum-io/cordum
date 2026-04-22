package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/scheduler"
	infrabus "github.com/cordum/cordum/core/infra/bus"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/golang-jwt/jwt/v5"
)

type integrationDelegationAuditSink struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

type allowDelegationSafetyChecker struct{}

func (allowDelegationSafetyChecker) Check(_ context.Context, req *pb.JobRequest) (scheduler.SafetyDecisionRecord, error) {
	if req == nil || strings.TrimSpace(req.GetTopic()) == "" {
		return scheduler.SafetyDecisionRecord{Decision: scheduler.SafetyDeny, Reason: "missing topic"}, nil
	}
	return scheduler.SafetyDecisionRecord{Decision: scheduler.SafetyAllow}, nil
}

func (s *integrationDelegationAuditSink) Send(event audit.SIEMEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *integrationDelegationAuditSink) Emit(_ context.Context, event audit.SIEMEvent) {
	s.Send(event)
}

func (s *integrationDelegationAuditSink) Close() error { return nil }

func (s *integrationDelegationAuditSink) snapshot() []audit.SIEMEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.SIEMEvent(nil), s.events...)
}

func (s *integrationDelegationAuditSink) countByType(eventType string) int {
	count := 0
	for _, event := range s.snapshot() {
		if event.EventType == eventType {
			count++
		}
	}
	return count
}

func (s *integrationDelegationAuditSink) firstByType(eventType string) (audit.SIEMEvent, bool) {
	for _, event := range s.snapshot() {
		if event.EventType == eventType {
			return event, true
		}
	}
	return audit.SIEMEvent{}, false
}

func newDelegationSubmitEngine(t *testing.T, bus *stubBus, jobStore storeBackedJobStore, sink *integrationDelegationAuditSink) *scheduler.Engine {
	t.Helper()

	registry := scheduler.NewMemoryRegistry()
	t.Cleanup(registry.Close)
	registry.UpdateHeartbeat(&pb.Heartbeat{
		WorkerId:        "worker-target",
		Pool:            "default",
		ActiveJobs:      0,
		MaxParallelJobs: 1,
	})

	engine := scheduler.NewEngine(
		bus,
		allowDelegationSafetyChecker{},
		registry,
		scheduler.NewLeastLoadedStrategy(scheduler.PoolRouting{
			Topics: map[string][]string{
				"job.test": {"default"},
			},
		}),
		jobStore,
		nil,
	).WithDispatchAuditSink(sink)
	t.Cleanup(engine.Stop)
	return engine
}

type storeBackedJobStore interface {
	scheduler.JobStore
	GetDelegationLineage(ctx context.Context, jobID string) (model.DelegationLineage, error)
	GetState(ctx context.Context, jobID string) (model.JobState, error)
	GetFailureReason(ctx context.Context, jobID string) (string, error)
	GetJobRequest(ctx context.Context, jobID string) (*pb.JobRequest, error)
}

func submitDelegatedJob(t *testing.T, s *server, principalID, token string) (string, *httptest.ResponseRecorder) {
	t.Helper()

	body := `{"prompt":"hello","topic":"job.test","delegation_token":"` + token + `","labels":{"preferred_worker_id":"worker-target"}}`
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: principalID,
		Role:        "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)

	jobID := ""
	if rec.Code == http.StatusOK {
		var resp struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode submit response: %v", err)
		}
		jobID = resp.JobID
		if strings.TrimSpace(jobID) == "" {
			t.Fatalf("expected job_id in submit response, got %s", rec.Body.String())
		}
	}
	return jobID, rec
}

func latestPublishedPacketForSubject(t *testing.T, bus *stubBus, subject string) *pb.BusPacket {
	t.Helper()

	bus.mu.Lock()
	defer bus.mu.Unlock()
	for i := len(bus.published) - 1; i >= 0; i-- {
		if bus.published[i].subject == subject {
			return bus.published[i].packet
		}
	}
	t.Fatalf("no published packet for subject %q; got %+v", subject, bus.published)
	return nil
}

func issueThreeLinkSubmitToken(t *testing.T, s *server) (string, delegation.VerifiedToken) {
	t.Helper()

	tokenRootMiddle, _ := issueDelegationTokenForTests(t, s, delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "rootBot",
		TargetAgentID:     "middleBot",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.test"},
	}, "middleBot")
	tokenMiddleLeaf, _ := issueDelegationTokenForTests(t, s, delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "middleBot",
		TargetAgentID:     "leafBot",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.test"},
		ParentToken:       tokenRootMiddle,
	}, "leafBot")
	return issueDelegationTokenForTests(t, s, delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "leafBot",
		TargetAgentID:     "leafBot",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.test"},
		ParentToken:       tokenMiddleLeaf,
	}, "leafBot")
}

func assertDelegationChain(t *testing.T, lineage model.DelegationLineage, want []string) {
	t.Helper()

	if lineage.ChainDepth != len(want) {
		t.Fatalf("ChainDepth = %d, want %d", lineage.ChainDepth, len(want))
	}
	if len(lineage.IssuerChain) != len(want) {
		t.Fatalf("IssuerChain length = %d, want %d", len(lineage.IssuerChain), len(want))
	}
	for i, wantAgentID := range want {
		if got := lineage.IssuerChain[i].AgentID; got != wantAgentID {
			t.Fatalf("IssuerChain[%d] = %q, want %q", i, got, wantAgentID)
		}
	}
}

func TestDelegationSubmitIntegration_ThreeLinkChainPersistsLineageAndAudits(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)
	sink := &integrationDelegationAuditSink{}
	s.auditExporter = sink

	createDelegationAgent(t, s, "default", "rootBot", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "middleBot", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "leafBot", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "leafBot", "worker-leaf"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	submitToken, verified := issueThreeLinkSubmitToken(t, s)
	engine := newDelegationSubmitEngine(t, bus, s.jobStore, sink)

	jobID, submitRec := submitDelegatedJob(t, s, "worker-leaf", submitToken)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", submitRec.Code, submitRec.Body.String())
	}

	if err := engine.HandlePacket(latestPublishedPacketForSubject(t, bus, capsdk.SubjectSubmit)); err != nil {
		t.Fatalf("HandlePacket() error = %v", err)
	}

	ctx := context.Background()
	state, err := s.jobStore.GetState(ctx, jobID)
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if state != model.JobStateRunning {
		t.Fatalf("state = %q, want %q", state, model.JobStateRunning)
	}

	lineage, err := s.jobStore.GetDelegationLineage(ctx, jobID)
	if err != nil {
		t.Fatalf("GetDelegationLineage() error = %v", err)
	}
	assertDelegationChain(t, lineage, []string{"rootBot", "middleBot", "leafBot"})
	if lineage.TokenJTI != verified.JTI {
		t.Fatalf("TokenJTI = %q, want %q", lineage.TokenJTI, verified.JTI)
	}
	if lineage.Audience != "leafBot" {
		t.Fatalf("Audience = %q, want leafBot", lineage.Audience)
	}
	if lineage.RootIssuer != "rootBot" {
		t.Fatalf("RootIssuer = %q, want rootBot", lineage.RootIssuer)
	}
	if lineage.ParentIssuer != "leafBot" {
		t.Fatalf("ParentIssuer = %q, want leafBot", lineage.ParentIssuer)
	}

	dispatchPacket := latestPublishedPacketForSubject(t, bus, infrabus.DirectSubject("worker-target"))
	if dispatchPacket.GetJobRequest() == nil || dispatchPacket.GetJobRequest().GetJobId() != jobID {
		t.Fatalf("expected direct dispatch for job %s, got %+v", jobID, dispatchPacket)
	}

	if sink.countByType(audit.EventSafetyDecision) != 1 || sink.countByType(audit.EventDelegationLineage) != 1 {
		t.Fatalf("unexpected audit event counts: %#v", sink.snapshot())
	}
	decisionEvent, ok := sink.firstByType(audit.EventSafetyDecision)
	if !ok {
		t.Fatalf("missing %s event", audit.EventSafetyDecision)
	}
	if decisionEvent.Extra["delegation.depth"] != "3" {
		t.Fatalf("delegation.depth = %q, want 3", decisionEvent.Extra["delegation.depth"])
	}
	if decisionEvent.Extra["delegation.root_issuer"] != "rootBot" {
		t.Fatalf("delegation.root_issuer = %q, want rootBot", decisionEvent.Extra["delegation.root_issuer"])
	}
	if decisionEvent.Extra["delegation.parent_issuer"] != "leafBot" {
		t.Fatalf("delegation.parent_issuer = %q, want leafBot", decisionEvent.Extra["delegation.parent_issuer"])
	}
	if decisionEvent.Extra["delegation.jti"] != verified.JTI {
		t.Fatalf("delegation.jti = %q, want %q", decisionEvent.Extra["delegation.jti"], verified.JTI)
	}

	lineageEvent, ok := sink.firstByType(audit.EventDelegationLineage)
	if !ok {
		t.Fatalf("missing %s event", audit.EventDelegationLineage)
	}
	if lineageEvent.Extra["chain"] != "rootBot>middleBot>leafBot" {
		t.Fatalf("chain = %q, want rootBot>middleBot>leafBot", lineageEvent.Extra["chain"])
	}
	if lineageEvent.Extra["chain_length"] != "3" {
		t.Fatalf("chain_length = %q, want 3", lineageEvent.Extra["chain_length"])
	}

	getReq := withAuth(httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+jobID, nil), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-leaf",
		Role:        "admin",
	})
	getReq.Header.Set("X-Tenant-ID", "default")
	getReq.SetPathValue("id", jobID)
	getRec := httptest.NewRecorder()
	s.handleGetJob(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}

	var getResp map[string]any
	if err := json.Unmarshal(getRec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode get response: %v", err)
	}
	delegationResp, ok := getResp["delegation"].(map[string]any)
	if !ok {
		t.Fatalf("expected delegation object, got %#v", getResp["delegation"])
	}
	if delegationResp["chain_depth"] != float64(3) {
		t.Fatalf("delegation.chain_depth = %#v, want 3", delegationResp["chain_depth"])
	}
	if delegationResp["root_issuer"] != "rootBot" || delegationResp["parent_issuer"] != "leafBot" {
		t.Fatalf("unexpected delegation issuers in response: %#v", delegationResp)
	}
	chain, ok := delegationResp["chain"].([]any)
	if !ok || len(chain) != 3 {
		t.Fatalf("delegation.chain = %#v, want 3 entries", delegationResp["chain"])
	}
}

func TestDelegationSubmitIntegration_ExpiredTokenRejectedBeforeEnqueue(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	enableTestAuth(s)
	signingKey := setDelegationKeys(t)
	sink := &integrationDelegationAuditSink{}
	s.auditExporter = sink

	createDelegationAgent(t, s, "default", "rootBot", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "leafBot", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "leafBot", "worker-leaf"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	now := time.Now().UTC().Add(-2 * time.Hour)
	expiresAt := now.Add(-15 * time.Minute)
	token := signDelegationToken(t, signingKey, delegation.DelegationClaims{
		Tenant:         "default",
		AllowedActions: []string{"read"},
		AllowedTopics:  []string{"job.test"},
		DelegationChain: []delegation.ChainLink{{
			AgentID:   "rootBot",
			IssuedAt:  now.Format(time.RFC3339Nano),
			ExpiresAt: expiresAt.Format(time.RFC3339Nano),
			JTI:       "dlg-expired-root",
			IssuedBy:  "cordum",
		}},
		ChainDepth: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "cordum",
			Subject:   "rootBot",
			Audience:  jwt.ClaimStrings{"leafBot"},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        "dlg-expired-root",
		},
	})

	_, rec := submitDelegatedJob(t, s, "worker-leaf", token)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error_code"] != "expired" {
		t.Fatalf("error_code = %#v, want expired", resp["error_code"])
	}
	if sink.countByType(audit.EventDelegationLineage) != 0 {
		t.Fatalf("unexpected delegation.lineage events: %#v", sink.snapshot())
	}
	if sink.countByType(audit.EventDelegationRejected) != 1 {
		t.Fatalf("expected 1 delegation.rejected event, got %#v", sink.snapshot())
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, published := range bus.published {
		if published.subject == capsdk.SubjectSubmit {
			t.Fatalf("expected no submit publish for expired token, got %+v", bus.published)
		}
	}
}

func TestDelegationSubmitIntegration_RevokedBeforeDispatchFailsJob(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)
	sink := &integrationDelegationAuditSink{}
	s.auditExporter = sink

	createDelegationAgent(t, s, "default", "rootBot", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "middleBot", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "leafBot", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "leafBot", "worker-leaf"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	submitToken, verified := issueThreeLinkSubmitToken(t, s)
	engine := newDelegationSubmitEngine(t, bus, s.jobStore, sink)

	jobID, submitRec := submitDelegatedJob(t, s, "worker-leaf", submitToken)
	if submitRec.Code != http.StatusOK {
		t.Fatalf("submit status=%d body=%s", submitRec.Code, submitRec.Body.String())
	}

	if err := delegation.NewRedisRevocationStoreFromClient(s.jobStore.Client()).Revoke(context.Background(), verified.JTI, verified.ExpiresAt); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if err := engine.HandlePacket(latestPublishedPacketForSubject(t, bus, capsdk.SubjectSubmit)); err != nil {
		t.Fatalf("HandlePacket() error = %v", err)
	}

	ctx := context.Background()
	state, err := s.jobStore.GetState(ctx, jobID)
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if state != model.JobStateFailed {
		t.Fatalf("state = %q, want %q", state, model.JobStateFailed)
	}
	reason, err := s.jobStore.GetFailureReason(ctx, jobID)
	if err != nil {
		t.Fatalf("GetFailureReason() error = %v", err)
	}
	if reason != "delegation.revoked_before_dispatch" {
		t.Fatalf("failure reason = %q, want delegation.revoked_before_dispatch", reason)
	}
	if sink.countByType(audit.EventDelegationLineage) != 0 {
		t.Fatalf("unexpected delegation.lineage event after revocation: %#v", sink.snapshot())
	}
	event, ok := sink.firstByType(audit.EventDelegationRevokedBeforeDispatch)
	if !ok {
		t.Fatalf("missing %s event", audit.EventDelegationRevokedBeforeDispatch)
	}
	if event.Reason != "delegation.revoked_before_dispatch" {
		t.Fatalf("audit reason = %q, want delegation.revoked_before_dispatch", event.Reason)
	}
}

func TestDelegationSubmitIntegration_InvalidChainRejectedAtSubmit(t *testing.T) {
	s, bus, _ := newTestGateway(t)
	enableTestAuth(s)
	signingKey := setDelegationKeys(t)
	sink := &integrationDelegationAuditSink{}
	s.auditExporter = sink

	createDelegationAgent(t, s, "default", "rootBot", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "middleBot", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "leafBot", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "leafBot", "worker-leaf"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(30 * time.Minute)
	token := signDelegationToken(t, signingKey, delegation.DelegationClaims{
		Tenant:         "default",
		AllowedActions: []string{"read"},
		AllowedTopics:  []string{"job.test"},
		DelegationChain: []delegation.ChainLink{
			{AgentID: "rootBot", IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: expiresAt.Format(time.RFC3339Nano), JTI: "dlg-root", IssuedBy: "cordum"},
			{AgentID: "middleBot", IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: expiresAt.Format(time.RFC3339Nano), JTI: "dlg-middle", ParentJTI: "dlg-root", IssuedBy: "rootBot"},
			{AgentID: "leafBot", IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: expiresAt.Format(time.RFC3339Nano), JTI: "dlg-leaf", ParentJTI: "dlg-middle", IssuedBy: "middleBot"},
			{AgentID: "leafBot", IssuedAt: now.Format(time.RFC3339Nano), ExpiresAt: expiresAt.Format(time.RFC3339Nano), JTI: "dlg-too-deep", ParentJTI: "dlg-leaf", IssuedBy: "leafBot"},
		},
		ChainDepth: 4,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "cordum",
			Subject:   "leafBot",
			Audience:  jwt.ClaimStrings{"leafBot"},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        "dlg-too-deep",
		},
	})

	_, rec := submitDelegatedJob(t, s, "worker-leaf", token)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error_code"] != "chain_too_deep" {
		t.Fatalf("error_code = %#v, want chain_too_deep", resp["error_code"])
	}
	if sink.countByType(audit.EventDelegationLineage) != 0 {
		t.Fatalf("unexpected delegation.lineage events: %#v", sink.snapshot())
	}

	bus.mu.Lock()
	defer bus.mu.Unlock()
	for _, published := range bus.published {
		if published.subject == capsdk.SubjectSubmit {
			t.Fatalf("expected no submit publish for invalid chain token, got %+v", bus.published)
		}
	}
}
