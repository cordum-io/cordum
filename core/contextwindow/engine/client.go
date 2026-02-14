package engine

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordum/cordum/core/infra/env"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
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
	creds, err := contextEngineTransportCredentials()
	if err != nil {
		return nil, nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
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

func contextEngineTransportCredentials() (credentials.TransportCredentials, error) {
	caPath := strings.TrimSpace(os.Getenv("CONTEXT_ENGINE_TLS_CA"))
	requireTLS := env.IsProduction() || env.Bool("CONTEXT_ENGINE_TLS_REQUIRED")
	insecureAllowed := env.Bool("CONTEXT_ENGINE_INSECURE")

	if caPath == "" {
		if requireTLS {
			return nil, fmt.Errorf("CONTEXT_ENGINE_TLS_CA required")
		}
		if insecureAllowed || !env.IsProduction() {
			return insecure.NewCredentials(), nil
		}
		return nil, fmt.Errorf("context engine tls required")
	}

	// #nosec G304,G703 -- CA path is configured by the operator (TLS cert path from env config).
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("context engine tls ca read: %w", err)
	}
	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(pem); !ok {
		return nil, fmt.Errorf("context engine tls ca parse: %s", caPath)
	}
	cfg := &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	}
	if env.TLSMinVersion() == tls.VersionTLS13 {
		cfg.MinVersion = tls.VersionTLS13
	}
	return credentials.NewTLS(cfg), nil
}
