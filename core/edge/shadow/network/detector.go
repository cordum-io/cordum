// Package network implements the observe-only network-signal aggregator
// specified in cordum/docs/edge/kubernetes-ci-shadow-detector-design.md
// §9. The detector ingests operator-supplied egress / proxy logs and
// emits ShadowAgentFinding records for traffic to direct LLM-provider
// endpoints (Anthropic / OpenAI / Google API hosts) coming from sources
// that lack a Cordum Edge attach.
//
// Cordum NEVER captures network traffic itself (NG7). The detector
// reads log records the operator has already produced — file paths,
// syslog endpoints, or stdin streams. No raw sockets, no TLS
// termination, no pcap / eBPF, no admission-side packet inspection.
// TestNetworkDetector_Ingest_NoCaptureNG7 grep-asserts the absence of
// banned APIs across this package.
//
// Persisted fields follow the §9.1 lawful-metadata catalog: hostname,
// category, count_bucket, workload identity (post-PII), endpoint hash.
// Defense-in-depth enforceCatalog strips anything outside the catalog
// at the boundary so a misbehaving ingestor cannot leak full URLs, IPs,
// or bearer tokens into the persisted finding.
//
// Principal-id pseudonymization is controlled by
// `CORDUM_EDGE_SHADOW_PII_MODE=pseudonymize|hash|drop` per the
// governor's binding Q2 ruling on parent task task-de50a293
// (comment-a17f4f1c). Default mode is pseudonymize (first 3 chars of
// the workload id + 8-char SHA-256 hex suffix). hash truncates to a
// SHA-256 prefix; drop emits a fixed sentinel.
package network

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow"
)

// Config is the operator-tunable configuration for the network
// aggregator. A zero-value Config plus nil resolver yields a usable
// detector once fillDefaults runs — every field below has a documented
// fallback so unit tests can construct minimal fixtures.
type Config struct {
	// SourceID is the human-readable operator-assigned identifier for
	// this log feed. Persisted in finding.SourceID as
	// "network:<SourceID>" so SIEM consumers can disambiguate multiple
	// network detectors per cluster (e.g. one per egress log file).
	SourceID string

	// QuarantineTenantID is the terminal-fallback tenant used when
	// §6.1 precedence (workload-identity → OIDC) yields no mapping.
	// Defaults to "cordum.shadow.quarantine".
	QuarantineTenantID string

	// HeartbeatStaleThreshold is the §9.3 risk-classification threshold:
	// a workload whose last KnownAttachWorkloadIDs timestamp is older
	// than this is treated as having stale Cordum Edge attach and the
	// finding is escalated to risk=high. Defaults to 5 minutes.
	HeartbeatStaleThreshold time.Duration

	// ProviderHostnames is the closed map of operator-supplied
	// hostnames classified into provider categories. Hostnames not
	// present in this map are silently skipped (observe-only). Empty
	// map falls back to DefaultProviderHostnames().
	ProviderHostnames map[string]string

	// ProxyAllowlist is the operator-supplied set of proxy hostnames
	// whose traffic should be treated as "managed by Cordum" and
	// therefore not flagged. Currently logged for telemetry only —
	// reserved for §9.4 proxy-aware risk reduction in EDGE-143.9.
	ProxyAllowlist []string

	// PIIMode selects the GDPR Q2 principal_id treatment. Empty falls
	// back to the CORDUM_EDGE_SHADOW_PII_MODE env value, then to
	// pseudonymize. Invalid value returns an error at NewDetector.
	PIIMode PIIMode

	// WorkloadTenantMap maps workload-identity tokens (the workload=
	// field on each log line) to the owning tenant_id. §6.1 highest
	// precedence.
	WorkloadTenantMap map[string]string

	// OIDCTenantMap maps OIDC subject claims (the oidc_sub= field) to
	// the owning tenant_id. §6.1 second precedence.
	OIDCTenantMap map[string]string

	// KnownAttachWorkloadIDs records the last-known timestamp at which
	// the workload reported Cordum Edge attach. Missing key → no
	// attach record (risk=medium). Stale entry (older than
	// HeartbeatStaleThreshold) → stale attach (risk=high). Operator
	// supplies this via the existing Edge heartbeat pipeline.
	KnownAttachWorkloadIDs map[string]time.Time
}

// DefaultProviderHostnames returns the §9.1 closed set of direct
// LLM-provider hostnames mapped to provider categories. Operators
// extend this via Config.ProviderHostnames; the defaults cover the
// three providers called out in the design doc.
func DefaultProviderHostnames() map[string]string {
	return map[string]string{
		"api.anthropic.com":                 "anthropic_api",
		"api.openai.com":                    "openai_api",
		"generativelanguage.googleapis.com": "google_api",
	}
}

// fillDefaults populates empty fields with safe fallbacks. Called
// once in NewDetector; subsequent mutation of the stored Config is
// not supported (the Detector copies the value).
func (c *Config) fillDefaults() error {
	mode, err := resolvePIIMode(c.PIIMode)
	if err != nil {
		return err
	}
	c.PIIMode = mode
	if c.QuarantineTenantID == "" {
		c.QuarantineTenantID = defaultQuarantineTenant
	}
	if c.HeartbeatStaleThreshold <= 0 {
		c.HeartbeatStaleThreshold = 5 * time.Minute
	}
	if c.SourceID == "" {
		c.SourceID = "network-detector"
	}
	if len(c.ProviderHostnames) == 0 {
		c.ProviderHostnames = DefaultProviderHostnames()
	}
	c.ProviderHostnames = cloneStringMap(c.ProviderHostnames)
	c.WorkloadTenantMap = cloneStringMap(c.WorkloadTenantMap)
	c.OIDCTenantMap = cloneStringMap(c.OIDCTenantMap)
	c.KnownAttachWorkloadIDs = cloneTimeMap(c.KnownAttachWorkloadIDs)
	return nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTimeMap(in map[string]time.Time) map[string]time.Time {
	if in == nil {
		return nil
	}
	out := make(map[string]time.Time, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// defaultQuarantineTenant mirrors k8s.QuarantineTenant so cross-
// detector reports converge on the same fallback bucket. Hard-coded
// here rather than imported because importing the k8s package would
// pull the entire kubernetes client into network/'s dependency graph.
const defaultQuarantineTenant = "cordum.shadow.quarantine"

// LogRecord is a single ingested log line normalized into the §9.1
// lawful-metadata catalog (plus the three forbidden fields RawURL /
// BearerToken / RemoteIP that the ingestor MAY observe but the
// detector MUST drop before persistence). enforceCatalog zeroes the
// forbidden fields at the boundary so no later code path can re-leak
// them.
type LogRecord struct {
	Timestamp    time.Time
	Hostname     string
	WorkloadID   string
	OIDCSub      string
	EndpointHash string
	Count        int

	// Forbidden by §9.1 catalog but allowed at the ingestor boundary
	// so a careless operator log line can be safely normalized. These
	// are zeroed by enforceCatalog before any persistence call —
	// defense in depth so a future code path can never accidentally
	// thread them into a finding.
	RawURL      string
	BearerToken string
	RemoteIP    string
}

// LogIngestor produces LogRecord values from an operator-supplied log
// source. Implementations MUST be observe-only — file ingestors read,
// syslog ingestors listen on read-only UDP/TCP, stdin ingestors
// consume an io.Reader. None install hooks, terminate TLS, or open
// raw sockets. Stream returns when ctx is canceled or the underlying
// source is exhausted (EOF); the returned error is ctx.Err() on
// cancel and the underlying I/O error otherwise.
type LogIngestor interface {
	Stream(ctx context.Context, out chan<- LogRecord) error
	SourceLabel() string
}

// Observer is the metrics + audit emission contract the network
// detector depends on. Production wiring backs this with prometheus
// counters + the shared audit.AuditSender; tests substitute a spy.
// All four methods MUST be safe for concurrent invocation.
type Observer interface {
	// RecordFindingEmit fires once per persisted finding with bounded
	// labels: signal is always "direct_provider_traffic"; risk is one
	// of low|medium|high|critical; ingestSource is one of file|
	// syslog|stdin (matches LogIngestor.SourceLabel). Hostname /
	// category / tenant / workload are NEVER labels — those live in
	// the finding payload + audit event.
	RecordFindingEmit(signal, risk, ingestSource string)

	// RecordPIIModeActive surfaces the active PII mode as a gauge
	// label. Called once at NewDetector so dashboards see the mode
	// even when no findings have emitted yet.
	RecordPIIModeActive(mode string)

	// RecordLogRecord fires per ingested record with bounded labels:
	// ingestSource (file|syslog|stdin), result (emitted|
	// skipped_unknown_host|store_error|parse_error|catalog_dropped).
	RecordLogRecord(ingestSource, result string)

	// EmitAudit forwards the per-finding shadow_agent.observed event
	// to the shared audit pipeline.
	EmitAudit(event audit.SIEMEvent)
}

// NoopObserver is the safe default when callers have not wired
// observability yet. Every method is a no-op.
type NoopObserver struct{}

// NewNoopObserver returns a NoopObserver value as an Observer.
func NewNoopObserver() Observer { return NoopObserver{} }

// RecordFindingEmit satisfies Observer.
func (NoopObserver) RecordFindingEmit(string, string, string) {}

// RecordPIIModeActive satisfies Observer.
func (NoopObserver) RecordPIIModeActive(string) {}

// RecordLogRecord satisfies Observer.
func (NoopObserver) RecordLogRecord(string, string) {}

// EmitAudit satisfies Observer.
func (NoopObserver) EmitAudit(audit.SIEMEvent) {}

// Detector orchestrates ingest → parse → catalog-enforce → tenant
// resolve → PII apply → risk classify → emit. Safe for concurrent
// ProcessRecord calls; clock + observer access is mutex-guarded.
type Detector struct {
	cfg      Config
	store    shadow.Store
	observer Observer
	resolver TenantResolver
	idGen    func() string

	mu    sync.Mutex
	clock func() time.Time
}

// NewDetector wires the detector. resolver=nil uses the §6.1/§6.2
// default precedence chain (Config.WorkloadTenantMap →
// Config.OIDCTenantMap → Config.QuarantineTenantID). idGen=nil leaves
// finding-ID generation to the underlying shadow.Store. Returns an
// error when the resolved PIIMode is invalid (mis-configured env var
// or explicit Config value).
func NewDetector(cfg Config, store shadow.Store, observer Observer, resolver TenantResolver, idGen func() string) (*Detector, error) {
	if store == nil {
		return nil, errors.New("network: shadow.Store is required")
	}
	if err := cfg.fillDefaults(); err != nil {
		return nil, err
	}
	if observer == nil {
		observer = NewNoopObserver()
	}
	if resolver == nil {
		resolver = newDefaultResolver(cfg)
	}
	d := &Detector{
		cfg:      cfg,
		store:    store,
		observer: observer,
		resolver: resolver,
		idGen:    idGen,
		clock:    time.Now,
	}
	observer.RecordPIIModeActive(string(cfg.PIIMode))
	return d, nil
}

// SetClock overrides the time source. Used by tests to pin
// DetectedAt / first_seen / last_seen.
func (d *Detector) SetClock(fn func() time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clock = fn
}

// IngestFrom consumes a LogIngestor until ctx is canceled or the
// ingestor's source is exhausted. Each yielded LogRecord is passed
// through ProcessRecord with the ingestor's SourceLabel as the
// metric tag. Errors from individual records are observed but not
// returned — the observe-mode contract forbids killing the ingestion
// loop on per-record persistence failures.
func (d *Detector) IngestFrom(ctx context.Context, ing LogIngestor) error {
	if ing == nil {
		return errors.New("network: nil LogIngestor")
	}
	ch := make(chan LogRecord, 32)
	errCh := make(chan error, 1)
	source := ing.SourceLabel()

	go func() {
		defer close(ch)
		errCh <- ing.Stream(ctx, ch)
	}()

	for {
		select {
		case <-ctx.Done():
			<-errCh
			return ctx.Err()
		case rec, ok := <-ch:
			if !ok {
				err := <-errCh
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return nil
				}
				return err
			}
			if err := d.ProcessRecord(ctx, rec, source); err != nil {
				d.observer.RecordLogRecord(source, "process_error")
			}
		}
	}
}

// ProcessRecord normalizes a single record through the §9 pipeline.
// Returns nil for skipped records (unknown hostname, empty hostname);
// returns the store error otherwise. The store error is observed via
// RecordLogRecord{result:"store_error"} and is intentionally NOT
// fatal to the calling ingestion loop.
func (d *Detector) ProcessRecord(ctx context.Context, rec LogRecord, ingestSource string) error {
	d.mu.Lock()
	clock := d.clock
	d.mu.Unlock()
	now := clock()

	if rec.Hostname == "" {
		d.observer.RecordLogRecord(ingestSource, "skipped_no_hostname")
		return nil
	}
	category, ok := d.cfg.ProviderHostnames[rec.Hostname]
	if !ok {
		d.observer.RecordLogRecord(ingestSource, "skipped_unknown_host")
		return nil
	}

	// §9.1 catalog enforcement — drop banned fields before any later
	// code path can read them. Defense in depth: rec is now safe to
	// thread through resolver / risk-classify / emit without risk of
	// leaking the forbidden inputs.
	rec = enforceCatalog(rec)

	tenantID, tenantSource := d.resolver.ResolveTenant(ctx, rec)
	if tenantID == "" {
		tenantID = d.cfg.QuarantineTenantID
		tenantSource = TenantSourceQuarantine
	}
	rawPrincipal, principalSource := d.resolver.ResolvePrincipal(ctx, rec)
	principalID := applyPIIMode(rawPrincipal, d.cfg.PIIMode)

	risk := classifyRisk(tenantSource, rec, d.cfg.KnownAttachWorkloadIDs, now, d.cfg.HeartbeatStaleThreshold)

	bucket := countBucket(rec.Count)
	summary := fmt.Sprintf("hostname=%s category=%s count_bucket=%d", rec.Hostname, category, bucket)

	req := shadow.CreateFindingRequest{
		TenantID:         tenantID,
		OwnerPrincipalID: principalID,
		PrincipalID:      principalID,
		AgentProduct:     networkAgentProduct,
		Risk:             risk,
		EvidenceType:     evidenceTypeDirectProvider,
		EvidenceSummary:  summary,
		Hostname:         rec.Hostname,
		RedactedPath:     shadow.RedactPath("network://" + category + "/" + rec.EndpointHash),
		DetectedAt:       now,

		SourceType:      shadow.SourceTypeNetwork,
		SourceID:        "network:" + d.cfg.SourceID,
		TenantSource:    tenantSource,
		PrincipalSource: principalSource,
		SignalSet:       []string{signalDirectProviderTraffic},
		FirstSeen:       ptrTime(now),
		LastSeen:        ptrTime(now),
		RetentionClass:  shadow.ShadowRetentionDefault,
	}
	if d.idGen != nil {
		req.FindingID = d.idGen()
	}

	finding, err := d.store.CreateFinding(ctx, req)
	if err != nil || finding == nil {
		d.observer.RecordLogRecord(ingestSource, "store_error")
		return err
	}

	d.observer.RecordFindingEmit(signalDirectProviderTraffic, string(risk), ingestSource)
	d.observer.RecordLogRecord(ingestSource, "emitted")
	d.observer.EmitAudit(audit.SIEMEvent{
		Timestamp: now,
		EventType: "edge.shadow_finding_created",
		Severity:  severityForRisk(risk),
		TenantID:  tenantID,
		Action:    "shadow_agent.observed",
		Decision:  "observed",
		Extra: map[string]string{
			"finding_id":    finding.FindingID,
			"source_type":   shadow.SourceTypeNetwork,
			"signal":        signalDirectProviderTraffic,
			"hostname":      rec.Hostname,
			"category":      category,
			"count_bucket":  strconv.Itoa(bucket),
			"tenant_id":     tenantID,
			"tenant_src":    tenantSource,
			"principal_src": principalSource,
			"risk":          string(risk),
		},
	})
	return nil
}

// Stable string constants used across the package.
const (
	networkAgentProduct         = "network_direct_provider"
	evidenceTypeDirectProvider  = "network_direct_provider_traffic"
	signalDirectProviderTraffic = "direct_provider_traffic"
)

func ptrTime(t time.Time) *time.Time { return &t }

func severityForRisk(r shadow.FindingRisk) string {
	switch r {
	case shadow.FindingRiskHigh, shadow.FindingRiskCritical:
		return "HIGH"
	case shadow.FindingRiskMedium:
		return "MEDIUM"
	default:
		return "INFO"
	}
}
