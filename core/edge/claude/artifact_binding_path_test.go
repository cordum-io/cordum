package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestArtifactBindingFollowsLinkBeforeDotDot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows removes .. by name before it follows a link, for the command too")
	}
	cwd, target := t.TempDir(), t.TempDir()
	artifactWriteFiles(t, target, map[string]string{"sub/keep": "", "kc.yaml": "current-context: dev\n"})
	if err := os.Symlink(filepath.Join(target, "sub"), filepath.Join(cwd, "link")); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"KUBECONFIG": filepath.Join(target, "kc.yaml")}
	digest := func(content string) string {
		t.Helper()
		artifactWriteFiles(t, target, map[string]string{"manifest.yaml": content})
		labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, "kubectl apply -f link/../manifest.yaml"), env, "unknown")
		if labels["artifact.binding"] != "bound" {
			t.Fatalf("labels = %v, want bound", labels)
		}
		return labels["artifact.digest"]
	}
	if digest("v1") == digest("v2") {
		t.Fatal("the digest must cover the manifest the kernel opens for link/../manifest.yaml")
	}
}

func TestArtifactBindingWindowsDriveNames(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows resolves a name against a current drive")
	}
	cwd := t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{"manifest.yaml": "kind: ConfigMap\n", "kc.yaml": "current-context: dev\n"})
	env := map[string]string{"KUBECONFIG": filepath.Join(cwd, "kc.yaml")}
	abs := filepath.ToSlash(filepath.Join(cwd, "manifest.yaml"))
	if strings.ContainsAny(abs, artifactShellSyntax) {
		t.Skipf("the temp dir %s needs shell quoting", abs)
	}
	for _, tc := range []struct{ command, reason string }{
		{"kubectl apply -f /manifest.yaml", "unreadable"},
		{"kubectl apply -f z:manifest.yaml", "unreadable"},
		{"kubectl apply -f " + abs, ""},
	} {
		t.Run(tc.command, func(t *testing.T) {
			labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, tc.command), env, "unknown")
			if (labels["artifact.binding"] == "bound") != (tc.reason == "") || labels["artifact.unbound_reason"] != tc.reason {
				t.Fatalf("labels = %v, want unbound reason %q", labels, tc.reason)
			}
		})
	}
}
