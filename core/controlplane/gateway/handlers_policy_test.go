package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/safetykernel"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/capprofile"
	runtimeconfig "github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

// TestPolicyEvaluate_UserRoleRejected proves red-team #19 is closed:
// a non-admin (user role) caller cannot access policy/evaluate.
func TestPolicyEvaluate_UserRoleRejected(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)

	body := `{"topic": "job.default", "risk_tags": ["low"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "test-api-key")
	req = withAuth(req, &auth.AuthContext{Tenant: "default", Role: "user", PrincipalID: "attacker"})
	rec := httptest.NewRecorder()

	s.handlePolicyEvaluate(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("RED-TEAM #19 BYPASS: user-role caller got %d (want 403): %s", rec.Code, rec.Body.String())
	}
}

// TestPolicyEvaluate_AdminAllowed_ReturnsResponse proves admin callers
// can still use the endpoint.
func TestPolicyEvaluate_AdminAllowed_ReturnsResponse(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)

	body := `{"topic": "job.default"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-API-Key", "test-api-key")
	req = withAuth(req, &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-user"})
	rec := httptest.NewRecorder()

	s.handlePolicyEvaluate(rec, req)

	if rec.Code == http.StatusForbidden {
		t.Fatalf("admin should be allowed, got 403: %s", rec.Body.String())
	}
}

func TestPolicyEvaluateProductionRejectsActorSpoofBeforeSafetyRPC(t *testing.T) {
	s, _, safetyClient := newTestGateway(t)
	s.capProfile = capprofile.Production
	body, err := json.Marshal(policyCheckRequest{
		Topic: "job.default", PrincipalId: "principal-a",
		Meta: &policyMetaRequest{TenantId: "tenant-a", ActorId: "actor-attacker"},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAuth(req, &auth.AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "principal-a"})
	rec := httptest.NewRecorder()

	s.handlePolicyEvaluate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	safetyClient.mu.Lock()
	defer safetyClient.mu.Unlock()
	if safetyClient.lastReq != nil {
		t.Fatalf("spoofed policy identity reached Safety Kernel: %#v", safetyClient.lastReq)
	}
}

func TestPolicyEvaluate_GatewayAllowedActionDoesNotForwardDescriptor(t *testing.T) {
	ctx := context.Background()
	s, _, safetyClient := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	s.edgeStore = edgecore.NewRedisStoreFromClient(s.redisClient())
	action := approvedPolicyEvaluateAction(t, ctx, s, "tenant-gateway-strip", "strip")
	s.wireActionGatePipeline()

	rec := httptest.NewRecorder()
	s.handlePolicyEvaluate(rec, policyEvaluateRequest(t, "tenant-gateway-strip", action))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	safetyClient.mu.Lock()
	lastReq := safetyClient.lastReq
	safetyClient.mu.Unlock()
	if lastReq == nil {
		t.Fatal("safety kernel was not called")
	}
	if _, ok := lastReq.GetLabels()[labelActionDescriptorJSON]; ok {
		t.Fatalf("gateway forwarded reserved action descriptor after local allow: labels=%v", lastReq.GetLabels())
	}
}

func TestPolicyEvaluate_RealSafetyKernelDoesNotRegateGatewayApprovedAction(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	s.edgeStore = edgecore.NewRedisStoreFromClient(s.redisClient())
	action := approvedPolicyEvaluateAction(t, ctx, s, "tenant-real-safety", "real-safety")
	s.wireActionGatePipeline()
	conn := startRealSafetyKernel(t)
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close safety kernel client: %v", err)
		}
	})
	s.safetyClient = pb.NewSafetyKernelClient(conn)

	rec := httptest.NewRecorder()
	s.handlePolicyEvaluate(rec, policyEvaluateRequest(t, "tenant-real-safety", action))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var got pb.PolicyCheckResponse
	if err := protojson.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode policy response: %v body=%s", err, rec.Body.String())
	}
	if got.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("decision = %v rule=%q reason=%q; want gateway-approved action to avoid kernel missing_auth re-gate",
			got.GetDecision(), got.GetRuleId(), got.GetReason())
	}
}

func approvedPolicyEvaluateAction(t *testing.T, ctx context.Context, s *server, tenant, suffix string) *runtimeconfig.ActionDescriptor {
	t.Helper()
	action := wirePipelineDestructiveAction("")
	approval := seedApprovedWirePipelineApproval(t, ctx, s.edgeStore.(*edgecore.RedisStore), tenant, suffix, action)
	action.ApprovalClaim.ApprovalRef = approval.ApprovalRef
	appendVerifierTestEvents(t, s.auditChainer, tenant, 2)
	appendApprovalEvidenceEvent(t, s.auditChainer, approval, nil)
	return action
}

func policyEvaluateRequest(t *testing.T, tenant string, action *runtimeconfig.ActionDescriptor) *http.Request {
	t.Helper()
	body, err := json.Marshal(policyCheckRequest{Topic: "job.default", PrincipalId: "agent-requester", Action: action})
	if err != nil {
		t.Fatalf("marshal policy request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", tenant)
	req.Header.Set("X-API-Key", "test-api-key")
	return withAuth(req, &auth.AuthContext{Tenant: tenant, Role: "admin", PrincipalID: "agent-requester"})
}

func startRealSafetyKernel(t *testing.T) *grpc.ClientConn {
	t.Helper()
	mr := miniredis.RunT(t)
	addr := reserveLocalAddr(t)
	t.Setenv("SAFETY_KERNEL_ADMIN_ADDR", reserveLocalAddr(t))
	// A day-long poll keeps the kernel from reloading or publishing while its
	// test runs; runRealSafetyKernel stops it when the test ends.
	t.Setenv("SAFETY_POLICY_RELOAD_INTERVAL", "24h")
	return dialTestSafetyKernel(t, addr, runRealSafetyKernel(t, allowAllKernelConfig(t, mr, addr)))
}

// allowAllKernelConfig points a kernel at mr, addr and an allow-all policy file.
func allowAllKernelConfig(t *testing.T, mr *miniredis.Miniredis, addr string) *runtimeconfig.Config {
	t.Helper()
	policyPath := filepath.Join(t.TempDir(), "safety.yaml")
	if err := os.WriteFile(policyPath, []byte("default_decision: allow\n"), 0o600); err != nil {
		t.Fatalf("write safety policy: %v", err)
	}
	return &runtimeconfig.Config{
		NatsURL:          "nats://127.0.0.1:1",
		RedisURL:         "redis://" + mr.Addr(),
		SafetyKernelAddr: addr,
		SafetyPolicyPath: policyPath,
	}
}

// runRealSafetyKernel runs the real safety kernel in this process until the
// test ends: its cleanup cancels the kernel and waits for it to return. The
// returned channel receives the kernel's return value; the cleanup leaves it
// for the caller.
func runRealSafetyKernel(t *testing.T, cfg *runtimeconfig.Config) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		errCh <- safetykernel.RunWithEntitlementsContext(ctx, cfg, nil)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-stopped:
		case <-time.After(20 * time.Second):
			t.Errorf("safety kernel on %s did not stop within 20s", cfg.SafetyKernelAddr)
		}
	})
	return errCh
}

// TestRealSafetyKernelStopsAtTestCleanup proves a kernel started through
// runRealSafetyKernel stops when its test ends: it returns, both listeners
// close, and it stops using its Redis.
func TestRealSafetyKernelStopsAtTestCleanup(t *testing.T) {
	mr := miniredis.RunT(t)
	addr, adminAddr := reserveLocalAddr(t), reserveLocalAddr(t)
	var errCh <-chan error
	t.Run("kernel", func(t *testing.T) {
		t.Setenv("SAFETY_KERNEL_ADMIN_ADDR", adminAddr)
		t.Setenv("SAFETY_POLICY_RELOAD_INTERVAL", "50ms")
		errCh = runRealSafetyKernel(t, allowAllKernelConfig(t, mr, addr))
		if err := dialTestSafetyKernel(t, addr, errCh).Close(); err != nil {
			t.Fatalf("close safety kernel client: %v", err)
		}
		// Prove both effects the checks below rule out were happening.
		require.Eventually(t, func() bool { return tcpAccepts(adminAddr) }, 5*time.Second, 20*time.Millisecond, "admin server never listened")
		before := mr.CommandCount()
		require.Eventually(t, func() bool { return mr.CommandCount() >= before+3 }, 5*time.Second, 20*time.Millisecond, "running kernel did not use Redis")
	})
	select {
	case err := <-errCh:
		require.NoError(t, err, "kernel returned an error when its test ended")
	default:
		t.Fatal("kernel still running after its test ended")
	}
	for _, a := range []string{addr, adminAddr} {
		require.False(t, tcpAccepts(a), "%s still accepts connections after the kernel's test ended", a)
	}
	stopped := mr.CommandCount()
	// CommandCount only grows, so one later read sees any use in between. Never would
	// leave a check running after the test returns, against a closed miniredis.
	time.Sleep(500 * time.Millisecond)
	require.Equal(t, stopped, mr.CommandCount(), "kernel used Redis after its test ended")
}

func tcpAccepts(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func dialTestSafetyKernel(t *testing.T, addr string, errCh <-chan error) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("create safety kernel client: %v", err)
	}
	conn.Connect()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state := conn.GetState()
		if state == connectivity.Ready {
			return conn
		}
		if state == connectivity.Shutdown {
			t.Fatalf("safety kernel client shut down before connecting to %s", addr)
		}
		select {
		case runErr := <-errCh:
			if closeErr := conn.Close(); closeErr != nil {
				t.Logf("close unready safety kernel client: %v", closeErr)
			}
			t.Fatalf("safety kernel exited before accepting connections: %v", runErr)
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		conn.WaitForStateChange(ctx, state)
		cancel()
		conn.Connect()
	}
	if err := conn.Close(); err != nil {
		t.Logf("close unready safety kernel client: %v", err)
	}
	t.Fatalf("safety kernel did not listen on %s before deadline", addr)
	return nil
}

func reserveLocalAddr(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve local addr: %v", err)
	}
	addr := lis.Addr().String()
	if err := lis.Close(); err != nil {
		t.Fatalf("close reserved local addr: %v", err)
	}
	return addr
}
