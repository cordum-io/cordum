package capprofile_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/infra/capprofile"
)

// repoRoot walks up from this package to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("repo root %s has no go.mod: %v", dir, err)
	}
	return dir
}

// composeDefault matches `- CORDUM_CAP_PROFILE=${CORDUM_CAP_PROFILE:-compat}`
// and captures the default.
var composeDefault = regexp.MustCompile(`CORDUM_CAP_PROFILE=\$\{CORDUM_CAP_PROFILE:-([^}]*)\}`)

// k8sDefault matches the `- name: CORDUM_CAP_PROFILE` / `value: "compat"` pair.
var k8sDefault = regexp.MustCompile(`name:\s*CORDUM_CAP_PROFILE\s*\n\s*value:\s*"([^"]*)"`)

// The deployed default must be a value the parser accepts and must NOT be
// production. A deploy surface that ships production-by-default would activate
// enforcement (and, today, refuse to boot) on every upgrade.
func TestDeployedProfileDefaultsAreCompatAndParseable(t *testing.T) {
	root := repoRoot(t)
	files := map[string]*regexp.Regexp{
		"docker-compose.yml":   composeDefault,
		"deploy/k8s/base.yaml": k8sDefault,
	}

	for rel, pattern := range files {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		matches := pattern.FindAllStringSubmatch(string(raw), -1)
		if len(matches) == 0 {
			t.Fatalf("%s declares no CORDUM_CAP_PROFILE default; the profile switch must be present on every deploy surface", rel)
		}
		for _, m := range matches {
			value := strings.TrimSpace(m[1])
			profile, err := capprofile.Parse(value)
			if err != nil {
				t.Fatalf("%s: deployed default %q is not a valid profile: %v", rel, value, err)
			}
			if profile.IsProduction() {
				t.Fatalf("%s: deployed default is %q; production must be opt-in, never the shipped default", rel, value)
			}
		}
		t.Logf("%s: %d CORDUM_CAP_PROFILE declaration(s), all compat", rel, len(matches))
	}
}

// All three control-plane components must expose the switch, otherwise an
// operator could set production for the scheduler while the gateway silently
// stays compat.
func TestAllControlPlaneComponentsDeclareTheProfileSwitch(t *testing.T) {
	root := repoRoot(t)
	cases := map[string]struct {
		pattern *regexp.Regexp
		want    int
	}{
		"docker-compose.yml":   {composeDefault, 3},
		"deploy/k8s/base.yaml": {k8sDefault, 3},
	}
	for rel, c := range cases {
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		got := len(c.pattern.FindAllStringSubmatch(string(raw), -1))
		if got < c.want {
			t.Fatalf("%s declares CORDUM_CAP_PROFILE %d time(s), want at least %d (scheduler, gateway, workflow-engine)", rel, got, c.want)
		}
	}
}
