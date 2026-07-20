package identity

import (
	"bytes"
	"errors"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func productionAuthority() *pb.IdentityBinding {
	return &pb.IdentityBinding{
		TenantId: "tenant-a", PrincipalId: "principal-a",
		ActorId: "actor-a", DelegationId: "delegation-a",
	}
}

func blankIdentityMirrors() *pb.JobRequest {
	return &pb.JobRequest{
		Meta:     &pb.JobMetadata{},
		Env:      map[string]string{},
		Identity: &pb.IdentityBinding{},
		Compensation: &pb.Compensation{
			Meta: &pb.JobMetadata{}, Env: map[string]string{}, Identity: &pb.IdentityBinding{},
		},
	}
}

func TestNormalizeProductionJobRequestRejectsIncompleteInputs(t *testing.T) {
	complete := productionAuthority()
	cases := []struct {
		name string
		req  *pb.JobRequest
		auth *pb.IdentityBinding
		want error
	}{
		{name: "nil request", auth: complete, want: ErrNilProductionJobRequest},
		{name: "nil authority", req: &pb.JobRequest{}, want: ErrIncompleteProductionIdentity},
		{name: "missing tenant", req: &pb.JobRequest{}, auth: &pb.IdentityBinding{PrincipalId: "p", ActorId: "a"}, want: ErrIncompleteProductionIdentity},
		{name: "missing principal", req: &pb.JobRequest{}, auth: &pb.IdentityBinding{TenantId: "t", ActorId: "a"}, want: ErrIncompleteProductionIdentity},
		{name: "missing actor", req: &pb.JobRequest{}, auth: &pb.IdentityBinding{TenantId: "t", PrincipalId: "p"}, want: ErrIncompleteProductionIdentity},
		{name: "whitespace tenant", req: &pb.JobRequest{}, auth: &pb.IdentityBinding{TenantId: " \t", PrincipalId: "p", ActorId: "a"}, want: ErrIncompleteProductionIdentity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeProductionJobRequest(tc.req, tc.auth)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizeProductionJobRequestFillsBlankMirrorsOnClone(t *testing.T) {
	auth := productionAuthority()
	input := &pb.JobRequest{
		JobId: "job-1", Topic: "job.demo", Labels: map[string]string{"keep": "yes"},
		Compensation: &pb.Compensation{Topic: "job.undo"},
	}

	got, err := NormalizeProductionJobRequest(input, auth)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	if got == input {
		t.Fatal("NormalizeProductionJobRequest() returned the input pointer")
	}
	assertCanonicalMirrors(t, got, auth)
	if input.GetIdentity() != nil || input.GetMeta() != nil || input.GetEnv() != nil {
		t.Fatal("NormalizeProductionJobRequest() mutated request mirrors")
	}
	if input.GetCompensation().GetIdentity() != nil || input.GetCompensation().GetMeta() != nil || input.GetCompensation().GetEnv() != nil {
		t.Fatal("NormalizeProductionJobRequest() mutated compensation mirrors")
	}
	got.Labels["keep"] = "changed"
	if input.Labels["keep"] != "yes" {
		t.Fatal("NormalizeProductionJobRequest() did not deep-clone request")
	}
}

func TestNormalizeProductionJobRequestAcceptsExactExistingMirrors(t *testing.T) {
	auth := productionAuthority()
	input := blankIdentityMirrors()
	fillExpectedMirrors(input, auth)
	before := proto.Clone(input).(*pb.JobRequest)

	got, err := NormalizeProductionJobRequest(input, auth)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	assertCanonicalMirrors(t, got, auth)
	if !proto.Equal(input, before) {
		t.Fatal("NormalizeProductionJobRequest() mutated matching input")
	}
}

func TestNormalizeProductionJobRequestPreservesUnknownIdentityFields(t *testing.T) {
	auth := productionAuthority()
	input := blankIdentityMirrors()
	fillExpectedMirrors(input, auth)
	requestUnknown := protowire.AppendTag(nil, 100, protowire.BytesType)
	requestUnknown = protowire.AppendString(requestUnknown, "request-extension")
	compUnknown := protowire.AppendTag(nil, 101, protowire.BytesType)
	compUnknown = protowire.AppendString(compUnknown, "compensation-extension")
	input.Identity.ProtoReflect().SetUnknown(requestUnknown)
	input.Compensation.Identity.ProtoReflect().SetUnknown(compUnknown)

	got, err := NormalizeProductionJobRequest(input, auth)
	if err != nil {
		t.Fatalf("NormalizeProductionJobRequest() error = %v", err)
	}
	if !bytes.Equal(got.GetIdentity().ProtoReflect().GetUnknown(), requestUnknown) {
		t.Fatal("request identity unknown fields were dropped")
	}
	if !bytes.Equal(got.GetCompensation().GetIdentity().ProtoReflect().GetUnknown(), compUnknown) {
		t.Fatal("compensation identity unknown fields were dropped")
	}
}

type mirrorConflictCase struct {
	name string
	set  func(*pb.JobRequest)
}

var mirrorConflictCases = []mirrorConflictCase{
	{"request tenant whitespace", func(r *pb.JobRequest) { r.TenantId = " tenant-a" }},
	{"request principal case", func(r *pb.JobRequest) { r.PrincipalId = "PRINCIPAL-A" }},
	{"request meta tenant", func(r *pb.JobRequest) { r.Meta.TenantId = "default" }},
	{"request meta actor", func(r *pb.JobRequest) { r.Meta.ActorId = "other" }},
	{"request env tenant", func(r *pb.JobRequest) { r.Env["tenant_id"] = "default" }},
	{"request env principal", func(r *pb.JobRequest) { r.Env["principal_id"] = "other" }},
	{"request env actor", func(r *pb.JobRequest) { r.Env["actor_id"] = "other" }},
	{"request env delegation", func(r *pb.JobRequest) { r.Env["delegation_id"] = "other" }},
	{"request identity tenant", func(r *pb.JobRequest) { r.Identity.TenantId = "other" }},
	{"request identity principal", func(r *pb.JobRequest) { r.Identity.PrincipalId = "other" }},
	{"request identity actor", func(r *pb.JobRequest) { r.Identity.ActorId = "other" }},
	{"request identity delegation", func(r *pb.JobRequest) { r.Identity.DelegationId = "other" }},
	{"compensation tenant", func(r *pb.JobRequest) { r.Compensation.TenantId = "other" }},
	{"compensation principal", func(r *pb.JobRequest) { r.Compensation.PrincipalId = "other" }},
	{"compensation meta tenant", func(r *pb.JobRequest) { r.Compensation.Meta.TenantId = "other" }},
	{"compensation meta actor", func(r *pb.JobRequest) { r.Compensation.Meta.ActorId = "other" }},
	{"compensation env tenant", func(r *pb.JobRequest) { r.Compensation.Env["tenant_id"] = "other" }},
	{"compensation env principal", func(r *pb.JobRequest) { r.Compensation.Env["principal_id"] = "other" }},
	{"compensation env actor", func(r *pb.JobRequest) { r.Compensation.Env["actor_id"] = "other" }},
	{"compensation env delegation", func(r *pb.JobRequest) { r.Compensation.Env["delegation_id"] = "other" }},
	{"compensation identity tenant", func(r *pb.JobRequest) { r.Compensation.Identity.TenantId = "other" }},
	{"compensation identity principal", func(r *pb.JobRequest) { r.Compensation.Identity.PrincipalId = "other" }},
	{"compensation identity actor", func(r *pb.JobRequest) { r.Compensation.Identity.ActorId = "other" }},
	{"compensation identity delegation", func(r *pb.JobRequest) { r.Compensation.Identity.DelegationId = "other" }},
}

func TestNormalizeProductionJobRequestRejectsEveryConflictingMirror(t *testing.T) {
	for _, tc := range mirrorConflictCases {
		t.Run(tc.name, func(t *testing.T) {
			input := blankIdentityMirrors()
			tc.set(input)
			before := proto.Clone(input).(*pb.JobRequest)
			_, err := NormalizeProductionJobRequest(input, productionAuthority())
			if !errors.Is(err, ErrProductionIdentityMismatch) {
				t.Fatalf("error = %v, want %v", err, ErrProductionIdentityMismatch)
			}
			if !proto.Equal(input, before) {
				t.Fatal("mismatch rejection mutated input")
			}
		})
	}
}

func TestNormalizeProductionJobRequestRejectsUnboundDelegation(t *testing.T) {
	auth := productionAuthority()
	auth.DelegationId = ""
	input := blankIdentityMirrors()
	input.Identity.DelegationId = "delegation-a"
	_, err := NormalizeProductionJobRequest(input, auth)
	if !errors.Is(err, ErrProductionIdentityMismatch) {
		t.Fatalf("error = %v, want %v", err, ErrProductionIdentityMismatch)
	}
}

func fillExpectedMirrors(req *pb.JobRequest, auth *pb.IdentityBinding) {
	req.TenantId, req.PrincipalId = auth.TenantId, auth.PrincipalId
	req.Meta.TenantId, req.Meta.ActorId = auth.TenantId, auth.ActorId
	fillExpectedEnv(req.Env, auth)
	req.Identity = proto.Clone(auth).(*pb.IdentityBinding)
	req.Compensation.TenantId, req.Compensation.PrincipalId = auth.TenantId, auth.PrincipalId
	req.Compensation.Meta.TenantId, req.Compensation.Meta.ActorId = auth.TenantId, auth.ActorId
	fillExpectedEnv(req.Compensation.Env, auth)
	req.Compensation.Identity = proto.Clone(auth).(*pb.IdentityBinding)
}

func fillExpectedEnv(env map[string]string, auth *pb.IdentityBinding) {
	env["tenant_id"], env["principal_id"] = auth.TenantId, auth.PrincipalId
	env["actor_id"], env["delegation_id"] = auth.ActorId, auth.DelegationId
}

func assertCanonicalMirrors(t *testing.T, req *pb.JobRequest, auth *pb.IdentityBinding) {
	t.Helper()
	checks := []struct{ path, got, want string }{
		{"request.tenant_id", req.GetTenantId(), auth.GetTenantId()},
		{"request.principal_id", req.GetPrincipalId(), auth.GetPrincipalId()},
		{"request.meta.tenant_id", req.GetMeta().GetTenantId(), auth.GetTenantId()},
		{"request.meta.actor_id", req.GetMeta().GetActorId(), auth.GetActorId()},
		{"request.env.tenant_id", req.GetEnv()["tenant_id"], auth.GetTenantId()},
		{"request.env.principal_id", req.GetEnv()["principal_id"], auth.GetPrincipalId()},
		{"request.env.actor_id", req.GetEnv()["actor_id"], auth.GetActorId()},
		{"request.env.delegation_id", req.GetEnv()["delegation_id"], auth.GetDelegationId()},
		{"request.identity", req.GetIdentity().String(), auth.String()},
		{"compensation.tenant_id", req.GetCompensation().GetTenantId(), auth.GetTenantId()},
		{"compensation.principal_id", req.GetCompensation().GetPrincipalId(), auth.GetPrincipalId()},
		{"compensation.meta.tenant_id", req.GetCompensation().GetMeta().GetTenantId(), auth.GetTenantId()},
		{"compensation.meta.actor_id", req.GetCompensation().GetMeta().GetActorId(), auth.GetActorId()},
		{"compensation.env.tenant_id", req.GetCompensation().GetEnv()["tenant_id"], auth.GetTenantId()},
		{"compensation.env.principal_id", req.GetCompensation().GetEnv()["principal_id"], auth.GetPrincipalId()},
		{"compensation.env.actor_id", req.GetCompensation().GetEnv()["actor_id"], auth.GetActorId()},
		{"compensation.env.delegation_id", req.GetCompensation().GetEnv()["delegation_id"], auth.GetDelegationId()},
		{"compensation.identity", req.GetCompensation().GetIdentity().String(), auth.String()},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Errorf("%s = %q, want %q", check.path, check.got, check.want)
		}
	}
}
