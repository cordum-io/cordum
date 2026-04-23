package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func captureWorkflowLogs(t *testing.T, level slog.Level, fn func()) []map[string]any {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	fn()

	raw := strings.TrimSpace(buf.String())
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unmarshal log line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func findWorkflowLog(records []map[string]any, msg string, match func(map[string]any) bool) map[string]any {
	for _, record := range records {
		if record["msg"] != msg {
			continue
		}
		if match == nil || match(record) {
			return record
		}
	}
	return nil
}

func TestEngineWorkflowStepLogsUseStableFields(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	wf := &Workflow{
		ID:    "wf-log",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"execute_low": {
				ID:    "execute_low",
				Type:  StepTypeWorker,
				Topic: "job.default",
			},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-log",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input:      map[string]any{"amount": 40},
		Status:     RunStatusPending,
		Steps:      map[string]*StepRun{},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	records := captureWorkflowLogs(t, slog.LevelInfo, func() {
		if err := engine.StartRun(testCtx(t), wf.ID, run.ID); err != nil {
			t.Fatalf("start run: %v", err)
		}
		if err := engine.HandleJobResult(testCtx(t), &pb.JobResult{
			JobId:    "run-log:execute_low@1",
			WorkerId: "worker-1",
			Status:   pb.JobStatus_JOB_STATUS_SUCCEEDED,
		}); err != nil {
			t.Fatalf("handle job result: %v", err)
		}
	})

	dispatched := findWorkflowLog(records, "workflow step transition", func(record map[string]any) bool {
		return record["event_type"] == "step_dispatched"
	})
	if dispatched == nil {
		t.Fatal("expected workflow step transition log for step_dispatched")
	}
	if dispatched["run_id"] != "run-log" || dispatched["step_id"] != "execute_low" || dispatched["job_id"] != "run-log:execute_low@1" {
		t.Fatalf("unexpected dispatched log fields: %#v", dispatched)
	}

	received := findWorkflowLog(records, "workflow step result received", nil)
	if received == nil {
		t.Fatal("expected workflow step result received log")
	}
	if received["run_id"] != "run-log" || received["step_id"] != "execute_low" || received["job_id"] != "run-log:execute_low@1" {
		t.Fatalf("unexpected result log fields: %#v", received)
	}

	completed := findWorkflowLog(records, "workflow step transition", func(record map[string]any) bool {
		return record["event_type"] == "step_completed"
	})
	if completed == nil {
		t.Fatal("expected workflow step transition log for step_completed")
	}
	if completed["status"] != string(StepStatusSucceeded) {
		t.Fatalf("unexpected completion status: %#v", completed)
	}
}

func TestEngineWorkflowStepResultIgnoredLogsMismatch(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	bus := &recordingBus{}
	engine := NewEngine(store, bus)
	wf := &Workflow{
		ID:    "wf-log-mismatch",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"execute_low": {
				ID:    "execute_low",
				Type:  StepTypeWorker,
				Topic: "job.default",
			},
		},
	}
	if err := store.SaveWorkflow(testCtx(t), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	run := &WorkflowRun{
		ID:         "run-log-mismatch",
		WorkflowID: wf.ID,
		OrgID:      "org-1",
		TeamID:     "team-1",
		Input:      map[string]any{"amount": 40},
		Status:     RunStatusRunning,
		Steps: map[string]*StepRun{
			"execute_low": {
				StepID: "execute_low",
				Status: StepStatusRunning,
				JobID:  "run-log-mismatch:execute_low@1",
			},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	if err := store.CreateRun(testCtx(t), run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	records := captureWorkflowLogs(t, slog.LevelInfo, func() {
		err := engine.HandleJobResult(context.Background(), &pb.JobResult{
			JobId:    "run-log-mismatch:execute_low@2",
			WorkerId: "worker-2",
			Status:   pb.JobStatus_JOB_STATUS_SUCCEEDED,
		})
		if err != nil {
			t.Fatalf("handle job result mismatch: %v", err)
		}
	})

	ignored := findWorkflowLog(records, "workflow step result ignored", func(record map[string]any) bool {
		return record["reason"] == "job_id_mismatch"
	})
	if ignored == nil {
		t.Fatal("expected workflow step result ignored log")
	}
	if ignored["run_id"] != "run-log-mismatch" || ignored["step_id"] != "execute_low" {
		t.Fatalf("unexpected ignored log fields: %#v", ignored)
	}
	if ignored["expected_job_id"] != "run-log-mismatch:execute_low@1" || ignored["job_id"] != "run-log-mismatch:execute_low@2" {
		t.Fatalf("unexpected ignored job ids: %#v", ignored)
	}
}
