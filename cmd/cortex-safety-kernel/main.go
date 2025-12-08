package main

import (
	"context"
	"log"
	"net"
	"time"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

type server struct {
	pb.UnimplementedSafetyKernelServer
}

func main() {
	cfg := config.Load()
	addr := cfg.SafetyKernelAddr

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("safety-kernel: failed to listen on %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	pb.RegisterSafetyKernelServer(grpcServer, &server{})
	reflection.Register(grpcServer)

	log.Printf("safety-kernel: listening on %s", addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("safety-kernel: server error: %v", err)
	}
}

func (s *server) Check(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	decision := pb.DecisionType_DECISION_TYPE_ALLOW
	reason := ""

	// Minimal policy: block dangerous topic.
	if req.GetTopic() == "sys.destroy" {
		decision = pb.DecisionType_DECISION_TYPE_DENY
		reason = "forbidden topic"
	}
	_ = req.GetTenant() // placeholder: future per-tenant policy

	// Include trivial latency to simulate real checks.
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) < 0 {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = "deadline exceeded"
		}
	}

	return &pb.PolicyCheckResponse{
		Decision: decision,
		Reason:   reason,
	}, nil
}
