package legacyshim

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	dto "github.com/prometheus/client_model/go"
)

// Wire-format constants for the Shape label on legacyPolicyAPICallsTotal.
// ShapeRequestOldResponseOld is the only shape this PR exposes — every
// shimmed call accepts an old request body and emits an old response. Future
// migration phases may introduce ShapeRequestNewResponseOld for clients that
// have moved to the unified request body but still consume legacy responses.
const (
	ShapeRequestOldResponseOld = "request_old_response_old"
)

// legacyPolicyAPICallsTotal counts every call routed through the legacy
// API shim. Operators watch the metric drop to zero before sunsetting the
// shim package; the {endpoint, shape} cardinality lets us sunset endpoint
// by endpoint rather than waiting for the slowest mover.
//
// Sunset criterion: shim removed when this metric reads zero across all
// endpoints for 30 consecutive days post cut-over.
var legacyPolicyAPICallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "cordum",
	Subsystem: "legacy_policy",
	Name:      "api_calls_total",
	Help:      "Calls served by the legacy policy API shim. Operators watch this drop to zero across {endpoint,shape} for 30 days before removing the shim package (see core/policy/legacyshim).",
}, []string{"endpoint", "shape"})

// RecordCall increments the legacy shim metric for the given (endpoint,
// shape) tuple. Call from each shimmed handler after a successful body
// decode but before dispatch — failed-decode requests should not pollute
// the metric, so the sunset check stays meaningful.
func RecordCall(endpoint, shape string) {
	if endpoint == "" || shape == "" {
		return
	}
	legacyPolicyAPICallsTotal.WithLabelValues(endpoint, shape).Inc()
}

// CallCount returns the current counter value for the given labels. It is
// exported to support testing without forcing callers to scrape Prometheus
// or hold a reference to the package-private CounterVec.
func CallCount(endpoint, shape string) float64 {
	m := &dto.Metric{}
	if err := legacyPolicyAPICallsTotal.WithLabelValues(endpoint, shape).Write(m); err != nil {
		return 0
	}
	if m.Counter == nil || m.Counter.Value == nil {
		return 0
	}
	return *m.Counter.Value
}
