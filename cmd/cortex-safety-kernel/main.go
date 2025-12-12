package main

import (
	"context"
	"log"
	"net"
	"strings"
	"time"

	"github.com/yaront1111/coretex-os/core/infra/config"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

type server struct {
	pb.UnimplementedSafetyKernelServer
	policy *config.SafetyPolicy
}

func main() {
	cfg := config.Load()
	addr := cfg.SafetyKernelAddr

	policy, err := config.LoadSafetyPolicy(cfg.SafetyPolicyPath)
	if err != nil {
		log.Fatalf("safety-kernel: failed to load policy: %v", err)
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("safety-kernel: failed to listen on %s: %v", addr, err)
	}

	grpcServer := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	pb.RegisterSafetyKernelServer(grpcServer, &server{policy: policy})
	reflection.Register(grpcServer)

	log.Printf("safety-kernel: listening on %s", addr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("safety-kernel: server error: %v", err)
	}
}

func (s *server) Check(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	decision := pb.DecisionType_DECISION_TYPE_ALLOW
	reason := ""

	tenant := strings.TrimSpace(req.GetTenant())
	topic := strings.TrimSpace(req.GetTopic())

	// Policy evaluation
	if s.policy != nil {
		if tenant == "" {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = "missing tenant"
		} else {
			allowed, why := s.policy.Evaluate(tenant, topic)
			if !allowed {
				decision = pb.DecisionType_DECISION_TYPE_DENY
				reason = why
			}
		}
	}

	// Baseline protections: block sys.* and missing tenant.
	if decision == pb.DecisionType_DECISION_TYPE_ALLOW {
		if tenant == "" {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = "missing tenant"
		} else if !strings.HasPrefix(topic, "job.") {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = "unsupported topic"
		} else if topic == "sys.destroy" {
			decision = pb.DecisionType_DECISION_TYPE_DENY
			reason = "forbidden topic"
		}
	}

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
