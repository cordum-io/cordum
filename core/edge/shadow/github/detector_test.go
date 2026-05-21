package github_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	gogithub "github.com/google/go-github/v74/github"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/redis/go-redis/v9"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow"
	ghdetector "github.com/cordum/cordum/core/edge/shadow/github"
)

const (
	testTenantA       = "tenant-a"
	testTenantB       = "tenant-b"
	testQuarantineTen = "cordum.shadow.quarantine"
	defaultIssuer     = "https://token.actions.githubusercontent.com"
)

// detectorFixture wires the GH detector against an httptest GH-API
// server, a miniredis-backed shadow.Store, and a spy Observer so each
// test can assert exact emitted findings + observer calls without
// touching global state.
type detectorFixture struct {
	detector *ghdetector.Detector
	store    shadow.Store
	observer *spyObserver
	server   *httptest.Server
	mux      *http.ServeMux
	mr       *miniredis.Miniredis
	clock    time.Time
}

func newFixture(t *testing.T, cfg ghdetector.Config) *detectorFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
	t.Cleanup(func() { _ = rdb.Close(); mr.Close() })

	clock := time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC)
	store, err := shadow.NewRedisStore(rdb,
		shadow.WithClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatalf("NewRedisStore: %v", err)
	}

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	ghClient := gogithub.NewClient(nil)
	baseURL, _ := url.Parse(server.URL + "/")
	ghClient.BaseURL = baseURL
	ghClient.UploadURL = baseURL

	observer := newSpyObserver()

	if len(cfg.Orgs) == 0 {
		cfg.Orgs = []string{"acme"}
	}
	if cfg.QuarantineTenantID == "" {
		cfg.QuarantineTenantID = testQuarantineTen
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
	if cfg.CordumAttachActionRef == "" {
		cfg.CordumAttachActionRef = "cordum/cordum-edge-attach"
	}
	if len(cfg.ProviderEndpointHosts) == 0 {
		cfg.ProviderEndpointHosts = []string{
			"api.anthropic.com", "api.openai.com", "generativelanguage.googleapis.com",
		}
	}
	if len(cfg.AgentConfigPaths) == 0 {
		cfg.AgentConfigPaths = []string{".claude/settings.json", ".cursor/config.toml", "AGENTS.md"}
	}
	if len(cfg.BotActorAllowlist) == 0 {
		cfg.BotActorAllowlist = []string{"dependabot[bot]", "renovate[bot]"}
	}

	d, err := ghdetector.NewDetector(cfg, ghClient, store, observer, nil)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}
	d.SetClock(func() time.Time { return clock })

	return &detectorFixture{
		detector: d,
		store:    store,
		observer: observer,
		server:   server,
		mux:      mux,
		mr:       mr,
		clock:    clock,
	}
}

func (f *detectorFixture) listAll(t *testing.T, tenant string) []shadow.ShadowAgentFinding {
	t.Helper()
	page, err := f.store.ListFindings(context.Background(), shadow.ListFindingsQuery{
		TenantID:           tenant,
		Limit:              100,
		IncludeManagedSkip: true,
	})
	if err != nil {
		t.Fatalf("ListFindings: %v", err)
	}
	return page.Findings
}

// --- canned API responses ---

// canWorkflowRun lets a test register a workflow run with arbitrary
// metadata. The detector calls /repos/{org}/{repo}/actions/runs to
// list runs and /repos/{org}/{repo}/actions/runs/{run_id}/jobs to
// enumerate jobs.
type canRun struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	HeadBranch string  `json:"head_branch"`
	HeadSHA    string  `json:"head_sha"`
	Path       string  `json:"path"`
	Event      string  `json:"event"`
	Actor      ghActor `json:"actor"`
	Status     string  `json:"status"`
	Repository ghRepo  `json:"repository"`
	HeadRepo   ghRepo  `json:"head_repository"`
	WorkflowID int64   `json:"workflow_id"`
	JobsURL    string  `json:"jobs_url"`
	RunNumber  int     `json:"run_number"`
}

type ghActor struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

type ghRepo struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Fork     bool   `json:"fork"`
	Owner    ghOwn  `json:"owner"`
}

type ghOwn struct {
	Login string `json:"login"`
}

type canJob struct {
	ID         int64     `json:"id"`
	RunID      int64     `json:"run_id"`
	Name       string    `json:"name"`
	Labels     []string  `json:"labels"`
	RunnerID   int64     `json:"runner_id"`
	RunnerName string    `json:"runner_name"`
	Status     string    `json:"status"`
	Steps      []canStep `json:"steps"`
}

type canStep struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// registerRuns wires the /actions/runs list endpoint for one repo.
func (f *detectorFixture) registerRuns(org, repo string, runs []canRun) {
	path := "/repos/" + org + "/" + repo + "/actions/runs"
	f.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]interface{}{
			"total_count":   len(runs),
			"workflow_runs": runs,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

func (f *detectorFixture) registerRunPages(org, repo string, pages [][]canRun) {
	path := "/repos/" + org + "/" + repo + "/actions/runs"
	f.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if r.URL.Query().Get("page") == "2" {
			page = 2
		}
		if page < len(pages) {
			next := f.server.URL + path + "?page=" + itoa(int64(page+1))
			w.Header().Set("Link", "<"+next+">; rel=\"next\"")
		}
		runs := pages[page-1]
		body, _ := json.Marshal(map[string]interface{}{
			"total_count":   len(runs),
			"workflow_runs": runs,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

func (f *detectorFixture) registerJobs(org, repo string, runID int64, jobs []canJob) {
	path := "/repos/" + org + "/" + repo + "/actions/runs/" + itoa(runID) + "/jobs"
	f.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]interface{}{
			"total_count": len(jobs),
			"jobs":        jobs,
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

func (f *detectorFixture) registerWorkflowYAML(org, repo, ref, path, yaml string) {
	apiPath := "/repos/" + org + "/" + repo + "/contents/" + path
	f.mux.HandleFunc(apiPath, func(w http.ResponseWriter, r *http.Request) {
		body, _ := json.Marshal(map[string]interface{}{
			"name":     pathBase(path),
			"path":     path,
			"sha":      "fakesha",
			"size":     len(yaml),
			"encoding": "base64",
			"content":  base64Encode(yaml),
			"type":     "file",
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

func (f *detectorFixture) registerNotFound(org, repo, path string) {
	apiPath := "/repos/" + org + "/" + repo + "/contents/" + path
	f.mux.HandleFunc(apiPath, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	})
}

// --- spy observer ---

type emitCall struct {
	Signal string
	Risk   string
}

type spyObserver struct {
	emits         []emitCall
	audits        []audit.SIEMEvent
	oidcOutcomes  []string
	rateRemaining []int
}

func newSpyObserver() *spyObserver { return &spyObserver{} }

func (s *spyObserver) RecordFindingEmit(signal, risk string) {
	s.emits = append(s.emits, emitCall{Signal: signal, Risk: risk})
}

func (s *spyObserver) EmitAudit(event audit.SIEMEvent) {
	s.audits = append(s.audits, event)
}

func (s *spyObserver) OIDCVerifyOutcome(result string) {
	s.oidcOutcomes = append(s.oidcOutcomes, result)
}

func (s *spyObserver) RateLimitRemaining(remaining int) {
	s.rateRemaining = append(s.rateRemaining, remaining)
}

// === §8.1 signal extractors ===

func TestGHDetector_Signals_WorkflowMetadata(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	runs := []canRun{{
		ID: 1001, Name: "ci", HeadBranch: "main", HeadSHA: "abc123",
		Path: ".github/workflows/ci.yml", Event: "push", WorkflowID: 200,
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		RunNumber:  42,
	}}
	f.registerRuns("acme", "web", runs)
	f.registerJobs("acme", "web", 1001, []canJob{{ID: 5001, RunID: 1001, Name: "build", Labels: []string{"ubuntu-latest"}, RunnerID: 99, RunnerName: "GitHub Actions 99"}})
	f.registerWorkflowYAML("acme", "web", "abc123", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	if len(findings) == 0 {
		t.Fatalf("expected at least one finding for workflow metadata signal; got 0")
	}
	got := findings[0]
	if got.WorkflowID != "200" {
		t.Errorf("WorkflowID = %q, want %q", got.WorkflowID, "200")
	}
	if got.RunID != "1001" {
		t.Errorf("RunID = %q, want %q", got.RunID, "1001")
	}
	if got.Repo != "acme/web" {
		t.Errorf("Repo = %q, want %q", got.Repo, "acme/web")
	}
	if got.Ref != "main" {
		t.Errorf("Ref = %q, want %q", got.Ref, "main")
	}
}

func TestGHDetector_Signals_RunnerIdentity(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1002, Name: "ci", HeadBranch: "main", WorkflowID: 200,
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	// self-hosted runner labels include "self-hosted" — design doc §8.1 row 2
	f.registerJobs("acme", "web", 1002, []canJob{{
		ID: 5002, RunID: 1002, Name: "build",
		Labels:   []string{"self-hosted", "linux", "x64"},
		RunnerID: 12, RunnerName: "byo-runner-12",
		Status: "completed",
	}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLPlain())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	found := false
	for _, e := range f.observer.emits {
		if e.Signal == "self_hosted_runner_unlabeled" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected self_hosted_runner_unlabeled emit, got %v", f.observer.emits)
	}
}

func TestGHDetector_Signals_CIEnvVarNames(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1003, WorkflowID: 200, HeadBranch: "main",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1003, []canJob{{ID: 5003, RunID: 1003, Name: "build", RunnerID: 99}})
	// Workflow YAML with `env:` containing both a name and a secret-shaped value.
	// The detector MUST capture only the NAME, never the value (§5.2).
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", strings.Join([]string{
		"name: ci",
		"on: push",
		"env:",
		"  ANTHROPIC_API_KEY: sk-test-leakedkey1234567890abcd",
		"  REGULAR_VAR: regularvalue",
		"jobs:",
		"  build:",
		"    runs-on: ubuntu-latest",
		"    steps:",
		"      - run: echo hi",
	}, "\n"))

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	if len(findings) == 0 {
		t.Fatalf("expected env-var-name signal, got 0 findings")
	}
	for _, fnd := range findings {
		if strings.Contains(fnd.EvidenceSummary, "sk-test-leakedkey") {
			t.Fatalf("secret value leaked into EvidenceSummary: %q", fnd.EvidenceSummary)
		}
		if strings.Contains(fnd.EvidenceSummary, "regularvalue") {
			t.Fatalf("env-var value leaked into EvidenceSummary: %q", fnd.EvidenceSummary)
		}
	}
}

func TestGHDetector_Signals_AgentConfigFilePresent(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1004, WorkflowID: 200, HeadBranch: "main", HeadSHA: "sha4",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1004, []canJob{{ID: 5004, RunID: 1004, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "sha4", ".github/workflows/ci.yml", workflowYAMLPlain())
	// .claude/settings.json — agent config file present in repo.
	f.registerWorkflowYAML("acme", "web", "sha4", ".claude/settings.json", `{"mcpServers":{"local":{"command":"./bin/mcp"}}}`)
	f.registerNotFound("acme", "web", ".cursor/config.toml")
	f.registerNotFound("acme", "web", "AGENTS.md")

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	if len(findings) == 0 {
		t.Fatalf("expected agent-config-present signal, got 0 findings")
	}
	hit := false
	for _, fnd := range findings {
		if !strings.Contains(fnd.EvidenceSummary, "agent_config_paths=github://acme/web/.claude/settings.json") {
			t.Fatalf("agent config path not redacted/persisted as safe metadata: %q", fnd.EvidenceSummary)
		}
		if strings.Contains(fnd.EvidenceSummary, "./bin/mcp") {
			t.Fatalf("agent config command leaked into EvidenceSummary: %q", fnd.EvidenceSummary)
		}
		for _, s := range fnd.SignalSet {
			if s == "agent_config_present" {
				hit = true
			}
		}
	}
	if !hit {
		t.Fatalf("no finding contained signal=agent_config_present; got %+v", findings)
	}
}

func TestGHDetector_Signals_MissingCordumAttach(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1005, WorkflowID: 200, HeadBranch: "main", HeadSHA: "sha5",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1005, []canJob{{ID: 5005, RunID: 1005, Name: "build", RunnerID: 99}})
	// Workflow uses anthropic-ai/claude-code-action but does NOT include cordum/cordum-edge-attach
	f.registerWorkflowYAML("acme", "web", "sha5", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	hit := false
	for _, e := range f.observer.emits {
		if e.Signal == "missing_cordum_attach" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected missing_cordum_attach signal, got emits=%+v", f.observer.emits)
	}
}

func TestGHDetector_Signals_DirectProviderEndpoint(t *testing.T) {
	hostHits := []string{
		"api.anthropic.com",
		"https://api.openai.com/v1/responses?api_key=sk-leakedkey1234567890abcd",
	}
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
		JobLogHostHits: func(_ context.Context, org, repo string, runID int64) ([]string, error) {
			if org == "acme" && repo == "web" && runID == 1006 {
				return hostHits, nil
			}
			return nil, nil
		},
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1006, WorkflowID: 200, HeadBranch: "main",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1006, []canJob{{ID: 5006, RunID: 1006, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLPlain())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	hit := false
	for _, e := range f.observer.emits {
		if e.Signal == "direct_provider_endpoint" {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected direct_provider_endpoint emit; got %+v", f.observer.emits)
	}
	findings := f.listAll(t, testTenantA)
	if len(findings) == 0 {
		t.Fatalf("expected persisted direct-provider finding")
	}
	summary := findings[0].EvidenceSummary
	if !strings.Contains(summary, "provider_hosts=api.anthropic.com,api.openai.com") {
		t.Fatalf("provider host metadata missing or unsorted: %q", summary)
	}
	if strings.Contains(summary, "api_key=") || strings.Contains(summary, "sk-leaked") {
		t.Fatalf("query string or token leaked into EvidenceSummary: %q", summary)
	}
}

func TestGHDetector_Signals_PaginatesWorkflowRuns(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRunPages("acme", "web", [][]canRun{
		{{
			ID: 1018, WorkflowID: 200, HeadBranch: "main",
			Actor:      ghActor{Login: "alice", Type: "User"},
			Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
			HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		}},
		{{
			ID: 1019, WorkflowID: 201, HeadBranch: "main",
			Actor:      ghActor{Login: "bob", Type: "User"},
			Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
			HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		}},
	})
	f.registerJobs("acme", "web", 1018, []canJob{{ID: 5018, RunID: 1018, Name: "build", RunnerID: 99}})
	f.registerJobs("acme", "web", 1019, []canJob{{ID: 5019, RunID: 1019, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2 (one per paginated run): %+v", len(findings), findings)
	}
}

// === §6.3 tenant mapping precedence + Q6 OIDC defaults ===

func TestGHDetector_OIDC_DefaultTrustRoot(t *testing.T) {
	// Q6: when env var unset, default issuer is the GH Actions OIDC root.
	t.Setenv("CORDUM_EDGE_SHADOW_OIDC_TRUST_github", "")
	t.Setenv("CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_github", "cordum-edge")
	cfg, err := ghdetector.LoadOIDCConfigFromEnv(ghdetector.Config{})
	if err != nil {
		t.Fatalf("LoadOIDCConfigFromEnv: %v", err)
	}
	if cfg.OIDCIssuer != defaultIssuer {
		t.Errorf("OIDCIssuer = %q, want default %q", cfg.OIDCIssuer, defaultIssuer)
	}
	if cfg.OIDCDisabled {
		t.Errorf("OIDCDisabled = true, want false when env var unset")
	}
	if cfg.OIDCAudience != "cordum-edge" {
		t.Errorf("OIDCAudience = %q, want %q", cfg.OIDCAudience, "cordum-edge")
	}
}

func TestGHDetector_OIDC_OperatorOverride(t *testing.T) {
	t.Setenv("CORDUM_EDGE_SHADOW_OIDC_TRUST_github", "https://oidc.internal.example.com")
	t.Setenv("CORDUM_EDGE_SHADOW_OIDC_AUDIENCE_github", "internal-aud")
	cfg, err := ghdetector.LoadOIDCConfigFromEnv(ghdetector.Config{})
	if err != nil {
		t.Fatalf("LoadOIDCConfigFromEnv: %v", err)
	}
	if cfg.OIDCIssuer != "https://oidc.internal.example.com" {
		t.Errorf("OIDCIssuer = %q, want operator override", cfg.OIDCIssuer)
	}
	if cfg.OIDCDisabled {
		t.Errorf("OIDCDisabled = true, want false for valid override")
	}
}

func TestGHDetector_OIDC_Disabled(t *testing.T) {
	t.Setenv("CORDUM_EDGE_SHADOW_OIDC_TRUST_github", "disabled")
	cfg, err := ghdetector.LoadOIDCConfigFromEnv(ghdetector.Config{})
	if err != nil {
		t.Fatalf("LoadOIDCConfigFromEnv: %v", err)
	}
	if !cfg.OIDCDisabled {
		t.Errorf("OIDCDisabled = false, want true when env var = 'disabled'")
	}
}

func TestGHDetector_OIDC_VerifiedClaimsPrecedeOrgRepo(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap: map[string]map[string]string{
			"acme": {"web": testTenantA},
		},
		OIDCClaimsProvider: func(context.Context, *gogithub.WorkflowRun) (*ghdetector.OIDCClaims, error) {
			return &ghdetector.OIDCClaims{
				Subject:  "repo:acme/web:ref:refs/heads/main",
				Repo:     "acme/web",
				Actor:    "oidc-alice",
				Issuer:   defaultIssuer,
				Audience: "cordum-edge",
			}, nil
		},
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1016, WorkflowID: 200, HeadBranch: "main",
		Actor:      ghActor{Login: "workflow-alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1016, []canJob{{ID: 5016, RunID: 1016, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	if len(findings) == 0 {
		t.Fatalf("expected OIDC-mapped finding")
	}
	got := findings[0]
	if got.TenantSource != ghdetector.TenantSourceOIDC {
		t.Fatalf("TenantSource = %q, want %q", got.TenantSource, ghdetector.TenantSourceOIDC)
	}
	if got.PrincipalSource != ghdetector.PrincipalSourceOIDCSubject {
		t.Fatalf("PrincipalSource = %q, want %q", got.PrincipalSource, ghdetector.PrincipalSourceOIDCSubject)
	}
	if got.PrincipalID != "repo:acme/web:ref:refs/heads/main" {
		t.Fatalf("PrincipalID = %q, want OIDC subject", got.PrincipalID)
	}
	if len(f.observer.oidcOutcomes) != 1 || f.observer.oidcOutcomes[0] != "ok" {
		t.Fatalf("OIDC outcomes = %+v, want [ok]", f.observer.oidcOutcomes)
	}
}

func TestGHDetector_OIDC_DisabledFallsBackOrgRepo(t *testing.T) {
	calls := 0
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
		OIDCClaimsProvider: func(context.Context, *gogithub.WorkflowRun) (*ghdetector.OIDCClaims, error) {
			calls++
			return nil, nil
		},
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1017, WorkflowID: 200, HeadBranch: "main",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1017, []canJob{{ID: 5017, RunID: 1017, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if calls != 0 {
		t.Fatalf("OIDC provider called %d times while disabled, want 0", calls)
	}
	findings := f.listAll(t, testTenantA)
	if len(findings) == 0 {
		t.Fatalf("expected org/repo mapped finding")
	}
	if got := findings[0].TenantSource; got != ghdetector.TenantSourceOrgRepoMap {
		t.Fatalf("TenantSource = %q, want %q", got, ghdetector.TenantSourceOrgRepoMap)
	}
}

func TestGHDetector_TenantMapping_OIDCThenOrgRepo(t *testing.T) {
	resolver := ghdetector.NewDefaultResolver(ghdetector.Config{
		OrgRepoMap: map[string]map[string]string{
			"acme": {"web": testTenantA, "api": testTenantB},
		},
		QuarantineTenantID: testQuarantineTen,
	})
	t.Run("tier1_oidc_claim", func(t *testing.T) {
		// OIDC claim subject identifies repo:<org>/<repo>:ref:<ref>
		claims := &ghdetector.OIDCClaims{
			Subject: "repo:acme/web:ref:refs/heads/main",
			Repo:    "acme/web",
			Ref:     "main",
			Actor:   "alice",
		}
		tenant, source := resolver.ResolveTenant(context.Background(), claims, nil, nil)
		if tenant != testTenantA {
			t.Errorf("tenant = %q, want %q", tenant, testTenantA)
		}
		if source != ghdetector.TenantSourceOIDC {
			t.Errorf("source = %q, want %q", source, ghdetector.TenantSourceOIDC)
		}
	})
	t.Run("tier2_org_repo_map", func(t *testing.T) {
		// No OIDC claim; fall through to org/repo map.
		repo := &gogithub.Repository{
			FullName: ptr("acme/api"),
			Owner:    &gogithub.User{Login: ptr("acme")},
			Name:     ptr("api"),
		}
		tenant, source := resolver.ResolveTenant(context.Background(), nil, nil, repo)
		if tenant != testTenantB {
			t.Errorf("tenant = %q, want %q", tenant, testTenantB)
		}
		if source != ghdetector.TenantSourceOrgRepoMap {
			t.Errorf("source = %q, want %q", source, ghdetector.TenantSourceOrgRepoMap)
		}
	})
	t.Run("tier3_quarantine", func(t *testing.T) {
		repo := &gogithub.Repository{
			FullName: ptr("unknown/repo"),
			Owner:    &gogithub.User{Login: ptr("unknown")},
			Name:     ptr("repo"),
		}
		tenant, source := resolver.ResolveTenant(context.Background(), nil, nil, repo)
		if tenant != testQuarantineTen {
			t.Errorf("tenant = %q, want %q", tenant, testQuarantineTen)
		}
		if source != ghdetector.TenantSourceQuarantine {
			t.Errorf("source = %q, want %q", source, ghdetector.TenantSourceQuarantine)
		}
	})
}

// === §14 false-positive controls ===

func TestGHDetector_FP_EphemeralRunner(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1007, WorkflowID: 200, HeadBranch: "main",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	// ephemeral runner — label "ephemeral" present
	f.registerJobs("acme", "web", 1007, []canJob{{
		ID: 5007, RunID: 1007, Name: "build",
		Labels:   []string{"self-hosted", "ephemeral"},
		RunnerID: 13, RunnerName: "ephem-runner-13",
	}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLPlain())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, e := range f.observer.emits {
		if e.Signal == "self_hosted_runner_unlabeled" {
			t.Fatalf("ephemeral runner promoted to self_hosted_runner_unlabeled; want filtered")
		}
	}
}

func TestGHDetector_FP_Fork(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1008, WorkflowID: 200, HeadBranch: "feature",
		Event:      "pull_request",
		Actor:      ghActor{Login: "stranger", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "stranger/web-fork", Fork: true, Owner: ghOwn{Login: "stranger"}},
	}})
	f.registerJobs("acme", "web", 1008, []canJob{{ID: 5008, RunID: 1008, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	// Fork runs route to quarantine tenant with false_positive_reason=fork_pr_ephemeral
	findings := f.listAll(t, testQuarantineTen)
	if len(findings) == 0 {
		t.Fatalf("expected fork PR to route to quarantine tenant, got 0 findings")
	}
	if findings[0].FalsePositiveReason != "fork_pr_ephemeral" {
		t.Errorf("FalsePositiveReason = %q, want %q", findings[0].FalsePositiveReason, "fork_pr_ephemeral")
	}
}

func TestGHDetector_FP_Scheduled(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1009, WorkflowID: 200, HeadBranch: "main", Event: "schedule",
		Actor:      ghActor{Login: "github-actions[bot]", Type: "Bot"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1009, []canJob{{ID: 5009, RunID: 1009, Name: "nightly", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	for _, fnd := range findings {
		if fnd.FalsePositiveReason == "" {
			t.Fatalf("scheduled run missing FalsePositiveReason; want 'scheduled': %+v", fnd)
		}
	}
}

func TestGHDetector_FP_Dependabot(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1010, WorkflowID: 200, HeadBranch: "dependabot/npm/lodash",
		Event:      "pull_request",
		Actor:      ghActor{Login: "dependabot[bot]", Type: "Bot"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1010, []canJob{{ID: 5010, RunID: 1010, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	hitFP := false
	for _, fnd := range findings {
		if fnd.FalsePositiveReason == "automation_bot" {
			hitFP = true
		}
	}
	if !hitFP && len(findings) > 0 {
		t.Errorf("dependabot run findings missing automation_bot FP tag; got %+v", findings)
	}
}

func TestGHDetector_FP_TestFixture(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:       map[string]map[string]string{"acme": {"web": testTenantA}},
		TestFixtureRepos: map[string]bool{"acme/web": true},
		OIDCDisabled:     true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1011, WorkflowID: 200, HeadBranch: "main",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1011, []canJob{{ID: 5011, RunID: 1011, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	for _, fnd := range findings {
		if fnd.FalsePositiveReason != "test_fixture" {
			t.Errorf("expected FalsePositiveReason=test_fixture; got %q on finding %s", fnd.FalsePositiveReason, fnd.FindingID)
		}
	}
}

func TestGHDetector_FP_DevSandbox(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:      map[string]map[string]string{"acme": {"web": testTenantA}},
		DevSandboxRepos: map[string]bool{"acme/web": true},
		OIDCDisabled:    true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1012, WorkflowID: 200, HeadBranch: "main",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1012, []canJob{{ID: 5012, RunID: 1012, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	for _, fnd := range findings {
		if fnd.FalsePositiveReason != "dev_sandbox" {
			t.Errorf("expected FalsePositiveReason=dev_sandbox; got %q on finding %s", fnd.FalsePositiveReason, fnd.FindingID)
		}
	}
}

// === data minimization ===

func TestGHDetector_DataMinimization_NoSecretValues(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1013, WorkflowID: 200, HeadBranch: "main",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1013, []canJob{{ID: 5013, RunID: 1013, Name: "build", RunnerID: 99}})
	// Canary: 'sk-leakedkey1234567890abcd' MUST NEVER appear in any persisted field.
	canary := "sk-leakedkey1234567890abcd"
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", strings.Join([]string{
		"name: ci",
		"on: push",
		"env:",
		"  API_TOKEN: " + canary,
		"jobs:",
		"  build:",
		"    runs-on: ubuntu-latest",
		"    steps:",
		"      - uses: anthropic-ai/claude-code-action@v1",
	}, "\n"))

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	for _, fnd := range findings {
		bytes, _ := json.Marshal(fnd)
		if strings.Contains(string(bytes), canary) {
			t.Fatalf("canary %q leaked into persisted finding: %s", canary, string(bytes))
		}
	}
}

// === emit typed §10.1 CI fields ===

func TestGHDetector_Emit_TypedFields(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1014, WorkflowID: 250, HeadBranch: "main", HeadSHA: "sha14",
		Path: ".github/workflows/release.yml", Event: "push",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		RunNumber:  7,
	}})
	f.registerJobs("acme", "web", 1014, []canJob{{
		ID: 5014, RunID: 1014, Name: "release",
		RunnerID: 31, RunnerName: "gha-runner-31",
	}})
	f.registerWorkflowYAML("acme", "web", "sha14", ".github/workflows/release.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	findings := f.listAll(t, testTenantA)
	if len(findings) == 0 {
		t.Fatalf("expected ≥1 finding; got 0")
	}
	got := findings[0]
	if got.SourceType != shadow.SourceTypeCI {
		t.Errorf("SourceType = %q, want %q", got.SourceType, shadow.SourceTypeCI)
	}
	if got.CIProvider != shadow.CIProviderGitHubActions {
		t.Errorf("CIProvider = %q, want %q", got.CIProvider, shadow.CIProviderGitHubActions)
	}
	if got.Repo != "acme/web" {
		t.Errorf("Repo = %q, want %q", got.Repo, "acme/web")
	}
	if got.WorkflowID != "250" {
		t.Errorf("WorkflowID = %q, want %q", got.WorkflowID, "250")
	}
	if got.RunID != "1014" {
		t.Errorf("RunID = %q, want %q", got.RunID, "1014")
	}
	if got.JobID == "" {
		t.Errorf("JobID empty; want non-empty")
	}
	if got.RunnerID == "" {
		t.Errorf("RunnerID empty; want non-empty")
	}
	if len(got.SignalSet) == 0 {
		t.Errorf("SignalSet empty; want at least one entry")
	}
	if got.RetentionClass != shadow.ShadowRetentionDefault {
		t.Errorf("RetentionClass = %q, want %q", got.RetentionClass, shadow.ShadowRetentionDefault)
	}
	if got.TenantSource == "" {
		t.Errorf("TenantSource empty; want §6.3 tier label")
	}
	if !strings.HasPrefix(got.SourceID, "github_actions:") {
		t.Errorf("SourceID = %q, want prefix %q", got.SourceID, "github_actions:")
	}
}

// === observability ===

func TestGHDetector_Observability(t *testing.T) {
	cfg := ghdetector.Config{
		OrgRepoMap:   map[string]map[string]string{"acme": {"web": testTenantA}},
		OIDCDisabled: true,
	}
	f := newFixture(t, cfg)
	f.registerRuns("acme", "web", []canRun{{
		ID: 1015, WorkflowID: 200, HeadBranch: "main",
		Actor:      ghActor{Login: "alice", Type: "User"},
		Repository: ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
		HeadRepo:   ghRepo{Name: "web", FullName: "acme/web", Owner: ghOwn{Login: "acme"}},
	}})
	f.registerJobs("acme", "web", 1015, []canJob{{ID: 5015, RunID: 1015, Name: "build", RunnerID: 99}})
	f.registerWorkflowYAML("acme", "web", "", ".github/workflows/ci.yml", workflowYAMLWithAgentNoAttach())

	if err := f.detector.Scan(context.Background()); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(f.observer.emits) == 0 {
		t.Fatalf("RecordFindingEmit never called; want ≥1")
	}
	if len(f.observer.audits) == 0 {
		t.Fatalf("EmitAudit never called; want ≥1")
	}
	if got := f.observer.audits[0].Decision; got != "observed" {
		t.Errorf("audit.Decision = %q, want %q", got, "observed")
	}
	if f.observer.audits[0].EventType != "edge.shadow_finding_created" {
		t.Errorf("audit.EventType = %q, want %q", f.observer.audits[0].EventType, "edge.shadow_finding_created")
	}
	if got := f.observer.audits[0].Extra["source_type"]; got != "github_actions" {
		t.Errorf("audit source_type = %q, want github_actions", got)
	}
}

func TestGHDetector_Observability_PrometheusObserver(t *testing.T) {
	reg := prometheus.NewRegistry()
	audits := newSpyObserver()
	obs := ghdetector.NewPrometheusObserver(reg, audits)

	obs.RecordFindingEmit("missing_cordum_attach", "high")
	obs.OIDCVerifyOutcome("ok")
	obs.RateLimitRemaining(4999)
	obs.EmitAudit(audit.SIEMEvent{EventType: "edge.shadow_finding_created"})

	requireMetricValue(t, reg, "cordum_edge_shadow_finding_emit_total", map[string]string{
		"source_type": "github_actions",
		"signal":      "missing_cordum_attach",
		"risk":        "high",
	}, 1)
	requireMetricValue(t, reg, "cordum_edge_shadow_oidc_verify_total", map[string]string{
		"provider": "github_actions",
		"result":   "ok",
	}, 1)
	requireMetricValue(t, reg, "cordum_edge_shadow_gh_rate_limit_remaining", map[string]string{
		"provider": "github_actions",
	}, 4999)
	if len(audits.audits) != 1 {
		t.Fatalf("audit forwarding count = %d, want 1", len(audits.audits))
	}
}

// --- helpers ---

func ptr[T interface{}](v T) *T { return &v }

func requireMetricValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string, want float64) {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, fam := range families {
		if fam.GetName() != name {
			continue
		}
		for _, metric := range fam.GetMetric() {
			if metricHasLabels(metric.GetLabel(), labels) {
				got := metric.GetGauge().GetValue()
				if metric.GetCounter() != nil {
					got = metric.GetCounter().GetValue()
				}
				if got != want {
					t.Fatalf("%s%v = %v, want %v", name, labels, got, want)
				}
				return
			}
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, labels)
}

func metricHasLabels(pairs []*dto.LabelPair, want map[string]string) bool {
	if len(pairs) != len(want) {
		return false
	}
	for _, pair := range pairs {
		if want[pair.GetName()] != pair.GetValue() {
			return false
		}
	}
	return true
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func pathBase(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}

func base64Encode(s string) string {
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b := []byte(s)
	var out []byte
	for len(b) >= 3 {
		out = append(out,
			tbl[b[0]>>2],
			tbl[((b[0]&0x03)<<4)|(b[1]>>4)],
			tbl[((b[1]&0x0f)<<2)|(b[2]>>6)],
			tbl[b[2]&0x3f])
		b = b[3:]
	}
	switch len(b) {
	case 1:
		out = append(out, tbl[b[0]>>2], tbl[(b[0]&0x03)<<4], '=', '=')
	case 2:
		out = append(out, tbl[b[0]>>2], tbl[((b[0]&0x03)<<4)|(b[1]>>4)], tbl[(b[1]&0x0f)<<2], '=')
	}
	return string(out)
}

func workflowYAMLPlain() string {
	return strings.Join([]string{
		"name: ci",
		"on: push",
		"jobs:",
		"  build:",
		"    runs-on: ubuntu-latest",
		"    steps:",
		"      - uses: actions/checkout@v4",
		"      - run: echo hello",
	}, "\n")
}

func workflowYAMLWithAgentNoAttach() string {
	return strings.Join([]string{
		"name: ci",
		"on: push",
		"jobs:",
		"  build:",
		"    runs-on: ubuntu-latest",
		"    steps:",
		"      - uses: actions/checkout@v4",
		"      - uses: anthropic-ai/claude-code-action@v1",
		"      - run: claude --prompt 'do work'",
	}, "\n")
}
