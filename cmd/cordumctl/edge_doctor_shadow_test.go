package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeKubeconfig writes a minimal kubeconfig that resolves syntactically so
// `--shadow-cluster` flag parsing accepts the path. Tests inject a fake
// kube client so the file contents are never used to dial a cluster.
func fakeKubeconfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	data := []byte(`apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster: {server: https://127.0.0.1:1}
contexts:
- name: fake
  context: {cluster: fake, user: fake}
current-context: fake
users:
- name: fake
  user: {token: fake-token}
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fake kubeconfig: %v", err)
	}
	return path
}

// withKubeClient swaps the package-level kube client builder for the
// duration of one test. The fake clientset records every K8s API call so
// the read-only invariant assertions can inspect Actions().
func withKubeClient(t *testing.T, client kubernetes.Interface) {
	t.Helper()
	prev := edgeDoctorKubeClientBuilder
	edgeDoctorKubeClientBuilder = func(_ string) (kubernetes.Interface, error) {
		return client, nil
	}
	t.Cleanup(func() { edgeDoctorKubeClientBuilder = prev })
}

func TestEdgeDoctor_ShadowCluster_RunsDetectorReadOnly(t *testing.T) {
	client := fake.NewSimpleClientset()
	withKubeClient(t, client)

	code, stdout, stderr := runEdgeDoctorForTest(t, "--shadow-cluster", fakeKubeconfig(t))
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	sawRead := false
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "list", "get", "watch":
			sawRead = true
		case "create", "update", "patch", "delete", "deletecollection":
			t.Fatalf("read-only invariant broken: detector invoked %s on %s", a.GetVerb(), a.GetResource().Resource)
		}
	}
	if !sawRead {
		t.Fatalf("detector did not invoke any read verb against fake client; Actions()=%+v", client.Actions())
	}
	if !strings.Contains(stdout, "shadow-cluster preview") {
		t.Fatalf("stdout missing preview banner: %s", stdout)
	}
}

func TestEdgeDoctor_ShadowCluster_JSON(t *testing.T) {
	client := fake.NewSimpleClientset()
	withKubeClient(t, client)

	code, stdout, stderr := runEdgeDoctorForTest(t, "--shadow-cluster", fakeKubeconfig(t), "--json")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	var env edgeDoctorShadowJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("decode JSON: %v raw=%s", err, stdout)
	}
	if env.Mode != "shadow_cluster_preview" {
		t.Fatalf("Mode=%q want shadow_cluster_preview", env.Mode)
	}
	if !env.DryRun {
		t.Fatalf("DryRun=false; preview is always dry-run")
	}
	if env.Findings == nil {
		t.Fatalf("Findings is nil; want at least an empty slice for JSON consumers")
	}
}

func TestEdgeDoctor_ShadowCluster_EmitsFinding(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "evil-claude",
			UID:       "uid-evil-claude",
			Labels:    map[string]string{"cordum.io/tenant-id": "tenant-test"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "main", Image: "evil-registry.example.com/claude-agent:latest"}},
		},
	}
	client := fake.NewSimpleClientset(pod)
	withKubeClient(t, client)

	code, stdout, _ := runEdgeDoctorForTest(t, "--shadow-cluster", fakeKubeconfig(t), "--json")
	if code != 0 {
		t.Fatalf("exit=%d stdout=%s", code, stdout)
	}
	var env edgeDoctorShadowJSONEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("decode JSON: %v raw=%s", err, stdout)
	}
	if len(env.Findings) == 0 {
		t.Fatalf("expected ≥1 finding for untrusted-image pod; got 0. JSON: %s", stdout)
	}
}

func TestEdgeDoctor_ShadowCluster_KubeconfigMissing(t *testing.T) {
	code, _, stderr := runEdgeDoctorForTest(t, "--shadow-cluster", "")
	if code == 0 {
		t.Fatalf("expected non-zero exit for empty --shadow-cluster path; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "shadow-cluster") {
		t.Fatalf("stderr missing --shadow-cluster reference: %s", stderr)
	}
}

func TestEdgeDoctor_ShadowCI_ProviderNotInBuild(t *testing.T) {
	for _, provider := range []string{"github", "gitlab", "jenkins", "buildkite", "circleci"} {
		t.Run(provider, func(t *testing.T) {
			code, _, stderr := runEdgeDoctorForTest(t, "--shadow-ci", provider+":fake-token")
			if code == 0 {
				t.Fatalf("expected non-zero exit for unsupported provider %s; stderr=%s", provider, stderr)
			}
			want := "provider " + provider + " not supported in this build"
			if !strings.Contains(stderr, want) {
				t.Fatalf("stderr missing %q for provider %s: %s", want, provider, stderr)
			}
		})
	}
}

func TestEdgeDoctor_ShadowCI_UnknownProvider(t *testing.T) {
	code, _, stderr := runEdgeDoctorForTest(t, "--shadow-ci", "azuredevops:tok")
	if code == 0 {
		t.Fatalf("expected non-zero exit for unknown provider; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "provider azuredevops not recognized") {
		t.Fatalf("stderr missing recognition error: %s", stderr)
	}
	for _, want := range []string{"github", "gitlab", "jenkins", "buildkite", "circleci"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing supported-list entry %q: %s", want, stderr)
		}
	}
}

func TestEdgeDoctor_ShadowCI_MalformedSpec(t *testing.T) {
	code, _, stderr := runEdgeDoctorForTest(t, "--shadow-ci", "github-no-colon")
	if code == 0 {
		t.Fatalf("expected non-zero exit for malformed spec; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "shadow-ci") || !strings.Contains(stderr, "<provider>:") {
		t.Fatalf("stderr missing format guidance: %s", stderr)
	}
}

func TestEdgeDoctor_ShadowCI_ReadOnly(t *testing.T) {
	for _, provider := range []string{"github", "gitlab", "jenkins", "buildkite", "circleci", "azuredevops"} {
		t.Run(provider, func(t *testing.T) {
			rec := &recordingRoundTripper{}
			prev := edgeDoctorCIHTTPTransport
			edgeDoctorCIHTTPTransport = rec
			t.Cleanup(func() { edgeDoctorCIHTTPTransport = prev })

			_, _, _ = runEdgeDoctorForTest(t, "--shadow-ci", provider+":tok")

			for _, m := range rec.methods {
				switch m {
				case "POST", "PUT", "PATCH", "DELETE":
					t.Fatalf("dry-run %s issued mutating HTTP %s; methods=%v", provider, m, rec.methods)
				}
			}
		})
	}
}

func TestEdgeDoctor_Flags_Registered(t *testing.T) {
	fs := newEdgeDoctorFlagSet()
	for _, name := range []string{"shadow-cluster", "shadow-ci"} {
		if fs.Lookup(name) == nil {
			t.Fatalf("flag --%s not registered on edge doctor flagSet", name)
		}
	}
	if u := fs.Lookup("shadow-cluster").Usage; !strings.Contains(strings.ToLower(u), "kubeconfig") {
		t.Fatalf("--shadow-cluster usage missing kubeconfig hint: %s", u)
	}
	if u := fs.Lookup("shadow-ci").Usage; !strings.Contains(strings.ToLower(u), "provider") {
		t.Fatalf("--shadow-ci usage missing provider hint: %s", u)
	}
}

// recordingRoundTripper captures every HTTP method invoked by CI
// dry-run paths so the read-only invariant test can assert no mutating
// verb was issued. Returns an empty 200 response so callers do not
// short-circuit on transport errors.
type recordingRoundTripper struct {
	methods []string
}

func (r *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.methods = append(r.methods, req.Method)
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}
