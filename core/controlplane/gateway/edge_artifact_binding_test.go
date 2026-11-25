package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/edge/agentd"
	"github.com/cordum/cordum/core/edge/claude"
	"github.com/cordum/cordum/core/infra/config"
)

const artifactBindingCommand = "kubectl apply -f manifest.yaml"

const artifactBindingPolicy = `version: artifact-binding-test
default_decision: deny
rules:
  - id: require-artifact-binding
    match:
      topics:
        - job.edge.action
      labels:
        "artifact.binding": unbound
    decision: deny
    reason: "the inputs of this command cannot be bound to an approval"
  - id: approve-bound-commands
    match:
      topics:
        - job.edge.action
      labels:
        "artifact.binding": bound
    decision: require_approval
    reason: "bound commands need an approval"
`

func artifactBindingWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func artifactBindingKubeconfig(current, devNamespace string) string {
	return "current-context: " + current + "\ncontexts:\n- name: dev\n  context:\n    namespace: " + devNamespace +
		"\n- name: prod\n  context:\n    namespace: prod-ns\n"
}

// artifactBindingWorkspace writes a manifest into a new cwd and a kubeconfig
// outside it, and returns the cwd, the kubeconfig path and the hook env.
func artifactBindingWorkspace(t *testing.T) (string, string, map[string]string) {
	t.Helper()
	cwd := t.TempDir()
	kubeconfig := filepath.Join(t.TempDir(), "config")
	artifactBindingWrite(t, filepath.Join(cwd, "manifest.yaml"), "kind: ConfigMap\nmetadata:\n  name: reviewed\n")
	artifactBindingWrite(t, kubeconfig, artifactBindingKubeconfig("dev", "dev-ns"))
	return cwd, kubeconfig, map[string]string{"KUBECONFIG": kubeconfig, "CORDUM_AGENTD_HOOK_NONCE": "artifact-binding-hook-key"}
}

func artifactBindingHook(t *testing.T, evaluator *agentd.Evaluator, session edgeSessionCreateResponseJSON, command, cwd string, env map[string]string) claude.ClaudeHookOutput {
	t.Helper()
	stdin, err := json.Marshal(map[string]any{"cwd": cwd, "hook_event_name": "PreToolUse", "tool_name": "Bash",
		"tool_use_id": "tool-artifact", "tool_input": map[string]any{"command": command}})
	if err != nil {
		t.Fatal(err)
	}
	hookEnv := map[string]string{"CORDUM_EDGE_SESSION_ID": session.SessionID, "CORDUM_EDGE_EXECUTION_ID": session.ExecutionID,
		"CORDUM_TENANT_ID": edgeRouteTenant, "CORDUM_PRINCIPAL_ID": "principal-edge-a", "CORDUM_EDGE_POLICY_MODE": "enforce"}
	maps.Copy(hookEnv, env)
	var stdout, stderr bytes.Buffer
	rc := claude.Run(context.Background(), claude.RunOptions{Stdin: bytes.NewReader(stdin), Stdout: &stdout, Stderr: &stderr, Agentd: evaluator, Env: hookEnv})
	var out claude.ClaudeHookOutput
	if rc != 0 || json.Unmarshal(stdout.Bytes(), &out) != nil {
		t.Fatalf("hook exit=%d stdout=%q stderr=%q", rc, stdout.String(), stderr.String())
	}
	return out
}

// artifactBindingStarted plays the agent with the repro's tool helper: the
// tool runs only when the hook output allows it, and leaves a marker.
func artifactBindingStarted(t *testing.T, out claude.ClaudeHookOutput) bool {
	t.Helper()
	_, err := os.Stat(referencedFileRunTool(t, out))
	return err == nil
}

func TestArtifactBindingApprovalFollowsManifestAndKubeTarget(t *testing.T) {
	editManifest := func(t *testing.T, cwd, _ string) {
		artifactBindingWrite(t, filepath.Join(cwd, "manifest.yaml"), "kind: ConfigMap\nmetadata:\n  name: changed\n")
	}
	cases := []struct {
		name, command string
		change        func(t *testing.T, cwd, kubeconfig string)
	}{
		{"unchanged_positive_control", artifactBindingCommand, nil},
		{"manifest_edited", artifactBindingCommand, editManifest},
		{"kube_context_switched", artifactBindingCommand, func(t *testing.T, _, kubeconfig string) {
			artifactBindingWrite(t, kubeconfig, artifactBindingKubeconfig("prod", "dev-ns"))
		}},
		{"kube_namespace_changed", artifactBindingCommand, func(t *testing.T, _, kubeconfig string) {
			artifactBindingWrite(t, kubeconfig, artifactBindingKubeconfig("dev", "other-ns"))
		}},
		{"attached_flag_positive_control", "kubectl apply -fmanifest.yaml", nil},
		{"attached_flag_manifest_edited", "kubectl apply -fmanifest.yaml", editManifest},
		{"comma_list_manifest_edited", "kubectl apply -f other.yaml,manifest.yaml", editManifest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, handler, session := setupEdgeEvaluateApprovalRequiredServer(t)
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			evaluator := referencedFileEvaluator(t, httpServer.URL, session)
			cwd, kubeconfig, env := artifactBindingWorkspace(t)
			if artifactBindingStarted(t, artifactBindingHook(t, evaluator, session, tc.command, cwd, env)) {
				t.Fatal("pending tool must not start")
			}
			referencedFileApprove(t, s, session)
			if tc.change != nil {
				tc.change(t, cwd, kubeconfig)
			}
			started := artifactBindingStarted(t, artifactBindingHook(t, evaluator, session, tc.command, cwd, env))
			if started != (tc.change == nil) {
				t.Fatalf("after %s the approved tool started = %v", tc.name, started)
			}
		})
	}
}

func TestArtifactBindingPolicyDeniesUnboundCommand(t *testing.T) {
	policy, err := config.ParseSafetyPolicy([]byte(artifactBindingPolicy))
	if err != nil {
		t.Fatal(err)
	}
	safety := &edgeEvaluatePolicySafetyClient{policy: policy, snapshot: "artifact-binding-policy"}
	s, handler := newEdgeEvaluateTestServer(t, safety)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	cwd, _, env := artifactBindingWorkspace(t)
	cases := []struct {
		name, command string
		approvals     int
	}{
		{"unbound_denied_by_rule", "cat manifest.yaml | kubectl apply -f -", 0},
		{"remote_kustomization_denied_by_rule", "kubectl apply -k github.com/org/repo", 0},
		{"inline_code_denied_by_rule", "perl -E system+q+./deploy.sh+", 0},
		{"bound_requires_approval", artifactBindingCommand, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := createEdgeEvaluateSessionWithPolicySnapshot(t, handler, safety.snapshot, edgecore.PolicyModeObserve)
			out := artifactBindingHook(t, referencedFileEvaluator(t, httpServer.URL, session), session, tc.command, cwd, env)
			if artifactBindingStarted(t, out) {
				t.Fatal("tool must not start")
			}
			page, err := s.edgeStore.ListApprovals(context.Background(), edgecore.ListApprovalsQuery{TenantID: edgeRouteTenant, SessionID: session.SessionID, Limit: 10})
			if err != nil || len(page.Items) != tc.approvals {
				t.Fatalf("approvals = %d (err %v), want %d", len(page.Items), err, tc.approvals)
			}
			reason := out.HookSpecificOutput.PermissionDecisionReason
			if tc.approvals == 0 && !strings.Contains(reason, "cannot be bound to an approval") {
				t.Fatalf("deny reason = %q, want the require-artifact-binding rule", reason)
			}
		})
	}
}
