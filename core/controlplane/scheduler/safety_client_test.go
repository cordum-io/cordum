package scheduler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/store"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/tools/certgen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type safetyTestServer struct {
	pb.UnimplementedSafetyKernelServer
	decision pb.DecisionType
	reason   string
	lastReq  *pb.PolicyCheckRequest
}

func (s *safetyTestServer) Check(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	s.lastReq = req
	return &pb.PolicyCheckResponse{
		Decision: s.decision,
		Reason:   s.reason,
	}, nil
}

func startTestSafetyServer(t *testing.T, decision pb.DecisionType, reason string) (*grpc.ClientConn, func()) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	pb.RegisterSafetyKernelServer(srv, &safetyTestServer{decision: decision, reason: reason})

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(lis)
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial safety kernel: %v", err)
	}
	cleanup := func() {
		srv.Stop()
		_ = lis.Close()
		_ = conn.Close()
		if err := <-errCh; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Fatalf("safety kernel serve error: %v", err)
		}
	}
	return conn, cleanup
}

func startInstrumentedTestSafetyServer(t *testing.T, decision pb.DecisionType, reason string) (*grpc.ClientConn, *safetyTestServer, func()) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	handler := &safetyTestServer{decision: decision, reason: reason}
	pb.RegisterSafetyKernelServer(srv, handler)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(lis)
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}
	conn, err := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial safety kernel: %v", err)
	}
	cleanup := func() {
		srv.Stop()
		_ = lis.Close()
		_ = conn.Close()
		if err := <-errCh; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Fatalf("safety kernel serve error: %v", err)
		}
	}
	return conn, handler, cleanup
}

func newTestCB() *RedisCircuitBreaker {
	return NewRedisCircuitBreaker(nil, "cordum:cb:safety:test", CircuitBreakerOpts{
		FailThreshold: safetyCircuitFailBudget,
		OpenDuration:  safetyCircuitOpenFor,
		HalfOpenMax:   safetyCircuitHalfOpenMax,
		CloseAfter:    safetyCircuitCloseAfter,
	})
}

func TestSafetyClientAllow(t *testing.T) {
	conn, cleanup := startTestSafetyServer(t, pb.DecisionType_DECISION_TYPE_ALLOW, "")
	defer cleanup()

	client := &SafetyClient{client: pb.NewSafetyKernelClient(conn), conn: conn, cb: newTestCB()}
	record, err := client.Check(context.Background(), &pb.JobRequest{JobId: "1", Topic: "job.default"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.Decision != SafetyAllow || record.Reason != "" {
		t.Fatalf("expected allow, got %v reason=%s", record.Decision, record.Reason)
	}
}

func TestSafetyClientDeny(t *testing.T) {
	conn, cleanup := startTestSafetyServer(t, pb.DecisionType_DECISION_TYPE_DENY, "blocked")
	defer cleanup()

	client := &SafetyClient{client: pb.NewSafetyKernelClient(conn), conn: conn, cb: newTestCB()}
	record, err := client.Check(context.Background(), &pb.JobRequest{JobId: "1", Topic: "sys.destroy"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.Decision != SafetyDeny || record.Reason != "blocked" {
		t.Fatalf("expected deny, got %v reason=%s", record.Decision, record.Reason)
	}
}

func TestSafetyClientDefaultProfileAllowsCompatIdentityWithoutActor(t *testing.T) {
	conn, handler, cleanup := startInstrumentedTestSafetyServer(t, pb.DecisionType_DECISION_TYPE_ALLOW, "")
	defer cleanup()
	client := &SafetyClient{client: pb.NewSafetyKernelClient(conn), conn: conn, cb: newTestCB()}
	req := &pb.JobRequest{
		JobId: "job-compat", Topic: "job.test", TenantId: "tenant-a", PrincipalId: "principal-a",
		Identity: &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a"},
	}

	record, err := client.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("Check() error = %v, want compatibility request admitted", err)
	}
	if record.Decision != SafetyAllow || handler.lastReq == nil {
		t.Fatalf("Check() = (%v, %#v), want allow delivered to Safety Kernel", record.Decision, handler.lastReq)
	}
}

func TestSafetyClientProductionRejectsIncompleteIdentityBeforeRPC(t *testing.T) {
	conn, handler, cleanup := startInstrumentedTestSafetyServer(t, pb.DecisionType_DECISION_TYPE_ALLOW, "")
	defer cleanup()
	client := (&SafetyClient{client: pb.NewSafetyKernelClient(conn), conn: conn, cb: newTestCB()}).
		WithProductionIdentityEnforcement(true)
	req := &pb.JobRequest{
		JobId: "job-production", Topic: "job.test", TenantId: "tenant-a", PrincipalId: "principal-a",
		Identity: &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a"},
	}

	_, err := client.Check(context.Background(), req)
	if !errors.Is(err, jobidentity.ErrIncompleteProductionIdentity) {
		t.Fatalf("Check() error = %v, want incomplete production identity", err)
	}
	if handler.lastReq != nil {
		t.Fatalf("incomplete production identity reached Safety Kernel: %#v", handler.lastReq)
	}
}

func TestSafetyClientRejectsIdentityMismatchBeforeRPC(t *testing.T) {
	conn, handler, cleanup := startInstrumentedTestSafetyServer(t, pb.DecisionType_DECISION_TYPE_ALLOW, "")
	defer cleanup()
	client := (&SafetyClient{client: pb.NewSafetyKernelClient(conn), conn: conn, cb: newTestCB()}).
		WithProductionIdentityEnforcement(true)
	req := &pb.JobRequest{
		JobId: "job-1", Topic: "job.test", TenantId: "tenant-attacker",
		Identity: &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "principal-a"},
	}

	_, err := client.Check(context.Background(), req)
	if !errors.Is(err, jobidentity.ErrProductionIdentityMismatch) {
		t.Fatalf("Check() error = %v, want identity mismatch", err)
	}
	if handler.lastReq != nil {
		t.Fatalf("identity mismatch reached Safety Kernel: %#v", handler.lastReq)
	}
}

func TestSafetyClientCarriesCanonicalIdentityToRPC(t *testing.T) {
	conn, handler, cleanup := startInstrumentedTestSafetyServer(t, pb.DecisionType_DECISION_TYPE_ALLOW, "")
	defer cleanup()
	client := (&SafetyClient{client: pb.NewSafetyKernelClient(conn), conn: conn, cb: newTestCB()}).
		WithProductionIdentityEnforcement(true)
	auth := &pb.IdentityBinding{TenantId: "tenant-a", PrincipalId: "principal-a", ActorId: "actor-a"}
	req, err := jobidentity.NormalizeProductionJobRequest(
		&pb.JobRequest{JobId: "job-1", Topic: "job.test"}, auth,
	)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}

	if _, err := client.Check(context.Background(), req); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if handler.lastReq == nil || handler.lastReq.GetIdentity().GetActorId() != auth.GetActorId() {
		t.Fatalf("Safety Kernel identity = %#v, want %#v", handler.lastReq.GetIdentity(), auth)
	}
}

type failingSafetyKernelClient struct{}

func (f failingSafetyKernelClient) Check(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

func (f failingSafetyKernelClient) Evaluate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

func (f failingSafetyKernelClient) Explain(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

func (f failingSafetyKernelClient) Simulate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

func (f failingSafetyKernelClient) ListSnapshots(context.Context, *pb.ListSnapshotsRequest, ...grpc.CallOption) (*pb.ListSnapshotsResponse, error) {
	return nil, fmt.Errorf("forced failure")
}

type allowSafetyKernelClient struct{}

func (a allowSafetyKernelClient) Check(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}

func (a allowSafetyKernelClient) Evaluate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}

func (a allowSafetyKernelClient) Explain(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}

func (a allowSafetyKernelClient) Simulate(context.Context, *pb.PolicyCheckRequest, ...grpc.CallOption) (*pb.PolicyCheckResponse, error) {
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}

func (a allowSafetyKernelClient) ListSnapshots(context.Context, *pb.ListSnapshotsRequest, ...grpc.CallOption) (*pb.ListSnapshotsResponse, error) {
	return &pb.ListSnapshotsResponse{}, nil
}

func TestSafetyClientCircuitOpens(t *testing.T) {
	client := &SafetyClient{client: failingSafetyKernelClient{}, cb: newTestCB()}
	req := &pb.JobRequest{JobId: "1", Topic: "job.default"}

	for i := 0; i < safetyCircuitFailBudget; i++ {
		record, err := client.Check(context.Background(), req)
		if err != nil {
			t.Fatalf("check failed: %v", err)
		}
		if record.Decision != SafetyUnavailable {
			t.Fatalf("expected unavailable on failure %d, got %v", i, record.Decision)
		}
	}

	record, err := client.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if record.Decision != SafetyUnavailable || record.Reason != "safety kernel circuit open" {
		t.Fatalf("expected circuit open unavailable, got %v reason=%s", record.Decision, record.Reason)
	}
}

func TestSafetyClientHalfOpenClosesAfterSuccesses(t *testing.T) {
	client := &SafetyClient{client: failingSafetyKernelClient{}, cb: newTestCB()}
	req := &pb.JobRequest{JobId: "1", Topic: "job.default"}

	// Trip the circuit open.
	for i := 0; i < safetyCircuitFailBudget; i++ {
		if _, err := client.Check(context.Background(), req); err != nil {
			t.Fatalf("check failed: %v", err)
		}
	}

	// Force transition into half-open state via the local breaker.
	client.cb.mu.Lock()
	client.cb.localOpenUntil = time.Now().Add(-time.Second)
	client.cb.localState = circuitOpen
	client.cb.mu.Unlock()

	// Swap client to a successful responder to allow closing.
	client.client = allowSafetyKernelClient{}

	record, err := client.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if record.Decision != SafetyAllow {
		t.Fatalf("expected allow during half-open probe, got %v", record.Decision)
	}
	// Second success should close the circuit.
	record, err = client.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if record.Decision != SafetyAllow {
		t.Fatalf("expected allow during half-open probe, got %v", record.Decision)
	}

	client.cb.mu.Lock()
	defer client.cb.mu.Unlock()
	if client.cb.localState != circuitClosed {
		t.Fatalf("expected circuit to close after two successes, state=%v", client.cb.localState)
	}
	if client.cb.localFailures != 0 || client.cb.localSuccesses != 0 {
		t.Fatalf("expected counters reset, failures=%d successes=%d", client.cb.localFailures, client.cb.localSuccesses)
	}
}

func TestDecisionFromProto(t *testing.T) {
	cases := map[pb.DecisionType]SafetyDecision{
		pb.DecisionType_DECISION_TYPE_ALLOW:                  SafetyAllow,
		pb.DecisionType_DECISION_TYPE_DENY:                   SafetyDeny,
		pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN:          SafetyRequireApproval,
		pb.DecisionType_DECISION_TYPE_THROTTLE:               SafetyThrottle,
		pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS: SafetyAllowWithConstraints,
		pb.DecisionType_DECISION_TYPE_UNSPECIFIED:            SafetyDeny,
	}
	for dec, expect := range cases {
		if got := decisionFromProto(dec); got != expect {
			t.Fatalf("decision %v expected %v got %v", dec, expect, got)
		}
	}
}

type safetyTLSClientTestServer struct {
	pb.UnimplementedSafetyKernelServer
	calls atomic.Int32
}

func (s *safetyTLSClientTestServer) Check(context.Context, *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	s.calls.Add(1)
	return &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}, nil
}

func safetyTLSClientFixture(t *testing.T) (string, *tls.Config) {
	t.Helper()
	dir := t.TempDir()
	caDir, serverDir := filepath.Join(dir, "ca"), filepath.Join(dir, "server")
	if err := certgen.GenerateCA(caDir, 1, false); err != nil {
		t.Fatal(err)
	}
	if err := certgen.GenerateServerCert(caDir, serverDir, 1, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := certgen.GenerateClientCert(caDir, filepath.Join(dir, "client"), "test-client", 1, false); err != nil {
		t.Fatal(err)
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(serverDir, "tls.crt"), filepath.Join(serverDir, "tls.key"))
	if err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(filepath.Join(caDir, "ca.crt"))
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("parse test CA")
	}
	return dir, &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}
}

func startSafetyTLSClientServer(t *testing.T, cfg *tls.Config) (string, *safetyTLSClientTestServer) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	handler := &safetyTLSClientTestServer{}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(cfg)))
	pb.RegisterSafetyKernelServer(server, handler)
	done := make(chan error, 1)
	go func() { done <- server.Serve(lis) }()
	t.Cleanup(func() {
		server.Stop()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				t.Errorf("serve TLS: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("TLS server did not stop")
		}
	})
	return lis.Addr().String(), handler
}

func TestSafetyTransportClientCertificate(t *testing.T) {
	dir, cfg := safetyTLSClientFixture(t)
	addr, handler := startSafetyTLSClientServer(t, cfg)
	t.Setenv("SAFETY_KERNEL_TLS_CA", filepath.Join(dir, "ca", "ca.crt"))
	for _, tc := range []struct {
		name      string
		withCert  bool
		wantCode  codes.Code
		wantCalls int32
	}{
		{"without_client_certificate", false, codes.Unavailable, 0},
		{"with_client_certificate", true, codes.OK, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SAFETY_KERNEL_TLS_CLIENT_CERT", "")
			t.Setenv("SAFETY_KERNEL_TLS_CLIENT_KEY", "")
			if tc.withCert {
				t.Setenv("SAFETY_KERNEL_TLS_CLIENT_CERT", filepath.Join(dir, "client", "tls.crt"))
				t.Setenv("SAFETY_KERNEL_TLS_CLIENT_KEY", filepath.Join(dir, "client", "tls.key"))
			}
			creds, err := safetyTransportCredentials()
			if err != nil {
				t.Fatal(err)
			}
			conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Errorf("close TLS client: %v", err)
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = pb.NewSafetyKernelClient(conn).Check(ctx, &pb.PolicyCheckRequest{})
			if code := status.Code(err); code != tc.wantCode {
				t.Errorf("Check code = %v, want %v: %v", code, tc.wantCode, err)
			}
			if calls := handler.calls.Load(); calls != tc.wantCalls {
				t.Errorf("server calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestSafetyTransportClientCertificateConfiguration(t *testing.T) {
	t.Setenv("CORDUM_ENV", "development")
	t.Setenv("CORDUM_PRODUCTION", "false")
	t.Setenv("SAFETY_KERNEL_TLS_REQUIRED", "false")
	t.Setenv("SAFETY_KERNEL_INSECURE", "true")
	for _, tc := range []struct {
		name, cert, key, ca, missing string
	}{
		{"certificate_only", "client.crt", "", "ca.crt", "SAFETY_KERNEL_TLS_CLIENT_KEY"},
		{"key_only", "", "client.key", "ca.crt", "SAFETY_KERNEL_TLS_CLIENT_CERT"},
		{"pair_without_ca", "client.crt", "client.key", "", "SAFETY_KERNEL_TLS_CA"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SAFETY_KERNEL_TLS_CLIENT_CERT", tc.cert)
			t.Setenv("SAFETY_KERNEL_TLS_CLIENT_KEY", tc.key)
			t.Setenv("SAFETY_KERNEL_TLS_CA", tc.ca)
			creds, err := safetyTransportCredentials()
			if err == nil || !strings.Contains(err.Error(), tc.missing) {
				t.Errorf("credentials error = %v, want missing %s", err, tc.missing)
			}
			if creds != nil {
				t.Error("invalid client configuration returned usable credentials")
			}
		})
	}
}

func TestSafetyTransportCredentialsInsecure(t *testing.T) {
	t.Setenv("SAFETY_KERNEL_TLS_CA", "")
	t.Setenv("SAFETY_KERNEL_INSECURE", "true")
	creds, err := safetyTransportCredentials()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if creds.Info().SecurityProtocol != "insecure" {
		t.Fatalf("expected insecure protocol, got %s", creds.Info().SecurityProtocol)
	}
}

func TestSafetyTransportCredentialsFallback(t *testing.T) {
	t.Setenv("SAFETY_KERNEL_TLS_CA", "/nonexistent")
	t.Setenv("SAFETY_KERNEL_INSECURE", "")
	if _, err := safetyTransportCredentials(); err == nil {
		t.Fatalf("expected error for invalid CA")
	}
}

func TestSafetyClientAttachesInputContentFromContextPtr(t *testing.T) {
	conn, handler, cleanup := startInstrumentedTestSafetyServer(t, pb.DecisionType_DECISION_TYPE_ALLOW, "")
	defer cleanup()

	_, rdb := newTestRedis(t)
	ctx := context.Background()
	payload := []byte(`{"instruction":"buy headphones","items":[{"name":"gift card","category":"gift_card"}]}`)
	if err := rdb.Set(ctx, store.MakeContextKey("job-ctx"), payload, 0).Err(); err != nil {
		t.Fatalf("seed redis context: %v", err)
	}

	client := (&SafetyClient{
		client:        pb.NewSafetyKernelClient(conn),
		conn:          conn,
		cb:            newTestCB(),
		contextClient: rdb,
	}).WithLegacyResourceCompatibility(nil)

	record, err := client.Check(ctx, &pb.JobRequest{
		JobId:      "job-ctx",
		TenantId:   "tenant-a",
		Topic:      "job.visa-governance.evaluate",
		ContextPtr: store.PointerForKey(store.MakeContextKey("job-ctx")),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if record.Decision != SafetyAllow {
		t.Fatalf("expected allow passthrough, got %v", record.Decision)
	}
	if handler.lastReq == nil {
		t.Fatal("expected safety kernel request to be captured")
	}
	if got := string(handler.lastReq.GetInputContent()); got != string(payload) {
		t.Fatalf("expected input content %q, got %q", string(payload), got)
	}
	if got := handler.lastReq.GetInputSizeBytes(); got != int64(len(payload)) {
		t.Fatalf("expected input size %d, got %d", len(payload), got)
	}
}
