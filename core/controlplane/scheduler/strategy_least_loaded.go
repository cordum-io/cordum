package scheduler

import (
	"fmt"

	"github.com/yaront1111/coretex-os/core/infra/logging"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

// LeastLoadedStrategy picks a worker from the pool configured for the job topic using a simple load score.
// Lower scores win; score combines active jobs + cpu + gpu utilization to avoid overloading busy workers.
type LeastLoadedStrategy struct {
	topicToPool map[string]string
}

func NewLeastLoadedStrategy(topicToPool map[string]string) *LeastLoadedStrategy {
	return &LeastLoadedStrategy{
		topicToPool: topicToPool,
	}
}

func (s *LeastLoadedStrategy) PickSubject(req *pb.JobRequest, workers map[string]*pb.Heartbeat) (string, error) {
	if req == nil || req.Topic == "" {
		return "", fmt.Errorf("missing topic")
	}

	pool, ok := s.topicToPool[req.Topic]
	if !ok {
		return "", fmt.Errorf("no pool configured for topic %q", req.Topic)
	}

	requiredLabels := req.GetLabels()

	var selected *pb.Heartbeat
	var bestScore float32
	for _, hb := range workers {
		if hb == nil || hb.GetPool() != pool {
			continue
		}
		if !matchesLabels(hb, requiredLabels) {
			continue
		}
		score := loadScore(hb)
		if selected == nil || score < bestScore {
			selected = hb
			bestScore = score
		}
	}

	if selected == nil {
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

	// Publish to the topic (pool subject); queue groups fan-in to a single worker.
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
