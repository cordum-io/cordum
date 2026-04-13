package telemetry

import "time"

const payloadSchemaVersion = "2026-04-telemetry-v1"

// TelemetryPayload is the anonymous medium-signal document persisted locally
// and optionally reported to Cordum telemetry.
type TelemetryPayload struct {
	SchemaVersion   string            `json:"schema_version"`
	CollectedAt     time.Time         `json:"collected_at"`
	InstallID       string            `json:"install_id"`
	Mode            Mode              `json:"mode"`
	Version         string            `json:"version"`
	Tier            string            `json:"tier"`
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
