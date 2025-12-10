package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/yaront1111/cortex-os/core/internal/infrastructure/config"
	"github.com/yaront1111/cortex-os/core/internal/infrastructure/memory"
	pb "github.com/yaront1111/cortex-os/core/pkg/pb/v1"
	"github.com/yaront1111/cortex-os/core/pkg/sdk/worker"
)

const (
	repoLintWorkerID = "worker-repo-lint-1"
)

type lintContext struct {
	RepoRoot string   `json:"repo_root"`
	BatchID  string   `json:"batch_id"`
	Files    []string `json:"files"`
	Language string   `json:"language"`
}

type lintResult struct {
	BatchID  string        `json:"batch_id"`
	Findings []lintFinding `json:"findings"`
}

type lintFinding struct {
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

var goVetPattern = regexp.MustCompile(`^(?P<file>[^:]+):(?P<line>\d+):(?P<col>\d+):\s+(?P<msg>.+)$`)

func main() {
	log.Println("cortex worker repo-lint starting...")

	cfg := config.Load()

	wConfig := worker.Config{
		WorkerID:        repoLintWorkerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      "workers-repo-lint",
		JobSubject:      "job.repo.lint",
		HeartbeatSub:    "sys.heartbeat.repo-lint",
		Capabilities:    []string{"repo-lint"},
		Pool:            "repo-lint",
		MaxParallelJobs: 1,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(lintHandler); err != nil {
		log.Fatalf("worker repo-lint failed: %v", err)
	}
}

func lintHandler(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
	payload, err := loadLintContext(ctx, req, store)
	if err != nil {
		return failResult(req), err
	}

	var findings []lintFinding
	switch strings.ToLower(payload.Language) {
	case "go":
		findings, err = runGoVet(ctx, payload.RepoRoot, payload.Files)
	default:
		// Unsupported language: return empty findings, but not an error.
		findings = nil
	}
	if err != nil {
		return failResult(req), err
	}

	result := lintResult{
		BatchID:  payload.BatchID,
		Findings: findings,
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
		WorkerId:    repoLintWorkerID,
		ExecutionMs: 0,
	}, nil
}

func loadLintContext(ctx context.Context, req *pb.JobRequest, store memory.Store) (*lintContext, error) {
	key, err := memory.KeyFromPointer(req.GetContextPtr())
	if err != nil {
		return nil, err
	}
	data, err := store.GetContext(ctx, key)
	if err != nil {
		return nil, err
	}
	var payload lintContext
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func runGoVet(ctx context.Context, repoRoot string, files []string) ([]lintFinding, error) {
	cmd := exec.CommandContext(ctx, "go", "vet", "./...")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		return nil, fmt.Errorf("go vet failed: %w", err)
	}
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		fileSet[filepath.Clean(filepath.Join(repoRoot, f))] = true
	}
	findings := parseGoVetOutput(string(output), repoRoot, fileSet)
	return findings, nil
}

func parseGoVetOutput(out string, repoRoot string, fileSet map[string]bool) []lintFinding {
	var findings []lintFinding
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		matches := goVetPattern.FindStringSubmatch(line)
		if len(matches) == 0 {
			continue
		}
		file := matches[1]
		lineNo := toInt(matches[2])
		colNo := toInt(matches[3])
		msg := matches[4]
		full := filepath.Clean(file)
		if len(fileSet) > 0 && !fileSet[full] {
			continue
		}
		rel, err := filepath.Rel(repoRoot, full)
		if err != nil {
			rel = full
		}
		findings = append(findings, lintFinding{
			FilePath: filepath.ToSlash(rel),
			Line:     lineNo,
			Column:   colNo,
			Severity: "warning",
			Rule:     "go_vet",
			Message:  msg,
		})
	}
	return findings
}

func toInt(val string) int {
	i, _ := strconv.Atoi(val)
	return i
}

func failResult(req *pb.JobRequest) *pb.JobResult {
	return &pb.JobResult{
		JobId:    req.GetJobId(),
		Status:   pb.JobStatus_JOB_STATUS_FAILED,
		WorkerId: repoLintWorkerID,
	}
}
