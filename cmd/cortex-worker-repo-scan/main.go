package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"github.com/yaront1111/cortex-os/core/pkg/sdk/worker"
)

const (
	repoScanWorkerID = "worker-repo-scan-1"
)

type scanContext struct {
	RepoURL      string   `json:"repo_url"`
	Branch       string   `json:"branch"`
	LocalPath    string   `json:"local_path"`
	IncludeGlobs []string `json:"include_globs"`
	ExcludeGlobs []string `json:"exclude_globs"`
}

type scanResult struct {
	RepoRoot string       `json:"repo_root"`
	Files    []fileRecord `json:"files"`
}

type fileRecord struct {
	Path          string `json:"path"`
	Language      string `json:"language"`
	Bytes         int64  `json:"bytes"`
	Loc           int64  `json:"loc"`
	RecentCommits int    `json:"recent_commits"`
}

func main() {
	log.Println("cortex worker repo-scan starting...")

	cfg := config.Load()

	wConfig := worker.Config{
		WorkerID:        repoScanWorkerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      "workers-repo-scan",
		JobSubject:      "job.repo.scan",
		HeartbeatSub:    "sys.heartbeat.repo-scan",
		Capabilities:    []string{"repo-scan"},
		Pool:            "repo-scan",
		MaxParallelJobs: 1,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(scanHandler); err != nil {
		log.Fatalf("worker repo-scan failed: %v", err)
	}
}

func scanHandler(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
	payload, err := loadScanContext(ctx, req, store)
	if err != nil {
		return failResult(req, err), err
	}

	repoRoot, cleanup, err := ensureRepo(ctx, payload)
	if err != nil {
		return failResult(req, err), err
	}
	// Do not defer cleanup to allow downstream reuse; only used for temp clones.
	if cleanup != nil {
		defer cleanup()
	}

	files, err := indexRepo(ctx, repoRoot, payload.IncludeGlobs, payload.ExcludeGlobs)
	if err != nil {
		return failResult(req, err), err
	}

	result := scanResult{
		RepoRoot: repoRoot,
		Files:    files,
	}
	resBytes, _ := json.Marshal(result)
	resKey := memory.MakeResultKey(req.JobId)
	if err := store.PutResult(ctx, resKey, resBytes); err != nil {
		return failResult(req, fmt.Errorf("store result: %w", err)), err
	}

	return &pb.JobResult{
		JobId:       req.JobId,
		Status:      pb.JobStatus_JOB_STATUS_COMPLETED,
		ResultPtr:   memory.PointerForKey(resKey),
		ExecutionMs: 0,
		WorkerId:    repoScanWorkerID,
	}, nil
}

func loadScanContext(ctx context.Context, req *pb.JobRequest, store memory.Store) (*scanContext, error) {
	if req == nil || req.ContextPtr == "" {
		return nil, errors.New("missing context_ptr")
	}
	key, err := memory.KeyFromPointer(req.ContextPtr)
	if err != nil {
		return nil, err
	}
	data, err := store.GetContext(ctx, key)
	if err != nil {
		return nil, err
	}
	var payload scanContext
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.LocalPath == "" && payload.RepoURL == "" {
		return nil, errors.New("local_path or repo_url required")
	}
	return &payload, nil
}

func ensureRepo(ctx context.Context, payload *scanContext) (string, func(), error) {
	if payload.LocalPath != "" {
		return payload.LocalPath, nil, nil
	}
	branch := payload.Branch
	if branch == "" {
		branch = "main"
	}
	tempDir, err := os.MkdirTemp("", "cortex-repo-*")
	if err != nil {
		return "", nil, err
	}
	args := []string{"clone", "--depth", "1", "--branch", branch, payload.RepoURL, tempDir}
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", func() { os.RemoveAll(tempDir) }, fmt.Errorf("git clone failed: %v %s", err, string(out))
	}
	return tempDir, func() { os.RemoveAll(tempDir) }, nil
}

func indexRepo(ctx context.Context, root string, includeGlobs, excludeGlobs []string) ([]fileRecord, error) {
	var records []fileRecord
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if shouldSkip(rel, includeGlobs, excludeGlobs) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		lang := detectLanguage(rel)
		loc, err := countLOC(path)
		if err != nil {
			return err
		}
		records = append(records, fileRecord{
			Path:          filepath.ToSlash(rel),
			Language:      lang,
			Bytes:         info.Size(),
			Loc:           loc,
			RecentCommits: 0, // optional: can be filled by git history in future
		})
		return nil
	})
	return records, err
}

func shouldSkip(rel string, includes, excludes []string) bool {
	rel = filepath.ToSlash(rel)
	for _, ex := range excludes {
		if globMatch(ex, rel) {
			return true
		}
	}
	if len(includes) == 0 {
		return false
	}
	for _, inc := range includes {
		if globMatch(inc, rel) {
			return false
		}
	}
	return true
}

func globMatch(pattern, rel string) bool {
	// Basic glob match; supports * and ** segments crudely.
	pattern = strings.ReplaceAll(pattern, "\\", "/")
	rel = strings.ReplaceAll(rel, "\\", "/")
	ok, _ := filepath.Match(pattern, rel)
	if ok {
		return true
	}
	// Fallback: if pattern has "**", allow substring containment checks.
	if strings.Contains(pattern, "**") {
		p := strings.ReplaceAll(pattern, "**", "")
		return strings.Contains(rel, p)
	}
	return false
}

func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".cs":
		return "csharp"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hxx":
		return "cpp"
	default:
		return "unknown"
	}
}

func countLOC(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return int64(strings.Count(string(data), "\n") + 1), nil
}

func failResult(req *pb.JobRequest, err error) *pb.JobResult {
	return &pb.JobResult{
		JobId:       req.GetJobId(),
		Status:      pb.JobStatus_JOB_STATUS_FAILED,
		WorkerId:    repoScanWorkerID,
		ResultPtr:   "",
		ExecutionMs: 0,
		// error is logged by handler caller
	}
}
