package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	worker "github.com/yaront1111/coretex-os/core/agent/runtime"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

const (
	repoTestsWorkerID = "worker-repo-tests-1"
)

type testsContext struct {
	RepoRoot    string            `json:"repo_root"`
	TestCommand string            `json:"test_command"`
	Env         map[string]string `json:"env"`
	TimeoutSec  int               `json:"timeout_sec"`
}

type testsResult struct {
	RepoRoot string `json:"repo_root"`
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Failed   bool   `json:"failed"`
	Output   string `json:"output"`
}

// Run starts the repo-tests worker.
func Run() {
	log.Println("coretex worker repo-tests starting...")

	cfg := config.Load()

	wConfig := worker.Config{
		WorkerID:        repoTestsWorkerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      "workers-repo-tests",
		JobSubject:      "job.repo.tests",
		DirectSubject:   bus.DirectSubject(repoTestsWorkerID),
		HeartbeatSub:    "sys.heartbeat",
		Capabilities:    []string{"repo-tests"},
		Pool:            "repo-tests",
		MaxParallelJobs: 1,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(testsHandler); err != nil {
		log.Fatalf("worker repo-tests failed: %v", err)
	}
}

func testsHandler(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
	payload, err := loadTestsContext(ctx, req, store)
	if err != nil {
		return failResult(req), err
	}

	timeout := time.Duration(payload.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", payload.TestCommand)
	cmd.Dir = payload.RepoRoot
	if payload.Env != nil {
		env := cmd.Env
		if len(env) == 0 {
			env = []string{}
		}
		for k, v := range payload.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	result := testsResult{
		RepoRoot: payload.RepoRoot,
		Command:  payload.TestCommand,
		ExitCode: exitCode,
		Failed:   runErr != nil,
		Output:   buf.String(),
	}
	resBytes, _ := json.Marshal(result)
	resKey := memory.MakeResultKey(req.JobId)
	if err := store.PutResult(ctx, resKey, resBytes); err != nil {
		return failResult(req), err
	}

	return &pb.JobResult{
		JobId:       req.JobId,
		Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr:   memory.PointerForKey(resKey),
		WorkerId:    resolveWorkerID(repoTestsWorkerID),
		ExecutionMs: 0,
	}, nil
}

func loadTestsContext(ctx context.Context, req *pb.JobRequest, store memory.Store) (*testsContext, error) {
	key, err := memory.KeyFromPointer(req.GetContextPtr())
	if err != nil {
		return nil, err
	}
	data, err := store.GetContext(ctx, key)
	if err != nil {
		return nil, err
	}
	var payload testsContext
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func failResult(req *pb.JobRequest) *pb.JobResult {
	return &pb.JobResult{
		JobId:    req.GetJobId(),
		Status:   pb.JobStatus_JOB_STATUS_FAILED,
		WorkerId: resolveWorkerID(repoTestsWorkerID),
	}
}

func resolveWorkerID(defaultID string) string {
	if v := os.Getenv("WORKER_ID"); v != "" {
		return v
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		if len(h) > 8 {
			h = h[len(h)-8:]
		}
		return fmt.Sprintf("%s-%s", defaultID, h)
	}
	return defaultID
}
