package safetykernel

import (
	"time"

	"github.com/cordum/cordum/core/infra/config"
	"github.com/prometheus/client_golang/prometheus"
)

// Phase-2 shadow-evaluation metrics.
//
// Four series per the plan:
//
//  1. cordum_shadow_eval_total{decision, diff} — counter incremented on
//     every emitted shadow_eval event. The `decision` label is the
//     shadow bundle's verdict (allow/deny/require_approval/throttle);
//     `diff` is one of escalated/relaxed/approval_differ/unchanged.
//     Splits by (decision, diff) so dashboards can answer both "what
//     would the shadow do?" and "how often does it disagree?" without
//     a second series.
//
//  2. cordum_shadow_eval_dropped_total{reason} — counter incremented
//     when Submit cannot enqueue. `reason` is queue_full or closed.
//     Alert on queue_full crossing a tenant-scaled threshold — it
//     means shadows are being lost.
//
//  3. cordum_shadow_eval_queue_depth — gauge of the in-flight queue.
//     Useful for tuning worker/queue size; paired with dropped_total
//     to distinguish "queue grew but recovered" from "queue saturated
//     and dropped".
//
//  4. cordum_shadow_eval_duration_seconds — histogram of shadow
//     evaluation latency (post-snapshot Evaluate call + audit.Send).
//     Buckets chosen to cover the 1 ms..500 ms range where typical
//     evaluations land; a spike at the high end signals a pathological
//     rule set.

var (
	shadowEvalTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cordum_shadow_eval_total",
		Help: "Total shadow_eval audit events emitted, partitioned by the shadow verdict and diff class",
	}, []string{"decision", "diff"})

	shadowEvalDropped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "cordum_shadow_eval_dropped_total",
		Help: "Shadow evaluation submissions dropped without emission, partitioned by reason (queue_full|closed)",
	}, []string{"reason"})

	shadowEvalQueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "cordum_shadow_eval_queue_depth",
		Help: "Current depth of the shadow evaluation queue (enqueued jobs waiting for a worker)",
	})

	shadowEvalDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "cordum_shadow_eval_duration_seconds",
		Help:    "Wall-clock latency of a single shadow-policy Evaluate call",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 5},
	})

	// safety_rule_delegation_match_total counts how often a PolicyRule with
	// a `delegation:` match block rejects an input, partitioned by the
	// sub-field that short-circuited the rule. Operators use this to see
	// which delegation constraint is doing the most work (or which one is
	// accidentally over-blocking legitimate chains). `outcome="deny"` is
	// currently the only value emitted; the label is kept for forward
	// compatibility with a future "match" outcome if we ever need to count
	// rules that matched via their delegation block.
	safetyRuleDelegationMatchTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "safety_rule_delegation_match_total",
		Help: "Counter of safety-rule delegation match outcomes, partitioned by the sub-field that fired",
	}, []string{"field", "outcome"})
)

func init() {
	prometheus.MustRegister(shadowEvalTotal)
	prometheus.MustRegister(shadowEvalDropped)
	prometheus.MustRegister(shadowEvalQueueDepth)
	prometheus.MustRegister(shadowEvalDuration)
	prometheus.MustRegister(safetyRuleDelegationMatchTotal)

	// Pre-materialise counter series for every known deny field so Prometheus
	// scrapes surface a stable zero-baseline rather than missing series
	// (dashboards that use rate() over a series that only appears on first
	// occurrence produce stair-step artefacts otherwise).
	for _, field := range config.DelegationMatchDenyFields {
		safetyRuleDelegationMatchTotal.WithLabelValues(field, "deny").Add(0)
	}

	// Wire the config-layer deny callback to the collector. The config
	// package stays metric-library-agnostic; swapping Prometheus for OTel
	// changes only this file.
	config.SetDelegationMatchDenyCallback(func(field string) {
		safetyRuleDelegationMatchTotal.WithLabelValues(field, "deny").Inc()
	})

	// Wire the evaluator's callback hooks to the Prometheus collectors.
	// The evaluator itself is metric-library-agnostic — keeping the
	// wiring here means swapping Prometheus for OTel later touches one
	// file instead of the evaluator's internals.
	shadowDroppedCallback = func(reason ShadowDropReason) {
		shadowEvalDropped.WithLabelValues(string(reason)).Inc()
	}
	shadowEmittedCallback = func(decision string, diff ShadowDiff, latency time.Duration) {
		shadowEvalTotal.WithLabelValues(decision, string(diff)).Inc()
		shadowEvalDuration.Observe(latency.Seconds())
	}
	shadowQueueDepthCallback = func(delta int64) {
		if delta > 0 {
			shadowEvalQueueDepth.Add(float64(delta))
		} else if delta < 0 {
			shadowEvalQueueDepth.Sub(float64(-delta))
		}
	}
}
