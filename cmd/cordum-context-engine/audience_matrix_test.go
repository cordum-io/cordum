package main

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestAudienceMatrixGRPCContextEngine(t *testing.T) {
	t.Setenv(env.EnvMode, "production")
	present := func(t *testing.T, c identity.Credential) identity.Result {
		return audienceContextPresent(t, c, audienceCredential(t, c))
	}
	probe := func(t *testing.T) identity.Result {
		result := audienceContextCall(t, nil)
		require.True(t, result.Accepted)
		require.True(t, result.Effect, "baseline must append memory history")
		return result
	}
	identity.Run(t, identity.GRPCContextEngine, present, identity.Baseline(probe),
		identity.Skip(identity.Anonymous, "PR1-18"),
		identity.Skip(identity.UserSession, "PR1-18"),
		identity.Skip(identity.APIKeyStatic, "PR1-18"),
		identity.Skip(identity.APIKeyManaged, "PR1-18"),
		identity.Skip(identity.GatewayJWT, "PR1-18"),
		identity.Skip(identity.GatewayJWTWrongAudience, "PR1-18"),
		identity.Skip(identity.OIDCBearer, "PR1-18"),
		identity.Skip(identity.OIDCBearerWrongAudience, "PR1-18"),
		identity.Skip(identity.DelegationToken, "PR1-18"),
		identity.Skip(identity.ServiceToken, "PR1-18"),
		identity.Skip(identity.WorkerCredential, "PR1-18"),
		identity.Skip(identity.WorkerSession, "PR1-18"),
		identity.Skip(identity.WorkerSessionWrongAudience, "PR1-18"),
		identity.Skip(identity.SCIMBearer, "PR1-18"),
		identity.Skip(identity.AgentdNonce, "PR1-18"),
		identity.Skip(identity.AgentIDHeader, "PR1-18"))
}

func audienceContextPresent(t *testing.T, c identity.Credential, token string) identity.Result {
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
		observed := audienceContextCall(t, md)
		result.Accepted = result.Accepted || observed.Accepted
		result.Effect = result.Effect || observed.Effect
		result.Evidence += carrier + ": " + observed.Evidence + "; "
	}
	return result
}

func audienceContextCall(t *testing.T, md metadata.MD) identity.Result {
	t.Helper()
	mr := miniredis.RunT(t)
	svc, err := engine.NewService("redis://" + mr.Addr())
	require.NoError(t, err)
	conn := audienceContextConn(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, md)
	before := mr.Dump()
	response, err := pb.NewContextEngineClient(conn).UpdateMemory(ctx, &pb.UpdateMemoryRequest{
		MemoryId: "matrix-memory", Mode: pb.ContextMode_CONTEXT_MODE_CHAT,
		ModelResponse: []byte("matrix-memory-marker"),
	})
	after := mr.Dump()
	if err != nil && status.Code(err) != codes.Unauthenticated && status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unexpected RPC failure (not an authentication refusal): %v", err)
	}
	return identity.Result{Accepted: err == nil && response != nil, Effect: before != after,
		Evidence: fmt.Sprintf("miniredis mem:matrix-memory:events before/after snapshot changed=%t; key exists=%t",
			before != after, mr.Exists("mem:matrix-memory:events"))}
}

func audienceContextConn(t *testing.T, svc *engine.Service) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := newGRPCServer(svc, grpc.Creds(insecure.NewCredentials()))
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
	conn, err := grpc.NewClient("passthrough:///matrix", grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return conn
}
