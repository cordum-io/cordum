package engine

import (
	"context"
	"fmt"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const defaultDialTimeout = 5 * time.Second

// NewClient dials the context engine and returns a client plus a closer.
func NewClient(ctx context.Context, addr string) (pb.ContextEngineClient, func(), error) {
	if addr == "" {
		addr = ":50070"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultDialTimeout)
		defer cancel()
	}
	conn, err := grpc.DialContext(ctx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return nil, nil, fmt.Errorf("dial context engine: %w", err)
	}
	return pb.NewContextEngineClient(conn), func() { conn.Close() }, nil
}
