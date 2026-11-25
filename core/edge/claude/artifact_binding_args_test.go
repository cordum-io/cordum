package claude

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestArtifactBindingRecognizesOnlyModeledCommands(t *testing.T) {
	cwd := t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{"deploy.sh": "echo deploy\n", "+c": "",
		"kc.yaml": "current-context: dev\ncontexts:\n- name: dev\n  context: {}\n"})
	env := map[string]string{"KUBECONFIG": filepath.Join(cwd, "kc.yaml")}
	const repo = "github.com/org/repo"
	for _, tc := range []struct{ command, reason string }{
		{"kubectl apply -k " + repo, "unrecognized"},
		{"kubectl apply -k" + repo, "unrecognized"},
		{"kubectl apply -k=" + repo, "unrecognized"},
		{"kubectl apply --kustomize " + repo, "unrecognized"},
		{"kubectl apply --kustomize=" + repo + "//overlays/prod", "unrecognized"},
		{"kubectl apply -k git@host:org/repo.git", "unrecognized"},
		{"kubectl diff -k " + repo, "unrecognized"},
		{"kubectl kustomize " + repo, "unrecognized"},
		{"kubectl --as=admin get pods", "unrecognized"},
		{"kubectl apply -f -", "unrecognized"},
		{"kubectl apply edit-last-applied deploy/web", "unrecognized"},
		{"kubectl apply -Rf edit-last-applied", "unrecognized"},
		{"kubectl -R get pods", "unrecognized"},
		{"kubectl create foo bar", "unrecognized"},
		{"kubectl myplugin", "unrecognized"},
		{"kubectl", "unrecognized"},
		{"kubectl get pods -n", "unrecognized"},
		{"git status", "unrecognized"},
		{"env bash ./deploy.sh", "unrecognized"},
		{"FOO=bar bash ./deploy.sh", "unrecognized"},
		{"bash missing.sh", "unrecognized"},
		{"perl -E system+q+./deploy.sh+", "inline-code"},
		{"node --eval=1", "inline-code"},
		{"node --eval 1", "inline-code"},
		{"node -p 1", "inline-code"},
		{"node --print 1", "inline-code"},
		{"bash +c ./deploy.sh", "inline-code"},
		{"bash", "inline-code"},
		{"bash ./deploy.sh -c ls", ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.Contains(tc.command, ":") {
				t.Skip("a ':' in a path is not a Windows file name: stat fails and the call is unbound")
			}
			labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, tc.command), env, "unknown")
			if (labels["artifact.binding"] == "bound") != (tc.reason == "") || labels["artifact.unbound_reason"] != tc.reason {
				t.Fatalf("labels = %v, want unbound reason %q", labels, tc.reason)
			}
		})
	}
}
