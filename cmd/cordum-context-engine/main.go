package main

import (
	"crypto/tls"
	"log"
	"net"
	"os"
	"strings"

	"github.com/cordum/cordum/core/context/engine"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/env"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg := config.Load()
	buildinfo.Log("cordum-context-engine")

	svc, err := engine.NewService(cfg.RedisURL)
	if err != nil {
		log.Fatalf("context engine init failed: %v", err)
	}

	addr := cfg.ContextEngineAddr
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	serverCreds := grpc.Creds(insecure.NewCredentials())
	certFile := strings.TrimSpace(os.Getenv("CONTEXT_ENGINE_TLS_CERT"))
	keyFile := strings.TrimSpace(os.Getenv("CONTEXT_ENGINE_TLS_KEY"))
	if certFile != "" || keyFile != "" {
		if certFile == "" || keyFile == "" {
			log.Fatalf("context engine tls requires both CONTEXT_ENGINE_TLS_CERT and CONTEXT_ENGINE_TLS_KEY")
		}
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			log.Fatalf("context engine tls keypair: %v", err)
		}
		cfg := &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
		if env.TLSMinVersion() == tls.VersionTLS13 {
			cfg.MinVersion = tls.VersionTLS13
		}
		serverCreds = grpc.Creds(credentials.NewTLS(cfg))
	}
	if env.IsProduction() && certFile == "" {
		log.Fatalf("context engine tls required in production")
	}

	server := grpc.NewServer(serverCreds)
	pb.RegisterContextEngineServer(server, svc)
	healthSrv := health.NewServer()
	healthpb.RegisterHealthServer(server, healthSrv)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	if env.Bool(env.EnvGRPCReflection) {
		reflection.Register(server)
	}

	log.Printf("context engine listening on %s (redis=%s)", addr, cfg.RedisURL)
	if err := server.Serve(lis); err != nil {
		log.Fatalf("context engine server error: %v", err)
	}
}
