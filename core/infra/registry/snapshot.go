package registry

import (
	"time"

	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

const SnapshotKey = "sys:workers:snapshot"

// Snapshot captures a point-in-time view of worker availability.
type Snapshot struct {
	CapturedAt string                   `json:"captured_at"`
	Pools      map[string]PoolSnapshot  `json:"pools,omitempty"`
	Topics     map[string]TopicSnapshot `json:"topics,omitempty"`
	Workers    []WorkerSummary          `json:"workers,omitempty"`
}

// WorkerSummary is a compact representation of a worker heartbeat.
type WorkerSummary struct {
	WorkerID        string   `json:"worker_id"`
	Pool            string   `json:"pool"`
	ActiveJobs      int32    `json:"active_jobs"`
	MaxParallelJobs int32    `json:"max_parallel_jobs"`
	Capabilities    []string `json:"capabilities,omitempty"`
	CpuLoad         float32  `json:"cpu_load,omitempty"`
	GpuUtilization  float32  `json:"gpu_utilization,omitempty"`
}

// PoolSnapshot aggregates worker capacity per pool.
type PoolSnapshot struct {
	Workers    int   `json:"workers"`
	ActiveJobs int32 `json:"active_jobs"`
	Capacity   int32 `json:"capacity"`
}

// TopicSnapshot maps a topic to pool availability.
type TopicSnapshot struct {
	Pool      string `json:"pool"`
	Workers   int    `json:"workers"`
	Capacity  int32  `json:"capacity"`
	Available bool   `json:"available"`
}

// BuildSnapshot aggregates heartbeats into a snapshot for control-plane consumers.
func BuildSnapshot(workers map[string]*pb.Heartbeat, topicToPool map[string]string) Snapshot {
	pools := map[string]PoolSnapshot{}
	summaries := make([]WorkerSummary, 0, len(workers))

	for _, hb := range workers {
		if hb == nil {
			continue
		}
		summaries = append(summaries, WorkerSummary{
			WorkerID:        hb.WorkerId,
			Pool:            hb.Pool,
			ActiveJobs:      hb.ActiveJobs,
			MaxParallelJobs: hb.MaxParallelJobs,
			Capabilities:    hb.Capabilities,
			CpuLoad:         hb.CpuLoad,
			GpuUtilization:  hb.GpuUtilization,
		})
		if hb.Pool == "" {
			continue
		}
		pool := pools[hb.Pool]
		pool.Workers++
		pool.ActiveJobs += hb.ActiveJobs
		if hb.MaxParallelJobs > 0 {
			pool.Capacity += hb.MaxParallelJobs
		}
		pools[hb.Pool] = pool
	}

	topics := map[string]TopicSnapshot{}
	for topic, poolName := range topicToPool {
		pool := pools[poolName]
		topics[topic] = TopicSnapshot{
			Pool:      poolName,
			Workers:   pool.Workers,
			Capacity:  pool.Capacity,
			Available: pool.Workers > 0,
		}
	}

	return Snapshot{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		Pools:      pools,
		Topics:     topics,
		Workers:    summaries,
	}
}
