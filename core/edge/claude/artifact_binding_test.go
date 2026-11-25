package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/edge/keychain"
)

func artifactHookInput(cwd, command string) HookInput {
	return HookInput{HookEventName: "PreToolUse", ToolName: "Bash", CWD: cwd, ToolInput: map[string]any{"command": command}}
}

func artifactWriteFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArtifactBindingOnlyForPreToolUseBash(t *testing.T) {
	for _, input := range []HookInput{
		{HookEventName: "PostToolUse", ToolName: "Bash", ToolInput: map[string]any{"command": "ls"}},
		{HookEventName: "PreToolUse", ToolName: "Write", ToolInput: map[string]any{"file_path": "a.txt"}},
	} {
		if labels := artifactBindingLabels(context.Background(), input, map[string]string{}, ""); labels != nil {
			t.Fatalf("%s %s got binding labels %v", input.HookEventName, input.ToolName, labels)
		}
	}
}

func TestArtifactBindingUnboundReasons(t *testing.T) {
	cwd := t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{"build.mk": "all:\n", "sub/keep": ""})
	many := make([]string, artifactMaxFiles+1)
	for i := range many {
		many[i] = fmt.Sprintf("f%02d.txt", i)
		artifactWriteFiles(t, cwd, map[string]string{many[i]: many[i]})
	}
	big := filepath.Join(cwd, "big.bin")
	artifactWriteFiles(t, cwd, map[string]string{"big.bin": ""})
	if err := os.Truncate(big, artifactMaxFileBytes+1); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, command, family, want string }{
		{"shell syntax", "cat build.mk | wc -l", "", "shell-syntax"},
		{"inline code", "bash -c ls", "", "inline-code"},
		{"code option in a cluster", "bash -ec ls", "", "inline-code"},
		{"module run", "python3 -m deploy", "", "inline-code"},
		{"attached module, versioned interpreter", "python3.12 -mdeploy", "", "inline-code"},
		{"url", "wget https://example.invalid/build.mk", "", "url"},
		{"network path", "kubectl apply -f//127.0.0.1/share/build.mk", "", "url"},
		{"directory", "ls sub", "", "directory"},
		{"device", "cat " + os.DevNull, "", "unreadable"},
		{"too many files", "cat " + strings.Join(many, " "), "", "limit"},
		{"file too large", "cat big.bin", "", "limit"},
		{"kube context unknown", "kubectl get pods", "", "kube-context"},
		{"build family", "make -f build.mk", "build", "implicit-inputs"},
		{"other reasons win over family", "go test ./... | tee out", "test", "shell-syntax"},
	}
	env := map[string]string{"KUBECONFIG": filepath.Join(cwd, "missing")}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want == "unreadable" && runtime.GOOS == "windows" {
				t.Skip("os.DevNull is not an absolute path on Windows")
			}
			labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, tc.command), env, tc.family)
			if labels["artifact.binding"] != "unbound" || labels["artifact.unbound_reason"] != tc.want {
				t.Fatalf("labels = %v, want unbound with reason %s", labels, tc.want)
			}
			if tc.want == "implicit-inputs" && labels["artifact.digest"] == "" {
				t.Fatalf("implicit-inputs still binds the files it names: %v", labels)
			}
		})
	}
}

func TestArtifactBindingCollectionTimesOut(t *testing.T) {
	release := make(chan struct{})
	watchdog := time.AfterFunc(5*time.Second, func() { close(release) })
	t.Cleanup(func() {
		if watchdog.Stop() {
			close(release)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	inputs, reason := artifactWithinBudget(ctx, func() (*artifactInputs, string) {
		<-release // a read that does not return, such as /proc/kmsg or a FUSE file
		return &artifactInputs{}, ""
	})
	if inputs != nil || reason != "timeout" {
		t.Fatalf("collection = %v, %q; want nil, timeout", inputs, reason)
	}
}

func TestArtifactBindingDigestFollowsContentAndKey(t *testing.T) {
	cwd := t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{"deploy.sh": "# v1\n"})
	digest := func(key string) string {
		t.Helper()
		labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, "bash ./deploy.sh"),
			map[string]string{"CORDUM_AGENTD_HOOK_NONCE": key}, "unknown")
		if labels["artifact.binding"] != "bound" || labels["artifact.unbound_reason"] != "" {
			t.Fatalf("labels = %v, want bound", labels)
		}
		return labels["artifact.digest"]
	}
	first := digest("key-a")
	if !regexp.MustCompile(`^[0-9a-f]{8}(\.[0-9a-f]{8}){7}$`).MatchString(first) {
		t.Fatalf("digest %q is not 8 groups of 8 hex", first)
	}
	if redacted := keychain.RedactSecretLike(first); redacted != first {
		t.Fatalf("agentd label redaction rewrites the digest to %q", redacted)
	}
	if again := digest("key-a"); again != first {
		t.Fatalf("digest changed without a change: %s then %s", first, again)
	}
	if other := digest("key-b"); other == first {
		t.Fatal("digest must depend on the hook key")
	}
	artifactWriteFiles(t, cwd, map[string]string{"deploy.sh": "# v2\n"})
	if changed := digest("key-a"); changed == first {
		t.Fatal("digest must change when a referenced file changes")
	}
}

func TestArtifactBindingCoversGitHead(t *testing.T) {
	cwd := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		base := []string{"-C", cwd, "-c", "user.name=artifact", "-c", "user.email=artifact@example.invalid", "-c", "commit.gpgsign=false"}
		if out, err := exec.Command("git", append(base, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	digest := func() string {
		t.Helper()
		labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, "git status"), map[string]string{}, "git_readonly")
		if labels["artifact.binding"] != "bound" {
			t.Fatalf("labels = %v, want bound", labels)
		}
		return labels["artifact.digest"]
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "one")
	first := digest()
	if first == "" {
		t.Fatal("a git cwd must bind its HEAD")
	}
	git("commit", "-q", "--allow-empty", "-m", "two")
	if second := digest(); second == first {
		t.Fatal("digest must change when HEAD moves")
	}
}

func TestArtifactBindingKubeTarget(t *testing.T) {
	cwd, home := t.TempDir(), t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{
		"kc.yaml": "current-context: dev\ncontexts:\n- name: dev\n  context:\n    namespace: dev-ns\n" +
			"- name: staging\n  context:\n    namespace: staging-ns\n",
		"a.yaml": "contexts:\n- name: x\n  context:\n    namespace: ns-a\n",
		"b.yaml": "current-context: x\ncontexts:\n- name: x\n  context:\n    namespace: ns-b\n",
	})
	artifactWriteFiles(t, home, map[string]string{".kube/config": "current-context: home\ncontexts:\n- name: home\n  context: {}\n"})
	kc := map[string]string{"KUBECONFIG": filepath.Join(cwd, "kc.yaml")}
	cases := []struct {
		name, command      string
		env                map[string]string
		context, namespace string
	}{
		{"flags with values", "kubectl apply --context staging --namespace team -f manifest.yaml", kc, "staging", "team"},
		{"flags with equals", "kubectl apply --context=staging -n=team -f manifest.yaml", kc, "staging", "team"},
		{"short namespace flag", "kubectl -n team get pods", kc, "dev", "team"},
		{"short namespace attached", "kubectl -nteam get pods", kc, "dev", "team"},
		{"flags after -- belong to the command", "kubectl -n team exec pod -- ls -n other", kc, "dev", "team"},
		{"kubectl.exe", "kubectl.exe get pods", kc, "dev", "dev-ns"},
		{"context flag reads its namespace", "kubectl --context staging get pods", kc, "staging", "staging-ns"},
		{"kubeconfig flag relative to cwd", "kubectl --kubeconfig kc.yaml get pods", map[string]string{}, "dev", "dev-ns"},
		{"KUBECONFIG assignment", "KUBECONFIG=kc.yaml kubectl get pods", map[string]string{}, "dev", "dev-ns"},
		{"merged KUBECONFIG list", "kubectl get pods", map[string]string{
			"KUBECONFIG": filepath.Join(cwd, "a.yaml") + string(os.PathListSeparator) + filepath.Join(cwd, "b.yaml"),
		}, "x", "ns-a"},
		{"HOME default, no namespace", "kubectl get pods", map[string]string{"HOME": home}, "home", "default"},
		{"USERPROFILE fallback", "kubectl get pods", map[string]string{"USERPROFILE": home}, "home", "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, tc.command), tc.env, "unknown")
			if labels["artifact.binding"] != "bound" || labels["kube.context"] != tc.context || labels["kube.namespace"] != tc.namespace {
				t.Fatalf("labels = %v, want bound %s/%s", labels, tc.context, tc.namespace)
			}
		})
	}
}

func TestArtifactBindingFollowsFlagSpellings(t *testing.T) {
	cwd := t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{"other.yaml": "kind: ConfigMap\n",
		"kc.yaml": "current-context: dev\ncontexts:\n- name: dev\n  context: {}\n"})
	env := map[string]string{"KUBECONFIG": filepath.Join(cwd, "kc.yaml")}
	for _, command := range []string{
		"kubectl apply -fmanifest.yaml",
		"kubectl apply -Rfmanifest.yaml",
		"kubectl apply -f other.yaml,manifest.yaml",
		"kubectl apply -f=other.yaml,manifest.yaml",
		"kubectl apply --filename=other.yaml,manifest.yaml",
		"kubectl create configmap cm --from-file=cfg=manifest.yaml",
		"kubectl create configmap cm --from-file=a=other.yaml,cfg=manifest.yaml",
		"kubectl create configmap cm --from-env-file=other.yaml,manifest.yaml",
		"curl -d @manifest.yaml localhost",
		"docker run -v ./manifest.yaml:/srv/manifest.yaml nginx",
	} {
		t.Run(command, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.HasPrefix(command, "docker ") {
				t.Skip("a ':' in a path is not a Windows file name: stat fails and the call is unbound")
			}
			digest := func(manifest string) string {
				t.Helper()
				artifactWriteFiles(t, cwd, map[string]string{"manifest.yaml": manifest})
				labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, command), env, "unknown")
				if labels["artifact.binding"] != "bound" {
					t.Fatalf("labels = %v, want bound", labels)
				}
				return labels["artifact.digest"]
			}
			if digest("v1") == digest("v2") {
				t.Fatal("the digest must change when the manifest the command names changes")
			}
		})
	}
}

func TestArtifactBindingRunDeniesWhenInputsChangeDuringEvaluation(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("rewrite=%v", rewrite), func(t *testing.T) {
			cwd := t.TempDir()
			artifactWriteFiles(t, cwd, map[string]string{"deploy.sh": "# reviewed\n"})
			agentd := &fakeAgentdClient{fn: func(_ context.Context, req AgentdRequest) (AgentdDecision, error) {
				if req.Labels["artifact.binding"] != "bound" || req.Labels["artifact.digest"] == "" {
					t.Errorf("agentd request labels = %v, want a bound digest", req.Labels)
				}
				if rewrite {
					artifactWriteFiles(t, cwd, map[string]string{"deploy.sh": "# changed\n"})
				}
				return AgentdDecision{Decision: DecisionAllow, Reason: "approved"}, nil
			}}
			stdin, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": cwd,
				"tool_use_id": "toolu_artifact", "tool_input": map[string]any{"command": "bash ./deploy.sh"}})
			if err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr := runHook(t, RunOptions{Stdin: bytes.NewReader(stdin), Agentd: agentd, Env: map[string]string{}})
			var out ClaudeHookOutput
			if code != 0 || json.Unmarshal([]byte(stdout), &out) != nil || out.HookSpecificOutput == nil {
				t.Fatalf("hook exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			want := "allow"
			if rewrite {
				want = "deny"
			}
			got := out.HookSpecificOutput
			if got.PermissionDecision != want || rewrite && !strings.HasSuffix(got.PermissionDecisionReason, "action was not run") {
				t.Fatalf("permission = %q reason = %q, want %s", got.PermissionDecision, got.PermissionDecisionReason, want)
			}
		})
	}
}
