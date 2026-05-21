package github

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
// metrics plus optional audit forwarding.
type PrometheusObserver struct {
	findingEmit        *prometheus.CounterVec
	oidcVerify         *prometheus.CounterVec
	rateLimitRemaining *prometheus.GaugeVec
	audits             AuditEmitter
}

// NewPrometheusObserver registers GitHub Actions shadow-detector
// metrics. A nil registerer returns the no-op observer used elsewhere
// in this package.
func NewPrometheusObserver(reg prometheus.Registerer, audits AuditEmitter) Observer {
	if reg == nil {
		return nopObserver{}
	}
	o := &PrometheusObserver{
		findingEmit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cordum_edge_shadow_finding_emit_total",
			Help: "Shadow findings emitted, labeled by bounded source type, signal, and risk.",
		}, []string{"source_type", "signal", "risk"}),
		oidcVerify: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cordum_edge_shadow_oidc_verify_total",
			Help: "GitHub Actions OIDC verification outcomes for CI shadow detection.",
		}, []string{"provider", "result"}),
		rateLimitRemaining: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cordum_edge_shadow_gh_rate_limit_remaining",
			Help: "GitHub API rate limit remaining for the GitHub Actions shadow detector.",
		}, []string{"provider"}),
		audits: audits,
	}
	reg.MustRegister(o.findingEmit, o.oidcVerify, o.rateLimitRemaining)
	return o
}

func (o *PrometheusObserver) RecordFindingEmit(signal, risk string) {
	o.findingEmit.WithLabelValues(githubActionsSourceType, boundedSignal(signal), boundedRisk(risk)).Inc()
}

func (o *PrometheusObserver) EmitAudit(event audit.SIEMEvent) {
	if o.audits != nil {
		o.audits.EmitAudit(event)
	}
}

func (o *PrometheusObserver) OIDCVerifyOutcome(result string) {
	o.oidcVerify.WithLabelValues(shadow.CIProviderGitHubActions, boundedOIDCResult(result)).Inc()
}

func (o *PrometheusObserver) RateLimitRemaining(remaining int) {
	if remaining < 0 {
		remaining = 0
	}
	o.rateLimitRemaining.WithLabelValues(shadow.CIProviderGitHubActions).Set(float64(remaining))
}

func boundedSignal(signal string) string {
	switch signal {
	case signalSelfHostedRunner, signalMissingCordumAttach, signalAgentConfigPresent,
		signalEnvVarIndicator, signalDirectProvider, signalAgentActionUsed:
		return signal
	default:
		return "other"
	}
}

func boundedRisk(risk string) string {
	switch risk {
	case string(shadow.FindingRiskLow), string(shadow.FindingRiskMedium),
		string(shadow.FindingRiskHigh), string(shadow.FindingRiskCritical):
		return risk
	default:
		return string(shadow.FindingRiskLow)
	}
}

func boundedOIDCResult(result string) string {
	switch result {
	case "ok", "sig_invalid", "exp", "aud_mismatch":
		return result
	default:
		return "sig_invalid"
	}
}
