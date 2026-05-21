package ci_test

import (
	"context"
	"testing"

	"github.com/cordum/cordum/core/edge/shadow/ci"
)

func TestResolver_TenantPrecedence_OIDCSubjectWinsOverOrgRepoMap(t *testing.T) {
	r := ci.NewDefaultResolver(map[string]map[string]string{
		"acme": {"web": "tenant-a"},
	}, "cordum.shadow.quarantine")

	claims := &ci.OIDCClaims{Subject: "project_path:acme/web:ref_type:branch:ref:main", Repo: "acme/web"}
	run := ci.Run{Provider: ci.ProviderGitLab, Repo: "acme/web", Actor: "alice"}
	tenant, source := r.ResolveTenant(context.Background(), claims, run)
	if tenant != "tenant-a" {
		t.Errorf("tenant=%q, want tenant-a", tenant)
	}
	if source != ci.TenantSourceOIDC {
		t.Errorf("source=%q, want %q", source, ci.TenantSourceOIDC)
	}
}

func TestResolver_TenantPrecedence_OrgRepoMapFallback_NoOIDC(t *testing.T) {
	r := ci.NewDefaultResolver(map[string]map[string]string{
		"acme": {"web": "tenant-a"},
	}, "cordum.shadow.quarantine")

	run := ci.Run{Provider: ci.ProviderJenkins, Repo: "acme/web", Actor: "alice"}
	tenant, source := r.ResolveTenant(context.Background(), nil, run)
	if tenant != "tenant-a" {
		t.Errorf("tenant=%q, want tenant-a", tenant)
	}
	if source != ci.TenantSourceOrgRepoMap {
		t.Errorf("source=%q, want %q", source, ci.TenantSourceOrgRepoMap)
	}
}

func TestResolver_TenantPrecedence_QuarantineUnknownRepo(t *testing.T) {
	r := ci.NewDefaultResolver(map[string]map[string]string{
		"acme": {"web": "tenant-a"},
	}, "cordum.shadow.quarantine")

	run := ci.Run{Provider: ci.ProviderBuildkite, Repo: "evil-corp/mystery", Actor: "alice"}
	tenant, source := r.ResolveTenant(context.Background(), nil, run)
	if tenant != "cordum.shadow.quarantine" {
		t.Errorf("tenant=%q, want quarantine", tenant)
	}
	if source != ci.TenantSourceQuarantine {
		t.Errorf("source=%q, want %q", source, ci.TenantSourceQuarantine)
	}
}

func TestResolver_PrincipalPrecedence_OIDCSubject(t *testing.T) {
	r := ci.NewDefaultResolver(nil, "quarantine")
	claims := &ci.OIDCClaims{Subject: "project_path:acme/web:ref:main"}
	run := ci.Run{Actor: "alice"}
	principal, source := r.ResolvePrincipal(context.Background(), claims, run)
	if principal != claims.Subject {
		t.Errorf("principal=%q, want %q", principal, claims.Subject)
	}
	if source != ci.PrincipalSourceOIDCSubject {
		t.Errorf("source=%q, want %q", source, ci.PrincipalSourceOIDCSubject)
	}
}

func TestResolver_PrincipalPrecedence_WorkflowActorWhenNoOIDC(t *testing.T) {
	r := ci.NewDefaultResolver(nil, "quarantine")
	run := ci.Run{Actor: "alice"}
	principal, source := r.ResolvePrincipal(context.Background(), nil, run)
	if principal != "alice" {
		t.Errorf("principal=%q, want alice", principal)
	}
	if source != ci.PrincipalSourceWorkflowActor {
		t.Errorf("source=%q, want %q", source, ci.PrincipalSourceWorkflowActor)
	}
}

func TestResolver_PrincipalPrecedence_UnknownWhenAllEmpty(t *testing.T) {
	r := ci.NewDefaultResolver(nil, "quarantine")
	run := ci.Run{}
	principal, source := r.ResolvePrincipal(context.Background(), nil, run)
	if principal != ci.PrincipalUnknown {
		t.Errorf("principal=%q, want %q", principal, ci.PrincipalUnknown)
	}
	if source != ci.PrincipalSourceQuarantine {
		t.Errorf("source=%q, want %q", source, ci.PrincipalSourceQuarantine)
	}
}
