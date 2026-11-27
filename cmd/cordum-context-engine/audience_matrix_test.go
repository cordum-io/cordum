package main

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
	"github.com/cordum/cordum/core/contextwindow/engine"
	"github.com/cordum/cordum/core/infra/env"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/cordum/cordum/tests/identity"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestAudienceMatrixGRPCContextEngine(t *testing.T) {
	t.Setenv(env.EnvMode, "production")
	f := newServerTLSFixture(t)
	t.Setenv("CONTEXT_ENGINE_TLS_CERT", f.cert)
	t.Setenv("CONTEXT_ENGINE_TLS_KEY", f.key)
	t.Setenv("CONTEXT_ENGINE_TLS_CLIENT_CA", f.ca)
	present := func(t *testing.T, c identity.Credential) identity.Result {
		return audienceContextPresent(t, c, audienceCredential(t, c), f)
	}
	probe := func(t *testing.T) identity.Result {
		result := audienceContextCall(t, nil, true, f)
		require.True(t, result.Accepted)
		require.True(t, result.Effect, "baseline must append memory history")
		return result
	}
	identity.Run(t, identity.GRPCContextEngine, present, identity.Baseline(probe))
}

func audienceContextPresent(t *testing.T, c identity.Credential, token string, f serverTLSFixture) identity.Result {
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
		observed := audienceContextCall(t, md, false, f)
		result.Accepted = result.Accepted || observed.Accepted
		result.Effect = result.Effect || observed.Effect
		result.Evidence += carrier + ": " + observed.Evidence + "; "
	}
	return result
}

func audienceContextCall(t *testing.T, md metadata.MD, baseline bool, f serverTLSFixture) identity.Result {
	t.Helper()
	mr := miniredis.RunT(t)
	svc, err := engine.NewService("redis://" + mr.Addr())
	require.NoError(t, err)
	conn, lis, tlsCfg := audienceContextConn(t, svc, baseline, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, md)
	before := mr.Dump()
	response, err := pb.NewContextEngineClient(conn).UpdateMemory(ctx, &pb.UpdateMemoryRequest{
		MemoryId: "matrix-memory", TenantId: "default", Mode: pb.ContextMode_CONTEXT_MODE_CHAT,
		ModelResponse: []byte("matrix-memory-marker"),
	})
	after := mr.Dump()
	refusedTLS := status.Code(err) == codes.Unavailable && (strings.Contains(err.Error(), "certificate required") || strings.Contains(err.Error(), "read/write on closed pipe"))
	if err != nil && !refusedTLS && status.Code(err) != codes.Unauthenticated && status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unexpected RPC failure (not an authentication refusal): %v", err)
	}
	probe := "not run (baseline)"
	if !baseline {
		probe = audienceContextTLSRefusal(t, lis, tlsCfg)
	}
	return identity.Result{Accepted: err == nil && response != nil, Effect: before != after,
		Evidence: fmt.Sprintf("miniredis mem:default/matrix-memory:events before/after snapshot changed=%t; key exists=%t; RPC error=%v; TLS probe=%s",
			before != after, mr.Exists("mem:default/matrix-memory:events"), err, probe)}
}

func audienceContextConn(t *testing.T, svc *engine.Service, baseline bool, f serverTLSFixture) (*grpc.ClientConn, *bufconn.Listener, *tls.Config) {
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

func audienceContextTLSRefusal(t *testing.T, lis *bufconn.Listener, tlsCfg *tls.Config) string {
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
