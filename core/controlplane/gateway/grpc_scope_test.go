package gateway

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// These tests pin the scopes managed API keys need on the gateway's gRPC
// server. Calls go through the production interceptor chain over bufconn, and
// a refusal is proved by a job that was not stored or a status not returned.

// grpcMethodStream carries only a method name, which is all grpc.Method reads.
type grpcMethodStream struct {
	grpc.ServerTransportStream
	method string
}

func (s grpcMethodStream) Method() string { return s.method }

// withGRPCMethod returns a context whose grpc.Method is method, as a server
// interceptor sees it.
func withGRPCMethod(method string) context.Context {
	return grpc.NewContextWithServerTransportStream(context.Background(), grpcMethodStream{method: method})
}

// newGRPCScopeFixture is the edge-scope fixture with the RBAC entitlement off
// and a gRPC client served through the gateway's interceptor chain.
func newGRPCScopeFixture(t *testing.T) (edgeScopeFixture, pb.CordumApiClient) {
	t.Helper()
	f := newEdgeScopeFixture(t)
	setTestEntitlements(t, f.s, licensing.PlanEnterprise, func(e *licensing.Entitlements) {
		e.RBAC = false
	})
	listener := bufconn.Listen(1024 * 1024)
	rpcServer := grpc.NewServer(grpc.ChainUnaryInterceptor(
		apiKeyUnaryInterceptor(f.s.auth),
		rateLimitUnaryInterceptor(f.s.auth, f.s.apiRL, f.s.publicRL),
	))
	pb.RegisterCordumApiServer(rpcServer, f.s)
	served := make(chan error, 1)
	go func() { served <- rpcServer.Serve(listener) }()
	t.Cleanup(func() {
		rpcServer.Stop()
		if err := <-served; err != nil {
			t.Errorf("gRPC Serve: %v", err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///grpc-scope",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
	)
	if err != nil {
		t.Fatalf("gRPC client: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("gRPC client close: %v", err)
		}
	})
	return f, pb.NewCordumApiClient(conn)
}

func grpcKeyContext(t *testing.T, apiKey string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("x-api-key", apiKey))
}

func grpcSubmit(t *testing.T, client pb.CordumApiClient, apiKey string) (*pb.SubmitJobResponse, error) {
	t.Helper()
	return client.SubmitJob(grpcKeyContext(t, apiKey), &pb.SubmitJobRequest{
		Prompt: "scope test", Topic: "job.test", OrgId: "default",
	})
}

// TestGRPCManagedKeyScopeCoverage fails when a method is added to the
// gateway's gRPC server without a scope: every CordumApi method is mapped, and
// every other registered service is refused to scoped keys.
func TestGRPCManagedKeyScopeCoverage(t *testing.T) {
	rpcServer := grpc.NewServer()
	pb.RegisterCordumApiServer(rpcServer, pb.UnimplementedCordumApiServer{})
	reflection.Register(rpcServer)
	services := rpcServer.GetServiceInfo()
	if _, ok := services[pb.CordumApi_ServiceDesc.ServiceName]; !ok {
		t.Fatalf("services %v lack %s", services, pb.CordumApi_ServiceDesc.ServiceName)
	}
	for service, info := range services {
		wantMapped := service == pb.CordumApi_ServiceDesc.ServiceName
		if !wantMapped && !strings.HasPrefix(service, "grpc.reflection.") {
			t.Errorf("service %s is not named here as mapped or refused", service)
		}
		for _, method := range info.Methods {
			fullMethod := "/" + service + "/" + method.Name
			scope, mapped := auth.GRPCMethodToScope(fullMethod)
			if mapped != wantMapped || mapped == (scope == "") {
				t.Errorf("GRPCMethodToScope(%s) = (%q, %v), want mapped=%v", fullMethod, scope, mapped, wantMapped)
			}
		}
	}
}

func TestGRPCManagedKeyScopeSubmitJob(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []string
		static bool
		want   codes.Code // codes.OK: the job is stored
	}{
		{name: "policy_write_refused", scopes: []string{"policy:write"}, want: codes.Unauthenticated},
		{name: "jobs_read_refused", scopes: []string{"jobs:read"}, want: codes.Unauthenticated},
		{name: "jobs_write_stores", scopes: []string{"jobs:write"}, want: codes.OK},
		{name: "unscoped_keeps_role_gate", want: codes.PermissionDenied},
		{name: "static_key_stores", static: true, want: codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, client := newGRPCScopeFixture(t)
			apiKey := edgeScopeStaticKey
			if !tc.static {
				_, apiKey = f.mint(t, tc.scopes...)
			}
			response, err := grpcSubmit(t, client, apiKey)
			if got := status.Code(err); got != tc.want {
				t.Fatalf("SubmitJob code = %v (%v), want %v", got, err, tc.want)
			}
			jobs, err := f.s.jobStore.ListRecentJobs(context.Background(), 10)
			if err != nil {
				t.Fatalf("ListRecentJobs: %v", err)
			}
			want := 0
			if tc.want == codes.OK {
				want = 1
				if len(jobs) != 1 || jobs[0].ID != response.GetJobId() {
					t.Fatalf("stored jobs=%v, response job=%q; want one matching job", jobs, response.GetJobId())
				}
			} else if len(jobs) != 0 {
				t.Fatalf("stored jobs=%v, want none", jobs)
			}
			for _, pattern := range []string{"job:state:*", "job:req:*", "ctx:*"} {
				if count := f.records(t, pattern); count != want {
					t.Errorf("stored %s records=%d, want %d", pattern, count, want)
				}
			}
		})
	}
}

func TestGRPCManagedKeyScopeGetJobStatus(t *testing.T) {
	f, client := newGRPCScopeFixture(t)
	submitted, err := grpcSubmit(t, client, edgeScopeStaticKey)
	if err != nil {
		t.Fatalf("SubmitJob with the static key: %v", err)
	}
	for _, tc := range []struct {
		name   string
		scopes []string
		want   codes.Code // codes.OK: the job's status is returned
	}{
		{name: "jobs_read_returns_status", scopes: []string{"jobs:read"}, want: codes.OK},
		{name: "policy_write_refused", scopes: []string{"policy:write"}, want: codes.Unauthenticated},
		{name: "unscoped_returns_status", want: codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, apiKey := f.mint(t, tc.scopes...)
			response, err := client.GetJobStatus(grpcKeyContext(t, apiKey), &pb.GetJobStatusRequest{JobId: submitted.GetJobId()})
			if got := status.Code(err); got != tc.want {
				t.Fatalf("GetJobStatus code = %v (%v), want %v", got, err, tc.want)
			}
			returned := response.GetJobId() == submitted.GetJobId() && response.GetStatus() != "" && response.GetStatus() != "UNKNOWN"
			if returned != (tc.want == codes.OK) {
				t.Fatalf("GetJobStatus response = %v, want status returned=%v", response, tc.want == codes.OK)
			}
		})
	}
}

// TestGRPCManagedKeyScopeUnmappedMethod covers methods the scope map does not
// know, and a context with no method name: scoped keys are refused, while
// unscoped managed keys and static keys authenticate as before.
func TestGRPCManagedKeyScopeUnmappedMethod(t *testing.T) {
	f := newEdgeScopeFixture(t)
	_, scoped := f.mint(t, "jobs:write")
	_, unscoped := f.mint(t)
	for _, tc := range []struct {
		name string
		ctx  context.Context
	}{
		{name: "unknown_method", ctx: withGRPCMethod("/cordum.v1.CordumApi/DeleteJob")},
		{name: "reflection", ctx: withGRPCMethod("/grpc.reflection.v1.ServerReflection/ServerReflectionInfo")},
		{name: "no_method_name", ctx: context.Background()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authenticate := func(apiKey string) (*auth.AuthContext, error) {
				return f.provider.AuthenticateGRPC(metadata.NewIncomingContext(tc.ctx, metadata.Pairs("x-api-key", apiKey)))
			}
			var scopeErr *auth.ScopeError
			if caller, err := authenticate(scoped); caller != nil || !errors.As(err, &scopeErr) {
				t.Fatalf("scoped key = (%+v, %v), want a scope refusal", caller, err)
			}
			for _, apiKey := range []string{unscoped, edgeScopeStaticKey} {
				if caller, err := authenticate(apiKey); err != nil || caller == nil {
					t.Errorf("unscoped or static key = (%+v, %v), want it authenticated", caller, err)
				}
			}
		})
	}
}
