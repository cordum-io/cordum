package engine

import (
	"context"
	"fmt"

	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// NewClient dials the context engine and returns a client plus a closer.
func NewClient(ctx context.Context, addr string) (pb.ContextEngineClient, func(), error) {
	if addr == "" {
		addr = ":50070"
	}
	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return nil, nil, fmt.Errorf("dial context engine: %w", err)
	}
	return pb.NewContextEngineClient(conn), func() { conn.Close() }, nil
}
