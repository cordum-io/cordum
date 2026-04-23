package scheduler

import (
	"context"
	"crypto/ed25519"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/infra/config"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func setSchedulerDelegationKeys(t *testing.T) delegation.SigningKey {
	t.Helper()

	signingKey, err := delegation.GenerateSigningKey("dlg-1")
	if err != nil {
		t.Fatalf("GenerateSigningKey() error = %v", err)
	}
	privatePEM, err := delegation.EncodePrivateKeyPEM(signingKey.PrivateKey)
	if err != nil {
		t.Fatalf("EncodePrivateKeyPEM() error = %v", err)
	}
	publicKey, err := delegation.EncodePublicKeyBase64(signingKey.PublicKey())
	if err != nil {
		t.Fatalf("EncodePublicKeyBase64() error = %v", err)
	}
	t.Setenv("CORDUM_DELEGATION_PRIVATE_KEY", string(privatePEM))
	t.Setenv("CORDUM_DELEGATION_PUBLIC_KEY_DLG_1", publicKey)
	t.Setenv("CORDUM_DELEGATION_KEY_ID", "dlg-1")
	return signingKey
}

func createSchedulerDelegationAgent(t *testing.T, agentStore *infraStore.AgentIdentityStore, tenant, id string, actions, topics []string) {
	t.Helper()
	_ = tenant // AgentIdentity has no TenantID field today; tenant
	// scoping is enforced at the gateway middleware, not at the
	// agent-identity store layer.
	if _, err := agentStore.Create(context.Background(), infraStore.AgentIdentity{
		ID:            id,
		Name:          id,
		Owner:         "admin",
		RiskTier:      "low",
		Status:        "active",
		AllowedTools:  actions,
		AllowedTopics: topics,
	}); err != nil {
		t.Fatalf("Create(%s) error = %v", id, err)
	}
}

func newDelegationDispatchTestStore(t *testing.T) (*infraStore.RedisJobStore, *infraStore.AgentIdentityStore, *delegation.RedisRevocationStore) {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	jobStore, err := infraStore.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("NewRedisJobStore() error = %v", err)
	}
	t.Cleanup(func() { _ = jobStore.Close() })

	agentStore := infraStore.NewAgentIdentityStoreFromClient(jobStore.Client())
	revocations := delegation.NewRedisRevocationStoreFromClient(jobStore.Client())
	return jobStore, agentStore, revocations
}

func issueSchedulerDelegationToken(t *testing.T, jobStore *infraStore.RedisJobStore, agentStore *infraStore.AgentIdentityStore, signingKey delegation.SigningKey, tenant, delegator, target string) (string, delegation.VerifiedToken) {
	t.Helper()

	service, err := delegation.NewTokenService(
		signingKey,
		map[string]ed25519.PublicKey{signingKey.KID: signingKey.PublicKey()},
		schedulerDelegationPermissionsResolver{store: agentStore},
		delegation.NewRedisRevocationStoreFromClient(jobStore.Client()),
	)
	if err != nil {
		t.Fatalf("new token service: %v", err)
	}
	token, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            tenant,
		DelegatingAgentID: delegator,
		TargetAgentID:     target,
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.default"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}
	verified, err := service.VerifyDelegationToken(context.Background(), token, target)
	if err != nil {
		t.Fatalf("VerifyDelegationToken() error = %v", err)
	}
	return token, verified
}

func TestProcessJobPersistsDelegationLineageAtDispatch(t *testing.T) {
	signingKey := setSchedulerDelegationKeys(t)
	jobStore, agentStore, _ := newDelegationDispatchTestStore(t)
	createSchedulerDelegationAgent(t, agentStore, "default", "agent-a", []string{"read", "write"}, []string{"job.default"})
	createSchedulerDelegationAgent(t, agentStore, "default", "agent-b", []string{"read"}, []string{"job.default"})

	token, verified := issueSchedulerDelegationToken(t, jobStore, agentStore, signingKey, "default", "agent-a", "agent-b")
	jobID := "job-delegation-valid"
	if err := jobStore.SetDelegationDispatchToken(context.Background(), jobID, model.DelegationDispatchToken{
		Token:    token,
		Audience: "agent-b",
	}); err != nil {
		t.Fatalf("SetDelegationDispatchToken() error = %v", err)
	}

	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.default",
		TenantId: "default",
		Labels: map[string]string{
			config.LabelDelegationDepth:        "1",
			config.LabelDelegationIssuer:       "agent-a",
			config.LabelDelegationIssuerChain:  "agent-a",
			config.LabelDelegationParentIssuer: "agent-a",
			config.LabelDelegationScope:        "read",
			config.LabelDelegationJTI:          verified.JTI,
			config.LabelDelegationSubject:      "agent-a",
		},
	}

	bus := &fakeBus{}
	sink := &recordingSink{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil).WithDispatchAuditSink(sink)
	if err := engine.processJob(testCtx(t), req, "trace-delegation-valid"); err != nil {
		t.Fatalf("processJob() error = %v", err)
	}

	lineage, err := jobStore.GetDelegationLineage(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetDelegationLineage() error = %v", err)
	}
	if lineage.TokenJTI != verified.JTI {
		t.Fatalf("TokenJTI = %q, want %q", lineage.TokenJTI, verified.JTI)
	}
	if lineage.Audience != "agent-b" {
		t.Fatalf("Audience = %q, want agent-b", lineage.Audience)
	}
	if lineage.RootIssuer != "agent-a" || lineage.ParentIssuer != "agent-a" {
		t.Fatalf("unexpected lineage issuers: %#v", lineage)
	}
	if lineage.ChainDepth != 1 {
		t.Fatalf("ChainDepth = %d, want 1", lineage.ChainDepth)
	}
	if len(lineage.Scope) != 1 || lineage.Scope[0] != "read" {
		t.Fatalf("Scope = %#v, want [read]", lineage.Scope)
	}
	if len(bus.snapshotPublished()) != 1 || bus.snapshotPublished()[0].subject != "job.default" {
		t.Fatalf("expected dispatch publish to job.default, got %+v", bus.snapshotPublished())
	}
	if sink.count() != 1 {
		t.Fatalf("expected 1 dispatch audit event, got %d", sink.count())
	}
	event := sink.last()
	if event.EventType != audit.EventDelegationLineage {
		t.Fatalf("event_type = %q, want %q", event.EventType, audit.EventDelegationLineage)
	}
	if event.JobID != jobID {
		t.Fatalf("job_id = %q, want %q", event.JobID, jobID)
	}
	if event.Extra["chain"] != "agent-a" {
		t.Fatalf("chain = %q, want agent-a", event.Extra["chain"])
	}
	if event.Extra["chain_length"] != "1" {
		t.Fatalf("chain_length = %q, want 1", event.Extra["chain_length"])
	}
}

func TestProcessJobFailsWhenDelegationRevokedBeforeDispatch(t *testing.T) {
	signingKey := setSchedulerDelegationKeys(t)
	jobStore, agentStore, revocations := newDelegationDispatchTestStore(t)
	createSchedulerDelegationAgent(t, agentStore, "default", "agent-a", []string{"read", "write"}, []string{"job.default"})
	createSchedulerDelegationAgent(t, agentStore, "default", "agent-b", []string{"read"}, []string{"job.default"})

	token, verified := issueSchedulerDelegationToken(t, jobStore, agentStore, signingKey, "default", "agent-a", "agent-b")
	jobID := "job-delegation-revoked"
	if err := jobStore.SetDelegationDispatchToken(context.Background(), jobID, model.DelegationDispatchToken{
		Token:    token,
		Audience: "agent-b",
	}); err != nil {
		t.Fatalf("SetDelegationDispatchToken() error = %v", err)
	}
	if err := revocations.Revoke(context.Background(), verified.JTI, verified.ExpiresAt); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	req := &pb.JobRequest{
		JobId:    jobID,
		Topic:    "job.default",
		TenantId: "default",
		Labels: map[string]string{
			config.LabelDelegationDepth:       "1",
			config.LabelDelegationIssuerChain: "agent-a",
			config.LabelDelegationJTI:         verified.JTI,
		},
	}

	bus := &fakeBus{}
	sink := &recordingSink{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil).WithDispatchAuditSink(sink)
	if err := engine.processJob(testCtx(t), req, "trace-delegation-revoked"); err != nil {
		t.Fatalf("processJob() error = %v", err)
	}

	state, err := jobStore.GetState(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if state != JobStateFailed {
		t.Fatalf("state = %s, want %s", state, JobStateFailed)
	}
	failureReason, err := jobStore.GetFailureReason(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetFailureReason() error = %v", err)
	}
	if failureReason != delegationRevokedBeforeDispatchReasonCode {
		t.Fatalf("failure reason = %q, want %q", failureReason, delegationRevokedBeforeDispatchReasonCode)
	}
	lineage, err := jobStore.GetDelegationLineage(context.Background(), jobID)
	if err != nil {
		t.Fatalf("GetDelegationLineage() error = %v", err)
	}
	if lineage.TokenJTI != "" {
		t.Fatalf("expected no lineage persisted on revoked token, got %#v", lineage)
	}

	published := bus.snapshotPublished()
	if len(published) != 1 || published[0].subject != capsdk.SubjectDLQ {
		t.Fatalf("expected one DLQ publish, got %+v", published)
	}
	result := published[0].packet.GetJobResult()
	if result == nil {
		t.Fatal("expected DLQ job result payload")
	}
	if result.GetErrorCode() != delegationRevokedBeforeDispatchReasonCode {
		t.Fatalf("error_code = %q, want %q", result.GetErrorCode(), delegationRevokedBeforeDispatchReasonCode)
	}
	if sink.count() != 1 {
		t.Fatalf("expected 1 dispatch audit event, got %d", sink.count())
	}
	event := sink.last()
	if event.EventType != audit.EventDelegationRevokedBeforeDispatch {
		t.Fatalf("event_type = %q, want %q", event.EventType, audit.EventDelegationRevokedBeforeDispatch)
	}
	if event.Reason != delegationRevokedBeforeDispatchReasonCode {
		t.Fatalf("reason = %q, want %q", event.Reason, delegationRevokedBeforeDispatchReasonCode)
	}
}

func TestProcessJobWithoutDelegationStoresNoLineage(t *testing.T) {
	jobStore, _, _ := newDelegationDispatchTestStore(t)

	req := &pb.JobRequest{
		JobId:    "job-no-delegation",
		Topic:    "job.default",
		TenantId: "default",
	}

	bus := &fakeBus{}
	engine := NewEngine(bus, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil)
	if err := engine.processJob(testCtx(t), req, "trace-no-delegation"); err != nil {
		t.Fatalf("processJob() error = %v", err)
	}

	lineage, err := jobStore.GetDelegationLineage(context.Background(), req.GetJobId())
	if err != nil {
		t.Fatalf("GetDelegationLineage() error = %v", err)
	}
	if lineage.TokenJTI != "" || lineage.ChainDepth != 0 {
		t.Fatalf("expected empty delegation lineage, got %#v", lineage)
	}
}

func TestEmitDelegationLineageAuditDedupesByJobAndToken(t *testing.T) {
	jobStore, _, _ := newDelegationDispatchTestStore(t)
	sink := &recordingSink{}
	engine := NewEngine(&fakeBus{}, NewSafetyBasic(), newTestRegistry(t), NewNaiveStrategy(), jobStore, nil).WithDispatchAuditSink(sink)

	req := &pb.JobRequest{
		JobId:    "job-delegation-dedupe",
		Topic:    "job.default",
		TenantId: "default",
		Labels:   map[string]string{"agent_id": "agent-b"},
	}
	lineage := model.DelegationLineage{
		TokenJTI:    "dlg-jti-1",
		Tenant:      "default",
		Audience:    "agent-b",
		IssuerChain: []model.DelegationChainLink{{AgentID: "agent-a"}, {AgentID: "agent-b"}},
	}

	engine.emitDelegationLineageAudit(context.Background(), req, lineage)
	engine.emitDelegationLineageAudit(context.Background(), req, lineage)

	if sink.count() != 1 {
		t.Fatalf("expected deduped lineage audit count 1, got %d", sink.count())
	}
	event := sink.last()
	if event.Extra["chain"] != "agent-a>agent-b" {
		t.Fatalf("chain = %q, want agent-a>agent-b", event.Extra["chain"])
	}
	if event.Extra["chain_length"] != "2" {
		t.Fatalf("chain_length = %q, want 2", event.Extra["chain_length"])
	}
}
