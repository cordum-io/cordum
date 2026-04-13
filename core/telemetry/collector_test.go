package telemetry

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/model"
	wf "github.com/cordum/cordum/core/workflow"
)

type telemetryTestEnv struct {
	store      *Store
	collector  *Collector
	reporter   *Reporter
	configSvc  *configsvc.Service
	jobStore   *store.RedisJobStore
	workflow   *wf.RedisStore
	schemas    *schema.Registry
	topics     *topicregistry.Service
	workers    *workercredentials.Service
	redis      *miniredis.Miniredis
	reportHits *int
}

func newTelemetryTestEnv(t *testing.T, mode Mode) *telemetryTestEnv {
	t.Helper()
	t.Setenv("REDIS_POOL_SIZE", "1")
	t.Setenv("REDIS_MIN_IDLE_CONNS", "0")
	t.Setenv("CORDUM_USER_AUTH_ENABLED", "1")
	t.Setenv("OUTPUT_POLICY_ENABLED", "1")
	t.Setenv("CORDUM_OIDC_ISSUER", "https://issuer.example.com")

	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	redisURL := "redis://" + mr.Addr()
	cfgSvc, err := configsvc.New(redisURL)
	if err != nil {
		t.Fatalf("config service: %v", err)
	}
	t.Cleanup(func() { _ = cfgSvc.Close() })

	jobStore, err := store.NewRedisJobStore(redisURL)
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	t.Cleanup(func() { _ = jobStore.Close() })

	workflowStore, err := wf.NewRedisWorkflowStore(redisURL)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	t.Cleanup(func() { _ = workflowStore.Close() })

	schemaRegistry, err := schema.NewRegistry(redisURL)
	if err != nil {
		t.Fatalf("schema registry: %v", err)
	}
	t.Cleanup(func() { _ = schemaRegistry.Close() })

	telemetryStore, err := NewStore(redisURL)
	if err != nil {
		t.Fatalf("telemetry store: %v", err)
	}
	t.Cleanup(func() { _ = telemetryStore.Close() })

	topics := topicregistry.NewService(cfgSvc)
	workers := workercredentials.NewService(cfgSvc)

	ctx := context.Background()
	if _, err := workers.Create(ctx, workercredentials.IssueInput{
		WorkerID:  "worker-1",
		CreatedBy: "test",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create worker credential: %v", err)
	}
	if err := jobStore.Client().SAdd(ctx, "cordum:workers:count", "worker-1").Err(); err != nil {
		t.Fatalf("seed worker count: %v", err)
	}

	for _, jobID := range []string{"job-1", "job-2"} {
		if err := jobStore.SetTenant(ctx, jobID, "default"); err != nil {
			t.Fatalf("set tenant %s: %v", jobID, err)
		}
		if err := jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
			t.Fatalf("set %s pending: %v", jobID, err)
		}
		if err := jobStore.SetState(ctx, jobID, model.JobStateScheduled); err != nil {
			t.Fatalf("set %s scheduled: %v", jobID, err)
		}
	}
	if err := jobStore.SetState(ctx, "job-1", model.JobStateRunning); err != nil {
		t.Fatalf("set job-1 state: %v", err)
	}
	if err := jobStore.SetState(ctx, "job-2", model.JobStateRunning); err != nil {
		t.Fatalf("set job-2 running: %v", err)
	}
	if err := jobStore.SetState(ctx, "job-2", model.JobStateSucceeded); err != nil {
		t.Fatalf("set job-2 state: %v", err)
	}

	if err := workflowStore.SaveWorkflow(ctx, &wf.Workflow{ID: "wf-1", OrgID: "default"}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := workflowStore.CreateRun(ctx, &wf.WorkflowRun{
		ID:         "run-1",
		WorkflowID: "wf-1",
		OrgID:      "default",
		Status:     wf.RunStatusRunning,
	}); err != nil {
		t.Fatalf("create workflow run: %v", err)
	}

	if err := schemaRegistry.Register(ctx, "schema-1", []byte(`{"type":"object"}`)); err != nil {
		t.Fatalf("register schema: %v", err)
	}
	if err := topics.Set(ctx, topicregistry.Registration{Name: "job.demo", Pool: "default", Status: topicregistry.StatusActive}); err != nil {
		t.Fatalf("set topic: %v", err)
	}
	if err := cfgSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "policy",
		Data: map[string]any{
			"bundles": map[string]any{
				"secops/default": map[string]any{"enabled": true},
			},
		},
	}); err != nil {
		t.Fatalf("set policy bundles: %v", err)
	}
	if err := cfgSvc.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "packs",
		Data: map[string]any{
			"installed": map[string]any{
				"demo-pack": map[string]any{"id": "demo-pack"},
			},
		},
	}); err != nil {
		t.Fatalf("set packs: %v", err)
	}

	reportHits := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reportHits++
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(server.Close)

	reporter := NewReporter(server.URL, server.Client())
	reporter.baseBackoff = 1
	collector := NewCollector(CollectorOptions{
		Mode:              mode,
		Store:             telemetryStore,
		Reporter:          reporter,
		TierProvider:      func() string { return "team" },
		JobStore:          jobStore,
		WorkflowStore:     workflowStore,
		ConfigSvc:         cfgSvc,
		SchemaRegistry:    schemaRegistry,
		TopicRegistry:     topics,
		WorkerCredentials: workers,
		TenantID:          "default",
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		CollectInterval:   time.Hour,
		ReportInterval:    time.Hour,
	})

	return &telemetryTestEnv{
		store:      telemetryStore,
		collector:  collector,
		reporter:   reporter,
		configSvc:  cfgSvc,
		jobStore:   jobStore,
		workflow:   workflowStore,
		schemas:    schemaRegistry,
		topics:     topics,
		workers:    workers,
		redis:      mr,
		reportHits: &reportHits,
	}
}

func TestCollectorCollectNowLocalOnlyBuildsPayload(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeLocalOnly)

	payload, err := env.collector.CollectNow(context.Background())
	if err != nil {
		t.Fatalf("CollectNow() error = %v", err)
	}
	if payload == nil {
		t.Fatal("expected payload")
	}
	if payload.Mode != ModeLocalOnly {
		t.Fatalf("payload.Mode = %q, want %q", payload.Mode, ModeLocalOnly)
	}
	if payload.Tier != "team" {
		t.Fatalf("payload.Tier = %q, want team", payload.Tier)
	}
	if payload.Workers.Registered != 1 || payload.Workers.Connected != 1 {
		t.Fatalf("unexpected worker counts: %+v", payload.Workers)
	}
	if payload.Usage.ActiveJobs != 1 {
		t.Fatalf("ActiveJobs = %d, want 1", payload.Usage.ActiveJobs)
	}
	if payload.Usage.ActiveWorkflowRuns != 1 {
		t.Fatalf("ActiveWorkflowRuns = %d, want 1", payload.Usage.ActiveWorkflowRuns)
	}
	if payload.Usage.JobsLast24h < 2 {
		t.Fatalf("JobsLast24h = %d, want at least 2", payload.Usage.JobsLast24h)
	}
	if payload.Usage.WorkflowRunsLast24h < 1 {
		t.Fatalf("WorkflowRunsLast24h = %d, want at least 1", payload.Usage.WorkflowRunsLast24h)
	}
	if !payload.FeaturesEnabled["user_auth"] || !payload.FeaturesEnabled["oidc"] || !payload.FeaturesEnabled["output_policy"] {
		t.Fatalf("expected core features enabled, got %+v", payload.FeaturesEnabled)
	}

	stored, err := env.store.InspectPayload(context.Background())
	if err != nil {
		t.Fatalf("InspectPayload() error = %v", err)
	}
	if stored == nil || stored.InstallID == "" {
		t.Fatalf("expected stored payload with install id, got %+v", stored)
	}
}

func TestCollectorReportNowAnonymousStoresReportStatus(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeAnonymous)

	if _, err := env.collector.CollectNow(context.Background()); err != nil {
		t.Fatalf("CollectNow() error = %v", err)
	}
	if err := env.collector.ReportNow(context.Background()); err != nil {
		t.Fatalf("ReportNow() error = %v", err)
	}
	if *env.reportHits != 1 {
		t.Fatalf("report hits = %d, want 1", *env.reportHits)
	}
	status, err := env.store.LastReportStatus(context.Background())
	if err != nil {
		t.Fatalf("LastReportStatus() error = %v", err)
	}
	if status == nil || !status.Success {
		t.Fatalf("expected successful report status, got %+v", status)
	}
}

func TestCollectorOffDisablesCollection(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeOff)

	payload, err := env.collector.CollectNow(context.Background())
	if err != nil {
		t.Fatalf("CollectNow() error = %v", err)
	}
	if payload != nil {
		t.Fatalf("expected nil payload when off, got %+v", payload)
	}
	stored, err := env.store.InspectPayload(context.Background())
	if err != nil {
		t.Fatalf("InspectPayload() error = %v", err)
	}
	if stored != nil {
		t.Fatalf("expected no stored payload when off, got %+v", stored)
	}
}

func TestCollectorCollectNowSkipsWhenLockHeld(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeLocalOnly)

	if err := env.store.client.Set(context.Background(), collectorLockKey, "other-owner", time.Minute).Err(); err != nil {
		t.Fatalf("seed collector lock: %v", err)
	}
	payload, err := env.collector.CollectNow(context.Background())
	if err != nil {
		t.Fatalf("CollectNow() error = %v", err)
	}
	if payload != nil {
		t.Fatalf("expected nil payload while lock held, got %+v", payload)
	}
	stored, err := env.store.InspectPayload(context.Background())
	if err != nil {
		t.Fatalf("InspectPayload() error = %v", err)
	}
	if stored != nil {
		t.Fatalf("expected no stored payload while lock held, got %+v", stored)
	}
}

func TestCollectorUsageStatusAndExport(t *testing.T) {
	env := newTelemetryTestEnv(t, ModeAnonymous)

	if _, err := env.collector.CollectNow(context.Background()); err != nil {
		t.Fatalf("CollectNow() error = %v", err)
	}
	if err := env.collector.ReportNow(context.Background()); err != nil {
		t.Fatalf("ReportNow() error = %v", err)
	}

	status, err := env.collector.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status == nil || status.LastCollectedAt == nil || status.LastReportedAt == nil {
		t.Fatalf("unexpected status: %+v", status)
	}
	if status.Endpoint == "" {
		t.Fatalf("expected endpoint in status, got %+v", status)
	}

	usage, err := env.collector.Usage(context.Background())
	if err != nil {
		t.Fatalf("Usage() error = %v", err)
	}
	if usage["usage"] == nil || usage["engagement"] == nil {
		t.Fatalf("unexpected usage payload: %+v", usage)
	}

	exported, err := env.collector.ExportPayload(context.Background())
	if err != nil {
		t.Fatalf("ExportPayload() error = %v", err)
	}
	if !strings.Contains(string(exported), `"schema_version"`) {
		t.Fatalf("expected serialized payload export, got %s", string(exported))
	}
}
