package scheduler

// PoolProfile describes static requirements for a pool.
type PoolProfile struct {
	Requires []string
}

// PoolRouting captures topic routing and pool capabilities.
type PoolRouting struct {
	Topics map[string][]string
	Pools  map[string]PoolProfile
}

// TopicToPool returns a single-pool mapping for legacy consumers.
func (r PoolRouting) TopicToPool() map[string]string {
	out := make(map[string]string, len(r.Topics))
	for topic, pools := range r.Topics {
		if len(pools) == 0 {
			continue
		}
		out[topic] = pools[0]
	}
	return out
}
