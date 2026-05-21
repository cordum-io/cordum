package ci_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/edge/shadow"
	"github.com/cordum/cordum/core/edge/shadow/ci"
)

const (
	testTenantA       = "tenant-a"
	testQuarantineTen = "cordum.shadow.quarantine"
)

// detectorFixture wires the CI detector against per-provider httptest mock
// servers, a miniredis-backed shadow.Store, and a spy observer. Each
// provider scanner gets its own httptest server URL injected via
// scanner-specific Config.BaseURL so tests never touch real provider APIs.
type detectorFixture struct {
	detector *ci.Detector
	store    shadow.Store
	observer *spyObserver
	servers  map[ci.Provider]*httptest.Server
	muxes    map[ci.Provider]*http.ServeMux
	clock    time.Time
}

func newFixture(t *testing.T) *detectorFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
	t.Cleanup(func() { _ = rdb.Close() })

	clock := time.Date(2026, 5, 18, 16, 0, 0, 0, time.UTC)
	store, err := shadow.NewRedisStore(rdb, shadow.WithClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}

	f := &detectorFixture{
		store:    store,
		observer: newSpyObserver(),
		servers:  make(map[ci.Provider]*httptest.Server, 4),
		muxes:    make(map[ci.Provider]*http.ServeMux, 4),
		clock:    clock,
	}
	for _, p := range []ci.Provider{ci.ProviderGitLab, ci.ProviderJenkins, ci.ProviderBuildkite, ci.ProviderCircleCI} {
		mux := http.NewServeMux()
		srv := httptest.NewServer(mux)
		t.Cleanup(srv.Close)
		f.servers[p] = srv
		f.muxes[p] = mux
	}
	return f
}

func (f *detectorFixture) wireDetector(t *testing.T, cfg ci.Config, scanners ...ci.ProviderScanner) {
	t.Helper()
	if cfg.QuarantineTenantID == "" {
		cfg.QuarantineTenantID = testQuarantineTen
	}
	if cfg.CordumAttachActionRef == "" {
		cfg.CordumAttachActionRef = "cordum/cordum-edge-attach"
	}
	if cfg.ScanInterval == 0 {
		cfg.ScanInterval = 60 * time.Second
	}
	if len(cfg.KnownAgentActionRefs) == 0 {
		cfg.KnownAgentActionRefs = []string{
			"anthropic-ai/claude-code-action",
			"cursor-sh/cursor-action",
			"openai/codex-action",
		}
	}
	if len(cfg.KnownAgentRunTokens) == 0 {
		cfg.KnownAgentRunTokens = []string{"claude", "codex", "cursor"}
	}
	if len(cfg.ProviderEndpointHosts) == 0 {
		cfg.ProviderEndpointHosts = []string{"api.anthropic.com", "api.openai.com"}
	}
	cfg.Scanners = scanners
	cfg.Observer = f.observer

	d, err := ci.NewDetector(cfg, f.store)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}
	d.SetClock(func() time.Time { return f.clock })
	f.detector = d
}

func (f *detectorFixture) listAll(t *testing.T, tenant string) []shadow.ShadowAgentFinding {
	t.Helper()
	page, err := f.store.ListFindings(context.Background(), shadow.ListFindingsQuery{
		TenantID: tenant, Limit: 100, IncludeManagedSkip: true,
	})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	return page.Findings
}

// === GitLab CI ===

func TestGitLabCIDetector_EmitsFinding_AgentPipelineMissingAttach(t *testing.T) {
	f := newFixture(t)
	// Project metadata + pipelines + jobs + variables + .gitlab-ci.yml.
	gitlabSrv := f.servers[ci.ProviderGitLab]
	f.muxes[ci.ProviderGitLab].HandleFunc("/api/v4/projects/acme%2Fweb", writeJSON(map[string]interface{}{
		"id": 17, "path_with_namespace": "acme/web", "default_branch": "main",
	}))
	f.muxes[ci.ProviderGitLab].HandleFunc("/api/v4/projects/17/pipelines", writeJSON([]map[string]interface{}{
		{"id": 1001, "sha": "abc123", "ref": "main", "status": "success", "source": "push", "user": map[string]interface{}{"username": "alice"}},
	}))
	f.muxes[ci.ProviderGitLab].HandleFunc("/api/v4/projects/17/pipelines/1001/jobs", writeJSON([]map[string]interface{}{
		{"id": 5001, "name": "build", "runner": map[string]interface{}{"id": 42, "description": "self-hosted-rnr", "tag_list": []string{"self-hosted", "linux"}}, "stage": "build"},
	}))
	f.muxes[ci.ProviderGitLab].HandleFunc("/api/v4/projects/17/variables", writeJSON([]map[string]interface{}{
		{"key": "ANTHROPIC_API_KEY", "variable_type": "env_var", "protected": false, "masked": true},
	}))
	f.muxes[ci.ProviderGitLab].HandleFunc("/api/v4/projects/17/repository/files/.gitlab-ci.yml/raw", writeRaw(gitlabPipelineYAMLWithAgent()))

	scanner := ci.NewGitLabScanner(ci.GitLabConfig{
		BaseURL: gitlabSrv.URL, Token: "tok", Projects: []string{"acme/web"},
	})

	f.wireDetector(t, ci.Config{
		OrgRepoMap: map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDC:       map[ci.Provider]ci.OIDCConfig{ci.ProviderGitLab: {Disabled: true}},
	}, scanner)

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := f.listAll(t, testTenantA)
	if len(got) == 0 {
		t.Fatalf("expected GitLab finding for agent-pipeline-missing-attach; got 0")
	}
	if got[0].CIProvider != shadow.CIProviderGitLabCI {
		t.Errorf("CIProvider = %q, want %q", got[0].CIProvider, shadow.CIProviderGitLabCI)
	}
	if got[0].SourceType != shadow.SourceTypeCI {
		t.Errorf("SourceType = %q, want %q", got[0].SourceType, shadow.SourceTypeCI)
	}
	if got[0].Repo != "acme/web" {
		t.Errorf("Repo = %q, want acme/web", got[0].Repo)
	}
	if got[0].RunID != "1001" {
		t.Errorf("RunID = %q, want 1001", got[0].RunID)
	}
	if got[0].RetentionClass != shadow.ShadowRetentionDefault {
		t.Errorf("RetentionClass = %q, want %q", got[0].RetentionClass, shadow.ShadowRetentionDefault)
	}
	// Data minimization: NAME present, VALUE absent.
	if !strings.Contains(got[0].EvidenceSummary, "ANTHROPIC_API_KEY") {
		t.Errorf("EvidenceSummary should include env NAME, got %q", got[0].EvidenceSummary)
	}
	if strings.Contains(got[0].EvidenceSummary, "ohnodontleakme") {
		t.Errorf("EvidenceSummary leaked env VALUE: %q", got[0].EvidenceSummary)
	}
}

// === Jenkins ===

func TestJenkinsDetector_EmitsFinding_AgentJenkinsfile(t *testing.T) {
	f := newFixture(t)
	jenkinsSrv := f.servers[ci.ProviderJenkins]
	f.muxes[ci.ProviderJenkins].HandleFunc("/job/myjob/api/json", writeJSON(map[string]interface{}{
		"name": "myjob", "fullName": "myjob", "url": jenkinsSrv.URL + "/job/myjob/",
		"lastBuild": map[string]interface{}{"number": 42, "url": jenkinsSrv.URL + "/job/myjob/42/"},
		"scm":       map[string]interface{}{"userRemoteConfigs": []map[string]interface{}{{"url": "https://github.com/acme/web.git"}}},
		"property":  []map[string]interface{}{},
	}))
	f.muxes[ci.ProviderJenkins].HandleFunc("/job/myjob/42/api/json", writeJSON(map[string]interface{}{
		"number": 42, "result": "SUCCESS", "fullDisplayName": "myjob #42",
		"actions": []map[string]interface{}{
			{"causes": []map[string]interface{}{{"userId": "alice", "userName": "alice"}}},
		},
		"builtOn":     "self-hosted-rnr",
		"environment": map[string]interface{}{"ANTHROPIC_API_KEY": "ohnodontleakme"},
	}))
	f.muxes[ci.ProviderJenkins].HandleFunc("/job/myjob/config.xml", writeXML(jenkinsfileWithAgent()))

	scanner := ci.NewJenkinsScanner(ci.JenkinsConfig{
		BaseURL: jenkinsSrv.URL, Username: "u", APIToken: "t", Jobs: []string{"myjob"},
	})
	f.wireDetector(t, ci.Config{
		OrgRepoMap: map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDC:       map[ci.Provider]ci.OIDCConfig{ci.ProviderJenkins: {Disabled: true}},
	}, scanner)

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := f.listAll(t, testTenantA)
	if len(got) == 0 {
		t.Fatalf("expected Jenkins finding for agent-jenkinsfile-missing-attach; got 0")
	}
	if got[0].CIProvider != shadow.CIProviderJenkins {
		t.Errorf("CIProvider = %q, want %q", got[0].CIProvider, shadow.CIProviderJenkins)
	}
	if got[0].RunID != "42" {
		t.Errorf("RunID = %q, want 42", got[0].RunID)
	}
	// Jenkins env reads MUST NOT persist values.
	if strings.Contains(got[0].EvidenceSummary, "ohnodontleakme") {
		t.Errorf("EvidenceSummary leaked env VALUE: %q", got[0].EvidenceSummary)
	}
}

// === Buildkite ===

func TestBuildkiteDetector_EmitsFinding_AgentPipelineYAML(t *testing.T) {
	f := newFixture(t)
	bkSrv := f.servers[ci.ProviderBuildkite]
	f.muxes[ci.ProviderBuildkite].HandleFunc("/v2/organizations/acme/pipelines/web", writeJSON(map[string]interface{}{
		"slug": "web", "name": "web", "repository": "git@github.com:acme/web.git",
		"default_branch": "main",
		"configuration":  buildkitePipelineYAMLWithAgent(),
	}))
	f.muxes[ci.ProviderBuildkite].HandleFunc("/v2/organizations/acme/pipelines/web/builds", writeJSON([]map[string]interface{}{
		{
			"id": "build-uuid-1", "number": 77, "state": "passed", "branch": "main", "commit": "abc123",
			"source": "webhook", "creator": map[string]interface{}{"name": "alice"},
			"jobs": []map[string]interface{}{{
				"id": "job-uuid-1", "type": "script",
				"agent": map[string]interface{}{"id": "agent-uuid-1", "name": "self-hosted-bk", "meta_data": []string{"queue=self-hosted"}},
				"env":   map[string]interface{}{"ANTHROPIC_API_KEY": "ohnodontleakme"},
			}},
		},
	}))

	scanner := ci.NewBuildkiteScanner(ci.BuildkiteConfig{
		BaseURL: bkSrv.URL, Token: "tok", Organizations: []string{"acme"}, Pipelines: []string{"acme/web"},
	})
	f.wireDetector(t, ci.Config{
		OrgRepoMap: map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDC:       map[ci.Provider]ci.OIDCConfig{ci.ProviderBuildkite: {Disabled: true}},
	}, scanner)

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := f.listAll(t, testTenantA)
	if len(got) == 0 {
		t.Fatalf("expected Buildkite finding; got 0")
	}
	if got[0].CIProvider != shadow.CIProviderBuildkite {
		t.Errorf("CIProvider = %q, want %q", got[0].CIProvider, shadow.CIProviderBuildkite)
	}
	if got[0].RunID != "77" {
		t.Errorf("RunID = %q, want 77", got[0].RunID)
	}
	if strings.Contains(got[0].EvidenceSummary, "ohnodontleakme") {
		t.Errorf("EvidenceSummary leaked env VALUE: %q", got[0].EvidenceSummary)
	}
}

// === CircleCI ===

func TestCircleCIDetector_EmitsFinding_AgentConfigYAML(t *testing.T) {
	f := newFixture(t)
	ccSrv := f.servers[ci.ProviderCircleCI]
	f.muxes[ci.ProviderCircleCI].HandleFunc("/api/v2/project/gh/acme/web", writeJSON(map[string]interface{}{
		"slug": "gh/acme/web", "name": "web", "organization_name": "acme", "vcs_info": map[string]interface{}{
			"vcs_url": "https://github.com/acme/web", "default_branch": "main",
		},
	}))
	f.muxes[ci.ProviderCircleCI].HandleFunc("/api/v2/project/gh/acme/web/pipeline", writeJSON(map[string]interface{}{
		"items": []map[string]interface{}{
			{
				"id": "pipe-uuid-1", "number": 33, "state": "created",
				"vcs":     map[string]interface{}{"branch": "main", "revision": "abc123"},
				"trigger": map[string]interface{}{"type": "webhook", "actor": map[string]interface{}{"login": "alice"}},
			},
		},
	}))
	f.muxes[ci.ProviderCircleCI].HandleFunc("/api/v2/pipeline/pipe-uuid-1/workflow", writeJSON(map[string]interface{}{
		"items": []map[string]interface{}{{"id": "wf-uuid-1", "pipeline_id": "pipe-uuid-1", "name": "build", "status": "success"}},
	}))
	f.muxes[ci.ProviderCircleCI].HandleFunc("/api/v2/workflow/wf-uuid-1/job", writeJSON(map[string]interface{}{
		"items": []map[string]interface{}{{"id": "job-uuid-1", "name": "build", "status": "success", "type": "build", "job_number": 99}},
	}))
	f.muxes[ci.ProviderCircleCI].HandleFunc("/api/v2/project/gh/acme/web/envvar", writeJSON(map[string]interface{}{
		"items": []map[string]interface{}{{"name": "ANTHROPIC_API_KEY", "value": "xxxx"}},
	}))
	f.muxes[ci.ProviderCircleCI].HandleFunc("/api/v1.1/project/github/acme/web/configuration", writeRaw(circleciConfigYAMLWithAgent()))

	scanner := ci.NewCircleCIScanner(ci.CircleCIConfig{
		BaseURL: ccSrv.URL, Token: "tok", Projects: []string{"gh/acme/web"},
	})
	f.wireDetector(t, ci.Config{
		OrgRepoMap: map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDC:       map[ci.Provider]ci.OIDCConfig{ci.ProviderCircleCI: {Disabled: true}},
	}, scanner)

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	got := f.listAll(t, testTenantA)
	if len(got) == 0 {
		t.Fatalf("expected CircleCI finding; got 0")
	}
	if got[0].CIProvider != shadow.CIProviderCircleCI {
		t.Errorf("CIProvider = %q, want %q", got[0].CIProvider, shadow.CIProviderCircleCI)
	}
	if got[0].RunID != "33" {
		t.Errorf("RunID = %q (pipeline number), want 33", got[0].RunID)
	}
}

// === Cross-provider: no mutation calls (observe-only) ===

func TestAllProviders_NoMutationCalls(t *testing.T) {
	// Wire each provider's mux to assert non-GET methods are NEVER seen.
	f := newFixture(t)
	for prov, mux := range f.muxes {
		captured := prov
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				t.Errorf("%s scanner issued non-read method %s %s", captured, r.Method, r.URL.Path)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			// Generic empty JSON body so handlers don't 404 the scan.
			_, _ = w.Write([]byte(`{}`))
		})
	}
	gitlabSrv := f.servers[ci.ProviderGitLab]
	jenkinsSrv := f.servers[ci.ProviderJenkins]
	bkSrv := f.servers[ci.ProviderBuildkite]
	ccSrv := f.servers[ci.ProviderCircleCI]
	f.wireDetector(t, ci.Config{
		OIDC: map[ci.Provider]ci.OIDCConfig{
			ci.ProviderGitLab:    {Disabled: true},
			ci.ProviderJenkins:   {Disabled: true},
			ci.ProviderBuildkite: {Disabled: true},
			ci.ProviderCircleCI:  {Disabled: true},
		},
	},
		ci.NewGitLabScanner(ci.GitLabConfig{BaseURL: gitlabSrv.URL, Token: "t", Projects: []string{"acme/web"}}),
		ci.NewJenkinsScanner(ci.JenkinsConfig{BaseURL: jenkinsSrv.URL, Username: "u", APIToken: "t", Jobs: []string{"myjob"}}),
		ci.NewBuildkiteScanner(ci.BuildkiteConfig{BaseURL: bkSrv.URL, Token: "t", Organizations: []string{"acme"}, Pipelines: []string{"acme/web"}}),
		ci.NewCircleCIScanner(ci.CircleCIConfig{BaseURL: ccSrv.URL, Token: "t", Projects: []string{"gh/acme/web"}}),
	)
	_ = f.detector.Scan(context.Background())
}

// === Helper: HTTP response writers ===

func writeJSON(payload interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(payload)
		_, _ = w.Write(body)
	}
}

func writeRaw(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}
}

func writeXML(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body))
	}
}

// === Synthetic workflow contents ===

func gitlabPipelineYAMLWithAgent() string {
	return `stages:
  - build
build_job:
  stage: build
  image: docker.io/anthropic-ai/claude-code-action:latest
  script:
    - claude run task.md
  variables:
    ANTHROPIC_API_KEY: $CI_ANTHROPIC_KEY
`
}

func jenkinsfileWithAgent() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<flow-definition>
  <definition>
    <script>
pipeline {
  agent any
  stages {
    stage('build') { steps { sh 'claude run plan.md' } }
  }
  environment { ANTHROPIC_API_KEY = credentials('anthropic-key') }
}
    </script>
  </definition>
</flow-definition>`
}

func buildkitePipelineYAMLWithAgent() string {
	return `steps:
  - command: claude run plan.md
    env:
      ANTHROPIC_API_KEY: "$ANTHROPIC_API_KEY"
`
}

func circleciConfigYAMLWithAgent() string {
	return `version: 2.1
jobs:
  build:
    docker:
      - image: anthropic-ai/claude-code-action:latest
    steps:
      - checkout
      - run: claude run plan.md
workflows:
  ci:
    jobs:
      - build
`
}
