package ci

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/edge/shadow"
)

// AuditEmitter is the minimal audit sink used by PrometheusObserver.
// It matches the existing edge audit wrapper without coupling this
// library package to a concrete exporter implementation.
type AuditEmitter interface {
	EmitAudit(event audit.SIEMEvent)
}

// PrometheusObserver implements Observer with bounded-cardinality
// metrics plus optional audit forwarding. Counter / gauge labels are
// clamped to known enum values so a buggy scanner cannot inflate
// cardinality.
type PrometheusObserver struct {
	findingEmit *prometheus.CounterVec
	oidcVerify  *prometheus.CounterVec
	audits      AuditEmitter
}

// NewPrometheusObserver registers the CI shadow-detector metrics. A nil
// registerer returns the no-op observer (same fail-safe as the GitHub
// detector) so test wiring can skip Prometheus entirely.
func NewPrometheusObserver(reg prometheus.Registerer, audits AuditEmitter) Observer {
	if reg == nil {
		return nopObserver{}
	}
	o := &PrometheusObserver{
		findingEmit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cordum_edge_shadow_finding_emit_total",
			Help: "Shadow findings emitted, labeled by bounded CI source type, signal, and risk.",
		}, []string{"source_type", "signal", "risk"}),
		oidcVerify: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cordum_edge_shadow_oidc_verify_total",
			Help: "CI OIDC verification outcomes per provider.",
		}, []string{"provider", "result"}),
		audits: audits,
	}
	reg.MustRegister(o.findingEmit, o.oidcVerify)
	return o
}

// RecordFindingEmit increments the bounded counter. Unknown signal /
// risk values clamp to `other` / `low` so a buggy extractor cannot
// inflate Prometheus cardinality.
func (o *PrometheusObserver) RecordFindingEmit(provider Provider, signal, risk string) {
	o.findingEmit.WithLabelValues(boundedSourceTypeLabel(provider), boundedSignalLabel(signal), boundedRiskLabel(risk)).Inc()
}

func (o *PrometheusObserver) EmitAudit(event audit.SIEMEvent) {
	if o.audits != nil {
		o.audits.EmitAudit(event)
	}
}

func (o *PrometheusObserver) OIDCVerifyOutcome(provider Provider, result string) {
	o.oidcVerify.WithLabelValues(boundedProviderLabel(provider), boundedOIDCResultLabel(result)).Inc()
}

// boundedSourceTypeLabel maps a Provider to the matching
// `shadow.CIProvider*` literal. Unknown providers degrade to "other".
func boundedSourceTypeLabel(p Provider) string {
	switch p {
	case ProviderGitLab:
		return shadow.CIProviderGitLabCI
	case ProviderJenkins:
		return shadow.CIProviderJenkins
	case ProviderBuildkite:
		return shadow.CIProviderBuildkite
	case ProviderCircleCI:
		return shadow.CIProviderCircleCI
	}
	return "other"
}

// boundedProviderLabel is the provider label for the OIDC counter. Same
// mapping as boundedSourceTypeLabel — kept separate so observability
// names remain self-describing per metric.
func boundedProviderLabel(p Provider) string {
	switch p {
	case ProviderGitLab:
		return shadow.CIProviderGitLabCI
	case ProviderJenkins:
		return shadow.CIProviderJenkins
	case ProviderBuildkite:
		return shadow.CIProviderBuildkite
	case ProviderCircleCI:
		return shadow.CIProviderCircleCI
	}
	return "other"
}

func boundedSignalLabel(signal string) string {
	switch signal {
	case signalSelfHostedRunner, signalMissingCordumAttach, signalAgentConfigPresent,
		signalEnvVarIndicator, signalDirectProvider, signalAgentActionUsed:
		return signal
	}
	return "other"
}

func boundedRiskLabel(risk string) string {
	switch risk {
	case string(shadow.FindingRiskLow), string(shadow.FindingRiskMedium),
		string(shadow.FindingRiskHigh), string(shadow.FindingRiskCritical):
		return risk
	}
	return string(shadow.FindingRiskLow)
}

func boundedOIDCResultLabel(result string) string {
	switch result {
	case "ok", "sig_invalid", "exp", "aud_mismatch", "disabled":
		return result
	}
	return "sig_invalid"
}
