package scan

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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	worker "github.com/yaront1111/coretex-os/core/agent/runtime"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

const (
	repoScanWorkerID = "worker-repo-scan-1"
)

var scanWorkerID = resolveWorkerID(repoScanWorkerID)

type scanContext struct {
	RepoURL      string   `json:"repo_url"`
	Branch       string   `json:"branch"`
	LocalPath    string   `json:"local_path"`
	IncludeGlobs []string `json:"include_globs"`
	ExcludeGlobs []string `json:"exclude_globs"`
}

type scanResult struct {
	RepoRoot   string       `json:"repo_root"`
	Files      []fileRecord `json:"files"`
	ArchivePtr string       `json:"archive_ptr"`
}

type fileRecord struct {
	Path          string `json:"path"`
	Language      string `json:"language"`
	Bytes         int64  `json:"bytes"`
	Loc           int64  `json:"loc"`
	RecentCommits int    `json:"recent_commits"`
}

// Run starts the repo-scan worker.
func Run() {
	log.Println("coretex worker repo-scan starting...")

	cfg := config.Load()

	wConfig := worker.Config{
		WorkerID:        scanWorkerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      "workers-repo-scan",
		JobSubject:      "job.repo.scan",
		HeartbeatSub:    "sys.heartbeat",
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
	log.Printf("[WORKER %s] start job_id=%s topic=%s", scanWorkerID, req.GetJobId(), req.GetTopic())
	payload, err := loadScanContext(ctx, req, store)
	if err != nil {
		log.Printf("[WORKER %s] invalid context job_id=%s err=%v", scanWorkerID, req.GetJobId(), err)
		return failResult(req, err), err
	}

	repoRoot, _, err := ensureRepo(ctx, payload)
	if err != nil {
		log.Printf("[WORKER %s] clone/ensure failed job_id=%s err=%v", scanWorkerID, req.GetJobId(), err)
		return failResult(req, err), err
	}
	// Intentionally keep the clone on disk so downstream steps (partition, lint, etc.)
	// can read the same filesystem path. A later workflow step should clean it up.

	files, err := indexRepo(ctx, repoRoot, payload.IncludeGlobs, payload.ExcludeGlobs)
	if err != nil {
		log.Printf("[WORKER %s] index failed job_id=%s err=%v", scanWorkerID, req.GetJobId(), err)
		return failResult(req, err), err
	}
	log.Printf("[WORKER %s] indexed job_id=%s repo_root=%s files=%d", scanWorkerID, req.GetJobId(), repoRoot, len(files))

	minFiles := envInt("MIN_SCAN_FILES", 1)
	if len(files) < minFiles {
		err := fmt.Errorf("scan found %d files (<%d); aborting", len(files), minFiles)
		log.Printf("[WORKER %s] %v job_id=%s", scanWorkerID, err, req.GetJobId())
		return failResult(req, err), err
	}

	archivePtr, err := archiveRepo(ctx, store, req.JobId, repoRoot, files)
	if err != nil {
		log.Printf("[WORKER %s] archive failed job_id=%s err=%v", scanWorkerID, req.GetJobId(), err)
		return failResult(req, fmt.Errorf("archive repo: %w", err)), err
	}
	log.Printf("[WORKER %s] archived job_id=%s archive_ptr=%s", scanWorkerID, req.GetJobId(), archivePtr)

	result := scanResult{
		RepoRoot:   repoRoot,
		Files:      files,
		ArchivePtr: archivePtr,
	}
	resBytes, _ := json.Marshal(result)
	resKey := memory.MakeResultKey(req.JobId)
	if err := store.PutResult(ctx, resKey, resBytes); err != nil {
		return failResult(req, fmt.Errorf("store result: %w", err)), err
	}

	return &pb.JobResult{
		JobId:       req.JobId,
		Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr:   memory.PointerForKey(resKey),
		ExecutionMs: 0,
		WorkerId:    scanWorkerID,
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
	tempDir, err := os.MkdirTemp("", "coretex-repo-*")
	if err != nil {
		return "", nil, err
	}
	args := []string{"clone", "--depth", "1", "--branch", branch, payload.RepoURL, tempDir}
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", func() { os.RemoveAll(tempDir) }, fmt.Errorf("git clone failed: %v %s", err, string(out))
	} else if len(out) > 0 {
		log.Printf("[WORKER %s] git clone output repo=%s: %s", scanWorkerID, payload.RepoURL, strings.TrimSpace(string(out)))
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

// archiveRepo stores the scanned files (without .git) as a tar.gz blob in the result store and returns a pointer.
func archiveRepo(ctx context.Context, store memory.Store, jobID, root string, files []fileRecord) (string, error) {
	buf := &bytes.Buffer{}
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)

	for _, f := range files {
		absPath := filepath.Join(root, filepath.FromSlash(f.Path))
		// Skip anything under .git just in case
		if strings.HasPrefix(filepath.ToSlash(f.Path), ".git/") {
			continue
		}
		info, err := os.Stat(absPath)
		if err != nil {
			return "", err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return "", err
		}
		// Ensure header name is the relative path with forward slashes
		hdr.Name = filepath.ToSlash(f.Path)
		if err := tw.WriteHeader(hdr); err != nil {
			return "", err
		}
		file, err := os.Open(absPath)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(tw, file); err != nil {
			file.Close()
			return "", err
		}
		file.Close()
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}

	// Validate archive is non-empty and readable
	if buf.Len() < 256 {
		return "", fmt.Errorf("archive too small (%d bytes)", buf.Len())
	}
	if err := validateArchive(buf.Bytes()); err != nil {
		return "", fmt.Errorf("archive validation failed: %w", err)
	}

	key := memory.MakeResultKey(jobID + "-repo-archive")
	if err := store.PutResult(ctx, key, buf.Bytes()); err != nil {
		return "", err
	}
	return memory.PointerForKey(key), nil
}

func validateArchive(data []byte) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	count := 0
	for {
		_, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
	}
	if count == 0 {
		return errors.New("archive has zero files")
	}
	return nil
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

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func failResult(req *pb.JobRequest, err error) *pb.JobResult {
	return &pb.JobResult{
		JobId:       req.GetJobId(),
		Status:      pb.JobStatus_JOB_STATUS_FAILED,
		WorkerId:    scanWorkerID,
		ResultPtr:   "",
		ExecutionMs: 0,
		// error is logged by handler caller
	}
}
