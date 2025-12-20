package safetykernel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path"
	"strings"

	"github.com/yaront1111/coretex-os/core/infra/config"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
)

type server struct {
	pb.UnimplementedSafetyKernelServer
	policy *config.SafetyPolicy
}

// Run starts the Safety Kernel gRPC server and blocks until it exits.
func Run(cfg *config.Config) error {
	if cfg == nil {
		cfg = config.Load()
	}

	policy, err := config.LoadSafetyPolicy(cfg.SafetyPolicyPath)
	if err != nil {
		return fmt.Errorf("load safety policy: %w", err)
	}

	lis, err := net.Listen("tcp", cfg.SafetyKernelAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.SafetyKernelAddr, err)
	}

	serverCreds := grpc.Creds(insecure.NewCredentials())
	if cert := os.Getenv("SAFETY_KERNEL_TLS_CERT"); cert != "" {
		key := os.Getenv("SAFETY_KERNEL_TLS_KEY")
		if key == "" {
			log.Printf("safety-kernel: tls cert provided without key, continuing insecure")
		} else if creds, err := credentials.NewServerTLSFromFile(cert, key); err != nil {
			log.Printf("safety-kernel: failed to load tls credentials, continuing insecure: %v", err)
		} else {
			serverCreds = grpc.Creds(creds)
		}
	}

	grpcServer := grpc.NewServer(serverCreds)
	pb.RegisterSafetyKernelServer(grpcServer, &server{policy: policy})
	reflection.Register(grpcServer)

	log.Printf("safety-kernel: listening on %s", cfg.SafetyKernelAddr)
	if err := grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("grpc serve: %w", err)
	}
	return nil
}

func (s *server) Check(ctx context.Context, req *pb.PolicyCheckRequest) (*pb.PolicyCheckResponse, error) {
	decision := pb.DecisionType_DECISION_TYPE_ALLOW
	reason := ""

	topic := strings.TrimSpace(req.GetTopic())
	tenant := strings.TrimSpace(req.GetTenant())
	if tenant == "" && s.policy != nil {
		tenant = strings.TrimSpace(s.policy.DefaultTenant)
	}

	if tenant == "" {
		return &pb.PolicyCheckResponse{
			Decision: pb.DecisionType_DECISION_TYPE_DENY,
			Reason:   "missing tenant",
		}, nil
	}

	if topic == "" {
		return &pb.PolicyCheckResponse{
			Decision: pb.DecisionType_DECISION_TYPE_DENY,
			Reason:   "missing topic",
		}, nil
	}

	if !strings.HasPrefix(topic, "job.") {
		return &pb.PolicyCheckResponse{
			Decision: pb.DecisionType_DECISION_TYPE_DENY,
			Reason:   "unsupported topic",
		}, nil
	}

	if s.policy != nil {
		tp, ok := s.resolveTenantPolicy(tenant)
		if ok {
			if matchAny(tp.DenyTopics, topic) {
				return &pb.PolicyCheckResponse{
					Decision: pb.DecisionType_DECISION_TYPE_DENY,
					Reason:   fmt.Sprintf("topic '%s' denied by tenant policy", topic),
				}, nil
			}
			if len(tp.AllowTopics) > 0 && !matchAny(tp.AllowTopics, topic) {
				return &pb.PolicyCheckResponse{
					Decision: pb.DecisionType_DECISION_TYPE_DENY,
					Reason:   fmt.Sprintf("topic '%s' not allowed by tenant policy", topic),
				}, nil
			}
		}
	}

	if eff, ok := parseEffectiveSafety(req.GetEffectiveConfig()); ok {
		if matchAny(eff.DeniedTopics, topic) {
			return &pb.PolicyCheckResponse{
				Decision: pb.DecisionType_DECISION_TYPE_DENY,
				Reason:   fmt.Sprintf("topic '%s' denied by effective config", topic),
			}, nil
		}
		if len(eff.AllowedTopics) > 0 && !matchAny(eff.AllowedTopics, topic) {
			return &pb.PolicyCheckResponse{
				Decision: pb.DecisionType_DECISION_TYPE_DENY,
				Reason:   fmt.Sprintf("topic '%s' not allowed by effective config", topic),
			}, nil
		}
	}

	return &pb.PolicyCheckResponse{
		Decision: decision,
		Reason:   reason,
	}, nil
}

func (s *server) resolveTenantPolicy(tenant string) (config.TenantPolicy, bool) {
	if s == nil || s.policy == nil {
		return config.TenantPolicy{}, false
	}
	if tenant != "" {
		if tp, ok := s.policy.Tenants[tenant]; ok {
			return tp, true
		}
	}
	if def := strings.TrimSpace(s.policy.DefaultTenant); def != "" {
		if tp, ok := s.policy.Tenants[def]; ok {
			return tp, true
		}
	}
	if tp, ok := s.policy.Tenants["default"]; ok {
		return tp, true
	}
	return config.TenantPolicy{}, false
}

func parseEffectiveSafety(payload []byte) (config.SafetyConfig, bool) {
	if len(payload) == 0 {
		return config.SafetyConfig{}, false
	}
	var wrapper struct {
		Safety config.SafetyConfig `json:"safety"`
	}
	if err := json.Unmarshal(payload, &wrapper); err != nil {
		log.Printf("safety-kernel: failed to parse effective_config: %v", err)
		return config.SafetyConfig{}, false
	}
	return wrapper.Safety, true
}

func matchAny(patterns []string, topic string) bool {
	for _, pat := range patterns {
		pat = strings.TrimSpace(pat)
		if pat == "" {
			continue
		}
		ok, _ := path.Match(pat, topic)
		if ok {
			return true
		}
	}
	return false
}
