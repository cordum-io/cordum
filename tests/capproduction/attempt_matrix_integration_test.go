//go:build capproduction

package capproduction

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/core/model"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h *productionHarness) testAttemptAndReplayMatrix(t *testing.T) {
	t.Helper()
	matrix := h.prepareAttemptMatrix(t)
	h.assertStaleAttemptRejected(t, matrix)
	h.assertReplayAndKeyRotation(t, matrix)
	h.publishWorkerEvent(t, matrix.token, "worker-key", h.workerKey,
		capsdk.SubjectResult, randomBytes(t, 16), matrix.identity,
		&pb.BusPacket{Payload: &pb.BusPacket_JobResult{JobResult: productionResult(
			matrix.jobID, h.workerID, matrix.current, matrix.identity,
		)}})
	h.awaitState(t, matrix.jobID, model.JobStateSucceeded)
	h.awaitDurableResult(t, matrix.jobID, 3)
}

type attemptMatrix struct {
	jobID    string
	token    string
	identity *pb.IdentityBinding
	stale    *pb.DispatchIdentity
	current  *pb.DispatchIdentity
}

func (h *productionHarness) prepareAttemptMatrix(t *testing.T) attemptMatrix {
	t.Helper()
	session := h.exchangeWorkerSession(t)
	identity := &pb.IdentityBinding{
		TenantId: h.tenantID, PrincipalId: "principal-" + h.runID, ActorId: "actor-" + h.runID,
	}
	jobID := "job-matrix-" + h.runID
	request, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: jobID, Topic: productionTopic}, identity,
	)
	if err != nil {
		t.Fatalf("normalize matrix request: %v", err)
	}
	ctx := context.Background()
	if err := h.store.SetJobMeta(ctx, request); err != nil {
		t.Fatalf("set matrix job metadata: %v", err)
	}
	if err := h.store.SetJobRequest(ctx, request); err != nil {
		t.Fatalf("set matrix job request: %v", err)
	}
	if err := h.store.SetState(ctx, jobID, model.JobStatePending); err != nil {
		t.Fatalf("set matrix job pending: %v", err)
	}
	staleID, staleAttempt, err := h.store.BeginDispatch(ctx, jobID, h.workerID, h.tenantID)
	if err != nil {
		t.Fatalf("begin stale dispatch: %v", err)
	}
	currentID, currentAttempt, err := h.store.BeginDispatch(ctx, jobID, h.workerID, h.tenantID)
	if err != nil {
		t.Fatalf("begin current dispatch: %v", err)
	}
	if err := h.store.SetState(ctx, jobID, model.JobStateRunning); err != nil {
		t.Fatalf("set matrix job running: %v", err)
	}
	stale := &pb.DispatchIdentity{
		DispatchId: staleID, Attempt: uint64(staleAttempt), AssignedWorkerId: h.workerID,
	}
	current := &pb.DispatchIdentity{
		DispatchId: currentID, Attempt: uint64(currentAttempt), AssignedWorkerId: h.workerID,
	}
	return attemptMatrix{jobID: jobID, token: session.Token, identity: identity, stale: stale, current: current}
}

func (h *productionHarness) assertStaleAttemptRejected(t *testing.T, matrix attemptMatrix) {
	t.Helper()
	h.publishWorkerEvent(t, matrix.token, "worker-key", h.workerKey,
		capsdk.SubjectResult, randomBytes(t, 16), matrix.identity,
		&pb.BusPacket{Payload: &pb.BusPacket_JobResult{JobResult: productionResult(matrix.jobID, h.workerID, matrix.stale, matrix.identity)}})
	h.publishWorkerEvent(t, matrix.token, "worker-key", h.workerKey,
		capsdk.SubjectProgress, randomBytes(t, 16), matrix.identity,
		&pb.BusPacket{Payload: &pb.BusPacket_JobProgress{JobProgress: productionProgress(matrix.jobID, matrix.stale, matrix.identity, 10)}})
	h.publishWorkerEvent(t, matrix.token, "worker-key", h.workerKey,
		capsdk.SubjectCancel, randomBytes(t, 16), matrix.identity,
		&pb.BusPacket{Payload: &pb.BusPacket_JobCancel{JobCancel: productionCancel(matrix.jobID, matrix.stale, matrix.identity)}})
	h.assertStateRemains(t, matrix.jobID, model.JobStateRunning)
	h.assertRuntimeEvents(t, matrix.jobID, 0)
}

func (h *productionHarness) assertReplayAndKeyRotation(t *testing.T, matrix attemptMatrix) {
	t.Helper()
	messageID := randomBytes(t, 16)
	progress := &pb.BusPacket{Payload: &pb.BusPacket_JobProgress{JobProgress: productionProgress(matrix.jobID, matrix.current, matrix.identity, 25)}}
	wire := h.signedWorkerEvent(t, matrix.token, "worker-key", h.workerKey,
		capsdk.SubjectProgress, messageID, matrix.identity, progress)
	h.publishRaw(t, capsdk.SubjectProgress, wire)
	h.publishRaw(t, capsdk.SubjectProgress, wire)
	conflict := &pb.BusPacket{Payload: &pb.BusPacket_JobProgress{JobProgress: productionProgress(matrix.jobID, matrix.current, matrix.identity, 30)}}
	h.publishWorkerEvent(t, matrix.token, "worker-key", h.workerKey,
		capsdk.SubjectProgress, messageID, matrix.identity, conflict)
	h.awaitRuntimeEvents(t, matrix.jobID, 1)

	h.publishWorkerEvent(t, matrix.token, "unknown-key", generateP256(t),
		capsdk.SubjectProgress, randomBytes(t, 16), matrix.identity,
		&pb.BusPacket{Payload: &pb.BusPacket_JobProgress{JobProgress: productionProgress(matrix.jobID, matrix.current, matrix.identity, 40)}})
	time.Sleep(150 * time.Millisecond)
	h.assertRuntimeEvents(t, matrix.jobID, 1)
	h.publishWorkerEvent(t, matrix.token, "worker-key-next", h.rotatedKey,
		capsdk.SubjectProgress, randomBytes(t, 16), matrix.identity,
		&pb.BusPacket{Payload: &pb.BusPacket_JobProgress{JobProgress: productionProgress(matrix.jobID, matrix.current, matrix.identity, 50)}})
	h.awaitRuntimeEvents(t, matrix.jobID, 2)
}

func (h *productionHarness) exchangeWorkerSession(t *testing.T) *capsdk.WorkerHandshakeSession {
	t.Helper()
	config := &capsdk.WorkerTrustConfig{
		WorkerID: h.workerID, ExpectedAgentID: h.agentID, TenantID: h.tenantID,
		Audience: capsdk.WorkerHandshakeAudience, ProofKeyID: "worker-key",
		ProofPrivateKey: h.workerKey, ExpectedSchedulerID: "cordum-scheduler",
		SchedulerPublicKeys: map[string]*ecdsa.PublicKey{"scheduler-key": &h.schedulerKey.PublicKey},
		SDKVersion:          "cap-go-production-matrix",
	}
	request, err := capsdk.BuildWorkerHandshakeChallengeRequest(config, capsdk.WorkerHandshakeRequestOptions{
		RequestID: randomHex(t, 16), TraceID: randomHex(t, 16),
		Purpose:     agentv1.WorkerHandshakePurpose_WORKER_HANDSHAKE_PURPOSE_ISSUE,
		ClientNonce: randomBytes(t, capsdk.WorkerHandshakeNonceSize), CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("build worker challenge request: %v", err)
	}
	challenge := h.requestHandshake(t, capsdk.WorkerHandshakeChallengeSubject, request)
	verified, err := capsdk.VerifyWorkerHandshakeChallenge(config, request, challenge, time.Now())
	if err != nil {
		t.Fatalf("verify worker challenge: %v", err)
	}
	authenticate, err := capsdk.BuildWorkerHandshakeAuthenticate(config, verified, &pb.Handshake{
		ComponentId: h.workerID, Role: pb.ComponentRole_COMPONENT_ROLE_WORKER,
		SupportedVersions: []int32{1}, SdkVersion: config.SDKVersion,
		ReadyTopics: []string{h.directSubject()},
	}, "", time.Now())
	if err != nil {
		t.Fatalf("build worker authenticate: %v", err)
	}
	result := h.requestHandshake(t, capsdk.WorkerHandshakeAuthenticateSubject, authenticate)
	session, err := capsdk.VerifyWorkerHandshakeResult(config, verified, authenticate, result, time.Now())
	if err != nil {
		t.Fatalf("verify worker handshake result: %v", err)
	}
	return session
}

func (h *productionHarness) requestHandshake(t *testing.T, subject string, packet *pb.BusPacket) *pb.BusPacket {
	t.Helper()
	wire, err := capsdk.MarshalWorkerTrustPacket(packet)
	if err != nil {
		t.Fatalf("marshal handshake request: %v", err)
	}
	message, err := h.connection.Request(subject, wire, 3*time.Second)
	if err != nil {
		t.Fatalf("request %s: %v", subject, err)
	}
	response, err := capsdk.UnmarshalWorkerTrustPacket(message.Data)
	if err != nil {
		t.Fatalf("unmarshal %s response: %v", subject, err)
	}
	return response
}

func (h *productionHarness) publishWorkerEvent(
	t *testing.T, token, keyID string, key *ecdsa.PrivateKey, subject string, messageID []byte,
	identity *pb.IdentityBinding, packet *pb.BusPacket,
) {
	t.Helper()
	wire := h.signedWorkerEvent(t, token, keyID, key, subject, messageID, identity, packet)
	h.publishRaw(t, subject, wire)
}

func (h *productionHarness) signedWorkerEvent(
	t *testing.T, token, keyID string, key *ecdsa.PrivateKey, subject string, messageID []byte,
	identity *pb.IdentityBinding, packet *pb.BusPacket,
) []byte {
	t.Helper()
	packet.TraceId, packet.SenderId = "trace-matrix-"+h.runID, h.workerID
	packet.ProtocolVersion, packet.CreatedAt = capsdk.DefaultProtocolVersion, timestamppb.Now()
	packet.AuthToken, packet.Identity = token, identity
	packet.SignatureMetadata = productionMetadata(keyID, subject, messageID)
	wire, err := capsdk.SignProductionPacket(packet, key)
	if err != nil {
		t.Fatalf("sign worker event: %v", err)
	}
	return wire
}

func productionResult(jobID, workerID string, dispatch *pb.DispatchIdentity, identity *pb.IdentityBinding) *pb.JobResult {
	return &pb.JobResult{
		JobId: jobID, WorkerId: workerID, Status: pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultRef: productionResourceRef(jobID), Dispatch: dispatch, Identity: identity,
	}
}

func productionResourceRef(name string) *pb.ResourceRef {
	digest := sha256.Sum256([]byte(name))
	return &pb.ResourceRef{
		Uri: "redis://resources/" + name, ResolverId: "cordum-redis",
		Sha256: digest[:], SizeBytes: 1, MediaType: "application/octet-stream",
		Purpose: "result", ExpiresAt: timestamppb.New(time.Now().Add(time.Minute)),
	}
}

func productionProgress(jobID string, dispatch *pb.DispatchIdentity, identity *pb.IdentityBinding, percent int32) *pb.JobProgress {
	return &pb.JobProgress{JobId: jobID, Percent: percent, Message: "production", Dispatch: dispatch, Identity: identity}
}

func productionCancel(jobID string, dispatch *pb.DispatchIdentity, identity *pb.IdentityBinding) *pb.JobCancel {
	return &pb.JobCancel{JobId: jobID, RequestedBy: hWorker(dispatch), Reason: "stale", Dispatch: dispatch, Identity: identity}
}

func hWorker(dispatch *pb.DispatchIdentity) string { return dispatch.GetAssignedWorkerId() }

func (h *productionHarness) assertStateRemains(t *testing.T, jobID string, want model.JobState) {
	t.Helper()
	time.Sleep(250 * time.Millisecond)
	state, err := h.store.GetState(context.Background(), jobID)
	if err != nil || state != want {
		t.Fatalf("job %s state = %q error=%v, want %q", jobID, state, err, want)
	}
}

func (h *productionHarness) awaitRuntimeEvents(t *testing.T, jobID string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.runtimeEventCount(t, jobID) == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	h.assertRuntimeEvents(t, jobID, want)
}

func (h *productionHarness) assertRuntimeEvents(t *testing.T, jobID string, want int) {
	t.Helper()
	if got := h.runtimeEventCount(t, jobID); got != want {
		t.Fatalf("job %s durable event count = %d, want %d", jobID, got, want)
	}
}

func (h *productionHarness) runtimeEventCount(t *testing.T, jobID string) int {
	t.Helper()
	key := "job:{" + base64.RawURLEncoding.EncodeToString([]byte(jobID)) + "}:runtime"
	fields, err := h.redis.HKeys(context.Background(), key).Result()
	if err != nil {
		t.Fatalf("read runtime fields: %v", err)
	}
	count := 0
	for _, field := range fields {
		if strings.HasPrefix(field, "event:") {
			count++
		}
	}
	return count
}

func randomBytes(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("random bytes: %v", err)
	}
	return value
}
