package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"github.com/yaront1111/cortex-os/core/pkg/sdk/worker"
)

const (
	repoPartitionWorkerID = "worker-repo-partition-1"
)

type partitionContext struct {
	RepoRoot    string       `json:"repo_root"`
	Files       []fileRecord `json:"files"`
	MaxFiles    int          `json:"max_files"`
	BatchSize   int          `json:"batch_size"`
	Strategy    string       `json:"strategy"`
	IncludeOnly []string     `json:"include_only"`
}

type fileRecord struct {
	Path          string `json:"path"`
	Language      string `json:"language"`
	Bytes         int64  `json:"bytes"`
	Loc           int64  `json:"loc"`
	RecentCommits int    `json:"recent_commits"`
}

type partitionResult struct {
	Batches []batch  `json:"batches"`
	Skipped []string `json:"skipped"`
}

type batch struct {
	BatchID string   `json:"batch_id"`
	Files   []string `json:"files"`
}

func main() {
	log.Println("cortex worker repo-partition starting...")

	cfg := config.Load()

	wConfig := worker.Config{
		WorkerID:        repoPartitionWorkerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      "workers-repo-partition",
		JobSubject:      "job.repo.partition",
		HeartbeatSub:    "sys.heartbeat.repo-partition",
		Capabilities:    []string{"repo-partition"},
		Pool:            "repo-partition",
		MaxParallelJobs: 1,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(partitionHandler); err != nil {
		log.Fatalf("worker repo-partition failed: %v", err)
	}
}

func partitionHandler(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
	payload, err := loadPartitionContext(ctx, req, store)
	if err != nil {
		return failResult(req), err
	}
	maxFiles := payload.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 500
	}
	batchSize := payload.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	filtered, skipped := filterFiles(payload.Files, payload.IncludeOnly)
	scored := scoreFiles(filtered)

	if len(scored) > maxFiles {
		scored = scored[:maxFiles]
	}

	batches := make([]batch, 0)
	for i := 0; i < len(scored); i += batchSize {
		end := i + batchSize
		if end > len(scored) {
			end = len(scored)
		}
		files := make([]string, 0, end-i)
		for _, rec := range scored[i:end] {
			files = append(files, rec.Path)
		}
		batches = append(batches, batch{
			BatchID: fmt.Sprintf("batch-%d", len(batches)+1),
			Files:   files,
		})
	}

	result := partitionResult{
		Batches: batches,
		Skipped: skipped,
	}
	resBytes, _ := json.Marshal(result)
	resKey := memory.MakeResultKey(req.JobId)
	if err := store.PutResult(ctx, resKey, resBytes); err != nil {
		return failResult(req), err
	}

	return &pb.JobResult{
		JobId:       req.JobId,
		Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
		ResultPtr:   memory.PointerForKey(resKey),
		WorkerId:    repoPartitionWorkerID,
		ExecutionMs: 0,
	}, nil
}

func loadPartitionContext(ctx context.Context, req *pb.JobRequest, store memory.Store) (*partitionContext, error) {
	key, err := memory.KeyFromPointer(req.GetContextPtr())
	if err != nil {
		return nil, err
	}
	data, err := store.GetContext(ctx, key)
	if err != nil {
		return nil, err
	}
	var payload partitionContext
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

type scoredFile struct {
	fileRecord
	score int64
}

func scoreFiles(files []fileRecord) []fileRecord {
	scored := make([]scoredFile, 0, len(files))
	for _, f := range files {
		score := f.Bytes + f.Loc*2
		if f.RecentCommits > 0 {
			score += int64(f.RecentCommits) * 50
		}
		if isSensitivePath(f.Path) {
			score += 500
		}
		scored = append(scored, scoredFile{fileRecord: f, score: score})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].score > scored[j].score })
	out := make([]fileRecord, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.fileRecord)
	}
	return out
}

func filterFiles(files []fileRecord, includeOnly []string) ([]fileRecord, []string) {
	if len(includeOnly) == 0 {
		return files, nil
	}
	includeSet := make(map[string]bool, len(includeOnly))
	for _, p := range includeOnly {
		includeSet[filepath.ToSlash(p)] = true
	}
	var kept []fileRecord
	var skipped []string
	for _, f := range files {
		path := filepath.ToSlash(f.Path)
		if includeSet[path] {
			kept = append(kept, f)
		} else {
			skipped = append(skipped, path)
		}
	}
	return kept, skipped
}

func isSensitivePath(p string) bool {
	p = strings.ToLower(p)
	sensitive := []string{"auth", "billing", "payment", "crypto", "security", "oauth", "token"}
	for _, s := range sensitive {
		if strings.Contains(p, s) {
			return true
		}
	}
	return false
}

func failResult(req *pb.JobRequest) *pb.JobResult {
	return &pb.JobResult{
		JobId:    req.GetJobId(),
		Status:   pb.JobStatus_JOB_STATUS_FAILED,
		WorkerId: repoPartitionWorkerID,
	}
}
