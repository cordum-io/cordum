package scheduler

import (
	"fmt"

	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/logging"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

// LeastLoadedStrategy picks a worker from the pool configured for the job topic using a simple load score.
// Lower scores win; score combines active jobs + cpu + gpu utilization to avoid overloading busy workers.
type LeastLoadedStrategy struct {
	topicToPool map[string]string
}

const overloadUtilizationThreshold = 0.9

func NewLeastLoadedStrategy(topicToPool map[string]string) *LeastLoadedStrategy {
	return &LeastLoadedStrategy{
		topicToPool: topicToPool,
	}
}

func (s *LeastLoadedStrategy) PickSubject(req *pb.JobRequest, workers map[string]*pb.Heartbeat) (string, error) {
	if req == nil || req.Topic == "" {
		return "", fmt.Errorf("missing topic")
	}

	labels := req.GetLabels()
	requiredLabels := filterPlacementLabels(labels)
	poolHint := labels["preferred_pool"]
	pool, ok := s.topicToPool[req.Topic]
	if poolHint != "" {
		pool = poolHint
		ok = true
	}
	if !ok {
		return "", fmt.Errorf("no pool configured for topic %q", req.Topic)
	}

	// Preferred worker shortcut if available and healthy.
	if preferredWorker := labels["preferred_worker_id"]; preferredWorker != "" {
		if hb, exists := workers[preferredWorker]; exists && hb.GetPool() == pool && matchesLabels(hb, requiredLabels) && !isOverloaded(hb) {
			if subject := bus.DirectSubject(preferredWorker); subject != "" {
				logging.Info("scheduler", "strategy pick preferred worker",
					"topic", req.Topic,
					"pool", pool,
					"selected_worker", hb.WorkerId,
					"hint", "preferred_worker_id",
				)
				return subject, nil
			}
		}
	}
	var selected *pb.Heartbeat
	var bestScore float32
	overloadedCandidates := 0
	totalCandidates := 0
	for _, hb := range workers {
		if hb == nil || hb.GetPool() != pool {
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
			return "", fmt.Errorf("all workers in pool %q are overloaded", pool)
		}
		return "", fmt.Errorf("no workers available for pool %q", pool)
	}

	logging.Info("scheduler", "strategy pick",
		"topic", req.Topic,
		"pool", pool,
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
	if capacity > 0 && hb.GetActiveJobs() >= int32(float32(capacity)*overloadUtilizationThreshold) {
		return true
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
		if k == "workflow_id" || k == "run_id" || k == "step_id" {
			continue
		}
		if k == "worker_id" {
			continue
		}
		out[k] = v
	}
	return out
}
