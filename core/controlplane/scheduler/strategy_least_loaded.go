package scheduler

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/logging"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

// LeastLoadedStrategy picks a worker from the pool configured for the job topic using a simple load score.
// Lower scores win; score combines active jobs + cpu + gpu utilization to avoid overloading busy workers.
type LeastLoadedStrategy struct {
	routing atomic.Value
}

const overloadUtilizationThreshold = 0.9

func NewLeastLoadedStrategy(routing PoolRouting) *LeastLoadedStrategy {
	strategy := &LeastLoadedStrategy{}
	strategy.UpdateRouting(routing)
	return strategy
}

// UpdateRouting replaces the routing table with a new snapshot.
func (s *LeastLoadedStrategy) UpdateRouting(routing PoolRouting) {
	s.routing.Store(cloneRouting(routing))
}

// CurrentRouting returns the latest routing snapshot.
func (s *LeastLoadedStrategy) CurrentRouting() PoolRouting {
	if current, ok := s.routing.Load().(PoolRouting); ok {
		return current
	}
	return PoolRouting{}
}

func (s *LeastLoadedStrategy) PickSubject(req *pb.JobRequest, workers map[string]*pb.Heartbeat) (string, error) {
	if req == nil || req.Topic == "" {
		return "", fmt.Errorf("missing topic")
	}

	routing := s.CurrentRouting()
	labels := req.GetLabels()
	requiredLabels := filterPlacementLabels(labels)
	poolHint := labels["preferred_pool"]
	topicPools := routing.Topics[req.Topic]
	if poolHint != "" {
		if !containsPool(topicPools, poolHint) {
			return "", fmt.Errorf("%w: preferred pool %q not mapped for topic %q", ErrNoPoolMapping, poolHint, req.Topic)
		}
		topicPools = []string{poolHint}
	}
	if len(topicPools) == 0 {
		return "", fmt.Errorf("%w: topic %q", ErrNoPoolMapping, req.Topic)
	}
	var jobRequires []string
	if meta := req.GetMeta(); meta != nil {
		jobRequires = meta.GetRequires()
	}
	eligiblePools := filterEligiblePools(topicPools, jobRequires, routing.Pools)
	if len(eligiblePools) == 0 {
		return "", fmt.Errorf("%w: no pool satisfies requires", ErrNoPoolMapping)
	}
	poolSet := make(map[string]struct{}, len(eligiblePools))
	for _, pool := range eligiblePools {
		poolSet[pool] = struct{}{}
	}

	// Preferred worker shortcut if available and healthy.
	if preferredWorker := labels["preferred_worker_id"]; preferredWorker != "" {
		if hb, exists := workers[preferredWorker]; exists {
			if _, ok := poolSet[hb.GetPool()]; ok && matchesLabels(hb, requiredLabels) && !isOverloaded(hb) {
				if subject := bus.DirectSubject(preferredWorker); subject != "" {
					logging.Info("scheduler", "strategy pick preferred worker",
						"topic", req.Topic,
						"pool", hb.Pool,
						"selected_worker", hb.WorkerId,
						"hint", "preferred_worker_id",
					)
					return subject, nil
				}
			}
		}
	}
	var selected *pb.Heartbeat
	var bestScore float32
	overloadedCandidates := 0
	totalCandidates := 0
	for _, hb := range workers {
		if hb == nil {
			continue
		}
		if _, ok := poolSet[hb.GetPool()]; !ok {
			continue
		}
		if !matchesLabels(hb, requiredLabels) {
			continue
		}
		totalCandidates++
		if isOverloaded(hb) {
			overloadedCandidates++
			continue
		}
		score := loadScore(hb)
		if selected == nil || score < bestScore {
			selected = hb
			bestScore = score
		}
	}

	if selected == nil {
		if totalCandidates > 0 && overloadedCandidates == totalCandidates {
			return "", fmt.Errorf("%w: pool %q", ErrPoolOverloaded, strings.Join(eligiblePools, ","))
		}
		return "", fmt.Errorf("%w: pool %q", ErrNoWorkers, strings.Join(eligiblePools, ","))
	}

	logging.Info("scheduler", "strategy pick",
		"topic", req.Topic,
		"pool", selected.Pool,
		"selected_worker", selected.WorkerId,
		"score", bestScore,
		"active_jobs", selected.ActiveJobs,
		"cpu_load", selected.CpuLoad,
		"gpu_utilization", selected.GpuUtilization,
	)

	if subject := bus.DirectSubject(selected.WorkerId); subject != "" {
		return subject, nil
	}
	// Fallback: publish to the topic (pool subject); queue groups fan-in to a single worker.
	return req.Topic, nil
}

func cloneRouting(routing PoolRouting) PoolRouting {
	topics := make(map[string][]string, len(routing.Topics))
	for topic, pools := range routing.Topics {
		clone := make([]string, len(pools))
		copy(clone, pools)
		topics[topic] = clone
	}
	pools := make(map[string]PoolProfile, len(routing.Pools))
	for name, profile := range routing.Pools {
		reqs := make([]string, len(profile.Requires))
		copy(reqs, profile.Requires)
		pools[name] = PoolProfile{Requires: reqs}
	}
	return PoolRouting{
		Topics: topics,
		Pools:  pools,
	}
}

func loadScore(hb *pb.Heartbeat) float32 {
	return float32(hb.GetActiveJobs()) + hb.GetCpuLoad()/100.0 + hb.GetGpuUtilization()/100.0
}

func matchesLabels(hb *pb.Heartbeat, required map[string]string) bool {
	if len(required) == 0 {
		return true
	}
	labels := hb.GetLabels()
	if len(labels) == 0 {
		return false
	}
	for k, v := range required {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func isOverloaded(hb *pb.Heartbeat) bool {
	capacity := hb.GetMaxParallelJobs()
	if capacity > 0 {
		utilization := float32(hb.GetActiveJobs()) / float32(capacity)
		if utilization >= overloadUtilizationThreshold {
			return true
		}
	}
	// Fallback on CPU load if capacity not set.
	if hb.GetCpuLoad() >= 90 {
		return true
	}
	if hb.GetGpuUtilization() >= 90 {
		return true
	}
	return false
}

func filterPlacementLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		if k == "preferred_worker_id" || k == "preferred_pool" {
			continue
		}
		// These labels are used for traceability/observability and should not constrain placement.
		// Placement constraints should be expressed via dedicated labels (e.g. hardware/region),
		// not workflow/run identifiers.
		if k == "workflow_id" || k == "run_id" || k == "step_id" || k == "node_id" {
			continue
		}
		if k == "worker_id" {
			continue
		}
		out[k] = v
	}
	return out
}

func filterEligiblePools(pools []string, requires []string, poolConfigs map[string]PoolProfile) []string {
	if len(pools) == 0 {
		return nil
	}
	if len(requires) == 0 {
		return append([]string{}, pools...)
	}
	out := make([]string, 0, len(pools))
	for _, pool := range pools {
		profile := poolConfigs[pool]
		if poolSatisfies(profile.Requires, requires) {
			out = append(out, pool)
		}
	}
	return out
}

func poolSatisfies(poolRequires, jobRequires []string) bool {
	if len(jobRequires) == 0 {
		return true
	}
	if len(poolRequires) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(poolRequires))
	for _, req := range poolRequires {
		req = strings.ToLower(strings.TrimSpace(req))
		if req != "" {
			set[req] = struct{}{}
		}
	}
	for _, req := range jobRequires {
		need := strings.ToLower(strings.TrimSpace(req))
		if need == "" {
			continue
		}
		if _, ok := set[need]; !ok {
			return false
		}
	}
	return true
}

func containsPool(pools []string, pool string) bool {
	for _, p := range pools {
		if p == pool {
			return true
		}
	}
	return false
}
