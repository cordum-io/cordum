package github_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	gogithub "github.com/google/go-github/v74/github"

	ghdetector "github.com/cordum/cordum/core/edge/shadow/github"
)

func TestGHDetector_Signals_RunCommandLeadingToken(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	registerBasicRun(t, f, 2001, "acme", "web", workflowYAMLRunTokenOnly())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !spyHasSignal(f.observer, "missing_cordum_attach") {
		t.Fatalf("missing_cordum_attach not emitted for run-token agent use: %+v", f.observer.emits)
	}
}

func TestGHDetector_Signals_EdgeHeartbeatSuppressesMissingAttach(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
		EdgeSessionHeartbeat: func(context.Context, *gogithub.WorkflowRun) (bool, error) {
			return true, nil
		},
	}
	f := newFixture(t, cfg)
	registerBasicRun(t, f, 2002, "acme", "web", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if spyHasSignal(f.observer, "missing_cordum_attach") {
		t.Fatalf("managed heartbeat emitted missing_cordum_attach: %+v", f.observer.emits)
	}
	if findings := f.listAll(t, testTenantA); len(findings) != 0 {
		t.Fatalf("heartbeat-managed run produced findings: %+v", findings)
	}
}

func TestGHDetector_Signals_WorkflowFetchErrorAudited(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{basicRun(2003, "acme", "web", ".github/workflows/missing.yml")})
	f.registerJobs("acme", "web", 2003, []canJob{{ID: 6003, RunID: 2003, Labels: []string{"self-hosted"}, RunnerID: 99}})
	f.registerNotFound("acme", "web", ".github/workflows/missing.yml")

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !spyHasAudit(f.observer, "edge.shadow_workflow_fetch_degraded") {
		t.Fatalf("workflow fetch error was not audited: %+v", f.observer.audits)
	}
}

func TestGHDetector_TenantMapping_QuarantineForcesUnknownPrincipal(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": ""}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	registerBasicRun(t, f, 2004, "acme", "web", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testQuarantineTen)
	if len(findings) != 1 {
		t.Fatalf("quarantine findings = %d, want 1: %+v", len(findings), findings)
	}
	if got := findings[0].PrincipalID; got != "unknown" {
		t.Fatalf("PrincipalID = %q, want unknown", got)
	}
	if got := findings[0].PrincipalSource; got != ghdetector.PrincipalSourceQuarantine {
		t.Fatalf("PrincipalSource = %q, want quarantine", got)
	}
}

func TestGHDetector_Signals_EdgeHeartbeatLookupErrorAudited(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
		EdgeSessionHeartbeat: func(context.Context, *gogithub.WorkflowRun) (bool, error) {
			return false, errors.New("edge session lookup failed")
		},
	}
	f := newFixture(t, cfg)
	registerBasicRun(t, f, 2005, "acme", "web", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !spyHasAudit(f.observer, "edge.shadow_session_lookup_error") {
		t.Fatalf("heartbeat lookup error was not audited: %+v", f.observer.audits)
	}
}

func registerBasicRun(t *testing.T, f *detectorFixture, id int64, org, repo, workflow string) {
	t.Helper()
	f.registerRuns(org, repo, []canRun{basicRun(id, org, repo, ".github/workflows/ci.yml")})
	f.registerJobs(org, repo, id, []canJob{{ID: id + 4000, RunID: id, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML(org, repo, "", ".github/workflows/ci.yml", workflow)
}

func basicRun(id int64, org, repo, path string) canRun {
	return canRun{
		ID: id, WorkflowID: 300, HeadBranch: "main", Path: path,
		Actor:      ghActor{Login: "workflow-alice", Type: "User"},
		Repository: ghRepo{Name: repo, FullName: org + "/" + repo, Owner: ghOwn{Login: org}},
		HeadRepo:   ghRepo{Name: repo, FullName: org + "/" + repo, Owner: ghOwn{Login: org}},
	}
}

func workflowYAMLRunTokenOnly() string {
	return strings.Join([]string{
		"name: ci",
		"on: push",
		"jobs:",
		"  build:",
		"    runs-on: ubuntu-latest",
		"    steps:",
		"      - run: claude --prompt 'do work'",
	}, "\n")
}

func spyHasSignal(obs *spyObserver, signal string) bool {
	for _, emit := range obs.emits {
		if emit.Signal == signal {
			return true
		}
	}
	return false
}

func spyHasAudit(obs *spyObserver, eventType string) bool {
	for _, event := range obs.audits {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}
