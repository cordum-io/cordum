package safetykernel

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestShadowEvalMetricsRegistered asserts the four shadow-eval
// Prometheus series are present in the default registry after
// package init. Dashboards and alert rules depend on these exact
// names; a silent rename would blank out grafana without a loud
// failure, so pin the names here.
func TestShadowEvalMetricsRegistered(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"cordum_shadow_eval_total":            false,
		"cordum_shadow_eval_dropped_total":    false,
		"cordum_shadow_eval_queue_depth":      false,
		"cordum_shadow_eval_duration_seconds": false,
	}
	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range gathered {
		name := mf.GetName()
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	// Counters with zero observations might not show up in Gather
	// until Inc has been called. Inc each one once to flush them into
	// the registry, then re-gather.
	shadowEvalTotal.WithLabelValues("allow", "unchanged").Inc()
	shadowEvalDropped.WithLabelValues("queue_full").Inc()
	shadowEvalQueueDepth.Set(0)
	shadowEvalDuration.Observe(0.001)

	gathered, err = prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather-2: %v", err)
	}
	for _, mf := range gathered {
		name := mf.GetName()
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, present := range want {
		if !present {
			t.Errorf("metric %q not registered in default Prometheus registry", name)
		}
	}
}

// TestShadowCallbacksAssignedByInit ensures the package-level
// callback vars are wired to real closures at init time — a nil
// callback would make the whole metric pipeline silent in prod.
func TestShadowCallbacksAssignedByInit(t *testing.T) {
	t.Parallel()
	if shadowDroppedCallback == nil {
		t.Error("shadowDroppedCallback not assigned")
	}
	if shadowEmittedCallback == nil {
		t.Error("shadowEmittedCallback not assigned")
	}
	if shadowQueueDepthCallback == nil {
		t.Error("shadowQueueDepthCallback not assigned")
	}
}

// TestShadowEvalDurationBucketsCoverRealisticRange pins the histogram
// buckets so a future tuning doesn't silently drop a bucket our
// dashboards/alerts depend on.
func TestShadowEvalDurationBucketsCoverRealisticRange(t *testing.T) {
	t.Parallel()
	mf, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, m := range mf {
		if m.GetName() != "cordum_shadow_eval_duration_seconds" {
			continue
		}
		found := m.GetMetric()
		if len(found) == 0 {
			t.Fatal("duration histogram has no metrics exported")
		}
		var boundaries []string
		for _, b := range found[0].GetHistogram().GetBucket() {
			boundaries = append(boundaries, formatFloat(b.GetUpperBound()))
		}
		joined := strings.Join(boundaries, ",")
		for _, need := range []string{"0.001", "0.01", "0.1", "1", "5"} {
			if !strings.Contains(joined, need) {
				t.Errorf("duration buckets missing %q; got %s", need, joined)
			}
		}
		return
	}
	t.Fatal("duration histogram not found in gathered metrics")
}

// TestSafetyRuleDelegationMatchMetricRegistered asserts the delegation
// deny counter is present in the default registry with a zero-baseline
// series for every known deny field, so rate() queries on dashboards
// don't suffer from "series first appears on incident" stair-steps.
func TestSafetyRuleDelegationMatchMetricRegistered(t *testing.T) {
	t.Parallel()
	gathered, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	seenFields := map[string]bool{}
	for _, mf := range gathered {
		if mf.GetName() != "safety_rule_delegation_match_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			var field, outcome string
			for _, lp := range m.GetLabel() {
				switch lp.GetName() {
				case "field":
					field = lp.GetValue()
				case "outcome":
					outcome = lp.GetValue()
				}
			}
			if outcome == "deny" {
				seenFields[field] = true
			}
		}
	}
	wantFields := []string{"forbid_delegated", "max_depth", "issuers", "require_issuer", "required_scope"}
	for _, want := range wantFields {
		if !seenFields[want] {
			t.Errorf("metric safety_rule_delegation_match_total{field=%q,outcome=\"deny\"} not pre-materialised", want)
		}
	}
}

func formatFloat(f float64) string {
	// Rough but readable; we only need substring matches for the test.
	switch f {
	case 0.001:
		return "0.001"
	case 0.005:
		return "0.005"
	case 0.01:
		return "0.01"
	case 0.025:
		return "0.025"
	case 0.05:
		return "0.05"
	case 0.1:
		return "0.1"
	case 0.25:
		return "0.25"
	case 0.5:
		return "0.5"
	case 1:
		return "1"
	case 5:
		return "5"
	default:
		return ""
	}
}
