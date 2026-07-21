package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/topicregistry"
	"github.com/cordum/cordum/core/controlplane/workercredentials"
	"github.com/cordum/cordum/core/infra/buildinfo"
	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/schema"
	"github.com/cordum/cordum/core/infra/store"
	wf "github.com/cordum/cordum/core/workflow"
	"github.com/redis/go-redis/v9"
)

const (
	defaultCollectInterval = 4 * time.Hour
	defaultReportInterval  = 24 * time.Hour
	collectorLockTTL       = 60 * time.Second
)

var telemetryStartupNotice sync.Once

var errCollectorLockNotAcquired = errors.New("telemetry collector lock not acquired")

type CollectorStatus struct {
	Mode            Mode       `json:"mode"`
	Endpoint        string     `json:"endpoint,omitempty"`
	LastCollectedAt *time.Time `json:"last_collected_at,omitempty"`
	LastReportedAt  *time.Time `json:"last_reported_at,omitempty"`
}

type CollectorOptions struct {
	Mode              Mode
	Store             *Store
	Reporter          *Reporter
	TierProvider      func() string
	JobStore          *store.RedisJobStore
	WorkflowStore     *wf.RedisStore
	ConfigSvc         *configsvc.Service
	SchemaRegistry    *schema.Registry
	TopicRegistry     *topicregistry.Service
	WorkerCredentials *workercredentials.Service
	TenantID          string
	Logger            *slog.Logger
	CollectInterval   time.Duration
	ReportInterval    time.Duration
}

// Collector periodically captures privacy-safe usage telemetry and optionally
// reports it when anonymous reporting is enabled.
type Collector struct {
	mu                sync.RWMutex
	mode              Mode
	store             *Store
	reporter          *Reporter
	tierProvider      func() string
	jobStore          *store.RedisJobStore
	workflowStore     *wf.RedisStore
	configSvc         *configsvc.Service
	schemaRegistry    *schema.Registry
	topicRegistry     *topicregistry.Service
	workerCredentials *workercredentials.Service
	tenantID          string
	logger            *slog.Logger
	collectInterval   time.Duration
	reportInterval    time.Duration

	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started sync.Once
	stopped sync.Once
}

func NewCollector(opts CollectorOptions) *Collector {
	mode := NormalizeMode(string(opts.Mode))
	if opts.Store == nil {
		return &Collector{mode: mode}
	}
	if opts.Reporter == nil {
		opts.Reporter = NewReporter("", nil)
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.CollectInterval <= 0 {
		opts.CollectInterval = defaultCollectInterval
	}
	if opts.ReportInterval <= 0 {
		opts.ReportInterval = defaultReportInterval
	}
	return &Collector{
		mode:              mode,
		store:             opts.Store,
		reporter:          opts.Reporter,
		tierProvider:      opts.TierProvider,
		jobStore:          opts.JobStore,
		workflowStore:     opts.WorkflowStore,
		configSvc:         opts.ConfigSvc,
		schemaRegistry:    opts.SchemaRegistry,
		topicRegistry:     opts.TopicRegistry,
		workerCredentials: opts.WorkerCredentials,
		tenantID:          strings.TrimSpace(opts.TenantID),
		logger:            opts.Logger,
		collectInterval:   opts.CollectInterval,
		reportInterval:    opts.ReportInterval,
	}
}

// Mode returns the current telemetry mode.
func (c *Collector) Mode() Mode {
	if c == nil {
		return ModeOff
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mode
}

// SetMode changes the telemetry mode at runtime. The new mode takes effect
// on the next collection/report cycle without requiring a restart.
func (c *Collector) SetMode(mode Mode) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mode = NormalizeMode(string(mode))
}

func (c *Collector) currentMode() Mode {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mode
}

func (c *Collector) Start(parent context.Context) {
	if c == nil {
		return
	}
	telemetryStartupNotice.Do(func() {
		c.logger.Info("Cordum telemetry: unless CORDUM_TELEMETRY_MODE=off, Cordum sends two one-time anonymous pings ever — an install ping on first startup (install_id, schema_version, version, tier, mode, os, arch) and a first-use ping after the first job/workflow completes (the same fields plus worker counts, jobs_24h, workflows_24h, packs_installed). Set CORDUM_TELEMETRY_MODE=anonymous to also enable ongoing 24h reporting, or =off to disable all telemetry including the one-time pings.", "mode", c.currentMode())
	})
	if !c.currentMode().Enabled() || c.store == nil {
		return
	}
	c.started.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		c.cancel = cancel
		c.wg.Add(1)
		go c.loop(ctx)
	})
}

func (c *Collector) Stop() {
	if c == nil {
		return
	}
	c.stopped.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		c.wg.Wait()
	})
}

func (c *Collector) Close() error {
	if c == nil {
		return nil
	}
	c.Stop()
	if c.store != nil {
		return c.store.Close()
	}
	return nil
}

func (c *Collector) Status(ctx context.Context) (*CollectorStatus, error) {
	mode := c.currentMode()
	status := &CollectorStatus{
		Mode: mode,
	}
	if c.reporter != nil && mode.ReportingEnabled() {
		status.Endpoint = c.reporter.Endpoint()
	}
	if c.store == nil {
		return status, nil
	}
	payload, err := c.store.InspectPayload(ctx)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		collectedAt := payload.CollectedAt.UTC()
		status.LastCollectedAt = &collectedAt
	}
	report, err := c.store.LastReportStatus(ctx)
	if err != nil {
		return nil, err
	}
	if report != nil {
		reportedAt := report.ReportedAt.UTC()
		status.LastReportedAt = &reportedAt
		if status.Endpoint == "" {
			status.Endpoint = report.Endpoint
		}
	}
	return status, nil
}

func (c *Collector) InspectPayload(ctx context.Context) (*TelemetryPayload, error) {
	if c == nil || c.store == nil {
		return nil, nil
	}
	return c.store.InspectPayload(ctx)
}

func (c *Collector) ExportPayload(ctx context.Context) ([]byte, error) {
	if c == nil || c.store == nil {
		return nil, nil
	}
	return c.store.ExportPayload(ctx)
}

func (c *Collector) Usage(ctx context.Context) (map[string]any, error) {
	payload, err := c.InspectPayload(ctx)
	if err != nil || payload == nil {
		return map[string]any{}, err
	}
	return map[string]any{
		"workers":          payload.Workers,
		"usage":            payload.Usage,
		"features_enabled": payload.FeaturesEnabled,
		"engagement":       payload.Engagement,
		"limits_hit":       payload.LimitsHit,
	}, nil
}

func (c *Collector) CollectNow(ctx context.Context) (*TelemetryPayload, error) {
	if c == nil || c.store == nil {
		return nil, fmt.Errorf("telemetry collector unavailable")
	}
	if !c.currentMode().Enabled() {
		return nil, nil
	}
	unlock, err := c.acquireLock(ctx)
	if err != nil {
		if errors.Is(err, errCollectorLockNotAcquired) {
			return nil, nil
		}
		return nil, err
	}
	defer unlock()

	payload, err := c.buildPayload(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.store.SaveSnapshot(ctx, payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *Collector) ReportNow(ctx context.Context) error {
	if c == nil || c.store == nil || c.reporter == nil || !c.currentMode().ReportingEnabled() {
		return nil
	}
	unlock, err := c.acquireLock(ctx)
	if err != nil {
		if errors.Is(err, errCollectorLockNotAcquired) {
			return nil
		}
		return err
	}
	defer unlock()

	payload, err := c.store.InspectPayload(ctx)
	if err != nil {
		return err
	}
	if payload == nil {
		generated, err := c.buildPayload(ctx)
		if err != nil {
			return err
		}
		if err := c.store.SaveSnapshot(ctx, generated); err != nil {
			return err
		}
		payload = &generated
	}
	if err := c.reporter.Report(ctx, *payload); err != nil {
		c.logger.Debug("telemetry report failed", "error", err)
		return nil
	}
	if err := c.store.SaveReportStatus(ctx, ReportStatus{
		ReportedAt: time.Now().UTC(),
		Endpoint:   c.reporter.Endpoint(),
		Success:    true,
	}); err != nil {
		return err
	}
	return nil
}

func (c *Collector) loop(ctx context.Context) {
	defer c.wg.Done()

	// One-time install ping fires on collector start in any mode != off.
	c.maybeSendInstallPing(ctx)

	_, _ = c.CollectNow(ctx)
	// One-time first-use ping is checked after each collect tick.
	c.maybeSendFirstUsePing(ctx)
	if c.currentMode().ReportingEnabled() {
		_ = c.ReportNow(ctx)
	}

	collectTicker := time.NewTicker(c.collectInterval)
	defer collectTicker.Stop()
	reportTicker := time.NewTicker(c.reportInterval)
	defer reportTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-collectTicker.C:
			if _, err := c.CollectNow(ctx); err != nil && !errors.Is(err, redis.Nil) && !errors.Is(err, errCollectorLockNotAcquired) {
				c.logger.Debug("telemetry collect failed", "error", err)
			}
			c.maybeSendFirstUsePing(ctx)
		case <-reportTicker.C:
			if err := c.ReportNow(ctx); err != nil && !errors.Is(err, redis.Nil) && !errors.Is(err, errCollectorLockNotAcquired) {
				c.logger.Debug("telemetry report loop failed", "error", err)
			}
		}
	}
}

// maybeSendInstallPing sends the one-time minimal install beacon if telemetry
// is not off and the install ping has not already fired. The pinged flag is
// only set after a successful send (via SetNX), so a failed send retries on
// the next collector start. Concurrent collectors/replicas converge via SetNX.
func (c *Collector) maybeSendInstallPing(ctx context.Context) {
	if c == nil || c.reporter == nil {
		return
	}
	if c.currentMode() == ModeOff {
		return
	}
	client := c.redisClient()
	if client == nil {
		return
	}
	// Claim the ping with SetNX BEFORE sending so concurrent collectors/replicas
	// converge to exactly one send. If the send fails, release the claim so it
	// retries on the next start.
	won, err := markPinged(ctx, client, installPingedKey)
	if err != nil {
		c.logger.Debug("telemetry install ping flag claim failed", "error", err)
		return
	}
	if !won {
		return
	}

	installID, err := GetInstallID(ctx, client)
	if err != nil {
		c.logger.Debug("telemetry install ping install id failed", "error", err)
		c.releasePingFlag(ctx, client, installPingedKey)
		return
	}
	payload := NewInstallBeacon(installID, c.currentMode(), buildinfo.Version, c.currentTier())
	if err := c.reporter.Report(ctx, payload); err != nil {
		c.logger.Debug("telemetry install ping failed", "error", err)
		c.releasePingFlag(ctx, client, installPingedKey)
		return
	}
	c.persistPingFlag(ctx, client, installPingedKey)
}

// releasePingFlag clears a one-time ping flag after a failed send so the ping
// retries on the next opportunity. Best-effort.
func (c *Collector) releasePingFlag(ctx context.Context, client redis.UniversalClient, key string) {
	if client == nil {
		return
	}
	relCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := client.Del(relCtx, key).Err(); err != nil {
		c.logger.Debug("telemetry ping flag release failed", "key", key, "error", err)
	}
}

// persistPingFlag promotes a claimed (TTL'd) ping flag to permanent after a
// confirmed send, so the ping never fires again. Best-effort.
func (c *Collector) persistPingFlag(ctx context.Context, client redis.UniversalClient, key string) {
	if client == nil {
		return
	}
	pCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := persistPinged(pCtx, client, key); err != nil {
		c.logger.Debug("telemetry ping flag persist failed", "key", key, "error", err)
	}
}

// maybeSendFirstUsePing sends the one-time fuller first-use beacon if telemetry
// is not off, the used marker is set (a job or workflow has completed), and the
// first-use ping has not already fired. The pinged flag is only set after a
// successful send (via SetNX), so a failed send retries on the next collect
// tick. Concurrent collectors/replicas converge via SetNX.
func (c *Collector) maybeSendFirstUsePing(ctx context.Context) {
	if c == nil || c.reporter == nil {
		return
	}
	if c.currentMode() == ModeOff {
		return
	}
	client := c.redisClient()
	if client == nil {
		return
	}
	alreadyPinged, err := keyExists(ctx, client, firstUsePingedKey)
	if err != nil {
		c.logger.Debug("telemetry first-use ping flag check failed", "error", err)
		return
	}
	if alreadyPinged {
		return
	}
	used, err := keyExists(ctx, client, UsedMarkerKey)
	if err != nil {
		c.logger.Debug("telemetry first-use used marker check failed", "error", err)
		return
	}
	if !used {
		return
	}

	// Claim the ping with SetNX BEFORE sending so concurrent collectors/replicas
	// converge to exactly one send. Release the claim if the send fails.
	won, err := markPinged(ctx, client, firstUsePingedKey)
	if err != nil {
		c.logger.Debug("telemetry first-use ping flag claim failed", "error", err)
		return
	}
	if !won {
		return
	}

	installID, err := GetInstallID(ctx, client)
	if err != nil {
		c.logger.Debug("telemetry first-use ping install id failed", "error", err)
		c.releasePingFlag(ctx, client, firstUsePingedKey)
		return
	}
	registeredWorkers, err := c.registeredWorkerCount(ctx)
	if err != nil {
		c.logger.Debug("telemetry first-use worker count failed", "error", err)
		c.releasePingFlag(ctx, client, firstUsePingedKey)
		return
	}
	connectedWorkers, err := c.connectedWorkerCount(ctx)
	if err != nil {
		c.logger.Debug("telemetry first-use connected worker count failed", "error", err)
		c.releasePingFlag(ctx, client, firstUsePingedKey)
		return
	}
	jobsLast24h, err := c.jobsLast24h(ctx)
	if err != nil {
		c.logger.Debug("telemetry first-use jobs count failed", "error", err)
		c.releasePingFlag(ctx, client, firstUsePingedKey)
		return
	}
	workflowRunsLast24h, err := c.workflowRunsLast24h(ctx)
	if err != nil {
		c.logger.Debug("telemetry first-use workflow count failed", "error", err)
		c.releasePingFlag(ctx, client, firstUsePingedKey)
		return
	}
	packsInstalled, err := c.packCount(ctx)
	if err != nil {
		c.logger.Debug("telemetry first-use pack count failed", "error", err)
		c.releasePingFlag(ctx, client, firstUsePingedKey)
		return
	}

	payload := NewFirstUseBeacon(installID, c.currentMode(), buildinfo.Version, c.currentTier(),
		registeredWorkers, connectedWorkers, jobsLast24h, workflowRunsLast24h, packsInstalled)
	if err := c.reporter.Report(ctx, payload); err != nil {
		c.logger.Debug("telemetry first-use ping failed", "error", err)
		c.releasePingFlag(ctx, client, firstUsePingedKey)
		return
	}
	c.persistPingFlag(ctx, client, firstUsePingedKey)
}

func (c *Collector) buildPayload(ctx context.Context) (TelemetryPayload, error) {
	installID, err := GetInstallID(ctx, c.redisClient())
	if err != nil {
		return TelemetryPayload{}, err
	}

	registeredWorkers, err := c.registeredWorkerCount(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}
	connectedWorkers, err := c.connectedWorkerCount(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}
	activeJobs, err := c.activeJobCount(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}
	activeWorkflowRuns, err := c.activeWorkflowCount(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}
	jobsLast24h, err := c.jobsLast24h(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}
	workflowRunsLast24h, err := c.workflowRunsLast24h(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}
	schemaCount, err := c.schemaCount(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}
	policyBundleCount, err := c.policyBundleCount(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}
	topicCount, err := c.topicCount(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}
	workflowCount, err := c.workflowCount(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}
	packCount, err := c.packCount(ctx)
	if err != nil {
		return TelemetryPayload{}, err
	}

	builder := NewPayloadBuilder().
		WithCollectedAt(time.Now().UTC()).
		WithInstallID(installID).
		WithMode(c.currentMode()).
		WithVersion(buildinfo.Version).
		WithTier(c.currentTier()).
		WithWorkers(registeredWorkers, connectedWorkers).
		WithUsage(UsageSignals{
			ActiveJobs:          activeJobs,
			ActiveWorkflowRuns:  activeWorkflowRuns,
			JobsLast24h:         jobsLast24h,
			WorkflowRunsLast24h: workflowRunsLast24h,
			Schemas:             schemaCount,
			PolicyBundles:       policyBundleCount,
		}).
		WithEngagement(EngagementSignals{
			TopicsConfigured:    topicCount,
			WorkflowsConfigured: workflowCount,
			PacksInstalled:      packCount,
			UserAuthEnabled:     env.Bool("CORDUM_USER_AUTH_ENABLED"),
			OIDCEnabled:         strings.TrimSpace(os.Getenv("CORDUM_OIDC_ISSUER")) != "",
			OutputPolicyEnabled: env.Bool("OUTPUT_POLICY_ENABLED"),
		})

	builder.
		WithFeature("user_auth", env.Bool("CORDUM_USER_AUTH_ENABLED")).
		WithFeature("oidc", strings.TrimSpace(os.Getenv("CORDUM_OIDC_ISSUER")) != "").
		WithFeature("output_policy", env.Bool("OUTPUT_POLICY_ENABLED")).
		WithFeature("schemas", schemaCount > 0).
		WithFeature("policy_bundles", policyBundleCount > 0).
		WithFeature("topics", topicCount > 0).
		WithFeature("workflows", workflowCount > 0).
		WithFeature("packs", packCount > 0)

	if registeredWorkers > 0 && connectedWorkers == 0 {
		builder.WithLimitHit("workers_disconnected", int64(registeredWorkers))
	}
	return builder.Build(), nil
}

func (c *Collector) acquireLock(ctx context.Context) (func(), error) {
	client := c.redisClient()
	if client == nil {
		return func() {}, nil
	}
	token, err := newInstallID()
	if err != nil {
		return nil, err
	}
	ok, err := client.SetNX(ctx, collectorLockKey, token, collectorLockTTL).Result()
	if err != nil {
		return nil, fmt.Errorf("telemetry collector lock: %w", err)
	}
	if !ok {
		return nil, errCollectorLockNotAcquired
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		current, err := client.Get(releaseCtx, collectorLockKey).Result()
		if err == nil && current == token {
			_ = client.Del(releaseCtx, collectorLockKey).Err()
		}
	}, nil
}

func (c *Collector) redisClient() redis.UniversalClient {
	if c == nil || c.store == nil {
		return nil
	}
	return c.store.client
}

func (c *Collector) currentTier() string {
	if c != nil && c.tierProvider != nil {
		if tier := strings.TrimSpace(c.tierProvider()); tier != "" {
			return tier
		}
	}
	return "community"
}

func (c *Collector) registeredWorkerCount(ctx context.Context) (int, error) {
	if c == nil || c.workerCredentials == nil {
		return 0, nil
	}
	records, err := c.workerCredentials.List(ctx, c.tenantID)
	if err != nil {
		return 0, fmt.Errorf("telemetry list worker credentials: %w", err)
	}
	count := 0
	for _, record := range records {
		if !record.Revoked() {
			count++
		}
	}
	return count, nil
}

func (c *Collector) connectedWorkerCount(ctx context.Context) (int, error) {
	client := c.redisClient()
	if client == nil {
		return 0, nil
	}
	count, err := client.SCard(ctx, "cordum:workers:count").Result()
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("telemetry count connected workers: %w", err)
	}
	return int(count), nil
}

func (c *Collector) activeJobCount(ctx context.Context) (int, error) {
	if c == nil || c.jobStore == nil || c.tenantID == "" {
		return 0, nil
	}
	return c.jobStore.CountActiveByTenant(ctx, c.tenantID)
}

func (c *Collector) activeWorkflowCount(ctx context.Context) (int, error) {
	if c == nil || c.workflowStore == nil || c.tenantID == "" {
		return 0, nil
	}
	return c.workflowStore.CountActiveRuns(ctx, c.tenantID)
}

func (c *Collector) jobsLast24h(ctx context.Context) (int64, error) {
	if c == nil || c.jobStore == nil {
		return 0, nil
	}
	return c.jobStore.CountRecentJobsSince(ctx, time.Now().UTC().Add(-24*time.Hour))
}

func (c *Collector) workflowRunsLast24h(ctx context.Context) (int64, error) {
	if c == nil || c.workflowStore == nil {
		return 0, nil
	}
	return c.workflowStore.CountRunsSince(ctx, time.Now().UTC().Add(-24*time.Hour))
}

func (c *Collector) schemaCount(ctx context.Context) (int, error) {
	if c == nil || c.schemaRegistry == nil {
		return 0, nil
	}
	ids, err := c.schemaRegistry.List(ctx, 1000)
	if err != nil {
		return 0, fmt.Errorf("telemetry count schemas: %w", err)
	}
	return len(ids), nil
}

func (c *Collector) topicCount(ctx context.Context) (int, error) {
	if c == nil || c.topicRegistry == nil {
		return 0, nil
	}
	snapshot, err := c.topicRegistry.List(ctx)
	if err != nil {
		return 0, fmt.Errorf("telemetry count topics: %w", err)
	}
	return len(snapshot.Items), nil
}

func (c *Collector) workflowCount(ctx context.Context) (int64, error) {
	if c == nil || c.workflowStore == nil {
		return 0, nil
	}
	return c.workflowStore.CountWorkflows(ctx, "")
}

func (c *Collector) packCount(ctx context.Context) (int, error) {
	if c == nil || c.configSvc == nil {
		return 0, nil
	}
	doc, err := c.configSvc.Get(ctx, configsvc.ScopeSystem, "packs")
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("telemetry count packs: %w", err)
	}
	installed, _ := doc.Data["installed"].(map[string]any)
	return len(installed), nil
}

func (c *Collector) policyBundleCount(ctx context.Context) (int, error) {
	if c == nil || c.configSvc == nil {
		return 0, nil
	}
	doc, err := c.configSvc.Get(ctx, configsvc.ScopeSystem, "policy")
	if err == redis.Nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("telemetry count policy bundles: %w", err)
	}
	bundles, _ := doc.Data["bundles"].(map[string]any)
	return len(bundles), nil
}
