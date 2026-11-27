package claude

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

func TestArtifactBindingEmptyCwdIsTheProcessCwd(t *testing.T) {
	dir := t.TempDir()
	artifactWriteFiles(t, dir, map[string]string{"deploy.sh": "echo deploy\n"})
	t.Chdir(dir)
	labels := artifactBindingLabels(context.Background(), artifactHookInput("", "bash deploy.sh"), map[string]string{}, "unknown")
	if labels["artifact.binding"] != "bound" || labels["artifact.digest"] == "" {
		t.Fatalf("labels = %v, want bound to deploy.sh in the hook's own cwd", labels)
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
	if strings.Trim(abs, artifactPlainBytes) != "" {
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

// TestArtifactBindingWindowsAliasNamesAreUnreadable covers names Git Bash's
// MSYS programs open as other files than Go does.
func TestArtifactBindingWindowsAliasNamesAreUnreadable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("trailing dots, NTFS streams and the SYSTEM attribute are Windows only")
	}
	cwd := t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{"deploy.sh": "echo deploy\n", "manifest.yaml": "kind: ConfigMap\n",
		"link.sh": "!<symlink>deploy.sh", "kc.yaml": "current-context: dev\n"})
	if err := os.WriteFile(filepath.Join(cwd, "deploy.sh:x"), []byte("echo stream\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "attrib", "+s", filepath.Join(cwd, "link.sh")).CombinedOutput(); err != nil {
		t.Fatalf("attrib: %v %s", err, out)
	}
	env := map[string]string{"KUBECONFIG": filepath.Join(cwd, "kc.yaml")}
	artifactCheckReaderPath(t, cwd, "bash deploy.sh", env, "")
	for _, command := range []string{"bash deploy.sh.", "kubectl apply -f manifest.yaml.", "bash deploy.sh:x", "bash link.sh"} {
		t.Run(command, func(t *testing.T) { artifactCheckReaderPath(t, cwd, command, env, "unreadable") })
	}
}

func TestArtifactBindingReaderRelativeNames(t *testing.T) {
	cwd := t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{
		"reviewed.sh": "printf reviewed", "executed.sh": "printf executed",
		"kc.yaml": "current-context: dev\n", "m.yaml": "kind: ConfigMap\n",
	})
	t.Run("plain-script", func(t *testing.T) {
		artifactCheckReaderPath(t, cwd, "bash reviewed.sh", nil, "")
	})
	switch runtime.GOOS {
	case "linux":
		artifactReaderLinux(t, cwd)
	case "windows":
		artifactReaderWindows(t, cwd)
	}
}

func artifactCheckReaderPath(t *testing.T, cwd, command string, env map[string]string, reason string) {
	t.Helper()
	labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, command), env, "unknown")
	if reason == "" {
		if labels["artifact.binding"] != "bound" || labels["artifact.digest"] == "" {
			t.Fatalf("%s: want bound with digest, got %v", command, labels)
		}
	} else if len(labels) != 2 || labels["artifact.binding"] != "unbound" || labels["artifact.unbound_reason"] != reason {
		t.Fatalf("%s: want only unbound/%s, got %v", command, reason, labels)
	}
}

func artifactReaderFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	return f
}

func artifactReaderLinux(t *testing.T, cwd string) {
	reviewed := artifactReaderFile(t, filepath.Join(cwd, "reviewed.sh"))
	kube := artifactReaderFile(t, filepath.Join(cwd, "kc.yaml"))
	fdPath := fmt.Sprintf("/proc/self/fd/%d", reviewed.Fd())
	kubePath := fmt.Sprintf("/proc/self/fd/%d", kube.Fd())
	other := t.TempDir()
	artifactWriteFiles(t, other, map[string]string{"external.sh": "printf external"})
	for name, target := range map[string]string{
		"alias.sh": fdPath, "relative.sh": strings.Repeat("../", 32) + fdPath[1:],
		"sel": "/proc/self", "a.sh": "b.sh", "b.sh": fmt.Sprintf("/dev/fd/%d", reviewed.Fd()),
		"kc-link.yaml": kubePath, "external.sh": filepath.Join(other, "external.sh"),
		"loop": "loop", "local.sh": "reviewed.sh", "home": "/proc/self",
	} {
		if err := os.Symlink(target, filepath.Join(cwd, name)); err != nil {
			t.Fatal(err)
		}
	}
	for name, path := range map[string]string{"fd-direct": fdPath, "fd-alias": "alias.sh"} {
		t.Run(name, func(t *testing.T) {
			artifactRunReaderScript(t, cwd, path, reviewed)
			artifactCheckReaderPath(t, cwd, "bash "+path, nil, "unreadable")
		})
	}
	artifactReaderLinuxCases(t, cwd, fdPath, kubePath)
	for _, path := range []string{"external.sh", "local.sh"} {
		t.Run(path, func(t *testing.T) { artifactCheckReaderPath(t, cwd, "bash "+path, nil, "") })
	}
}

func artifactRunReaderScript(t *testing.T, cwd, path string, reviewed *os.File) {
	t.Helper()
	executed := artifactReaderFile(t, filepath.Join(cwd, "executed.sh"))
	fd := int(reviewed.Fd())
	if fd < 3 || fd > 256 {
		t.Fatalf("unexpected fd %d", fd)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", path)
	cmd.Dir = cwd
	cmd.ExtraFiles = make([]*os.File, fd-2)
	for i := range cmd.ExtraFiles {
		cmd.ExtraFiles[i] = reviewed
	}
	cmd.ExtraFiles[fd-3] = executed
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "executed" {
		t.Fatalf("child must read its own descriptor: output=%q err=%v", out, err)
	}
}

func artifactReaderLinuxCases(t *testing.T, cwd, fdPath, kubePath string) {
	env := map[string]string{"KUBECONFIG": filepath.Join(cwd, "kc.yaml")}
	for _, tc := range []struct{ name, command string }{
		{"rel-alias", "bash relative.sh"},
		{"dev-fd", "kubectl apply -f " + strings.Replace(fdPath, "/proc/self", "/dev", 1)},
		{"dotdot", "bash sel/../self/fd/" + filepath.Base(fdPath)},
		{"chain", "bash a.sh"}, {"loop", "bash loop"},
		{"kubeconfig-word", "KUBECONFIG=kc-link.yaml kubectl get pods"},
		{"kubeconfig-flag", "kubectl --kubeconfig=kc-link.yaml get pods"},
		{"missing-special", "bash /proc/no-such-script"},
		{"dev-stdin", "bash /dev/stdin"}, {"sys", "bash /sys/kernel/notes"},
		{"root-dotdot", "bash /tmp/../proc/self/fd/" + filepath.Base(fdPath)},
		{"thread-self", "bash /proc/thread-self/fd/" + filepath.Base(fdPath)},
	} {
		t.Run(tc.name, func(t *testing.T) { artifactCheckReaderPath(t, cwd, tc.command, env, "unreadable") })
	}
	for _, tc := range []struct{ name, command, key, value string }{
		{"kubeconfig-env", "kubectl get pods", "KUBECONFIG", kubePath},
		{"kubeconfig-list", "kubectl get pods", "KUBECONFIG", env["KUBECONFIG"] + ":" + kubePath},
		{"kubeconfig-explicit-context", "kubectl --context=dev get pods", "KUBECONFIG", kubePath},
		{"kubeconfig-home", "kubectl get pods", "HOME", cwd + "/home/../self"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artifactCheckReaderPath(t, cwd, tc.command, map[string]string{"KUBECONFIG": "", tc.key: tc.value}, "kube-context")
		})
	}
}

func artifactReaderWindows(t *testing.T, cwd string) {
	t.Run("junction-control", func(t *testing.T) {
		real := t.TempDir()
		artifactWriteFiles(t, real, map[string]string{"m.yaml": "kind: ConfigMap\n"})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "cmd", "/c", "mklink", "/J", filepath.Join(cwd, "j"), real).CombinedOutput(); err != nil { // no-shell-exec-lint: mklink is a cmd builtin; this bounded fixture uses only test-owned paths.
			t.Fatalf("mklink: %v %s", err, out)
		}
		artifactCheckReaderPath(t, cwd, "kubectl apply -f j/m.yaml", map[string]string{"KUBECONFIG": filepath.Join(cwd, "kc.yaml")}, "")
	})
	for _, device := range []string{"CONIN$", "CONOUT$"} {
		t.Run("kubeconfig-console/"+device, func(t *testing.T) {
			env := map[string]string{"KUBECONFIG": device + ";" + filepath.Join(cwd, "kc.yaml")}
			artifactCheckReaderPath(t, cwd, "kubectl get pods", env, "kube-context")
		})
	}
}

func TestArtifactSpecialPathWalksEveryHop(t *testing.T) {
	if runtime.GOOS == "windows" {
		for _, path := range []string{`\\.\CONIN$`, `\\?\C:\Windows\win.ini`, `//./pipe/x`, `\\host\share\m.yaml`, `\??\C:\x`, "CONIN$", "CONOUT$"} {
			t.Run(path, func(t *testing.T) {
				if !artifactSpecialPath(artifactPath(t.TempDir(), path)) {
					t.Fatalf("namespace or device accepted: %s", path)
				}
			})
		}
		return
	}
	cwd := t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{"script": "printf ok", "sub/file": ""})
	for i := 0; i < 41; i++ {
		target := "script"
		if i > 0 {
			target = fmt.Sprintf("link%d", i-1)
		}
		if err := os.Symlink(target, filepath.Join(cwd, fmt.Sprintf("link%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(cwd)
	for path, want := range map[string]bool{
		"/proc": true, "//proc/self/fd/0": true, "/tmp/../proc/self": true,
		"/dev/stdin": true, "/sys": true, "./link39": false, "./link40": true,
		"sub/../script": false, "nope": false, "/procfoo/nope": false, "script/x": false,
	} {
		t.Run(path, func(t *testing.T) {
			if got := artifactSpecialPath(path); got != want {
				t.Fatalf("special(%q) = %v, want %v", path, got, want)
			}
		})
	}
}
