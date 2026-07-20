// Package identity canonicalizes CAP identity mirrors against authenticated
// transport authority.
package identity

import (
	"errors"
	"fmt"
	"strings"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrNilProductionJobRequest reports a missing request.
	ErrNilProductionJobRequest = errors.New("identity: production job request is nil")
	// ErrIncompleteProductionIdentity reports missing authenticated authority.
	ErrIncompleteProductionIdentity = errors.New("identity: production authority is incomplete")
	// ErrProductionIdentityMismatch reports disagreement with authenticated authority.
	ErrProductionIdentityMismatch = errors.New("identity: production identity mirror mismatch")
)

type authority struct {
	tenant, principal, actor, delegation string
}

type mirror struct {
	path, got, want string
}

// NormalizeProductionJobRequest returns a deep clone whose identity mirrors
// equal authoritative. Every populated mirror is checked first; disagreement
// rejects the request without mutating it.
func NormalizeProductionJobRequest(
	req *pb.JobRequest,
	authoritative *pb.IdentityBinding,
) (*pb.JobRequest, error) {
	if req == nil {
		return nil, ErrNilProductionJobRequest
	}
	auth, err := newAuthority(authoritative)
	if err != nil {
		return nil, err
	}
	if err := validateRequestMirrors(req, auth); err != nil {
		return nil, err
	}
	cloned, ok := proto.Clone(req).(*pb.JobRequest)
	if !ok || cloned == nil {
		return nil, errors.New("identity: clone production job request")
	}
	fillRequestMirrors(cloned, auth)
	return cloned, nil
}

func newAuthority(binding *pb.IdentityBinding) (authority, error) {
	if binding == nil {
		return authority{}, ErrIncompleteProductionIdentity
	}
	auth := authority{
		tenant: binding.GetTenantId(), principal: binding.GetPrincipalId(),
		actor: binding.GetActorId(), delegation: binding.GetDelegationId(),
	}
	required := []mirror{
		{path: "tenant_id", got: auth.tenant},
		{path: "principal_id", got: auth.principal},
		{path: "actor_id", got: auth.actor},
	}
	for _, field := range required {
		if strings.TrimSpace(field.got) == "" {
			return authority{}, fmt.Errorf("%w: %s", ErrIncompleteProductionIdentity, field.path)
		}
	}
	return auth, nil
}

func validateRequestMirrors(req *pb.JobRequest, auth authority) error {
	checks := []mirror{
		{"request.tenant_id", req.GetTenantId(), auth.tenant},
		{"request.principal_id", req.GetPrincipalId(), auth.principal},
	}
	checks = append(checks, metadataMirrors("request.meta", req.GetMeta(), auth)...)
	checks = append(checks, environmentMirrors("request.env", req.GetEnv(), auth)...)
	checks = append(checks, bindingMirrors("request.identity", req.GetIdentity(), auth)...)
	if comp := req.GetCompensation(); comp != nil {
		checks = append(checks,
			mirror{"compensation.tenant_id", comp.GetTenantId(), auth.tenant},
			mirror{"compensation.principal_id", comp.GetPrincipalId(), auth.principal},
		)
		checks = append(checks, metadataMirrors("compensation.meta", comp.GetMeta(), auth)...)
		checks = append(checks, environmentMirrors("compensation.env", comp.GetEnv(), auth)...)
		checks = append(checks, bindingMirrors("compensation.identity", comp.GetIdentity(), auth)...)
	}
	return rejectMismatches(checks)
}

func metadataMirrors(prefix string, meta *pb.JobMetadata, auth authority) []mirror {
	if meta == nil {
		return nil
	}
	return []mirror{
		{prefix + ".tenant_id", meta.GetTenantId(), auth.tenant},
		{prefix + ".actor_id", meta.GetActorId(), auth.actor},
	}
}

func environmentMirrors(prefix string, env map[string]string, auth authority) []mirror {
	if env == nil {
		return nil
	}
	return []mirror{
		{prefix + ".tenant_id", env["tenant_id"], auth.tenant},
		{prefix + ".principal_id", env["principal_id"], auth.principal},
		{prefix + ".actor_id", env["actor_id"], auth.actor},
		{prefix + ".delegation_id", env["delegation_id"], auth.delegation},
	}
}

func bindingMirrors(prefix string, binding *pb.IdentityBinding, auth authority) []mirror {
	if binding == nil {
		return nil
	}
	return []mirror{
		{prefix + ".tenant_id", binding.GetTenantId(), auth.tenant},
		{prefix + ".principal_id", binding.GetPrincipalId(), auth.principal},
		{prefix + ".actor_id", binding.GetActorId(), auth.actor},
		{prefix + ".delegation_id", binding.GetDelegationId(), auth.delegation},
	}
}

func rejectMismatches(checks []mirror) error {
	for _, check := range checks {
		if check.got != "" && check.got != check.want {
			return fmt.Errorf("%w: %s", ErrProductionIdentityMismatch, check.path)
		}
	}
	return nil
}

func fillRequestMirrors(req *pb.JobRequest, auth authority) {
	req.TenantId, req.PrincipalId = auth.tenant, auth.principal
	req.Meta = fillMetadata(req.GetMeta(), auth)
	req.Env = fillEnvironment(req.GetEnv(), auth)
	req.Identity = fillBinding(req.GetIdentity(), auth)
	if comp := req.GetCompensation(); comp != nil {
		comp.TenantId, comp.PrincipalId = auth.tenant, auth.principal
		comp.Meta = fillMetadata(comp.GetMeta(), auth)
		comp.Env = fillEnvironment(comp.GetEnv(), auth)
		comp.Identity = fillBinding(comp.GetIdentity(), auth)
	}
}

func fillMetadata(meta *pb.JobMetadata, auth authority) *pb.JobMetadata {
	if meta == nil {
		meta = &pb.JobMetadata{}
	}
	meta.TenantId, meta.ActorId = auth.tenant, auth.actor
	return meta
}

func fillEnvironment(env map[string]string, auth authority) map[string]string {
	if env == nil {
		env = make(map[string]string, 4)
	}
	env["tenant_id"], env["principal_id"] = auth.tenant, auth.principal
	env["actor_id"] = auth.actor
	if auth.delegation != "" {
		env["delegation_id"] = auth.delegation
	}
	return env
}

func fillBinding(binding *pb.IdentityBinding, auth authority) *pb.IdentityBinding {
	if binding == nil {
		binding = &pb.IdentityBinding{}
	}
	binding.TenantId, binding.PrincipalId = auth.tenant, auth.principal
	binding.ActorId, binding.DelegationId = auth.actor, auth.delegation
	return binding
}
