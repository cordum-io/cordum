package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Deleting a StatefulSet without --cascade=orphan deletes its pods too, so on a
// serving cluster the page's steps would cause a full outage. Every kubectl line
// that deletes a StatefulSet must keep the pods.
func TestK8sDeploymentDocsOrphanStatefulSetDeletes(t *testing.T) {
	root, err := repoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	page, err := os.ReadFile(filepath.Join(root, "docs", "k8s-deployment.md"))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for i, line := range strings.Split(string(page), "\n") {
		deletes := strings.Contains(line, "delete statefulset") || strings.Contains(line, "delete sts ")
		if !deletes || !strings.Contains(line, "kubectl") {
			continue
		}
		found++
		if !strings.Contains(line, "--cascade=orphan") {
			t.Errorf("docs/k8s-deployment.md:%d deletes a StatefulSet without --cascade=orphan: %s", i+1, strings.TrimSpace(line))
		}
	}
	if found == 0 {
		t.Error("docs/k8s-deployment.md has no kubectl line that deletes a StatefulSet; the check would pass vacuously")
	}
}
