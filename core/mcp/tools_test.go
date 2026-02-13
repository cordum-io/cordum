package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

type mockServiceBridge struct {
	submitJob       func(ctx context.Context, req SubmitJobInput) (*SubmitJobOutput, error)
	cancelJob       func(ctx context.Context, jobID string, reason string) error
	triggerWorkflow func(ctx context.Context, req TriggerWorkflowInput) (*TriggerOutput, error)
	approveJob      func(ctx context.Context, jobID string, note string) error
	rejectJob       func(ctx context.Context, jobID string, reason string) error
	simulatePolicy  func(ctx context.Context, req PolicySimInput) (*PolicySimOutput, error)
}

func (m *mockServiceBridge) SubmitJob(ctx context.Context, req SubmitJobInput) (*SubmitJobOutput, error) {
	if m.submitJob == nil {
		return nil, ErrBridgeUnavailable
	}
	return m.submitJob(ctx, req)
}

func (m *mockServiceBridge) CancelJob(ctx context.Context, jobID string, reason string) error {
	if m.cancelJob == nil {
		return ErrBridgeUnavailable
	}
	return m.cancelJob(ctx, jobID, reason)
}

func (m *mockServiceBridge) TriggerWorkflow(ctx context.Context, req TriggerWorkflowInput) (*TriggerOutput, error) {
	if m.triggerWorkflow == nil {
		return nil, ErrBridgeUnavailable
	}
	return m.triggerWorkflow(ctx, req)
}

func (m *mockServiceBridge) ApproveJob(ctx context.Context, jobID string, note string) error {
	if m.approveJob == nil {
		return ErrBridgeUnavailable
	}
	return m.approveJob(ctx, jobID, note)
}

func (m *mockServiceBridge) RejectJob(ctx context.Context, jobID string, reason string) error {
	if m.rejectJob == nil {
		return ErrBridgeUnavailable
	}
	return m.rejectJob(ctx, jobID, reason)
}

func (m *mockServiceBridge) SimulatePolicy(ctx context.Context, req PolicySimInput) (*PolicySimOutput, error) {
	if m.simulatePolicy == nil {
		return nil, ErrBridgeUnavailable
	}
	return m.simulatePolicy(ctx, req)
}

func TestSubmitJobTool(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			submitJob: func(_ context.Context, req SubmitJobInput) (*SubmitJobOutput, error) {
				if req.Prompt != "hello" {
					t.Fatalf("unexpected prompt %q", req.Prompt)
				}
				return &SubmitJobOutput{JobID: "job-1", TraceID: "trace-1"}, nil
			},
		})
		result, err := callToolForTest(reg, ToolSubmitJob, map[string]any{"prompt": "hello"})
		if err != nil {
			t.Fatalf("submit tool error: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success result, got error: %+v", result)
		}
		got := mustStructuredMap(t, result)
		if got["job_id"] != "job-1" {
			t.Fatalf("expected job_id=job-1, got %#v", got["job_id"])
		}
		if got["status"] != "pending" {
			t.Fatalf("expected status=pending, got %#v", got["status"])
		}
	})

	t.Run("missing prompt", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			submitJob: func(_ context.Context, _ SubmitJobInput) (*SubmitJobOutput, error) {
				return &SubmitJobOutput{}, nil
			},
		})
		_, err := callToolForTest(reg, ToolSubmitJob, map[string]any{"topic": "job.default"})
		if !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("expected ErrInvalidParams, got %v", err)
		}
	})

	t.Run("backpressure", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			submitJob: func(_ context.Context, _ SubmitJobInput) (*SubmitJobOutput, error) {
				return nil, NewBridgeError(http.StatusTooManyRequests, "rate_limited", "queue full", nil)
			},
		})
		result, err := callToolForTest(reg, ToolSubmitJob, map[string]any{"prompt": "hello"})
		if err != nil {
			t.Fatalf("submit tool call failed: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected isError=true, got %+v", result)
		}
		if code := structuredErrorCode(t, result); code != "system_at_capacity" {
			t.Fatalf("expected system_at_capacity, got %q", code)
		}
	})
}

func TestCancelJobTool(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			cancelJob: func(_ context.Context, jobID string, _ string) error {
				if jobID != "job-1" {
					t.Fatalf("unexpected job id %q", jobID)
				}
				return nil
			},
		})
		result, err := callToolForTest(reg, ToolCancelJob, map[string]any{"job_id": "job-1"})
		if err != nil {
			t.Fatalf("cancel tool call failed: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success result, got %+v", result)
		}
	})

	t.Run("not found", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			cancelJob: func(_ context.Context, _ string, _ string) error {
				return NewBridgeError(http.StatusNotFound, "not_found", "job not found", nil)
			},
		})
		result, err := callToolForTest(reg, ToolCancelJob, map[string]any{"job_id": "missing"})
		if err != nil {
			t.Fatalf("cancel tool call failed: %v", err)
		}
		if code := structuredErrorCode(t, result); code != "job_not_found" {
			t.Fatalf("expected job_not_found, got %q", code)
		}
	})

	t.Run("already completed", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			cancelJob: func(_ context.Context, _ string, _ string) error {
				return NewBridgeError(http.StatusConflict, "conflict", "already terminal", nil)
			},
		})
		result, err := callToolForTest(reg, ToolCancelJob, map[string]any{"job_id": "done"})
		if err != nil {
			t.Fatalf("cancel tool call failed: %v", err)
		}
		if code := structuredErrorCode(t, result); code != "job_already_completed" {
			t.Fatalf("expected job_already_completed, got %q", code)
		}
	})
}

func TestTriggerWorkflowTool(t *testing.T) {
	t.Parallel()

	t.Run("valid input", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			triggerWorkflow: func(_ context.Context, req TriggerWorkflowInput) (*TriggerOutput, error) {
				if !req.DryRun {
					t.Fatal("expected dry run true")
				}
				return &TriggerOutput{RunID: "run-1", WorkflowID: req.WorkflowID}, nil
			},
		})
		result, err := callToolForTest(reg, ToolTriggerWorkflow, map[string]any{
			"workflow_id": "wf-1",
			"input":       map[string]any{"name": "demo"},
			"dry_run":     true,
		})
		if err != nil {
			t.Fatalf("trigger workflow call failed: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success result, got %+v", result)
		}
		got := mustStructuredMap(t, result)
		if got["run_id"] != "run-1" {
			t.Fatalf("expected run_id=run-1, got %#v", got["run_id"])
		}
	})

	t.Run("invalid input schema", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			triggerWorkflow: func(_ context.Context, _ TriggerWorkflowInput) (*TriggerOutput, error) {
				return &TriggerOutput{}, nil
			},
		})
		_, err := callToolForTest(reg, ToolTriggerWorkflow, map[string]any{
			"workflow_id": "wf-1",
			"input":       "not-an-object",
		})
		if !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("expected ErrInvalidParams, got %v", err)
		}
	})

	t.Run("validation error remap", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			triggerWorkflow: func(_ context.Context, _ TriggerWorkflowInput) (*TriggerOutput, error) {
				return nil, NewBridgeError(http.StatusUnprocessableEntity, "input_validation_failed", "input validation failed", nil)
			},
		})
		result, err := callToolForTest(reg, ToolTriggerWorkflow, map[string]any{"workflow_id": "wf-1"})
		if err != nil {
			t.Fatalf("trigger workflow call failed: %v", err)
		}
		if code := structuredErrorCode(t, result); code != "input_validation_failed" {
			t.Fatalf("expected input_validation_failed, got %q", code)
		}
	})
}

func TestApproveJobTool(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			approveJob: func(_ context.Context, _ string, _ string) error { return nil },
		})
		result, err := callToolForTest(reg, ToolApproveJob, map[string]any{"job_id": "job-1"})
		if err != nil {
			t.Fatalf("approve tool call failed: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success result, got %+v", result)
		}
	})

	t.Run("not in approval state", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			approveJob: func(_ context.Context, _ string, _ string) error {
				return NewBridgeError(http.StatusConflict, "conflict", "job not awaiting approval", nil)
			},
		})
		result, err := callToolForTest(reg, ToolApproveJob, map[string]any{"job_id": "job-1"})
		if err != nil {
			t.Fatalf("approve tool call failed: %v", err)
		}
		if code := structuredErrorCode(t, result); code != "job_not_in_approval_state" {
			t.Fatalf("expected job_not_in_approval_state, got %q", code)
		}
	})

	t.Run("policy changed", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			approveJob: func(_ context.Context, _ string, _ string) error {
				return NewBridgeError(http.StatusConflict, "conflict", "policy snapshot changed; re-evaluate before approving", nil)
			},
		})
		result, err := callToolForTest(reg, ToolApproveJob, map[string]any{"job_id": "job-1"})
		if err != nil {
			t.Fatalf("approve tool call failed: %v", err)
		}
		if code := structuredErrorCode(t, result); code != "policy_changed_since_request" {
			t.Fatalf("expected policy_changed_since_request, got %q", code)
		}
	})
}

func TestRejectJobTool(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			rejectJob: func(_ context.Context, _ string, _ string) error { return nil },
		})
		result, err := callToolForTest(reg, ToolRejectJob, map[string]any{"job_id": "job-1", "reason": "unsafe"})
		if err != nil {
			t.Fatalf("reject tool call failed: %v", err)
		}
		if result.IsError {
			t.Fatalf("expected success result, got %+v", result)
		}
	})

	t.Run("missing reason", func(t *testing.T) {
		reg := newToolRegistryForTest(t, &mockServiceBridge{
			rejectJob: func(_ context.Context, _ string, _ string) error { return nil },
		})
		_, err := callToolForTest(reg, ToolRejectJob, map[string]any{"job_id": "job-1"})
		if !errors.Is(err, ErrInvalidParams) {
			t.Fatalf("expected ErrInvalidParams, got %v", err)
		}
	})
}

func TestQueryPolicyTool(t *testing.T) {
	t.Parallel()

	reg := newToolRegistryForTest(t, &mockServiceBridge{
		simulatePolicy: func(_ context.Context, req PolicySimInput) (*PolicySimOutput, error) {
			switch req.Topic {
			case "job.allow":
				return &PolicySimOutput{Decision: "allow", Reason: "ok"}, nil
			case "job.deny":
				return &PolicySimOutput{Decision: "deny", Reason: "blocked"}, nil
			default:
				return &PolicySimOutput{Decision: "DECISION_TYPE_REQUIRE_HUMAN", Reason: "approval"}, nil
			}
		},
	})

	check := func(topic, decision string) {
		t.Helper()
		result, err := callToolForTest(reg, ToolQueryPolicy, map[string]any{"topic": topic})
		if err != nil {
			t.Fatalf("query policy %s failed: %v", topic, err)
		}
		if result.IsError {
			t.Fatalf("expected success result for %s, got %+v", topic, result)
		}
		got := mustStructuredMap(t, result)
		if got["decision"] != decision {
			t.Fatalf("expected decision=%s, got %#v", decision, got["decision"])
		}
	}

	check("job.allow", "allow")
	check("job.deny", "deny")
	check("job.approval", "require_approval")
}

func TestToolInputValidation(t *testing.T) {
	t.Parallel()
	reg := newToolRegistryForTest(t, &mockServiceBridge{
		submitJob: func(_ context.Context, _ SubmitJobInput) (*SubmitJobOutput, error) {
			return &SubmitJobOutput{JobID: "job-1"}, nil
		},
		cancelJob: func(_ context.Context, _, _ string) error { return nil },
		triggerWorkflow: func(_ context.Context, req TriggerWorkflowInput) (*TriggerOutput, error) {
			return &TriggerOutput{RunID: "run-1", WorkflowID: req.WorkflowID}, nil
		},
		approveJob: func(_ context.Context, _, _ string) error { return nil },
		rejectJob:  func(_ context.Context, _, _ string) error { return nil },
		simulatePolicy: func(_ context.Context, _ PolicySimInput) (*PolicySimOutput, error) {
			return &PolicySimOutput{Decision: "allow"}, nil
		},
	})

	cases := []struct {
		name string
		args map[string]any
	}{
		{name: ToolSubmitJob, args: map[string]any{}},
		{name: ToolCancelJob, args: map[string]any{}},
		{name: ToolTriggerWorkflow, args: map[string]any{}},
		{name: ToolApproveJob, args: map[string]any{}},
		{name: ToolRejectJob, args: map[string]any{"job_id": "job-1"}},
		{name: ToolQueryPolicy, args: map[string]any{}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := callToolForTest(reg, tc.name, tc.args)
			if !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("expected ErrInvalidParams for %s, got %v", tc.name, err)
			}
		})
	}
}

func newToolRegistryForTest(t *testing.T, bridge ServiceBridge) *ToolRegistry {
	t.Helper()
	reg := NewToolRegistry()
	if err := RegisterAllTools(reg, bridge); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	return reg
}

func callToolForTest(reg *ToolRegistry, name string, args map[string]any) (*ToolCallResult, error) {
	payload, _ := json.Marshal(args)
	return reg.Call(context.Background(), name, payload)
}

func mustStructuredMap(t *testing.T, result *ToolCallResult) map[string]any {
	t.Helper()
	out, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("expected structured map result, got %#v", result.StructuredContent)
	}
	return out
}

func structuredErrorCode(t *testing.T, result *ToolCallResult) string {
	t.Helper()
	payload := mustStructuredMap(t, result)
	errMap, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error payload, got %#v", payload)
	}
	code, _ := errMap["code"].(string)
	return code
}
