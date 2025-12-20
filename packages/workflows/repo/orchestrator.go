package repo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/yaront1111/coretex-os/core/controlplane/scheduler"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	repoOrchestratorWorkerID   = "worker-repo-orchestrator-1"
	repoOrchestratorQueueGroup = "workers-repo-orchestrator"
	repoWorkflowSubject        = "job.workflow.repo.code_review"
	repoHeartbeatSubject       = "sys.heartbeat"
	defaultChildTimeout        = 4 * time.Minute
	defaultTotalTimeout        = 20 * time.Minute
	childPollInterval          = 400 * time.Millisecond
	childRetryBackoff          = 1 * time.Second
)

var topicChildTimeouts = map[string]time.Duration{
	"job.code.llm": 20 * time.Minute,
}

var activeJobs int32
var cancelMu sync.Mutex
var activeCancels = map[string]context.CancelFunc{}

type repoWorkflowContext struct {
	RepoURL      string   `json:"repo_url"`
	Branch       string   `json:"branch"`
	LocalPath    string   `json:"local_path"`
	IncludeGlobs []string `json:"include_globs"`
	ExcludeGlobs []string `json:"exclude_globs"`
	MaxFiles     int      `json:"max_files"`
	BatchSize    int      `json:"batch_size"`
	MaxBatches   int      `json:"max_batches"`
	RunTests     bool     `json:"run_tests"`
	TestCommand  string   `json:"test_command"`
}

type partitionResult struct {
	Batches []struct {
		BatchID string   `json:"batch_id"`
		Files   []string `json:"files"`
	} `json:"batches"`
	Skipped []string `json:"skipped"`
}

type scanResult struct {
	RepoRoot   string `json:"repo_root"`
	ArchivePtr string `json:"archive_ptr"`
	Files      []struct {
		Path     string `json:"path"`
		Language string `json:"language"`
		Bytes    int64  `json:"bytes"`
		Loc      int64  `json:"loc"`
	} `json:"files"`
}

type lintResult struct {
	BatchID  string `json:"batch_id"`
	Findings []any  `json:"findings"`
}

type fileOutcome struct {
	FilePath       string
	PatchPtr       string
	Analysis       json.RawMessage
	ExplanationPtr string
	Language       string
}

type repoConfig struct {
	MaxFiles    int
	BatchSize   int
	MaxBatches  int
	RunTests    bool
	TestCommand string
}

// Run starts the repo orchestrator worker.
func Run() {
	log.Println("coretex worker repo orchestrator starting...")

	cfg := config.Load()
	memStore, err := memory.NewRedisStore(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to connect to Redis: %v", err)
	}
	defer memStore.Close()

	jobStore, err := memory.NewRedisJobStore(cfg.RedisURL)
	if err != nil {
		log.Fatalf("failed to connect to Redis for job store: %v", err)
	}
	defer jobStore.Close()

	natsBus, err := bus.NewNatsBus(cfg.NatsURL)
	if err != nil {
		log.Fatalf("failed to connect to NATS: %v", err)
	}
	defer natsBus.Close()

	if err := natsBus.Subscribe("sys.job.cancel", "", handleCancelPacket()); err != nil {
		log.Fatalf("failed to subscribe to job cancel: %v", err)
	}

	if err := natsBus.Subscribe(repoWorkflowSubject, repoOrchestratorQueueGroup, handleWorkflow(natsBus, memStore, jobStore, cfg)); err != nil {
		log.Fatalf("failed to subscribe to repo workflow: %v", err)
	}
	if direct := bus.DirectSubject(repoOrchestratorWorkerID); direct != "" {
		if err := natsBus.Subscribe(direct, "", handleWorkflow(natsBus, memStore, jobStore, cfg)); err != nil {
			log.Fatalf("failed to subscribe to direct repo workflow: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendHeartbeats(ctx, natsBus)
	}()

	log.Println("worker repo-orchestrator running. waiting for jobs...")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	cancel()
	wg.Wait()
}

func handleCancelPacket() func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil || req.GetJobId() == "" {
			return
		}
		cancelMu.Lock()
		cancel := activeCancels[req.GetJobId()]
		cancelMu.Unlock()
		if cancel != nil {
			log.Printf("[WORKFLOW repo] cancelling job_id=%s reason=%s", req.GetJobId(), req.GetEnv()["cancel_reason"])
			cancel()
		}
	}
}

func registerCancel(jobID string, cancel context.CancelFunc) {
	if jobID == "" || cancel == nil {
		return
	}
	cancelMu.Lock()
	activeCancels[jobID] = cancel
	cancelMu.Unlock()
}

func clearCancel(jobID string) {
	if jobID == "" {
		return
	}
	cancelMu.Lock()
	delete(activeCancels, jobID)
	cancelMu.Unlock()
}

func handleWorkflow(b *bus.NatsBus, store memory.Store, jobStore scheduler.JobStore, cfg *config.Config) func(*pb.BusPacket) {
	return func(packet *pb.BusPacket) {
		req := packet.GetJobRequest()
		if req == nil {
			return
		}
		atomic.AddInt32(&activeJobs, 1)
		defer atomic.AddInt32(&activeJobs, -1)

		traceID := packet.TraceId
		ctx, cancel := context.WithTimeout(context.Background(), defaultTotalTimeout)
		registerCancel(req.JobId, cancel)
		defer cancel()
		defer clearCancel(req.JobId)

		wcfg := loadRepoConfig()

		parentCtx, err := loadRepoWorkflowContext(ctx, store, req)
		if err != nil {
			failParent(ctx, b, jobStore, store, req, traceID, fmt.Errorf("invalid context: %w", err))
			return
		}
		merged := mergeConfig(parentCtx, wcfg)

		// Step 1: Scan
		scanPtr, err := runChildWithContext(ctx, b, jobStore, store, traceID, req, "job.repo.scan", parentCtx)
		if err != nil {
			failParent(ctx, b, jobStore, store, req, traceID, fmt.Errorf("scan failed: %w", err))
			return
		}
		scanRes, err := loadScanResult(ctx, store, scanPtr)
		if err != nil {
			failParent(ctx, b, jobStore, store, req, traceID, fmt.Errorf("scan result: %w", err))
			return
		}

		// Hydrate repo locally for downstream steps
		localRoot, cleanup, err := hydrateRepo(ctx, store, scanRes)
		if err != nil {
			failParent(ctx, b, jobStore, store, req, traceID, fmt.Errorf("hydrate repo: %w", err))
			return
		}
		defer func() {
			if cleanup != nil {
				cleanup()
			}
		}()
		scanRes.RepoRoot = localRoot

		// Optional planner: prioritize files
		priorityFiles := []string{}
		if cfg.UsePlanner {
			priorityFiles = requestPlan(ctx, b, jobStore, store, traceID, req, cfg.PlannerTopic, scanRes, merged)
		}

		// SAST step (heuristic scan)
		sastCtx := map[string]any{
			"repo_root": localRoot,
		}
		if len(priorityFiles) > 0 {
			sastCtx["files"] = priorityFiles
		} else {
			var all []string
			for _, f := range scanRes.Files {
				all = append(all, f.Path)
			}
			sastCtx["files"] = all
		}
		sastPtr, err := runChildWithContext(ctx, b, jobStore, store, traceID, req, "job.repo.sast", sastCtx)
		if err != nil {
			failParent(ctx, b, jobStore, store, req, traceID, fmt.Errorf("sast failed: %w", err))
			return
		}

		// Step 2: Partition
		partCtx := map[string]any{
			"repo_root":  localRoot,
			"files":      scanRes.Files,
			"max_files":  merged.MaxFiles,
			"batch_size": merged.BatchSize,
			"strategy":   "risk_first",
		}
		if len(priorityFiles) > 0 {
			partCtx["priority_files"] = priorityFiles
		}
		partPtr, err := runChildWithContext(ctx, b, jobStore, store, traceID, req, "job.repo.partition", partCtx)
		if err != nil {
			failParent(ctx, b, jobStore, store, req, traceID, fmt.Errorf("partition failed: %w", err))
			return
		}
		partRes, err := loadPartitionResult(ctx, store, partPtr)
		if err != nil {
			failParent(ctx, b, jobStore, store, req, traceID, fmt.Errorf("partition result: %w", err))
			return
		}

		fileOutcomes := []fileOutcome{}
		processedBatches := 0

		for _, batch := range partRes.Batches {
			if merged.MaxBatches > 0 && processedBatches >= merged.MaxBatches {
				break
			}
			processedBatches++
			filesForBatch := selectFiles(scanRes.Files, batch.Files)
			if len(filesForBatch) == 0 {
				continue
			}

			// Optional lint per batch (Go only)
			if hasLanguage(filesForBatch, "go") {
				lintCtx := map[string]any{
					"repo_root": scanRes.RepoRoot,
					"batch_id":  batch.BatchID,
					"files":     batch.Files,
					"language":  "go",
				}
				if _, err := runChildWithContext(ctx, b, jobStore, store, traceID, req, "job.repo.lint", lintCtx); err != nil {
					// Continue but record failure in report summary
					log.Printf("[WORKFLOW repo] lint failed batch=%s err=%v", batch.BatchID, err)
				}
			}

			for _, f := range filesForBatch {
				// Skip files where language is unknown to avoid wasting review cycles on docs/binaries.
				if strings.EqualFold(f.Language, "unknown") {
					log.Printf("[WORKFLOW repo] skip file with unknown language path=%s", f.Path)
					continue
				}
				outcome, err := reviewFile(ctx, b, jobStore, store, traceID, req, scanRes.RepoRoot, f)
				if err != nil {
					failParent(ctx, b, jobStore, store, req, traceID, fmt.Errorf("review file %s: %w", f.Path, err))
					return
				}
				fileOutcomes = append(fileOutcomes, outcome)
				if merged.MaxFiles > 0 && len(fileOutcomes) >= merged.MaxFiles {
					break
				}
			}
			if merged.MaxFiles > 0 && len(fileOutcomes) >= merged.MaxFiles {
				break
			}
		}

		var testsPtr string
		if merged.RunTests {
			testsCtx := map[string]any{
				"repo_root":    localRoot,
				"test_command": merged.TestCommand,
			}
			ptr, err := runChildWithContext(ctx, b, jobStore, store, traceID, req, "job.repo.tests", testsCtx)
			if err != nil {
				log.Printf("[WORKFLOW repo] tests failed: %v", err)
			} else {
				testsPtr = ptr
			}
		}

		// Report
		reportCtx := map[string]any{
			"repo_root": scanRes.RepoRoot,
			"tests_ptr": testsPtr,
			"sast_ptr":  sastPtr,
		}
		filesCtx := []map[string]any{}
		for _, fo := range fileOutcomes {
			filesCtx = append(filesCtx, map[string]any{
				"file_path":       fo.FilePath,
				"patch_ptr":       fo.PatchPtr,
				"analysis":        fo.Analysis,
				"explanation_ptr": fo.ExplanationPtr,
			})
		}
		reportCtx["files"] = filesCtx

		reportPtr, err := runChildWithContext(ctx, b, jobStore, store, traceID, req, "job.repo.report", reportCtx)
		if err != nil {
			failParent(ctx, b, jobStore, store, req, traceID, fmt.Errorf("report failed: %w", err))
			return
		}

		completeParent(ctx, b, jobStore, req, traceID, reportPtr)
	}
}

func loadRepoWorkflowContext(ctx context.Context, store memory.Store, req *pb.JobRequest) (*repoWorkflowContext, error) {
	key, err := memory.KeyFromPointer(req.GetContextPtr())
	if err != nil {
		return nil, err
	}
	data, err := store.GetContext(ctx, key)
	if err != nil {
		return nil, err
	}
	var payload repoWorkflowContext
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.LocalPath == "" && payload.RepoURL == "" {
		return nil, fmt.Errorf("repo_url or local_path required")
	}
	return &payload, nil
}

func loadScanResult(ctx context.Context, store memory.Store, ptr string) (*scanResult, error) {
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return nil, err
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		return nil, err
	}
	var res scanResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

func loadPartitionResult(ctx context.Context, store memory.Store, ptr string) (*partitionResult, error) {
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return nil, err
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		return nil, err
	}
	var res partitionResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

type planResult struct {
	Workflow string   `json:"workflow"`
	Steps    []any    `json:"steps,omitempty"`
	Files    []string `json:"files,omitempty"`
}

func requestPlan(ctx context.Context, b *bus.NatsBus, jobStore scheduler.JobStore, store memory.Store, traceID string, parentReq *pb.JobRequest, plannerTopic string, scanRes *scanResult, cfg repoConfig) []string {
	if scanRes == nil {
		return nil
	}
	payload := map[string]any{
		"workflow":    "repo_code_review",
		"files":       scanRes.Files,
		"max_files":   cfg.MaxFiles,
		"max_batches": cfg.MaxBatches,
	}
	ptr, err := runChildWithContext(ctx, b, jobStore, store, traceID, parentReq, plannerTopic, payload)
	if err != nil {
		log.Printf("[WORKFLOW repo] planner failed: %v", err)
		return nil
	}
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return nil
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		log.Printf("[WORKFLOW repo] planner result missing: %v", err)
		return nil
	}
	var res planResult
	if err := json.Unmarshal(data, &res); err != nil {
		log.Printf("[WORKFLOW repo] planner result decode failed: %v", err)
		return nil
	}
	return res.Files
}

func hydrateRepo(ctx context.Context, store memory.Store, res *scanResult) (string, func(), error) {
	if res == nil {
		return "", nil, fmt.Errorf("nil scan result")
	}
	if res.ArchivePtr == "" {
		if res.RepoRoot != "" {
			if _, err := os.Stat(res.RepoRoot); err == nil {
				return res.RepoRoot, nil, nil
			}
		}
		return "", nil, fmt.Errorf("missing archive_ptr and repo_root not accessible")
	}
	key, err := memory.KeyFromPointer(res.ArchivePtr)
	if err != nil {
		return "", nil, err
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		return "", nil, err
	}
	if len(data) < 256 {
		return "", nil, fmt.Errorf("repo archive too small (%d bytes)", len(data))
	}
	tmpDir, err := os.MkdirTemp("", "coretex-repo-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}

	gzr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		cleanup()
		return "", nil, err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return "", nil, err
		}
		target := filepath.Join(tmpDir, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				cleanup()
				return "", nil, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				cleanup()
				return "", nil, err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				cleanup()
				return "", nil, err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				cleanup()
				return "", nil, err
			}
			out.Close()
		}
	}
	return tmpDir, cleanup, nil
}

func reviewFile(ctx context.Context, b *bus.NatsBus, jobStore scheduler.JobStore, store memory.Store, traceID string, parentReq *pb.JobRequest, repoRoot string, f struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Bytes    int64  `json:"bytes"`
	Loc      int64  `json:"loc"`
}) (fileOutcome, error) {
	filePath := filepath.Join(repoRoot, filepath.FromSlash(f.Path))
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fileOutcome{}, err
	}
	codeCtx := map[string]any{
		"file_path":    f.Path,
		"code_snippet": string(content),
		"instruction":  "Review this code for bugs, edge cases, and readability. Suggest minimal safe changes.",
	}
	codePtr, err := runChildWithContext(ctx, b, jobStore, store, traceID, parentReq, "job.code.llm", codeCtx)
	if err != nil {
		return fileOutcome{}, err
	}

	patchData, err := store.GetResult(ctx, mustKeyFromPointer(codePtr))
	if err != nil {
		return fileOutcome{}, err
	}

	explainPrompt := fmt.Sprintf("You are a senior engineer. Explain this patch for %s.\n\nPatch:\n%s", f.Path, extractPatchContent(patchData))
	explainCtx := map[string]any{
		"prompt": explainPrompt,
	}
	explPtr, err := runChildWithContext(ctx, b, jobStore, store, traceID, parentReq, "job.chat.simple", explainCtx)
	if err != nil {
		return fileOutcome{}, err
	}

	return fileOutcome{
		FilePath:       f.Path,
		PatchPtr:       codePtr,
		Analysis:       json.RawMessage(patchData),
		ExplanationPtr: explPtr,
		Language:       f.Language,
	}, nil
}

func extractPatchContent(data []byte) string {
	var patch struct {
		Patch struct {
			Content string `json:"content"`
		} `json:"patch"`
	}
	if err := json.Unmarshal(data, &patch); err != nil {
		return string(data)
	}
	return patch.Patch.Content
}

func childTimeoutForTopic(topic string) time.Duration {
	if d, ok := topicChildTimeouts[topic]; ok {
		return d
	}
	return defaultChildTimeout
}

func runChildWithContext(ctx context.Context, b *bus.NatsBus, jobStore scheduler.JobStore, store memory.Store, traceID string, parentReq *pb.JobRequest, topic string, payload any) (string, error) {
	childID := uuid.NewString()
	ctxBytes, _ := json.Marshal(payload)
	ctxKey := memory.MakeContextKey(childID)
	if err := store.PutContext(ctx, ctxKey, ctxBytes); err != nil {
		return "", err
	}
	childEnv := map[string]string{}
	for k, v := range parentReq.GetEnv() {
		childEnv[k] = v
	}
	childReq := &pb.JobRequest{
		JobId:       childID,
		Topic:       topic,
		Priority:    parentReq.Priority,
		ContextPtr:  memory.PointerForKey(ctxKey),
		Env:         childEnv,
		ParentJobId: parentReq.JobId,
		WorkflowId:  parentReq.WorkflowId,
	}
	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        repoOrchestratorWorkerID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobRequest{
			JobRequest: childReq,
		},
	}
	log.Printf("[WORKFLOW repo] dispatch child topic=%s job_id=%s", topic, childID)
	if err := b.Publish("sys.job.submit", packet); err != nil {
		return "", err
	}
	timeout := childTimeoutForTopic(topic)
	if err := waitForChild(ctx, jobStore, childID, topic, timeout); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			publishCancel(b, childID, err.Error())
		}
		return "", err
	}
	ptr, _ := jobStore.GetResultPtr(ctx, childID)
	return ptr, nil
}

func waitForChild(ctx context.Context, jobStore scheduler.JobStore, childID, topic string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		state, err := jobStore.GetState(ctx, childID)
		if err == nil {
			if state == scheduler.JobStateSucceeded {
				return nil
			}
			if state == scheduler.JobStateCancelled {
				return fmt.Errorf("child job cancelled: %w", context.Canceled)
			}
			if state == scheduler.JobStateTimeout {
				return fmt.Errorf("child job timeout: %w", context.DeadlineExceeded)
			}
			if state == scheduler.JobStateFailed || state == scheduler.JobStateDenied {
				return fmt.Errorf("child job failed state=%s", state)
			}
		}
		time.Sleep(childPollInterval)
	}
	return fmt.Errorf("child job timeout topic=%s after %s: %w", topic, timeout, context.DeadlineExceeded)
}

func publishCancel(b *bus.NatsBus, jobID, reason string) {
	if b == nil || jobID == "" {
		return
	}
	cancelReq := &pb.JobRequest{
		JobId: jobID,
		Topic: "sys.job.cancel",
		Env: map[string]string{
			"cancel_reason": reason,
		},
	}
	packet := &pb.BusPacket{
		TraceId:         jobID,
		SenderId:        repoOrchestratorWorkerID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload:         &pb.BusPacket_JobRequest{JobRequest: cancelReq},
	}
	_ = b.Publish("sys.job.cancel", packet)
}

func selectFiles(all []struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Bytes    int64  `json:"bytes"`
	Loc      int64  `json:"loc"`
}, names []string) []struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Bytes    int64  `json:"bytes"`
	Loc      int64  `json:"loc"`
} {
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	var out []struct {
		Path     string `json:"path"`
		Language string `json:"language"`
		Bytes    int64  `json:"bytes"`
		Loc      int64  `json:"loc"`
	}
	for _, f := range all {
		if nameSet[f.Path] {
			out = append(out, f)
		}
	}
	return out
}

func hasLanguage(files []struct {
	Path     string `json:"path"`
	Language string `json:"language"`
	Bytes    int64  `json:"bytes"`
	Loc      int64  `json:"loc"`
}, lang string) bool {
	for _, f := range files {
		if strings.EqualFold(f.Language, lang) {
			return true
		}
	}
	return false
}

func mergeConfig(ctx *repoWorkflowContext, defaults repoConfig) repoConfig {
	res := defaults
	if ctx.MaxFiles > 0 {
		res.MaxFiles = ctx.MaxFiles
	}
	if ctx.BatchSize > 0 {
		res.BatchSize = ctx.BatchSize
	}
	if ctx.MaxBatches > 0 {
		res.MaxBatches = ctx.MaxBatches
	}
	if ctx.TestCommand != "" {
		res.TestCommand = ctx.TestCommand
	}
	res.RunTests = ctx.RunTests
	return res
}

func loadRepoConfig() repoConfig {
	maxFiles := envInt("REPO_REVIEW_MAX_FILES", 500)
	batchSize := envInt("REPO_REVIEW_BATCH_SIZE", 50)
	maxBatches := envInt("REPO_REVIEW_MAX_BATCHES", 5)
	runTests := os.Getenv("REPO_REVIEW_RUN_TESTS") == "true"
	testCmd := os.Getenv("REPO_REVIEW_TEST_COMMAND")
	if testCmd == "" {
		testCmd = "go test ./..."
	}
	return repoConfig{
		MaxFiles:    maxFiles,
		BatchSize:   batchSize,
		MaxBatches:  maxBatches,
		RunTests:    runTests,
		TestCommand: testCmd,
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func failParent(ctx context.Context, b *bus.NatsBus, jobStore scheduler.JobStore, store memory.Store, req *pb.JobRequest, traceID string, err error) {
	log.Printf("[WORKFLOW repo] failing parent job_id=%s error=%v", req.JobId, err)
	errPayload := map[string]any{
		"error":    err.Error(),
		"job_id":   req.JobId,
		"trace_id": traceID,
		"time":     time.Now().UTC().Format(time.RFC3339),
	}
	errBytes, _ := json.Marshal(errPayload)
	resKey := memory.MakeResultKey(req.JobId)
	resultPtr := ""
	if err := store.PutResult(ctx, resKey, errBytes); err == nil {
		resultPtr = memory.PointerForKey(resKey)
		_ = jobStore.SetResultPtr(ctx, req.JobId, resultPtr)
	}

	status := pb.JobStatus_JOB_STATUS_FAILED
	state := scheduler.JobStateFailed
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		status = pb.JobStatus_JOB_STATUS_CANCELLED
		state = scheduler.JobStateCancelled
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		status = pb.JobStatus_JOB_STATUS_TIMEOUT
		state = scheduler.JobStateTimeout
	}
	res := &pb.JobResult{
		JobId:        req.JobId,
		Status:       status,
		ResultPtr:    resultPtr,
		WorkerId:     repoOrchestratorWorkerID,
		ErrorMessage: err.Error(),
	}
	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        repoOrchestratorWorkerID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobResult{
			JobResult: res,
		},
	}
	_ = jobStore.SetState(ctx, req.JobId, state)
	_ = b.Publish("sys.job.result", packet)
}

func completeParent(ctx context.Context, b *bus.NatsBus, jobStore scheduler.JobStore, req *pb.JobRequest, traceID, resultPtr string) {
	res := &pb.JobResult{
		JobId:     req.JobId,
		Status:    pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr: resultPtr,
		WorkerId:  repoOrchestratorWorkerID,
	}
	packet := &pb.BusPacket{
		TraceId:         traceID,
		SenderId:        repoOrchestratorWorkerID,
		CreatedAt:       timestamppb.Now(),
		ProtocolVersion: 1,
		Payload: &pb.BusPacket_JobResult{
			JobResult: res,
		},
	}
	_ = jobStore.SetResultPtr(ctx, req.JobId, resultPtr)
	_ = jobStore.SetState(ctx, req.JobId, scheduler.JobStateSucceeded)
	if err := b.Publish("sys.job.result", packet); err != nil {
		log.Printf("[WORKFLOW repo] failed to publish parent result: %v", err)
	}
}

func sendHeartbeats(ctx context.Context, b *bus.NatsBus) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			hb := &pb.Heartbeat{
				WorkerId:        repoOrchestratorWorkerID,
				Region:          "local",
				Type:            "cpu",
				CpuLoad:         10,
				GpuUtilization:  0,
				ActiveJobs:      atomic.LoadInt32(&activeJobs),
				Capabilities:    []string{"workflow"},
				Pool:            "workflow-repo",
				MaxParallelJobs: 1,
			}
			packet := &pb.BusPacket{
				TraceId:         "hb-" + repoOrchestratorWorkerID,
				SenderId:        repoOrchestratorWorkerID,
				CreatedAt:       timestamppb.Now(),
				ProtocolVersion: 1,
				Payload: &pb.BusPacket_Heartbeat{
					Heartbeat: hb,
				},
			}
			if err := b.Publish(repoHeartbeatSubject, packet); err != nil {
				log.Printf("[WORKFLOW repo] failed to publish heartbeat: %v", err)
			}
		}
	}
}

func mustKeyFromPointer(ptr string) string {
	key, _ := memory.KeyFromPointer(ptr)
	return key
}
