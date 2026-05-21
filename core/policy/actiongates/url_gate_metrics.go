package actiongates

import "github.com/prometheus/client_golang/prometheus"

var urlGateResolverCacheEvictionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "url_gate_resolver_cache_evictions_total",
	Help: "Total URL gate resolver cache entries evicted due to the capacity-bounded LRU.",
})

func init() {
	prometheus.MustRegister(urlGateResolverCacheEvictionsTotal)
}

func recordURLGateResolverCacheEvictions(count int) {
	if count <= 0 {
		return
	}
	urlGateResolverCacheEvictionsTotal.Add(float64(count))
}
