package gateway

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestGRPCRateLimitUsesTenantKey(t *testing.T) {
	apiLimiterMu.Lock()
	origAPI := apiLimiter
	origPublic := publicLimiter
	defer func() {
		apiLimiter = origAPI
		publicLimiter = origPublic
		apiLimiterMu.Unlock()
	}()

	apiLimiter = newKeyedRateLimiter(1, 1)
	publicLimiter = newKeyedRateLimiter(1, 1)
	interceptor := rateLimitUnaryInterceptor(nil)
	info := &grpc.UnaryServerInfo{FullMethod: "/cordum.api.v1.CordumApi/SubmitJob"}
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }

	ctx1 := grpcContextWithPeer(context.WithValue(context.Background(), authContextKey{}, &AuthContext{Tenant: "tenant-a"}), "10.0.0.1")
	if _, err := interceptor(ctx1, nil, info, handler); err != nil {
		t.Fatalf("expected first request to pass: %v", err)
	}

	ctx2 := grpcContextWithPeer(context.WithValue(context.Background(), authContextKey{}, &AuthContext{Tenant: "tenant-a"}), "10.0.0.2")
	if _, err := interceptor(ctx2, nil, info, handler); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected rate limit by tenant, got %v", err)
	}
}

func TestGRPCRateLimitFallsBackToIP(t *testing.T) {
	apiLimiterMu.Lock()
	origAPI := apiLimiter
	origPublic := publicLimiter
	defer func() {
		apiLimiter = origAPI
		publicLimiter = origPublic
		apiLimiterMu.Unlock()
	}()

	apiLimiter = newKeyedRateLimiter(1, 1)
	publicLimiter = newKeyedRateLimiter(1, 1)
	interceptor := rateLimitUnaryInterceptor(nil)
	info := &grpc.UnaryServerInfo{FullMethod: "/cordum.api.v1.CordumApi/SubmitJob"}
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }

	ctx1 := grpcContextWithPeer(context.Background(), "10.0.0.10")
	if _, err := interceptor(ctx1, nil, info, handler); err != nil {
		t.Fatalf("expected first request to pass: %v", err)
	}

	ctx2 := grpcContextWithPeer(context.Background(), "10.0.0.11")
	if _, err := interceptor(ctx2, nil, info, handler); err != nil {
		t.Fatalf("expected different IP to pass: %v", err)
	}

	if _, err := interceptor(ctx2, nil, info, handler); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("expected rate limit on repeat IP, got %v", err)
	}
}

func grpcContextWithPeer(ctx context.Context, ip string) context.Context {
	addr := &net.TCPAddr{IP: net.ParseIP(ip), Port: 12345}
	return peer.NewContext(ctx, &peer.Peer{Addr: addr})
}
