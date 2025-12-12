package main

import (
	"log"
	"net"

	"github.com/yaront1111/cortex-os/core/context/engine"
	"github.com/yaront1111/cortex-os/core/infra/config"
	pb "github.com/yaront1111/cortex-os/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg := config.Load()

	svc, err := engine.NewService(cfg.RedisURL)
	if err != nil {
		log.Fatalf("context engine init failed: %v", err)
	}

	addr := cfg.ContextEngineAddr
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", addr, err)
	}

	server := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	pb.RegisterContextEngineServer(server, svc)
	reflection.Register(server)

	log.Printf("context engine listening on %s (redis=%s)", addr, cfg.RedisURL)
	if err := server.Serve(lis); err != nil {
		log.Fatalf("context engine server error: %v", err)
	}
}
