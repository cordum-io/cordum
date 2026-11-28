package safetykernel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/tests/identity"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestAudienceMatrixGRPCSafetyKernel(t *testing.T) {
	t.Setenv(env.EnvMode, "production")
	f := newServerTLSFixture(t)
	t.Setenv("SAFETY_KERNEL_TLS_CERT", f.cert)
	t.Setenv("SAFETY_KERNEL_TLS_KEY", f.key)
	t.Setenv("SAFETY_KERNEL_TLS_CLIENT_CA", f.ca)
	present := func(t *testing.T, c identity.Credential) identity.Result {
		return audienceSafetyPresent(t, c, audienceCredential(t, c), f)
	}
	probe := func(t *testing.T) identity.Result {
		result := audienceSafetyCall(t, nil, true, f)
		require.True(t, result.Accepted)
		require.True(t, result.Effect, "baseline must write a velocity member")
		return result
	}
	identity.Run(t, identity.GRPCSafetyKernel, present, identity.Baseline(probe))
}

func audienceSafetyPresent(t *testing.T, c identity.Credential, token string, f serverTLSFixture) identity.Result {
	t.Helper()
	result := identity.Result{}
	carriers := []string{"x-api-key", "api-key", "authorization", "none"}
	if c == identity.AgentIDHeader {
		carriers = append(carriers, "x-agent-id")
	}
	for _, carrier := range carriers {
		var md metadata.MD
		if token != "" && carrier != "none" {
			value := token
			if carrier == "authorization" {
				value = "Bearer " + token
			}
			md = metadata.Pairs(carrier, value)
		}
		observed := audienceSafetyCall(t, md, false, f)
		result.Accepted = result.Accepted || observed.Accepted
		result.Effect = result.Effect || observed.Effect
		result.Evidence += carrier + ": " + observed.Evidence + "; "
	}
	return result
}

func audienceSafetyCall(t *testing.T, md metadata.MD, baseline bool, f serverTLSFixture) identity.Result {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	svc := &server{resultClient: rdb, velocityChecker: newVelocityChecker(rdb)}
	policy := &config.SafetyPolicy{DefaultDecision: "allow", DefaultTenant: "default",
		Rules: []config.PolicyRule{{ID: "matrix-velocity", Decision: "deny",
			Velocity: &config.VelocityConfig{MaxRequests: 10, WindowSeconds: 60, Key: "labels.session_id"}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, svc.setPolicy(ctx, policy, "matrix-snapshot"))
	conn, lis, tlsCfg := audienceSafetyConn(t, svc, baseline, f)
	ctx = metadata.NewOutgoingContext(ctx, md)
	before := mr.Dump()
	decision, checkErr := pb.NewSafetyKernelClient(conn).Check(ctx, &pb.PolicyCheckRequest{
		JobId: "matrix-job", Topic: "job.matrix", Tenant: "default",
		Labels: map[string]string{"session_id": "matrix-session"},
	})
	afterCheck := mr.Dump()
	output, outputErr := pb.NewOutputPolicyServiceClient(conn).CheckOutput(ctx, &pb.OutputCheckRequest{
		JobId: "matrix-job", Topic: "job.matrix", Tenant: "default",
	})
	afterOutput := mr.Dump()
	audienceSafetyRPCError(t, checkErr)
	audienceSafetyRPCError(t, outputErr)
	if baseline {
		require.NoError(t, checkErr)
		require.NoError(t, outputErr)
		require.NotNil(t, decision)
		require.NotNil(t, output)
	}
	probe := "not run (baseline)"
	if !baseline {
		probe = audienceSafetyTLSRefusal(t, lis, tlsCfg)
	}
	// CheckOutput computes a decision without a Redis write. Do not fabricate
	// an effect from that response: acceptance is independently aggregated.
	return identity.Result{
		Accepted: (checkErr == nil && decision != nil) || (outputErr == nil && output != nil),
		Effect:   before != afterCheck || afterCheck != afterOutput,
		Evidence: fmt.Sprintf("miniredis keys=%s; Check snapshot changed=%t; CheckOutput snapshot changed=%t; Check error=%v; CheckOutput error=%v; TLS probe=%s",
			strings.Join(mr.Keys(), ","), before != afterCheck, afterCheck != afterOutput, checkErr, outputErr, probe),
	}
}

func audienceSafetyRPCError(t *testing.T, err error) {
	t.Helper()
	if status.Code(err) == codes.Unavailable && (strings.Contains(err.Error(), "certificate required") || strings.Contains(err.Error(), "read/write on closed pipe")) {
		return
	}
	if err != nil && status.Code(err) != codes.Unauthenticated && status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unexpected RPC failure (not an authentication refusal): %v", err)
	}
}

func audienceSafetyConn(t *testing.T, svc *server, baseline bool, f serverTLSFixture) (*grpc.ClientConn, *bufconn.Listener, *tls.Config) {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	creds, err := serverTransportCredentials(t.Context())
	require.NoError(t, err)
	srv := newGRPCServer(svc, creds)
	done := make(chan error, 1)
	go func() { done <- srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		if err := lis.Close(); err != nil {
			t.Errorf("close listener: %v", err)
		}
		if err := <-done; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("serve: %v", err)
		}
	})
	tlsCfg := &tls.Config{RootCAs: f.roots, ServerName: "localhost", MinVersion: tls.VersionTLS12}
	if baseline {
		tlsCfg.Certificates = []tls.Certificate{f.client}
	}
	conn, err := grpc.NewClient("passthrough:///matrix", grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return conn, lis, tlsCfg
}

func audienceSafetyTLSRefusal(t *testing.T, lis *bufconn.Listener, tlsCfg *tls.Config) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	raw, err := lis.DialContext(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, raw.Close()) })
	cfg := tlsCfg.Clone()
	cfg.NextProtos = []string{"h2"}
	conn := tls.Client(raw, cfg)
	require.NoError(t, conn.HandshakeContext(ctx))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	// Do not write a preface: it can race the server's TLS 1.3 refusal alert.
	n, err := conn.Read(make([]byte, 1))
	require.Zero(t, n, "unauthenticated TLS probe received data")
	require.ErrorContains(t, err, "certificate required")
	return err.Error()
}
