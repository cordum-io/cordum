package runtime

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"strings"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewWorker_ValidConfig(t *testing.T) {
	_, natsURL := startTestNATS(t)

	cfg := Config{
		Type:            "echo",
		Pool:            "test-pool",
		NatsURL:         natsURL,
		MaxParallelJobs: 5,
		Capabilities:    []string{"echo", "ping"},
		Labels:          map[string]string{"env": "test"},
		WorkerID:        "test-worker-1",
	}

	w, err := NewWorker(cfg)
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	if w.workerID != "test-worker-1" {
		t.Errorf("expected workerID 'test-worker-1', got %q", w.workerID)
	}
	if w.pool != "test-pool" {
		t.Errorf("expected pool 'test-pool', got %q", w.pool)
	}
	if cap(w.sem) != 5 {
		t.Errorf("expected semaphore capacity 5, got %d", cap(w.sem))
	}
	if w.conn == nil {
		t.Error("expected NATS connection to be established")
	}
}

func TestNewWorker_DefaultsApplied(t *testing.T) {
	_, natsURL := startTestNATS(t)
	os.Unsetenv("WORKER_ID")

	cfg := Config{
		Type:    "processor",
		NatsURL: natsURL,
	}

	w, err := NewWorker(cfg)
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	// MaxParallelJobs defaults to 1
	if cap(w.sem) != 1 {
		t.Errorf("expected default semaphore capacity 1, got %d", cap(w.sem))
	}

	// Pool defaults to Type
	if w.pool != "processor" {
		t.Errorf("expected pool to default to type 'processor', got %q", w.pool)
	}

	// Subject derived from Type
	expectedSubject := "job.processor.*"
	found := false
	for _, s := range w.subjects {
		if s == expectedSubject {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected subject %q in %v", expectedSubject, w.subjects)
	}
}

func TestNewWorker_SubjectsProvided(t *testing.T) {
	_, natsURL := startTestNATS(t)

	cfg := Config{
		Subjects: []string{"custom.subject.1", "custom.subject.2"},
		NatsURL:  natsURL,
		WorkerID: "subject-worker",
	}

	w, err := NewWorker(cfg)
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	if len(w.subjects) != 2 {
		t.Errorf("expected 2 subjects, got %d", len(w.subjects))
	}
}

func TestNewWorker_SubjectDeduplication(t *testing.T) {
	_, natsURL := startTestNATS(t)

	cfg := Config{
		Subjects: []string{"job.echo.*", "job.echo.*", "  job.echo.*  "},
		NatsURL:  natsURL,
		WorkerID: "dedup-worker",
	}

	w, err := NewWorker(cfg)
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	// Should deduplicate to single subject
	if len(w.subjects) != 1 {
		t.Errorf("expected 1 deduplicated subject, got %d: %v", len(w.subjects), w.subjects)
	}
}

func TestNewWorker_InvalidSubjects(t *testing.T) {
	_, natsURL := startTestNATS(t)

	cfg := Config{
		// No subjects and no Type - should fail
		NatsURL:  natsURL,
		WorkerID: "no-subjects-worker",
	}

	_, err := NewWorker(cfg)
	if err == nil {
		t.Fatal("expected error for missing subjects")
	}
}

func TestNewWorker_ConnectionFailure(t *testing.T) {
	cfg := Config{
		Type:    "echo",
		NatsURL: "nats://invalid-host-that-does-not-exist:4222",
	}

	_, err := NewWorker(cfg)
	if err == nil {
		t.Fatal("expected error for NATS connection failure")
	}
}

func TestNewWorker_NatsURLFromEnv(t *testing.T) {
	_, natsURL := startTestNATS(t)
	t.Setenv("NATS_URL", natsURL)

	cfg := Config{
		Type:     "echo",
		WorkerID: "env-nats-worker",
		// NatsURL intentionally empty
	}

	w, err := NewWorker(cfg)
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()
}

func TestWorker_Run_HandlerRequired(t *testing.T) {
	_, natsURL := startTestNATS(t)

	w, err := NewWorker(Config{Type: "echo", NatsURL: natsURL})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err = w.Run(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil handler")
	}
}

func TestWorker_Run_ProcessesJobs(t *testing.T) {
	_, natsURL := startTestNATS(t)

	// Create worker
	w, err := NewWorker(Config{
		Type:           "echo",
		NatsURL:        natsURL,
		WorkerID:       "process-jobs-worker",
		HeartbeatEvery: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	var (
		received    atomic.Bool
		receivedJob *agentv1.JobRequest
		mu          sync.Mutex
	)

	// Start worker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Run(ctx, func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
			mu.Lock()
			receivedJob = req
			mu.Unlock()
			received.Store(true)
			return &agentv1.JobResult{
				JobId:     req.GetJobId(),
				Status:    agentv1.JobStatus_JOB_STATUS_SUCCEEDED,
				ResultPtr: "result:test-job-123",
			}, nil
		})
	}()

	// Give worker time to subscribe
	time.Sleep(100 * time.Millisecond)

	// Create client to send job
	nc := testNATSConn(t, natsURL)

	// Build job request
	jobReq := &agentv1.JobRequest{
		JobId:      "test-job-123",
		Topic:      "job.echo.submit",
		ContextPtr: "ctx:test-job-123",
	}
	packet := &agentv1.BusPacket{
		TraceId:         "trace-123",
		SenderId:        "test-client",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &agentv1.BusPacket_JobRequest{
			JobRequest: jobReq,
		},
	}
	data, err := capsdk.MarshalDeterministic(packet)
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}

	// Publish job
	if err := nc.Publish("job.echo.submit", data); err != nil {
		t.Fatalf("publish job: %v", err)
	}
	nc.Flush()

	// Wait for job to be processed
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if received.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !received.Load() {
		t.Fatal("job was not received by handler")
	}

	mu.Lock()
	if receivedJob.GetJobId() != "test-job-123" {
		t.Errorf("expected job id 'test-job-123', got %q", receivedJob.GetJobId())
	}
	mu.Unlock()
}

func TestWorker_Run_ConcurrencyLimit(t *testing.T) {
	_, natsURL := startTestNATS(t)

	maxParallel := int32(2)
	w, err := NewWorker(Config{
		Type:            "slow",
		NatsURL:         natsURL,
		WorkerID:        "concurrency-worker",
		MaxParallelJobs: maxParallel,
		HeartbeatEvery:  100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	var (
		concurrent atomic.Int32
		maxSeen    atomic.Int32
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Run(ctx, func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
			cur := concurrent.Add(1)
			if cur > maxSeen.Load() {
				maxSeen.Store(cur)
			}
			time.Sleep(100 * time.Millisecond) // Simulate slow job
			concurrent.Add(-1)
			return &agentv1.JobResult{
				JobId:  req.GetJobId(),
				Status: agentv1.JobStatus_JOB_STATUS_SUCCEEDED,
			}, nil
		})
	}()

	time.Sleep(50 * time.Millisecond) // Let worker subscribe

	nc := testNATSConn(t, natsURL)

	// Send 5 jobs quickly
	for i := 0; i < 5; i++ {
		jobReq := &agentv1.JobRequest{
			JobId: "concurrent-job-" + string(rune('0'+i)),
			Topic: "job.slow.submit",
		}
		packet := &agentv1.BusPacket{
			TraceId:         jobReq.JobId,
			SenderId:        "test-client",
			ProtocolVersion: capsdk.DefaultProtocolVersion,
			CreatedAt:       timestamppb.Now(),
			Payload:         &agentv1.BusPacket_JobRequest{JobRequest: jobReq},
		}
		data, _ := capsdk.MarshalDeterministic(packet)
		nc.Publish("job.slow.submit", data)
	}
	nc.Flush()

	// Wait for jobs to complete
	time.Sleep(500 * time.Millisecond)

	// Max concurrent should not exceed MaxParallelJobs
	if maxSeen.Load() > maxParallel {
		t.Errorf("max concurrent %d exceeded MaxParallelJobs %d", maxSeen.Load(), maxParallel)
	}
}

func TestWorker_Run_DirectSubject(t *testing.T) {
	_, natsURL := startTestNATS(t)

	workerID := "direct-subject-worker"
	w, err := NewWorker(Config{
		Type:           "echo",
		NatsURL:        natsURL,
		WorkerID:       workerID,
		HeartbeatEvery: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	var received atomic.Bool

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Run(ctx, func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
			received.Store(true)
			return &agentv1.JobResult{
				JobId:  req.GetJobId(),
				Status: agentv1.JobStatus_JOB_STATUS_SUCCEEDED,
			}, nil
		})
	}()

	time.Sleep(100 * time.Millisecond)

	nc := testNATSConn(t, natsURL)

	// Send to direct subject
	directSubject := DirectSubject(workerID)
	jobReq := &agentv1.JobRequest{
		JobId: "direct-job",
		Topic: "job.echo.submit",
	}
	packet := &agentv1.BusPacket{
		TraceId:         "direct-trace",
		SenderId:        "test-client",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload:         &agentv1.BusPacket_JobRequest{JobRequest: jobReq},
	}
	data, _ := capsdk.MarshalDeterministic(packet)

	if err := nc.Publish(directSubject, data); err != nil {
		t.Fatalf("publish to direct subject: %v", err)
	}
	nc.Flush()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if received.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !received.Load() {
		t.Fatal("job not received on direct subject")
	}
}

func TestWorker_Run_PublishesResult(t *testing.T) {
	_, natsURL := startTestNATS(t)

	w, err := NewWorker(Config{
		Type:           "result-test",
		NatsURL:        natsURL,
		WorkerID:       "result-worker",
		HeartbeatEvery: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Run(ctx, func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
			return &agentv1.JobResult{
				JobId:     req.GetJobId(),
				Status:    agentv1.JobStatus_JOB_STATUS_SUCCEEDED,
				ResultPtr: "result:success",
			}, nil
		})
	}()

	time.Sleep(100 * time.Millisecond)

	nc := testNATSConn(t, natsURL)

	// Subscribe to results
	var resultReceived atomic.Bool
	var receivedResult *agentv1.JobResult
	var mu sync.Mutex

	sub, err := nc.Subscribe(SubjectResult, func(msg *nats.Msg) {
		var pkt agentv1.BusPacket
		if err := proto.Unmarshal(msg.Data, &pkt); err != nil {
			return
		}
		if res := pkt.GetJobResult(); res != nil {
			mu.Lock()
			receivedResult = res
			mu.Unlock()
			resultReceived.Store(true)
		}
	})
	if err != nil {
		t.Fatalf("subscribe to results: %v", err)
	}
	defer sub.Unsubscribe()

	// Send job
	jobReq := &agentv1.JobRequest{
		JobId: "result-job-1",
		Topic: "job.result-test.submit",
	}
	packet := &agentv1.BusPacket{
		TraceId:         "trace-result",
		SenderId:        "test-client",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload:         &agentv1.BusPacket_JobRequest{JobRequest: jobReq},
	}
	data, _ := capsdk.MarshalDeterministic(packet)
	nc.Publish("job.result-test.submit", data)
	nc.Flush()

	// Wait for result
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if resultReceived.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !resultReceived.Load() {
		t.Fatal("result not published")
	}

	mu.Lock()
	defer mu.Unlock()
	if receivedResult.GetJobId() != "result-job-1" {
		t.Errorf("expected job id 'result-job-1', got %q", receivedResult.GetJobId())
	}
	if receivedResult.GetStatus() != agentv1.JobStatus_JOB_STATUS_SUCCEEDED {
		t.Errorf("expected succeeded status, got %v", receivedResult.GetStatus())
	}
}

func TestWorker_Run_HandlerError(t *testing.T) {
	_, natsURL := startTestNATS(t)

	w, err := NewWorker(Config{
		Type:           "error-test",
		NatsURL:        natsURL,
		WorkerID:       "error-worker",
		HeartbeatEvery: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Run(ctx, func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
			return &agentv1.JobResult{
				JobId:  req.GetJobId(),
				Status: agentv1.JobStatus_JOB_STATUS_FAILED,
			}, nil // Return error status but no Go error
		})
	}()

	time.Sleep(100 * time.Millisecond)

	nc := testNATSConn(t, natsURL)

	var resultReceived atomic.Bool
	var receivedResult *agentv1.JobResult
	var mu sync.Mutex

	sub, err := nc.Subscribe(SubjectResult, func(msg *nats.Msg) {
		var pkt agentv1.BusPacket
		if err := proto.Unmarshal(msg.Data, &pkt); err != nil {
			return
		}
		if res := pkt.GetJobResult(); res != nil {
			mu.Lock()
			receivedResult = res
			mu.Unlock()
			resultReceived.Store(true)
		}
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	jobReq := &agentv1.JobRequest{JobId: "error-job", Topic: "job.error-test.submit"}
	packet := &agentv1.BusPacket{
		TraceId:         "trace-error",
		SenderId:        "test",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload:         &agentv1.BusPacket_JobRequest{JobRequest: jobReq},
	}
	data, _ := capsdk.MarshalDeterministic(packet)
	nc.Publish("job.error-test.submit", data)
	nc.Flush()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if resultReceived.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !resultReceived.Load() {
		t.Fatal("result not published")
	}

	mu.Lock()
	defer mu.Unlock()
	if receivedResult.GetStatus() != agentv1.JobStatus_JOB_STATUS_FAILED {
		t.Errorf("expected failed status, got %v", receivedResult.GetStatus())
	}
}

func TestWorker_Run_HandlerPanic(t *testing.T) {
	_, natsURL := startTestNATS(t)

	w, err := NewWorker(Config{
		Type:           "panic-test",
		NatsURL:        natsURL,
		WorkerID:       "panic-worker",
		HeartbeatEvery: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Run(ctx, func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
			panic("boom")
		})
	}()

	time.Sleep(100 * time.Millisecond)

	nc := testNATSConn(t, natsURL)

	results := make(map[string]*agentv1.JobResult)
	var mu sync.Mutex

	sub, err := nc.Subscribe(SubjectResult, func(msg *nats.Msg) {
		var pkt agentv1.BusPacket
		if err := proto.Unmarshal(msg.Data, &pkt); err != nil {
			return
		}
		if res := pkt.GetJobResult(); res != nil {
			mu.Lock()
			results[res.GetJobId()] = res
			mu.Unlock()
		}
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	publishJob := func(id string) {
		jobReq := &agentv1.JobRequest{JobId: id, Topic: "job.panic-test.submit"}
		packet := &agentv1.BusPacket{
			TraceId:         id,
			SenderId:        "test",
			ProtocolVersion: capsdk.DefaultProtocolVersion,
			CreatedAt:       timestamppb.Now(),
			Payload:         &agentv1.BusPacket_JobRequest{JobRequest: jobReq},
		}
		data, _ := capsdk.MarshalDeterministic(packet)
		nc.Publish("job.panic-test.submit", data)
	}

	publishJob("panic-job-1")
	publishJob("panic-job-2")
	nc.Flush()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(results)
		mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(results) != 2 {
		t.Fatalf("expected 2 results after panic, got %d", len(results))
	}
	for id, res := range results {
		if res.GetStatus() != agentv1.JobStatus_JOB_STATUS_FAILED {
			t.Fatalf("expected failed status for %s, got %v", id, res.GetStatus())
		}
		if !strings.Contains(res.GetErrorMessage(), "handler panic") {
			t.Fatalf("expected panic error message for %s, got %q", id, res.GetErrorMessage())
		}
	}
}

func TestWorker_Close_GracefulShutdown(t *testing.T) {
	_, natsURL := startTestNATS(t)

	w, err := NewWorker(Config{
		Type:           "shutdown-test",
		NatsURL:        natsURL,
		WorkerID:       "shutdown-worker",
		HeartbeatEvery: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	runDone := make(chan struct{})
	go func() {
		_ = w.Run(ctx, func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
			return &agentv1.JobResult{JobId: req.GetJobId(), Status: agentv1.JobStatus_JOB_STATUS_SUCCEEDED}, nil
		})
		close(runDone)
	}()

	time.Sleep(100 * time.Millisecond)

	// Close should work
	if err := w.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	cancel() // Cancel context to stop Run

	select {
	case <-runDone:
		// OK
	case <-time.After(2 * time.Second):
		t.Error("Run did not exit after Close and cancel")
	}
}

func TestWorker_Close_DoubleClose(t *testing.T) {
	_, natsURL := startTestNATS(t)

	w, err := NewWorker(Config{
		Type:     "double-close",
		NatsURL:  natsURL,
		WorkerID: "double-close-worker",
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Errorf("first Close failed: %v", err)
	}

	// Second close should not panic
	if err := w.Close(); err != nil {
		t.Logf("second Close returned error (may be expected): %v", err)
	}
}

func TestWorker_HeartbeatEmission(t *testing.T) {
	_, natsURL := startTestNATS(t)

	w, err := NewWorker(Config{
		Type:            "heartbeat-test",
		NatsURL:         natsURL,
		WorkerID:        "heartbeat-worker",
		Pool:            "test-pool",
		MaxParallelJobs: 3,
		HeartbeatEvery:  50 * time.Millisecond, // Fast heartbeat for testing
	})
	if err != nil {
		t.Fatalf("NewWorker failed: %v", err)
	}
	defer w.Close()

	nc := testNATSConn(t, natsURL)

	var heartbeatCount atomic.Int32
	var lastHeartbeat *agentv1.Heartbeat
	var mu sync.Mutex

	sub, err := nc.Subscribe(SubjectHeartbeat, func(msg *nats.Msg) {
		var pkt agentv1.BusPacket
		if err := proto.Unmarshal(msg.Data, &pkt); err != nil {
			return
		}
		if hb := pkt.GetHeartbeat(); hb != nil {
			mu.Lock()
			lastHeartbeat = hb
			mu.Unlock()
			heartbeatCount.Add(1)
		}
	})
	if err != nil {
		t.Fatalf("subscribe to heartbeat: %v", err)
	}
	defer sub.Unsubscribe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = w.Run(ctx, func(ctx context.Context, req *agentv1.JobRequest) (*agentv1.JobResult, error) {
			return &agentv1.JobResult{JobId: req.GetJobId(), Status: agentv1.JobStatus_JOB_STATUS_SUCCEEDED}, nil
		})
	}()

	// Wait for multiple heartbeats
	time.Sleep(200 * time.Millisecond)

	count := heartbeatCount.Load()
	if count < 2 {
		t.Errorf("expected at least 2 heartbeats, got %d", count)
	}

	mu.Lock()
	defer mu.Unlock()
	if lastHeartbeat == nil {
		t.Fatal("no heartbeat received")
	}
	if lastHeartbeat.GetWorkerId() != "heartbeat-worker" {
		t.Errorf("expected worker id 'heartbeat-worker', got %q", lastHeartbeat.GetWorkerId())
	}
	if lastHeartbeat.GetPool() != "test-pool" {
		t.Errorf("expected pool 'test-pool', got %q", lastHeartbeat.GetPool())
	}
	if lastHeartbeat.GetMaxParallelJobs() != 3 {
		t.Errorf("expected max parallel 3, got %d", lastHeartbeat.GetMaxParallelJobs())
	}
}
