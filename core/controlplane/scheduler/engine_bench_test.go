package scheduler

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// silenceLogs suppresses log output during benchmarks to prevent log lines from
// corrupting Go benchmark result format (needed for CI benchmark-action parsing).
func silenceLogs(b *testing.B) {
	b.Helper()
	// Silence standard log package (used by core/infra/logging)
	origLog := log.Writer()
	log.SetOutput(io.Discard)
	// Silence slog (used by some gateway/store code)
	origSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	// Redirect os.Stderr to /dev/null as defense-in-depth
	origStderr := os.Stderr
	if devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0); err == nil {
		os.Stderr = devNull
		b.Cleanup(func() {
			os.Stderr = origStderr
			devNull.Close()
		})
	}
	b.Cleanup(func() {
		log.SetOutput(origLog)
		slog.SetDefault(origSlog)
	})
}

// benchBus discards all publishes to avoid unbounded slice growth in benchmarks.
type benchBus struct{}

func (b *benchBus) Publish(string, *pb.BusPacket) error                    { return nil }
func (b *benchBus) Subscribe(string, string, func(*pb.BusPacket) error) error { return nil }

// BenchmarkHandlePacket exercises the full HandlePacket -> processJob path:
// safety check, strategy pick, bus publish, and state transitions.
func BenchmarkHandlePacket(b *testing.B) {
	silenceLogs(b)
	registry := NewMemoryRegistry()
	defer registry.Close()

	store := newFakeJobStore()
	engine := NewEngine(&benchBus{}, NewSafetyBasic(), registry, NewNaiveStrategy(), store, nil)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		jobID := fmt.Sprintf("bench-job-%d", i)
		pkt := &pb.BusPacket{
			TraceId:   "bench-trace",
			SenderId:  "bench-sender",
			CreatedAt: timestamppb.Now(),
			Payload: &pb.BusPacket_JobRequest{
				JobRequest: &pb.JobRequest{
					JobId: jobID,
					Topic: "job.bench.test",
				},
			},
		}
		_ = engine.HandlePacket(pkt)
	}
}

// BenchmarkHandleHeartbeat measures the cost of heartbeat processing through HandlePacket.
func BenchmarkHandleHeartbeat(b *testing.B) {
	silenceLogs(b)
	registry := NewMemoryRegistry()
	defer registry.Close()

	engine := NewEngine(&benchBus{}, NewSafetyBasic(), registry, NewNaiveStrategy(), newFakeJobStore(), nil)

	pkt := &pb.BusPacket{
		TraceId:  "bench-trace",
		SenderId: "bench-sender",
		Payload: &pb.BusPacket_Heartbeat{
			Heartbeat: &pb.Heartbeat{
				WorkerId:        "bench-worker",
				Pool:            "default",
				ActiveJobs:      2,
				MaxParallelJobs: 10,
				CpuLoad:         30,
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = engine.HandlePacket(pkt)
	}
}

// BenchmarkHandlePacketWithLeastLoaded uses the LeastLoadedStrategy with workers registered
// in the registry to exercise the pool-based selection path.
func BenchmarkHandlePacketWithLeastLoaded(b *testing.B) {
	silenceLogs(b)
	registry := NewMemoryRegistry()
	defer registry.Close()

	routing := PoolRouting{
		Topics: map[string][]string{"job.bench": {"bench-pool"}},
		Pools:  map[string]PoolProfile{"bench-pool": {}},
	}
	strategy := NewLeastLoadedStrategy(routing)
	store := newFakeJobStore()
	engine := NewEngine(&benchBus{}, NewSafetyBasic(), registry, strategy, store, nil)

	for j := 0; j < 10; j++ {
		registry.UpdateHeartbeat(&pb.Heartbeat{
			WorkerId:        fmt.Sprintf("w-%d", j),
			Pool:            "bench-pool",
			ActiveJobs:      int32(j % 5),
			MaxParallelJobs: 10,
			CpuLoad:         float32(j * 8),
		})
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		jobID := fmt.Sprintf("bench-ll-%d", i)
		pkt := &pb.BusPacket{
			TraceId:  "bench-trace",
			SenderId: "bench-sender",
			Payload: &pb.BusPacket_JobRequest{
				JobRequest: &pb.JobRequest{
					JobId: jobID,
					Topic: "job.bench",
				},
			},
		}
		_ = engine.HandlePacket(pkt)
	}
}
