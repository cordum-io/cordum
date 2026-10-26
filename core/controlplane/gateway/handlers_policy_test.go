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
	"github.com/cordum/cordum/core/licensing"
	runtimeconfig "github.com/cordum/cordum/core/infra/config"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

// TestPolicyCheckAccess pins who may call the policy check routes.
// /api/v1/policy/evaluate is a read-only "would this be allowed" call: a
// caller who can submit jobs (jobs.write with the admin or user role, as
// POST /api/v1/jobs requires) learns the same decision by submitting, so
// evaluate is open to job submitters as well as policy readers and writers.
// Every call is still audited for enumeration detection. simulate and explain
// stay policy.write + admin; red-team #19 (a user-role caller probing them)
// stays closed there.
func TestPolicyCheckAccess(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rbac       bool
		rolePerms  []string // RBAC role definition for role when rbac is set
		role       string
		mode       string
		wantForbid bool
	}{
		{name: "user evaluates", role: "user", mode: "evaluate"},
		{name: "admin evaluates", role: "admin", mode: "evaluate"},
		{name: "viewer cannot evaluate", role: "viewer", mode: "evaluate", wantForbid: true},
		{name: "user cannot simulate", role: "user", mode: "simulate", wantForbid: true},
		{name: "user cannot explain", role: "user", mode: "explain", wantForbid: true},
		{name: "rbac user with jobs.write evaluates", rbac: true, rolePerms: []string{auth.PermJobsWrite}, role: "user", mode: "evaluate"},
		{name: "rbac user without jobs.write cannot evaluate", rbac: true, rolePerms: []string{auth.PermJobsRead}, role: "user", mode: "evaluate", wantForbid: true},
		{name: "rbac user with jobs.write cannot simulate", rbac: true, rolePerms: []string{auth.PermJobsWrite}, role: "user", mode: "simulate", wantForbid: true},
		// secops passes the legacy admin role check; holding only policy.read
		// isolates the policy-reader grant.
		{name: "rbac policy reader evaluates", rbac: true, rolePerms: []string{auth.PermPolicyRead}, role: "secops", mode: "evaluate"},
		{name: "rbac policy reader cannot simulate", rbac: true, rolePerms: []string{auth.PermPolicyRead}, role: "secops", mode: "simulate", wantForbid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			s.auth = newBasicAuthForTest(t, nil)
			if tc.rbac {
				setTestEntitlements(t, s, licensing.PlanEnterprise, func(e *licensing.Entitlements) { e.RBAC = true })
				if err := s.rbacStore.PutRole(context.Background(), &auth.RoleDefinition{Name: tc.role, Permissions: tc.rolePerms}); err != nil {
					t.Fatalf("PutRole: %v", err)
				}
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/"+tc.mode, strings.NewReader(`{"topic": "job.default"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Tenant-ID", "default")
			req = withAuth(req, &auth.AuthContext{Tenant: "default", Role: tc.role, PrincipalID: "caller"})
			rec := httptest.NewRecorder()
			s.handlePolicyCheck(rec, req, tc.mode)
			if forbidden := rec.Code == http.StatusForbidden; forbidden != tc.wantForbid {
				t.Fatalf("status %d (forbidden=%v, want %v): %s", rec.Code, forbidden, tc.wantForbid, rec.Body.String())
			}
		})
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
	policyPath := filepath.Join(t.TempDir(), "safety.yaml")
	if err := os.WriteFile(policyPath, []byte("default_decision: allow\n"), 0o600); err != nil {
		t.Fatalf("write safety policy: %v", err)
	}
	addr := reserveLocalAddr(t)
	t.Setenv("SAFETY_KERNEL_ADMIN_ADDR", reserveLocalAddr(t))
	errCh := make(chan error, 1)
	go func() {
		errCh <- safetykernel.RunWithEntitlements(&runtimeconfig.Config{
			NatsURL:          "nats://127.0.0.1:1",
			RedisURL:         "redis://" + mr.Addr(),
			SafetyKernelAddr: addr,
			SafetyPolicyPath: policyPath,
		}, nil)
	}()
	return dialTestSafetyKernel(t, addr, errCh)
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
