package gateway

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// cordum_audit_export_total counts compliance-export HTTP requests by
// the format they requested and the terminal outcome (ok / forbidden /
// bad_request / unavailable / error). SRE / oncall uses this to spot
// a surge in 403s (entitlement drift) or errors (Redis flaps) and to
// size the export workload for capacity planning.
var auditExportCounter = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "cordum_audit_export_total",
		Help: "Compliance audit-export requests by format and status.",
	},
	[]string{"format", "status"},
)

// cordum_audit_export_events observes the number of events emitted per
// successful export so operators can spot a tenant whose exports
// routinely hit the MaxEvents cap (a signal that the query window or
// retention window needs tightening).
var auditExportEventsHistogram = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name: "cordum_audit_export_events",
		Help: "Events emitted per compliance export, bucketed.",
		// Buckets span 0 through the Enterprise cap. The 1k/10k
		// bucket catches the Team plan's default ceiling; 100k and
		// 1M catch enterprise workloads.
		Buckets: []float64{0, 100, 1000, 10_000, 100_000, 1_000_000},
	},
	[]string{"format"},
)

// init binds observeAuditExport (the placeholder declared in
// handlers_audit_compliance.go) to the real Prometheus counters.
// Using init keeps the wiring co-located with the metric definitions
// while avoiding a circular dependency between the handler file and
// the metrics file.
func init() {
	observeAuditExport = func(format, status string, eventCount int) {
		if format == "" {
			format = "unknown"
		}
		if status == "" {
			status = "unknown"
		}
		auditExportCounter.WithLabelValues(format, status).Inc()
		// Histograms only make sense on successful exports — a 403 has
		// no real event count. Still observe 0 on success with empty
		// window so the count/histogram cardinalities match for
		// alerting maths.
		if status == "ok" {
			auditExportEventsHistogram.WithLabelValues(format).Observe(float64(eventCount))
		}
	}
}
