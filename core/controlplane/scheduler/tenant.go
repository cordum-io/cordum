package scheduler

import (
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const DefaultTenant = model.DefaultTenant

// ExtractTenant returns tenant ID with fallbacks to env.
func ExtractTenant(req *pb.JobRequest) string {
	return model.ExtractTenant(req)
}

// ExtractPrincipal extracts principal ID if present.
func ExtractPrincipal(req *pb.JobRequest) string {
	return model.ExtractPrincipal(req)
}
