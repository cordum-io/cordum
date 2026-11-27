package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
	return artifactBindingToolHook(t, evaluator, session, "Bash", artifactBindingInput(command), cwd, env)
}

func artifactBindingInput(command string) map[string]any { return map[string]any{"command": command} }

func artifactBindingToolHook(t *testing.T, evaluator *agentd.Evaluator, session edgeSessionCreateResponseJSON, tool string, input map[string]any, cwd string, env map[string]string) claude.ClaudeHookOutput {
	t.Helper()
	stdin, err := json.Marshal(map[string]any{"cwd": cwd, "hook_event_name": "PreToolUse", "tool_name": tool,
		"tool_use_id": "tool-artifact", "tool_input": input})
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
		listCopy      bool // KUBECONFIG lists the kubeconfig, then a copy of it
	}{
		{"unchanged_positive_control", artifactBindingCommand, nil, false},
		{"manifest_edited", artifactBindingCommand, editManifest, false},
		{"kube_context_switched", artifactBindingCommand, func(t *testing.T, _, kubeconfig string) {
			artifactBindingWrite(t, kubeconfig, artifactBindingKubeconfig("prod", "dev-ns"))
		}, false},
		{"kube_namespace_changed", artifactBindingCommand, func(t *testing.T, _, kubeconfig string) {
			artifactBindingWrite(t, kubeconfig, artifactBindingKubeconfig("dev", "other-ns"))
		}, false},
		{"kube_context_repeated_key", artifactBindingCommand, func(t *testing.T, _, kubeconfig string) {
			// kubectl keeps the last value of a repeated key and targets prod.
			artifactBindingWrite(t, kubeconfig, artifactBindingKubeconfig("dev", "dev-ns")+"current-context: prod\n")
		}, true},
		{"kube_context_merge_key", artifactBindingCommand, func(t *testing.T, _, kubeconfig string) {
			// kubectl applies a merge key after the keys before it and targets prod.
			artifactBindingWrite(t, kubeconfig, artifactBindingKubeconfig("dev", "dev-ns")+"<<: {current-context: prod}\n")
		}, false},
		{"attached_flag_positive_control", "kubectl apply -fmanifest.yaml", nil, false},
		{"attached_flag_manifest_edited", "kubectl apply -fmanifest.yaml", editManifest, false},
		{"comma_list_manifest_edited", "kubectl apply -f other.yaml,manifest.yaml", editManifest, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, handler, session := setupEdgeEvaluateApprovalRequiredServer(t)
			httpServer := httptest.NewServer(handler)
			defer httpServer.Close()
			evaluator := referencedFileEvaluator(t, httpServer.URL, session)
			cwd, kubeconfig, env := artifactBindingWorkspace(t)
			artifactBindingWrite(t, filepath.Join(cwd, "other.yaml"), "kind: ConfigMap\nmetadata:\n  name: other\n")
			if tc.listCopy {
				listed := kubeconfig + ".copy"
				artifactBindingWrite(t, listed, artifactBindingKubeconfig("dev", "dev-ns"))
				env["KUBECONFIG"] = kubeconfig + string(os.PathListSeparator) + listed
			}
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

type artifactPolicyCase struct {
	name, tool string
	input      map[string]any
	approvals  int
}

// artifactBindingPlatformCases returns the rows one platform sets up: a
// reader-relative /proc name on Linux, and on Windows a SYSTEM file and a
// name ending in '.', which Git Bash's MSYS bash opens as other files.
func artifactBindingPlatformCases(t *testing.T, cwd string) []artifactPolicyCase {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		file, err := os.Open(filepath.Join(cwd, "manifest.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = file.Close() })
		return []artifactPolicyCase{{"reader_relative_denied_by_rule", "Bash", artifactBindingInput(fmt.Sprintf("bash /proc/self/fd/%d", file.Fd())), 0}}
	case "windows":
		artifactBindingWrite(t, filepath.Join(cwd, "deploy.sh"), "echo reviewed\n")
		link := filepath.Join(cwd, "link.sh")
		artifactBindingWrite(t, link, "!<symlink>deploy.sh")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "attrib", "+s", link).CombinedOutput(); err != nil {
			t.Fatalf("attrib: %v %s", err, out)
		}
		return []artifactPolicyCase{{"system_attribute_denied_by_rule", "Bash", artifactBindingInput("bash link.sh"), 0},
			{"trailing_dot_denied_by_rule", "Bash", artifactBindingInput("bash deploy.sh."), 0}}
	}
	return nil
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
	if err := os.MkdirAll(filepath.Join(cwd, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	artifactBindingWrite(t, filepath.Join(cwd, "bin", "kubectl"), "a vendored kubectl\n")
	artifactBindingWrite(t, filepath.Join(cwd, "deploy.js"), "console.log('reviewed')\n")
	cmd := artifactBindingInput
	cases := append([]artifactPolicyCase{
		{"unbound_denied_by_rule", "Bash", cmd("cat manifest.yaml | kubectl apply -f -"), 0},
		{"remote_kustomization_denied_by_rule", "Bash", cmd("kubectl apply -k github.com/org/repo"), 0},
		{"inline_code_denied_by_rule", "Bash", cmd("perl -E system+q+./deploy.sh+"), 0},
		{"remote_command_denied_by_rule", "Bash", cmd("kubectl exec db-0 -- ./migrate.sh"), 0},
		{"assignment_prefix_denied_by_rule", "Bash", cmd("X=x/bash ./bin/kubectl apply -k github.com/org/repo"), 0},
		{"powershell_denied_by_rule", "PowerShell", cmd(artifactBindingCommand), 0},
		{"control_character_denied_by_rule", "Bash", cmd("kubectl apply -f manifest.yaml\r --context dev"), 0},
		{"from_file_control_character_denied_by_rule", "Bash", cmd("kubectl create configmap cm --from-file=k=manifest.yaml\r --context dev"), 0},
		{"dotdot_script_denied_by_rule", "Bash", cmd("node bin/../deploy.js"), 0},
		{"code_tool_denied_by_rule", "mcp__ide__executeCode", map[string]any{"code": "import os; os.system('node deploy.js')"}, 0},
		{"bound_requires_approval", "Bash", cmd(artifactBindingCommand), 1},
	}, artifactBindingPlatformCases(t, cwd)...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := createEdgeEvaluateSessionWithPolicySnapshot(t, handler, safety.snapshot, edgecore.PolicyModeObserve)
			out := artifactBindingToolHook(t, referencedFileEvaluator(t, httpServer.URL, session), session, tc.tool, tc.input, cwd, env)
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
