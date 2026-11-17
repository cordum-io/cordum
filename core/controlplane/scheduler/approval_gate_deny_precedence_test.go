package scheduler

// A job that a policy deny rule matches stays denied when an action gate on
// the same job asks for a human: the scheduler never parks it for approval,
// so no approval can be recorded and no worker ever receives it.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/safetykernel"
	runtimeconfig "github.com/cordum/cordum/core/infra/config"
	infraStore "github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

const (
	gateDenyRedisEnv  = "GATE_DENY_KERNEL_REDIS_URL"
	gateDenyAddrEnv   = "GATE_DENY_KERNEL_ADDR"
	gateDenyPolicyEnv = "GATE_DENY_KERNEL_POLICY"

	gateDenyTopic       = "job.gate-deny.export"
	gateDenyReviewTopic = "job.gate-deny.review"
)

// gateDenyPolicy allows by default and denies gateDenyTopic outright.
const gateDenyPolicy = `version: gate-deny
default_tenant: default
default_decision: allow
rules:
  - id: gate-deny-export
    match:
      topics: [job.gate-deny.export]
    decision: deny
    reason: exports are not allowed
`

// gateDenyAction is a PII upload to a host the URL gate has never seen, which
// the gate answers with REQUIRE_HUMAN.
const gateDenyAction = `{"kind":"url","verb":"write","target_url":"https://203.0.113.10/upload","risk_tags":["data:pii"]}`

// gateDenyKernelBinary is this test binary. Started with gateDenyRedisEnv set,
// its initializer runs the safety kernel instead of the tests until stdin closes.
var gateDenyKernelBinary = gateDenyRunKernel()

func gateDenyRunKernel() string {
	redisURL := os.Getenv(gateDenyRedisEnv)
	if redisURL == "" {
		binary, _ := os.Executable()
		return binary
	}
	go func() {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}()
	err := safetykernel.RunWithEntitlements(&runtimeconfig.Config{
		NatsURL: "nats://127.0.0.1:1", RedisURL: redisURL,
		SafetyKernelAddr: os.Getenv(gateDenyAddrEnv), SafetyPolicyPath: os.Getenv(gateDenyPolicyEnv),
	}, nil)
	_, _ = fmt.Fprintln(os.Stderr, "safety kernel exited:", err)
	os.Exit(1)
	return ""
}

// gateDenyStartKernel runs the safety kernel on policy in its own process,
// against a fresh Redis, and returns the kernel's address and the Redis URL.
func gateDenyStartKernel(t *testing.T, policy string) (addr, redisURL string) {
	t.Helper()
	for _, key := range []string{"CORDUM_ENV", "CORDUM_PRODUCTION", "CORDUM_POLICY_STRICT",
		"SAFETY_POLICY_SIGNATURE_REQUIRED", "SAFETY_KERNEL_TLS_CERT", "SAFETY_KERNEL_TLS_KEY",
		"SAFETY_KERNEL_TLS_CA", "SAFETY_KERNEL_TLS_REQUIRED", "SAFETY_DECISION_CACHE_TTL"} {
		t.Setenv(key, "")
	}
	t.Setenv("SAFETY_KERNEL_ADMIN_ADDR", "127.0.0.1:0")
	mr := miniredis.RunT(t)
	policyPath := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
		t.Fatalf("policy: %v", err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve addr: %v", err)
	}
	addr = lis.Addr().String()
	_ = lis.Close()
	redisURL = "redis://" + mr.Addr()

	var out bytes.Buffer
	cmd := exec.Command(gateDenyKernelBinary)
	cmd.Env = append(os.Environ(), gateDenyRedisEnv+"="+redisURL,
		gateDenyAddrEnv+"="+addr, gateDenyPolicyEnv+"="+policyPath)
	cmd.Stdout, cmd.Stderr = &out, &out
	// The kernel exits when this pipe closes, so it never outlives the test binary.
	if _, err := cmd.StdinPipe(); err != nil {
		t.Fatalf("stdin: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start kernel: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("kernel output:\n%s", out.Bytes()[max(0, out.Len()-4096):])
		}
	})
	return addr, redisURL
}

// newGateDenyHarness is a scheduler whose safety client is the real kernel,
// running policy on the scheduler's Redis, with a worker for each topic.
func newGateDenyHarness(t *testing.T, policy string, topics ...string) *approvalHarness {
	t.Helper()
	addr, redisURL := gateDenyStartKernel(t, policy)
	client, err := NewSafetyClient(addr)
	if err != nil {
		t.Fatalf("safety client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	for deadline := time.Now().Add(15 * time.Second); client.CurrentPolicySnapshot(context.Background()) == ""; {
		if time.Now().After(deadline) {
			t.Fatalf("safety kernel did not load a policy within 15s")
		}
		time.Sleep(100 * time.Millisecond)
	}

	js, err := infraStore.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	t.Cleanup(func() { _ = js.Close() })
	routing := PoolRouting{Topics: map[string][]string{}, Pools: map[string]PoolProfile{"default": {}}}
	for _, topic := range topics {
		routing.Topics[topic] = []string{"default"}
	}
	registry := newTestRegistry(t)
	registry.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Type: "cpu", Pool: "default"})
	bus := &fakeBus{}
	engine := NewEngine(bus, client, registry, NewLeastLoadedStrategy(routing), js, nil)
	return &approvalHarness{t: t, store: js, bus: bus, registry: registry, engine: engine}
}

// gateDenySubmit submits req as the gateway stores it, then, when the
// scheduler parked it for approval, records a human approval and republishes
// the approved request as the approve handler does. It returns the decision
// the scheduler stored at submit.
func gateDenySubmit(h *approvalHarness, req *pb.JobRequest) SafetyDecisionRecord {
	h.t.Helper()
	ctx := testCtx(h.t)
	h.must("request", h.store.SetJobRequest(ctx, req))
	h.must("meta", h.store.SetJobMeta(ctx, req))
	h.must("state", h.store.SetState(ctx, req.GetJobId(), model.JobStatePending))
	h.must("submit", h.engine.handleJobRequest(proto.Clone(req).(*pb.JobRequest), "trace"))
	record, err := h.store.GetSafetyDecision(ctx, req.GetJobId())
	h.must("decision", err)
	resolved, err := h.store.ResolveApproval(ctx, infraStore.ApprovalResolutionParams{
		JobID: req.GetJobId(), Decision: model.ApprovalDecisionApprove, ResultState: model.JobStatePending,
		ApprovedBy: "alice", PolicySnapshot: record.PolicySnapshot,
		LabelUpdates: map[string]string{"approval_granted": "true", "approval_snapshot": record.PolicySnapshot},
	})
	if err == nil {
		h.must("republish", h.engine.handleJobRequest(resolved.Request, "trace"))
	}
	return record
}

func gateDenyJob(id, topic string) *pb.JobRequest {
	return &pb.JobRequest{JobId: id, Topic: topic, TenantId: "default", Labels: map[string]string{
		safetykernel.LabelActionDescriptorJSON: gateDenyAction,
	}}
}

func TestDenyRuleOutranksActionGateApproval(t *testing.T) {
	h := newGateDenyHarness(t, gateDenyPolicy, gateDenyTopic, gateDenyReviewTopic)

	// The gate alone parks the job, and an approval dispatches it once.
	record := gateDenySubmit(h, gateDenyJob("gate-review", gateDenyReviewTopic))
	if record.Decision != SafetyRequireApproval {
		t.Fatalf("gate-only decision = %s, want %s", record.Decision, SafetyRequireApproval)
	}
	if n := h.dispatches("gate-review"); n != 1 {
		t.Fatalf("approved gate-only job dispatched %d times, want 1", n)
	}

	// With a deny rule on the topic as well, nothing an approver does sends it.
	record = gateDenySubmit(h, gateDenyJob("gate-export", gateDenyTopic))
	if record.Decision != SafetyDeny {
		t.Errorf("decision = %s (rule %q), want %s", record.Decision, record.RuleID, SafetyDeny)
	}
	if n := h.dispatches("gate-export"); n != 0 {
		t.Errorf("denied job dispatched %d times after an approval attempt, want 0", n)
	}
	if state := h.meta("gate-export")["state"]; state != string(model.JobStateDenied) {
		t.Errorf("state = %s, want %s", state, model.JobStateDenied)
	}
}
