package identity

import (
	"errors"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrNilProductionJobPacket reports a missing transport envelope.
	ErrNilProductionJobPacket = errors.New("identity: production job packet is nil")
	// ErrMissingProductionJobRequest reports a job envelope without its payload.
	ErrMissingProductionJobRequest = errors.New("identity: production job packet has no job request")
	// ErrNilProductionPolicyCheck reports a missing safety request.
	ErrNilProductionPolicyCheck = errors.New("identity: production policy check is nil")
)

// NormalizeProductionJobPacket returns a deep-cloned packet whose envelope
// and JobRequest mirrors equal authenticated authority.
func NormalizeProductionJobPacket(
	packet *pb.BusPacket,
	authoritative *pb.IdentityBinding,
) (*pb.BusPacket, error) {
	if packet == nil {
		return nil, ErrNilProductionJobPacket
	}
	auth, err := newAuthority(authoritative)
	if err != nil {
		return nil, err
	}
	if packet.GetJobRequest() == nil {
		return nil, ErrMissingProductionJobRequest
	}
	if err := rejectMismatches(bindingMirrors("packet.identity", packet.GetIdentity(), auth)); err != nil {
		return nil, err
	}
	request, err := NormalizeProductionJobRequest(packet.GetJobRequest(), authoritative)
	if err != nil {
		return nil, err
	}
	cloned, ok := proto.Clone(packet).(*pb.BusPacket)
	if !ok || cloned == nil {
		return nil, errors.New("identity: clone production job packet")
	}
	cloned.Identity = fillBinding(cloned.GetIdentity(), auth)
	cloned.Payload = &pb.BusPacket_JobRequest{JobRequest: request}
	return cloned, nil
}

// NormalizeProductionPolicyCheckRequest returns a deep-cloned safety request
// whose identity-bearing fields equal authenticated authority.
func NormalizeProductionPolicyCheckRequest(
	req *pb.PolicyCheckRequest,
	authoritative *pb.IdentityBinding,
) (*pb.PolicyCheckRequest, error) {
	if req == nil {
		return nil, ErrNilProductionPolicyCheck
	}
	auth, err := newAuthority(authoritative)
	if err != nil {
		return nil, err
	}
	checks := []mirror{
		{"policy.tenant", req.GetTenant(), auth.tenant},
		{"policy.principal_id", req.GetPrincipalId(), auth.principal},
	}
	checks = append(checks, metadataMirrors("policy.meta", req.GetMeta(), auth)...)
	checks = append(checks, bindingMirrors("policy.identity", req.GetIdentity(), auth)...)
	if err := rejectMismatches(checks); err != nil {
		return nil, err
	}
	cloned, ok := proto.Clone(req).(*pb.PolicyCheckRequest)
	if !ok || cloned == nil {
		return nil, errors.New("identity: clone production policy check")
	}
	cloned.Tenant, cloned.PrincipalId = auth.tenant, auth.principal
	cloned.Meta = fillMetadata(cloned.GetMeta(), auth)
	cloned.Identity = fillBinding(cloned.GetIdentity(), auth)
	return cloned, nil
}
