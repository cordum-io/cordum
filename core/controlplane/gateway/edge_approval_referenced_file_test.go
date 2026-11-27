package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/agentd"
	"github.com/cordum/cordum/core/edge/claude"
)

func TestReferencedFileToolHelper(t *testing.T) {
	if path := os.Getenv("CORDUM_REFERENCED_FILE_MARKER"); path != "" {
		if err := os.WriteFile(path, []byte("started"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func referencedFileRunTool(t *testing.T, out claude.ClaudeHookOutput) string {
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
	// A -race -cover gateway test binary can outlast a short fixed budget on a loaded CI runner.
	deadline, ok := t.Deadline()
	if ok {
		deadline = deadline.Add(-10 * time.Second)
	} else {
		deadline = time.Now().Add(2 * time.Minute)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestReferencedFileToolHelper$")
	cmd.Env = append(os.Environ(), "CORDUM_REFERENCED_FILE_MARKER="+marker)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tool helper: %v: %s", err, output)
	}
	return marker
}

func referencedFileHook(t *testing.T, evaluator *agentd.Evaluator, session edgeSessionCreateResponseJSON, command, cwd string) claude.ClaudeHookOutput {
	t.Helper()
	encoded, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := json.Marshal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	rc := claude.Run(context.Background(), claude.RunOptions{
		Stdin:  bytes.NewBufferString(`{"cwd":` + string(dir) + `,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"tool-input","tool_input":{"command":` + string(encoded) + `}}`),
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

func referencedFileEvaluator(t *testing.T, handlerURL string, session edgeSessionCreateResponseJSON) *agentd.Evaluator {
	t.Helper()
	client, err := agentd.NewGatewayClient(agentd.GatewayClientConfig{
		BaseURL: handlerURL, APIKey: edgeRouteTestAPIKey, TenantID: edgeRouteTenant,
		PrincipalID: "principal-edge-a", Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return agentd.NewEvaluator(agentd.EvaluatorConfig{Client: client, State: agentd.SessionState{
		SessionID: session.SessionID, ExecutionID: session.ExecutionID, TenantID: edgeRouteTenant,
		PrincipalID: "principal-edge-a", PolicyMode: edgecore.PolicyModeEnforce, PolicySnapshot: session.PolicySnapshot,
	}})
}

func referencedFileApprove(t *testing.T, s *server, session edgeSessionCreateResponseJSON) {
	t.Helper()
	page, err := s.edgeStore.ListApprovals(context.Background(), edgecore.ListApprovalsQuery{TenantID: edgeRouteTenant, SessionID: session.SessionID, Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].Status != edgecore.ApprovalStatusPending {
		t.Fatalf("expected one pending approval: err=%v page=%#v", err, page)
	}
	if _, err := s.edgeStore.ApproveApproval(context.Background(), edgecore.ApprovalResolution{
		TenantID: edgeRouteTenant, ApprovalRef: page.Items[0].ApprovalRef, ResolverID: "reviewer", ResolvedBy: "reviewer", ResolvedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReferencedFileApprovalRequiresUnchangedBytes(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "unchanged_positive_control"
		if changed {
			name = "changed_file"
		}
		t.Run(name, func(t *testing.T) {
			s, handler, session := setupEdgeEvaluateApprovalRequiredServer(t)
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			evaluator := referencedFileEvaluator(t, httpServer.URL, session)
			cwd := t.TempDir()
			path := filepath.Join(cwd, "reviewed.sh")
			if err := os.WriteFile(path, []byte("# reviewed version\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			command := "bash ./reviewed.sh"
			initial := referencedFileHook(t, evaluator, session, command, cwd)
			if _, err := os.Stat(referencedFileRunTool(t, initial)); !os.IsNotExist(err) {
				t.Fatalf("pending tool must not start: %v", err)
			}
			referencedFileApprove(t, s, session)
			if changed {
				if err := os.WriteFile(path, []byte("# changed version\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			retry := referencedFileHook(t, evaluator, session, command, cwd)
			_, err := os.Stat(referencedFileRunTool(t, retry))
			if changed && !os.IsNotExist(err) {
				t.Fatalf("tool must not start after a referenced file changes; stat=%v", err)
			}
			if !changed && err != nil {
				t.Fatalf("positive control did not start: %v", err)
			}
		})
	}
}
