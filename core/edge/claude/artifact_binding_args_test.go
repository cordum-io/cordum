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
	artifactWriteFiles(t, cwd, map[string]string{"deploy.sh": "echo deploy\n", "+c": "", "inspect": "", "kubectl": "", "bin/kubectl": "",
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
		{"kubectl exec db-0 -- ./migrate.sh", "unrecognized"},
		{"kubectl exec db-0 -- psql -f migrations/up.sql", "unrecognized"},
		{"kubectl -n team exec db-0 -- kubectl apply -k " + repo, "unrecognized"},
		{"kubectl exec db-0 ls", "unrecognized"},
		{"kubectl run x --image=busybox", "unrecognized"},
		{"kubectl debug node/n --image=busybox", "unrecognized"},
		{"kubectl attach db-0", "unrecognized"},
		{"kubectl port-forward db-0 8080", "unrecognized"},
		{"kubectl get -- pods", "unrecognized"},
		{"X=x/bash ./bin/kubectl apply -k " + repo, "unrecognized"},
		{"X=x/python3 ./bin/kubectl apply -k " + repo, "unrecognized"},
		{"KUBECONFIG=kc.yaml X=x/bash ./bin/kubectl apply -k " + repo, "unrecognized"},
		{"X=x/bash kubectl apply -k " + repo, "unrecognized"},
		{"BASH_ENV=deploy.sh bash ./deploy.sh", "unrecognized"},
		{"KUBECONFIG+=kc.yaml kubectl get pods", "unrecognized"},
		{"command kubectl get pods", "unrecognized"},
		{"builtin kubectl get pods", "unrecognized"},
		{"exec kubectl get pods", "unrecognized"},
		{"sudo kubectl get pods", "unrecognized"},
		{"nohup bash ./deploy.sh", "unrecognized"},
		{"nice kubectl get pods", "unrecognized"},
		{"time kubectl get pods", "unrecognized"},
		{"timeout 5 bash ./deploy.sh", "unrecognized"},
		{"xargs kubectl apply -f", "unrecognized"},
		{"noglob kubectl get pods", "unrecognized"},
		{"- kubectl get pods", "unrecognized"},
		{"coproc kubectl get pods", "unrecognized"},
		{"node inspect ./deploy.sh", "unrecognized"},
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

// TestArtifactBindingShellFeaturesAreUnbound names each bash and zsh feature
// that lets the shell, not the command line, decide what runs or is read.
func TestArtifactBindingShellFeaturesAreUnbound(t *testing.T) {
	cwd := t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{"manifest.yaml": "kind: ConfigMap\n", "deploy.sh": "echo deploy\n",
		"kc.yaml": "current-context: dev\ncontexts:\n- name: dev\n  context: {}\n"})
	env := map[string]string{"KUBECONFIG": filepath.Join(cwd, "kc.yaml")}
	for _, tc := range []struct{ feature, command string }{
		{"single quotes", "kubectl apply -f 'manifest.yaml'"},
		{"double quotes", "kubectl apply -f \"manifest.yaml\""},
		{"backslash escape", "kubectl apply -f manifest\\.yaml"},
		{"ANSI-C quoting", "kubectl apply -f $'manifest.yaml'"},
		{"list ;", "kubectl get pods; bash ./deploy.sh"},
		{"and list &&", "kubectl get pods && bash ./deploy.sh"},
		{"or list ||", "kubectl get pods || bash ./deploy.sh"},
		{"background &", "bash ./deploy.sh &"},
		{"pipe |", "cat manifest.yaml | kubectl apply -f -"},
		{"zsh pipe |&", "bash ./deploy.sh |& cat"},
		{"newline", "kubectl get pods\nbash ./deploy.sh"},
		{"input redirection", "kubectl apply -f - < manifest.yaml"},
		{"output redirection", "bash ./deploy.sh > out.txt"},
		{"zsh clobber >|", "bash ./deploy.sh >| out.txt"},
		{"here-document", "kubectl apply -f - <<EOF"},
		{"here-string", "kubectl apply -f - <<< manifest.yaml"},
		{"fd duplication", "bash ./deploy.sh 2>&1"},
		{"command substitution $()", "kubectl apply -f $(echo manifest.yaml)"},
		{"command substitution backticks", "kubectl apply -f `echo manifest.yaml`"},
		{"process substitution <()", "kubectl apply -f <(cat manifest.yaml)"},
		{"zsh process substitution =()", "kubectl apply -f =(cat manifest.yaml)"},
		{"parameter expansion", "kubectl apply -f $M"},
		{"braced parameter expansion", "kubectl apply -f ${M}"},
		{"arithmetic expansion", "kubectl get pods -n $((1))"},
		{"zsh arithmetic $[]", "kubectl get pods -n $[1]"},
		{"subshell", "(kubectl get pods)"},
		{"group", "{ kubectl get pods; }"},
		{"conditional expression", "[[ -f manifest.yaml ]]"},
		{"brace expansion list", "kubectl apply -f {manifest,other}.yaml"},
		{"brace expansion range", "kubectl apply -f m{1..3}.yaml"},
		{"glob *", "kubectl apply -f *.yaml"},
		{"glob ?", "kubectl apply -f manifest.y?ml"},
		{"glob []", "kubectl apply -f [m]anifest.yaml"},
		{"bash extglob", "kubectl apply -f +(manifest).yaml"},
		{"zsh extended glob ^", "kubectl apply -f ^other.yaml"},
		{"zsh extended glob #", "kubectl apply -f m#.yaml"},
		{"zsh extended glob ~", "kubectl apply -f *.yaml~other.yaml"},
		{"zsh glob qualifier", "kubectl apply -f manifest.yaml(.)"},
		{"zsh numeric range", "kubectl apply -f m<1-3>.yaml"},
		{"tilde", "kubectl apply -f ~/manifest.yaml"},
		{"tilde user", "kubectl apply -f ~root/manifest.yaml"},
		{"tilde in an assignment", "KUBECONFIG=~/kc.yaml kubectl get pods"},
		{"zsh = word", "kubectl apply -f =manifest.yaml"},
		{"zsh = program", "=kubectl apply -f manifest.yaml"},
		{"zsh = value", "kubectl create configmap cm --from-file =settings.env"},
		{"zsh = in an assignment", "KUBECONFIG==kubectl kubectl get pods"},
		{"zsh = after : in an assignment", "KUBECONFIG=kc.yaml:=kubectl kubectl get pods"},
		{"zsh MAGIC_EQUAL_SUBST", "kubectl apply --filename==manifest.yaml"},
		{"comment", "kubectl get pods #-k github.com/org/repo"},
		{"pipeline negation", "! kubectl get pods"},
		{"history expansion", "kubectl apply -f !!"},
		{"history substitution", "^pods^nodes"},
	} {
		t.Run(tc.feature, func(t *testing.T) {
			labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, tc.command), env, "unknown")
			if labels["artifact.binding"] != "unbound" || labels["artifact.unbound_reason"] != "shell-syntax" {
				t.Fatalf("%s: labels = %v, want unbound with reason shell-syntax", tc.command, labels)
			}
		})
	}
}

func TestArtifactBindingSpecialFilesAreUnreadable(t *testing.T) {
	commands := map[string][]string{
		"windows": {"kubectl apply -f CON", "kubectl apply -f con.yaml", "kubectl apply -f sub/aux.yaml", "bash COM1", "kubectl apply --filename=LPT1"},
		"linux":   {"kubectl apply -f /proc/self/environ", "bash /proc/self/cmdline"},
	}[runtime.GOOS]
	if commands == nil {
		t.Skip("Windows device names and Linux procfs files only")
	}
	cwd := t.TempDir()
	artifactWriteFiles(t, cwd, map[string]string{"kc.yaml": "current-context: dev\n"})
	env := map[string]string{"KUBECONFIG": filepath.Join(cwd, "kc.yaml")}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			labels := artifactBindingLabels(context.Background(), artifactHookInput(cwd, command), env, "unknown")
			if labels["artifact.binding"] != "unbound" || labels["artifact.unbound_reason"] != "unreadable" {
				t.Fatalf("%s: labels = %v, want unbound with reason unreadable", command, labels)
			}
		})
	}
}
