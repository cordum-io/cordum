package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/agentd"
	"github.com/cordum/cordum/core/edge/claude"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

func TestWaitAuthorityToolHelper(t *testing.T) {
	if path := os.Getenv("CORDUM_WAIT_AUTHORITY_MARKER"); path != "" {
		if err := os.WriteFile(path, []byte("started"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func waitAuthorityRunTool(t *testing.T, out claude.ClaudeHookOutput) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "started")
	if out.HookSpecificOutput == nil {
		t.Fatal("tool permission must be explicit")
	}
	permission := out.HookSpecificOutput.PermissionDecision
	if permission != "allow" && permission != "deny" {
		t.Fatalf("unexpected tool permission: %q", permission)
	}
	if out.Decision == "block" || permission == "deny" {
		return marker
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWaitAuthorityToolHelper$")
	cmd.Env = append(os.Environ(), "CORDUM_WAIT_AUTHORITY_MARKER="+marker)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tool helper: %v: %s", err, output)
	}
	return marker
}

func waitAuthorityHook(t *testing.T, evaluator *agentd.Evaluator, session edgeSessionCreateResponseJSON, command string) claude.ClaudeHookOutput {
	t.Helper()
	encoded, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	rc := claude.Run(context.Background(), claude.RunOptions{
		Stdin:  bytes.NewBufferString(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"tool-input","tool_input":{"command":` + string(encoded) + `}}`),
		Stdout: &stdout, Stderr: &stderr, Agentd: evaluator,
		Env: map[string]string{"CORDUM_EDGE_SESSION_ID": session.SessionID, "CORDUM_EDGE_EXECUTION_ID": session.ExecutionID,
			"CORDUM_TENANT_ID": edgeRouteTenant, "CORDUM_PRINCIPAL_ID": "principal-edge-a", "CORDUM_EDGE_POLICY_MODE": "enforce"},
	})
	if rc != 0 {
		t.Fatalf("hook failed: exit=%d stderr=%s", rc, stderr.String())
	}
	var out claude.ClaudeHookOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("hook output: %v", err)
	}
	return out
}

func waitAuthorityEvaluator(t *testing.T, handlerURL string, session edgeSessionCreateResponseJSON) *agentd.Evaluator {
	t.Helper()
	client, err := agentd.NewGatewayClient(agentd.GatewayClientConfig{
		BaseURL: handlerURL, APIKey: edgeRouteTestAPIKey, TenantID: edgeRouteTenant,
		PrincipalID: "principal-edge-a", Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return agentd.NewEvaluator(agentd.EvaluatorConfig{Client: waitAuthorityClient{client}, State: agentd.SessionState{
		SessionID: session.SessionID, ExecutionID: session.ExecutionID, TenantID: edgeRouteTenant,
		PrincipalID: "principal-edge-a", PolicyMode: edgecore.PolicyModeEnforce, PolicySnapshot: session.PolicySnapshot,
	}})
}

type waitAuthorityClient struct{ *agentd.GatewayClient }

func (c waitAuthorityClient) Evaluate(ctx context.Context, req agentd.EvaluateRequest) (*agentd.EvaluateResponse, error) {
	req.WaitForApproval = true
	req.ApprovalWaitTimeoutMS = 1500
	return c.GatewayClient.Evaluate(ctx, req)
}

func waitAuthorityResolve(ctx context.Context, s *server, safety *edgeEvaluateStubSafetyClient, session edgeSessionCreateResponseJSON, mode string) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := s.edgeStore.ListApprovals(ctx, edgecore.ListApprovalsQuery{TenantID: edgeRouteTenant, SessionID: session.SessionID, Limit: 10})
		if err != nil {
			return err
		}
		if len(page.Items) == 1 && page.Items[0].Status == edgecore.ApprovalStatusPending {
			if mode == "policy_deny" {
				safety.mu.Lock()
				safety.response = &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "action is no longer permitted", PolicySnapshot: session.PolicySnapshot}
				safety.mu.Unlock()
			}
			_, err := s.edgeStore.ApproveApproval(ctx, edgecore.ApprovalResolution{
				TenantID: edgeRouteTenant, ApprovalRef: page.Items[0].ApprovalRef, ResolverID: "reviewer", ResolvedBy: "reviewer", ResolvedAt: time.Now().UTC(),
			})
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("pending approval not observed: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func TestWaitAuthorityUsesCurrentDecision(t *testing.T) {
	for _, mode := range []string{"unchanged_positive_control", "policy_deny"} {
		t.Run(mode, func(t *testing.T) {
			safety := &edgeEvaluateStubSafetyClient{response: &pb.PolicyCheckResponse{
				Decision: pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, Reason: "approval required", ApprovalRequired: true,
			}}
			s, handler := newEdgeEvaluateTestServer(t, safety)
			session := createEdgeRouteSession(t, handler)
			safety.response.PolicySnapshot = session.PolicySnapshot
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			evaluator := waitAuthorityEvaluator(t, httpServer.URL, session)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			resolved := make(chan error, 1)
			go func() { resolved <- waitAuthorityResolve(ctx, s, safety, session, mode) }()
			out := waitAuthorityHook(t, evaluator, session, "echo reviewed-action")
			if err := <-resolved; err != nil {
				t.Fatalf("resolution setup failed: %v", err)
			}
			_, err := os.Stat(waitAuthorityRunTool(t, out))
			if mode != "unchanged_positive_control" && !os.IsNotExist(err) {
				t.Fatalf("tool must not start after authority changes during the wait; stat=%v", err)
			}
			if mode == "unchanged_positive_control" && err != nil {
				t.Fatalf("positive control did not start: %v", err)
			}
		})
	}
}
