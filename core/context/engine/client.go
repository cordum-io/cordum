package engine

import (
	"context"
	"fmt"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
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
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("dial context engine: %w", err)
	}
	if err := waitForReady(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("dial context engine: %w", err)
	}
	return pb.NewContextEngineClient(conn), func() { _ = conn.Close() }, nil
}

func waitForReady(ctx context.Context, conn *grpc.ClientConn) error {
	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if state == connectivity.Shutdown {
			return fmt.Errorf("connection shutdown")
		}
		if !conn.WaitForStateChange(ctx, state) {
			if err := ctx.Err(); err != nil {
				return err
			}
			return fmt.Errorf("connection timeout")
		}
	}
}
