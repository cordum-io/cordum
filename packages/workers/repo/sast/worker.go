package sast

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	worker "github.com/yaront1111/coretex-os/core/agent/runtime"
	"github.com/yaront1111/coretex-os/core/infra/bus"
	"github.com/yaront1111/coretex-os/core/infra/config"
	"github.com/yaront1111/coretex-os/core/infra/memory"
	pb "github.com/yaront1111/coretex-os/core/protocol/pb/v1"
)

const (
	repoSastWorkerID = "worker-repo-sast-1"
)

type sastContext struct {
	RepoRoot string   `json:"repo_root"`
	Files    []string `json:"files"`
}

type sastFinding struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

type sastResult struct {
	Findings []sastFinding `json:"findings"`
}

// Run starts the repo-sast worker.
func Run() {
	log.Println("coretex worker repo-sast starting...")

	cfg := config.Load()

	wConfig := worker.Config{
		WorkerID:        resolveWorkerID(repoSastWorkerID),
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      "workers-repo-sast",
		JobSubject:      "job.repo.sast",
		DirectSubject:   bus.DirectSubject(repoSastWorkerID),
		HeartbeatSub:    "sys.heartbeat",
		Capabilities:    []string{"repo-sast"},
		Pool:            "repo-sast",
		MaxParallelJobs: 1,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(sastHandler); err != nil {
		log.Fatalf("worker repo-sast failed: %v", err)
	}
}

func sastHandler(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
	payload, err := loadSastContext(ctx, req, store)
	if err != nil {
		log.Printf("[WORKER %s] invalid context job_id=%s err=%v", repoSastWorkerID, req.GetJobId(), err)
		return failResult(req), err
	}

	findings := runHeuristics(payload.RepoRoot, payload.Files)

	res := sastResult{Findings: findings}
	resBytes, _ := json.Marshal(res)
	resKey := memory.MakeResultKey(req.JobId)
	if err := store.PutResult(ctx, resKey, resBytes); err != nil {
		return failResult(req), err
	}

	return &pb.JobResult{
		JobId:       req.JobId,
		Status:      pb.JobStatus_JOB_STATUS_SUCCEEDED,
		ResultPtr:   memory.PointerForKey(resKey),
		WorkerId:    repoSastWorkerID,
		ExecutionMs: 0,
	}, nil
}

func loadSastContext(ctx context.Context, req *pb.JobRequest, store memory.Store) (*sastContext, error) {
	key, err := memory.KeyFromPointer(req.GetContextPtr())
	if err != nil {
		return nil, err
	}
	data, err := store.GetContext(ctx, key)
	if err != nil {
		return nil, err
	}
	var payload sastContext
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.RepoRoot == "" {
		return nil, fmt.Errorf("repo_root required")
	}
	return &payload, nil
}

func runHeuristics(root string, files []string) []sastFinding {
	var findings []sastFinding

	if len(files) == 0 {
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
	}

	for _, rel := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if info, err := os.Stat(abs); err != nil || info.IsDir() || info.Size() > 2*1024*1024 {
			continue
		}
		fs := scanFile(abs)
		for _, f := range fs {
			f.FilePath = rel
			findings = append(findings, f)
		}
	}
	return findings
}

func scanFile(path string) []sastFinding {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	var out []sastFinding
	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if !utf8.ValidString(line) {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "aws_secret_access_key") || strings.Contains(lower, "aws_access_key_id"):
			out = append(out, finding(path, lineNum, "high", "aws_key", "Possible AWS credentials"))
		case strings.Contains(lower, "private key-----") || strings.Contains(lower, "begin rsa"):
			out = append(out, finding(path, lineNum, "high", "private_key", "Possible private key material"))
		case strings.Contains(lower, "password=") || strings.Contains(lower, "secret=") || strings.Contains(lower, "token="):
			out = append(out, finding(path, lineNum, "medium", "hardcoded_secret", "Possible hardcoded secret/token"))
		}
	}
	return out
}

func finding(path string, line int, severity, rule, msg string) sastFinding {
	return sastFinding{
		FilePath: path,
		Line:     line,
		Severity: severity,
		Rule:     rule,
		Message:  msg,
	}
}

func failResult(req *pb.JobRequest) *pb.JobResult {
	return &pb.JobResult{
		JobId:    req.GetJobId(),
		Status:   pb.JobStatus_JOB_STATUS_FAILED,
		WorkerId: repoSastWorkerID,
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
