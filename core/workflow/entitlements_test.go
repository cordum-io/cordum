package workflow

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cordum/cordum/core/licensing"
)

func newWorkflowEntitlementResolver(t *testing.T, plan licensing.Plan, mutate func(*licensing.Entitlements)) *licensing.EntitlementResolver {
	t.Helper()

	entitlements := licensing.DefaultEntitlements(plan)
	if mutate != nil {
		mutate(&entitlements)
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}

	payloadBytes, err := json.Marshal(licensing.Claims{
		Plan:         string(plan),
		Entitlements: &entitlements,
	})
	if err != nil {
		t.Fatalf("marshal license payload: %v", err)
	}

	licenseBytes, err := json.Marshal(map[string]any{
		"payload":   json.RawMessage(payloadBytes),
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, payloadBytes)),
	})
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}

	t.Setenv("CORDUM_LICENSE_FILE", "")
	t.Setenv("CORDUM_LICENSE_TOKEN", string(licenseBytes))
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY_PATH", "")
	t.Setenv("CORDUM_LICENSE_PUBLIC_KEY", base64.StdEncoding.EncodeToString(publicKey))

	resolver := licensing.NewEntitlementResolver()
	resolver.Init()
	return resolver
}

func TestValidateWorkflowDefinitionMaxWorkflowStepsLimit(t *testing.T) {
	engine := (&Engine{}).WithEntitlements(newWorkflowEntitlementResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.MaxWorkflowSteps = 1
	}))

	wf := &Workflow{
		ID: "wf-too-many-steps",
		Steps: map[string]*Step{
			"s1": {ID: "s1", Type: StepTypeDelay, DelaySec: 1},
			"s2": {ID: "s2", Type: StepTypeDelay, DelaySec: 1},
		},
	}

	err := engine.validateWorkflowDefinition(wf)
	var limitErr *licensing.TierLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected tier limit error, got %v", err)
	}
	if limitErr.Limit != "max_workflow_steps" {
		t.Fatalf("limit = %q, want max_workflow_steps", limitErr.Limit)
	}
}

func TestValidateWorkflowDefinitionApprovalModeLimit(t *testing.T) {
	communityEngine := (&Engine{}).WithEntitlements(newWorkflowEntitlementResolver(t, licensing.PlanCommunity, nil))
	teamEngine := (&Engine{}).WithEntitlements(newWorkflowEntitlementResolver(t, licensing.PlanTeam, nil))
	enterpriseEngine := (&Engine{}).WithEntitlements(newWorkflowEntitlementResolver(t, licensing.PlanEnterprise, nil))

	multiApproval := &Workflow{
		ID: "wf-multi-approval",
		Steps: map[string]*Step{
			"approve-a": {ID: "approve-a", Type: StepTypeApproval},
			"approve-b": {ID: "approve-b", Type: StepTypeApproval},
		},
	}
	customApproval := &Workflow{
		ID: "wf-custom-approval",
		Steps: map[string]*Step{
			"approve-a": {ID: "approve-a", Type: StepTypeApproval},
			"approve-b": {ID: "approve-b", Type: StepTypeApproval, DependsOn: []string{"approve-a"}},
		},
	}

	if got := requestedApprovalModeForWorkflow(multiApproval.Steps); got != string(licensing.ApprovalModeMulti) {
		t.Fatalf("requestedApprovalModeForWorkflow(multi) = %q", got)
	}
	if got := requestedApprovalModeForWorkflow(customApproval.Steps); got != string(licensing.ApprovalModeCustom) {
		t.Fatalf("requestedApprovalModeForWorkflow(custom) = %q", got)
	}

	var communityErr *licensing.TierLimitError
	if err := communityEngine.validateWorkflowDefinition(multiApproval); !errors.As(err, &communityErr) || communityErr.Limit != "approval_mode" {
		t.Fatalf("expected community approval-mode tier limit, got %v", err)
	}

	var teamErr *licensing.TierLimitError
	if err := teamEngine.validateWorkflowDefinition(customApproval); !errors.As(err, &teamErr) || teamErr.Limit != "approval_mode" {
		t.Fatalf("expected team approval-mode tier limit, got %v", err)
	}

	if err := enterpriseEngine.validateWorkflowDefinition(customApproval); err != nil {
		t.Fatalf("enterprise custom approval chain should be allowed, got %v", err)
	}
}

func TestStartRunMaxActiveWorkflowsLimit(t *testing.T) {
	store := newWorkflowStore(t)
	defer func() { _ = store.Close() }()

	engine := NewEngine(store, &recordingBus{}).
		WithEntitlements(newWorkflowEntitlementResolver(t, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
			entitlements.MaxActiveWorkflows = 1
		}))

	now := time.Now().UTC()
	wf := &Workflow{
		ID:    "wf-active-limit",
		OrgID: "org-1",
		Steps: map[string]*Step{
			"delay": {ID: "delay", Type: StepTypeDelay, DelaySec: 1},
		},
	}
	if err := store.SaveWorkflow(context.Background(), wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	for _, runID := range []string{"run-1", "run-2"} {
		run := &WorkflowRun{
			ID:         runID,
			WorkflowID: wf.ID,
			OrgID:      wf.OrgID,
			Status:     RunStatusPending,
			Steps:      map[string]*StepRun{},
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		if err := store.CreateRun(context.Background(), run); err != nil {
			t.Fatalf("create run %s: %v", runID, err)
		}
	}

	err := engine.StartRun(context.Background(), wf.ID, "run-2")
	var limitErr *licensing.TierLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected tier limit error, got %v", err)
	}
	if limitErr.Limit != "max_active_workflows" {
		t.Fatalf("limit = %q, want max_active_workflows", limitErr.Limit)
	}
}
