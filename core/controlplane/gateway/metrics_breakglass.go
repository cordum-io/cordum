package gateway

import (
	"strings"

	"github.com/cordum/cordum/core/licensing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var licenseBreakGlassDecisionsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "license_breakglass_decisions_total",
		Help: "Break-glass permission decisions by outcome and license state.",
	},
	[]string{"decision", "state"},
)

func observeBreakGlassDecision(decision string, state licensing.BreakGlassState) {
	decision = strings.TrimSpace(decision)
	if decision == "" {
		decision = "unknown"
	}

	stateLabel := strings.TrimSpace(string(state))
	if stateLabel == "" {
		stateLabel = "unknown"
	}

	licenseBreakGlassDecisionsTotal.WithLabelValues(decision, stateLabel).Inc()
}
