package edge

import "github.com/prometheus/client_golang/prometheus"

// edgeRequestLabelsStrippedTotal counts every label drop performed by
// the trust boundary in `mapLabelsForPolicy` / `putPolicyLabel`. A
// request-body label is "stripped" when its key starts with a
// classifier-owned namespace (see `reservedPolicyLabelPrefixes`) — the
// classifier emits these on its own, so a request body trying to set
// them is either a buggy client or an injection attempt. Operators can
// alert on a sustained non-zero rate per `namespace`.
//
// Registered globally via prometheus.MustRegister so handlers don't
// need plumbing — the metric is simply incremented inside
// putPolicyLabel where the strip happens.
var edgeRequestLabelsStrippedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "cordum_edge_request_labels_stripped_total",
	Help: "Number of request-body labels stripped at the policy mapper trust boundary because the key matches a classifier-owned namespace, labeled by namespace prefix (without trailing dot).",
}, []string{"namespace"})

func init() {
	prometheus.MustRegister(edgeRequestLabelsStrippedTotal)
}
