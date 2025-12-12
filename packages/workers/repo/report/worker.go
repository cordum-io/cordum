package report

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	worker "github.com/yaront1111/cortex-os/core/agent/runtime"
	"github.com/yaront1111/cortex-os/core/infra/config"
	"github.com/yaront1111/cortex-os/core/infra/memory"
	pb "github.com/yaront1111/cortex-os/core/protocol/pb/v1"
)

const (
	repoReportWorkerID = "worker-repo-report-1"
)

type reportContext struct {
	RepoRoot string          `json:"repo_root"`
	Files    []reportFileRef `json:"files"`
	TestsPtr string          `json:"tests_ptr"`
	SastPtr  string          `json:"sast_ptr"`
}

type reportFileRef struct {
	FilePath       string          `json:"file_path"`
	PatchPtr       string          `json:"patch_ptr"`
	Analysis       json.RawMessage `json:"analysis"`
	ExplanationPtr string          `json:"explanation_ptr"`
}

type reportResult struct {
	Summary        string          `json:"summary"`
	ActionRequired bool            `json:"action_required"`
	Decision       string          `json:"decision"`
	Sections       []reportSection `json:"sections"`
	TestsSummary   *testsSummary   `json:"tests_summary,omitempty"`
	SastSummary    *sastSummary    `json:"sast_summary,omitempty"`
}

type reportSection struct {
	Title string        `json:"title"`
	Items []reportEntry `json:"items"`
}

type reportEntry struct {
	FilePath    string `json:"file_path"`
	Description string `json:"description"`
	Severity    string `json:"severity"`
	PatchPtr    string `json:"patch_ptr"`
}

type testsSummary struct {
	Ran     bool   `json:"ran"`
	Failed  bool   `json:"failed"`
	Details string `json:"details"`
}

type codePatch struct {
	FilePath     string `json:"file_path"`
	OriginalCode string `json:"original_code"`
	Instruction  string `json:"instruction"`
	Patch        struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	} `json:"patch"`
	Analysis []struct {
		Type        string `json:"type"`
		Description string `json:"description"`
		Severity    string `json:"severity"`
	} `json:"analysis"`
}

type explanationPayload struct {
	Response string `json:"response"`
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

type sastSummary struct {
	Total  int `json:"total"`
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
}

// Run starts the repo-report worker.
func Run() {
	log.Println("cortex worker repo-report starting...")

	cfg := config.Load()
	workerID := resolveWorkerID(repoReportWorkerID)
	wConfig := worker.Config{
		WorkerID:        workerID,
		NatsURL:         cfg.NatsURL,
		RedisURL:        cfg.RedisURL,
		QueueGroup:      "workers-repo-report",
		JobSubject:      "job.repo.report",
		HeartbeatSub:    "sys.heartbeat",
		Capabilities:    []string{"repo-report"},
		Pool:            "repo-report",
		MaxParallelJobs: 1,
	}

	w, err := worker.New(wConfig)
	if err != nil {
		log.Fatalf("failed to initialize worker: %v", err)
	}

	if err := w.Start(reportHandler); err != nil {
		log.Fatalf("worker repo-report failed: %v", err)
	}
}

func reportHandler(ctx context.Context, req *pb.JobRequest, store memory.Store) (*pb.JobResult, error) {
	payload, err := loadReportContext(ctx, req, store)
	if err != nil {
		return failResult(req), err
	}

	entries := make([]reportEntry, 0, len(payload.Files))
	highSeverity := 0
	for _, f := range payload.Files {
		patch, _ := loadPatch(ctx, store, f.PatchPtr)
		expl, _ := loadExplanation(ctx, store, f.ExplanationPtr)
		sev := extractSeverity(patch)
		if sev == "" {
			sev = "medium"
		}
		if sev == "high" {
			highSeverity++
		}
		desc := buildDescription(patch, expl)
		entries = append(entries, reportEntry{
			FilePath:    f.FilePath,
			Description: desc,
			Severity:    sev,
			PatchPtr:    f.PatchPtr,
		})
	}

	sections := []reportSection{
		{
			Title: "Code Findings",
			Items: entries,
		},
	}

	var tests *testsSummary
	if payload.TestsPtr != "" {
		tests = loadTestsSummary(ctx, store, payload.TestsPtr)
	}

	var sast *sastSummary
	if payload.SastPtr != "" {
		sast = loadSastSummary(ctx, store, payload.SastPtr)
	}

	summary := fmt.Sprintf("Reviewed %d files. High severity: %d.", len(entries), highSeverity)
	if tests != nil {
		if tests.Failed {
			summary += " Tests failed."
		} else {
			summary += " Tests passed."
		}
	}
	if sast != nil && sast.Total > 0 {
		summary += fmt.Sprintf(" SAST findings: %d (high=%d, medium=%d).", sast.Total, sast.High, sast.Medium)
	}

	actionRequired := highSeverity > 0
	decisions := make([]string, 0, 3)
	if highSeverity > 0 {
		decisions = append(decisions, fmt.Sprintf("Apply %d high-severity patches", highSeverity))
	}
	if sast != nil && sast.Total > 0 {
		if sast.High > 0 {
			actionRequired = true
			decisions = append(decisions, fmt.Sprintf("Resolve %d high SAST findings", sast.High))
		} else {
			decisions = append(decisions, "Review SAST findings before merge")
		}
	}
	if tests != nil && tests.Failed {
		actionRequired = true
		decisions = append(decisions, "Fix failing tests before merge")
	}
	if len(decisions) == 0 {
		decisions = append(decisions, "No blocking issues; safe to proceed after review.")
	}

	result := reportResult{
		Summary:        summary,
		ActionRequired: actionRequired,
		Decision:       strings.Join(decisions, " | "),
		Sections:       sections,
		TestsSummary:   tests,
		SastSummary:    sast,
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
		WorkerId:    resolveWorkerID(repoReportWorkerID),
		ExecutionMs: 0,
	}, nil
}

func loadReportContext(ctx context.Context, req *pb.JobRequest, store memory.Store) (*reportContext, error) {
	key, err := memory.KeyFromPointer(req.GetContextPtr())
	if err != nil {
		return nil, err
	}
	data, err := store.GetContext(ctx, key)
	if err != nil {
		return nil, err
	}
	var payload reportContext
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func loadPatch(ctx context.Context, store memory.Store, ptr string) (*codePatch, error) {
	if ptr == "" {
		return nil, fmt.Errorf("missing patch_ptr")
	}
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return nil, err
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		return nil, err
	}
	var patch codePatch
	if err := json.Unmarshal(data, &patch); err != nil {
		return nil, err
	}
	return &patch, nil
}

func loadExplanation(ctx context.Context, store memory.Store, ptr string) (string, error) {
	if ptr == "" {
		return "", nil
	}
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return "", err
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		return "", err
	}
	var out explanationPayload
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	return out.Response, nil
}

func loadSastSummary(ctx context.Context, store memory.Store, ptr string) *sastSummary {
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return nil
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		return nil
	}
	var res sastResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil
	}
	sum := &sastSummary{}
	for _, f := range res.Findings {
		sum.Total++
		switch strings.ToLower(f.Severity) {
		case "high":
			sum.High++
		case "medium":
			sum.Medium++
		default:
			sum.Low++
		}
	}
	return sum
}

func loadTestsSummary(ctx context.Context, store memory.Store, ptr string) *testsSummary {
	key, err := memory.KeyFromPointer(ptr)
	if err != nil {
		return &testsSummary{Ran: false}
	}
	data, err := store.GetResult(ctx, key)
	if err != nil {
		return &testsSummary{Ran: false}
	}
	var res struct {
		ExitCode int    `json:"exit_code"`
		Failed   bool   `json:"failed"`
		Output   string `json:"output"`
		Command  string `json:"command"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return &testsSummary{Ran: false}
	}
	return &testsSummary{
		Ran:     true,
		Failed:  res.Failed,
		Details: fmt.Sprintf("command=%s exit_code=%d failed=%v output=%s", res.Command, res.ExitCode, res.Failed, res.Output),
	}
}

func extractSeverity(patch *codePatch) string {
	if patch == nil {
		return ""
	}
	for _, a := range patch.Analysis {
		if a.Severity != "" {
			return strings.ToLower(a.Severity)
		}
	}
	return ""
}

func buildDescription(patch *codePatch, explanation string) string {
	if explanation != "" {
		return explanation
	}
	if patch != nil && len(patch.Analysis) > 0 {
		parts := make([]string, 0, len(patch.Analysis))
		for _, a := range patch.Analysis {
			parts = append(parts, a.Description)
		}
		return strings.Join(parts, " | ")
	}
	return "Patch available."
}

func failResult(req *pb.JobRequest) *pb.JobResult {
	return &pb.JobResult{
		JobId:    req.GetJobId(),
		Status:   pb.JobStatus_JOB_STATUS_FAILED,
		WorkerId: resolveWorkerID(repoReportWorkerID),
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
