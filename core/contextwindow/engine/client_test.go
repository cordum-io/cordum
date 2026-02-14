package engine

import (
	"context"
	"net"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
)

type testContextEngineServer struct {
	pb.UnimplementedContextEngineServer
}

func TestNewClient(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterContextEngineServer(grpcServer, &testContextEngineServer{})
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = lis.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, closeFn, err := NewClient(ctx, lis.Addr().String())
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if client == nil || closeFn == nil {
		t.Fatalf("expected client and close function")
	}
	closeFn()
}
