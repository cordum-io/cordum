package telemetry

import (
	"runtime"
	"time"
)

const payloadSchemaVersion = "2026-04-telemetry-v1"

// Telemetry event types. Every payload carries exactly one of these in its
// Event field so the telemetry server can cleanly separate install pings,
// first-use activations, and ongoing periodic reports.
const (
	// EventInstall is the one-time minimal beacon sent on collector start.
	EventInstall = "install"
	// EventFirstUse is the one-time fuller beacon sent after the first job or
	// workflow completes.
	EventFirstUse = "first_use"
	// EventPeriodic is the ongoing anonymous 24h report (anonymous mode only).
	EventPeriodic = "periodic"
)

// TelemetryPayload is the anonymous medium-signal document persisted locally
// and optionally reported to Cordum telemetry.
type TelemetryPayload struct {
	SchemaVersion   string            `json:"schema_version"`
	Event           string            `json:"event"`
	CollectedAt     time.Time         `json:"collected_at"`
	InstallID       string            `json:"install_id"`
	Mode            Mode              `json:"mode"`
	Version         string            `json:"version"`
	Tier            string            `json:"tier"`
	OS              string            `json:"os,omitempty"`
	Arch            string            `json:"arch,omitempty"`
	Workers         WorkerSignals     `json:"workers"`
	Usage           UsageSignals      `json:"usage"`
	FeaturesEnabled map[string]bool   `json:"features_enabled,omitempty"`
	Engagement      EngagementSignals `json:"engagement"`
	LimitsHit       map[string]int64  `json:"limits_hit,omitempty"`
}

type WorkerSignals struct {
	Registered int `json:"registered"`
	Connected  int `json:"connected"`
}

type UsageSignals struct {
	ActiveJobs          int   `json:"active_jobs"`
	ActiveWorkflowRuns  int   `json:"active_workflow_runs"`
	JobsLast24h         int64 `json:"jobs_last_24h"`
	WorkflowRunsLast24h int64 `json:"workflow_runs_last_24h"`
	Schemas             int   `json:"schemas"`
	PolicyBundles       int   `json:"policy_bundles"`
}

type EngagementSignals struct {
	TopicsConfigured    int   `json:"topics_configured"`
	WorkflowsConfigured int64 `json:"workflows_configured"`
	PacksInstalled      int   `json:"packs_installed"`
	UserAuthEnabled     bool  `json:"user_auth_enabled"`
	OIDCEnabled         bool  `json:"oidc_enabled"`
	OutputPolicyEnabled bool  `json:"output_policy_enabled"`
}

// PayloadBuilder incrementally constructs a telemetry payload.
type PayloadBuilder struct {
	payload TelemetryPayload
}

func NewPayloadBuilder() *PayloadBuilder {
	return &PayloadBuilder{
		payload: TelemetryPayload{
			SchemaVersion:   payloadSchemaVersion,
			FeaturesEnabled: map[string]bool{},
			LimitsHit:       map[string]int64{},
		},
	}
}

func (b *PayloadBuilder) WithEvent(event string) *PayloadBuilder {
	if b != nil {
		b.payload.Event = event
	}
	return b
}

func (b *PayloadBuilder) WithOS(os string) *PayloadBuilder {
	if b != nil {
		b.payload.OS = os
	}
	return b
}

func (b *PayloadBuilder) WithArch(arch string) *PayloadBuilder {
	if b != nil {
		b.payload.Arch = arch
	}
	return b
}

// WithPlatform records the running operating system and architecture from the
// Go runtime (GOOS/GOARCH).
func (b *PayloadBuilder) WithPlatform() *PayloadBuilder {
	if b != nil {
		b.payload.OS = runtime.GOOS
		b.payload.Arch = runtime.GOARCH
	}
	return b
}

func (b *PayloadBuilder) WithCollectedAt(collectedAt time.Time) *PayloadBuilder {
	if b != nil {
		b.payload.CollectedAt = collectedAt.UTC()
	}
	return b
}

func (b *PayloadBuilder) WithInstallID(installID string) *PayloadBuilder {
	if b != nil {
		b.payload.InstallID = installID
	}
	return b
}

func (b *PayloadBuilder) WithMode(mode Mode) *PayloadBuilder {
	if b != nil {
		b.payload.Mode = NormalizeMode(string(mode))
	}
	return b
}

func (b *PayloadBuilder) WithVersion(version string) *PayloadBuilder {
	if b != nil {
		b.payload.Version = version
	}
	return b
}

func (b *PayloadBuilder) WithTier(tier string) *PayloadBuilder {
	if b != nil {
		b.payload.Tier = tier
	}
	return b
}

func (b *PayloadBuilder) WithWorkers(registered, connected int) *PayloadBuilder {
	if b != nil {
		b.payload.Workers = WorkerSignals{Registered: registered, Connected: connected}
	}
	return b
}

func (b *PayloadBuilder) WithUsage(usage UsageSignals) *PayloadBuilder {
	if b != nil {
		b.payload.Usage = usage
	}
	return b
}

func (b *PayloadBuilder) WithEngagement(engagement EngagementSignals) *PayloadBuilder {
	if b != nil {
		b.payload.Engagement = engagement
	}
	return b
}

func (b *PayloadBuilder) WithFeature(name string, enabled bool) *PayloadBuilder {
	if b != nil && name != "" {
		if b.payload.FeaturesEnabled == nil {
			b.payload.FeaturesEnabled = map[string]bool{}
		}
		b.payload.FeaturesEnabled[name] = enabled
	}
	return b
}

func (b *PayloadBuilder) WithLimitHit(name string, count int64) *PayloadBuilder {
	if b != nil && name != "" && count > 0 {
		if b.payload.LimitsHit == nil {
			b.payload.LimitsHit = map[string]int64{}
		}
		b.payload.LimitsHit[name] = count
	}
	return b
}

func (b *PayloadBuilder) Build() TelemetryPayload {
	if b == nil {
		return NewPayloadBuilder().Build()
	}
	payload := b.payload
	if payload.SchemaVersion == "" {
		payload.SchemaVersion = payloadSchemaVersion
	}
	if payload.Event == "" {
		payload.Event = EventPeriodic
	}
	if payload.CollectedAt.IsZero() {
		payload.CollectedAt = time.Now().UTC()
	}
	if payload.FeaturesEnabled == nil {
		payload.FeaturesEnabled = map[string]bool{}
	}
	if payload.LimitsHit == nil {
		payload.LimitsHit = map[string]int64{}
	}
	return payload
}

// NewInstallBeacon builds the minimal one-time install beacon described in the
// telemetry spec: install_id, event=install, schema_version, cordum_version,
// tier, mode, os, arch. It carries no usage counts, feature flags, or
// engagement signals.
func NewInstallBeacon(installID string, mode Mode, version, tier string) TelemetryPayload {
	return NewPayloadBuilder().
		WithEvent(EventInstall).
		WithCollectedAt(time.Now().UTC()).
		WithInstallID(installID).
		WithMode(mode).
		WithVersion(version).
		WithTier(tier).
		WithPlatform().
		Build()
}

// NewFirstUseBeacon builds the fuller one-time first-use beacon: the minimal
// install beacon plus worker counts, jobs_24h, workflows_24h, and
// packs_installed. It still omits per-feature detail and the engagement object.
func NewFirstUseBeacon(installID string, mode Mode, version, tier string, registeredWorkers, connectedWorkers int, jobsLast24h, workflowRunsLast24h int64, packsInstalled int) TelemetryPayload {
	return NewPayloadBuilder().
		WithEvent(EventFirstUse).
		WithCollectedAt(time.Now().UTC()).
		WithInstallID(installID).
		WithMode(mode).
		WithVersion(version).
		WithTier(tier).
		WithPlatform().
		WithWorkers(registeredWorkers, connectedWorkers).
		WithUsage(UsageSignals{
			JobsLast24h:         jobsLast24h,
			WorkflowRunsLast24h: workflowRunsLast24h,
		}).
		WithEngagement(EngagementSignals{
			PacksInstalled: packsInstalled,
		}).
		Build()
}
