package gateway

import (
	"context"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	capsdk "github.com/cordum/cordum/core/protocol/capsdk"
	jobidentity "github.com/cordum/cordum/core/protocol/identity"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *server) normalizeHTTPJobRequest(
	r *http.Request,
	req *pb.JobRequest,
) (*pb.JobRequest, error) {
	if s == nil || !s.capProfile.IsProduction() {
		return req, nil
	}
	var authCtx *auth.AuthContext
	if r != nil {
		authCtx = auth.FromRequest(r)
	}
	return normalizeAuthenticatedJobRequest(req, authCtx)
}

func (s *server) normalizeGRPCJobRequest(
	ctx context.Context,
	req *pb.JobRequest,
) (*pb.JobRequest, error) {
	if s == nil || !s.capProfile.IsProduction() {
		return req, nil
	}
	return normalizeAuthenticatedJobRequest(req, auth.FromContext(ctx))
}

func normalizeAuthenticatedJobRequest(
	req *pb.JobRequest,
	authCtx *auth.AuthContext,
) (*pb.JobRequest, error) {
	authority := &pb.IdentityBinding{}
	if req != nil {
		authority.TenantId = strings.TrimSpace(req.GetTenantId())
	}
	if authCtx != nil {
		if authority.TenantId == "" {
			authority.TenantId = strings.TrimSpace(authCtx.Tenant)
		}
		authority.PrincipalId = strings.TrimSpace(authCtx.PrincipalID)
		authority.ActorId = authority.PrincipalId
	}
	return jobidentity.NormalizeProductionJobRequest(req, authority)
}

func (s *server) normalizeStoredJobRequest(req *pb.JobRequest) (*pb.JobRequest, error) {
	if s == nil || !s.capProfile.IsProduction() {
		return req, nil
	}
	if req == nil {
		return jobidentity.NormalizeProductionJobRequest(nil, nil)
	}
	return jobidentity.NormalizeProductionJobRequest(req, req.GetIdentity())
}

func (s *server) productionHTTPIdentity(
	r *http.Request,
	tenantID string,
) (*pb.IdentityBinding, error) {
	if s == nil || !s.capProfile.IsProduction() {
		return nil, nil
	}
	probe := &pb.JobRequest{TenantId: strings.TrimSpace(tenantID)}
	normalized, err := s.normalizeHTTPJobRequest(r, probe)
	if err != nil {
		return nil, err
	}
	return normalized.GetIdentity(), nil
}

func jobRequestPacket(traceID, senderID string, req *pb.JobRequest) *pb.BusPacket {
	packet := &pb.BusPacket{
		TraceId: traceID, SenderId: senderID, CreatedAt: timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
	}
	packet.Payload = &pb.BusPacket_JobRequest{JobRequest: req}
	if req != nil && req.GetIdentity() != nil {
		packet.Identity = req.GetIdentity()
	}
	return packet
}
