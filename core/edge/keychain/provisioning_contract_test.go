package keychain

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProvisioningCommandsMatchGoKeyringTuple(t *testing.T) {
	t.Parallel()
	if keyringServiceName != "cordum-agentd" {
		t.Fatalf("keyringServiceName=%q, want cordum-agentd", keyringServiceName)
	}

	root := repoRoot(t)
	cases := []struct {
		name    string
		relPath string
		want    []string
	}{
		{
			name:    "posix installer",
			relPath: "tools/scripts/agentd-install/install.sh",
			want: []string{
				"security add-generic-password -a cordum_agentd_nonce -s cordum-agentd",
				"security add-generic-password -a cordum_api_key -s cordum-agentd",
				"secret-tool clear service cordum-agentd username cordum_agentd_nonce",
				"secret-tool clear service cordum-agentd username cordum_api_key",
				"service cordum-agentd username cordum_agentd_nonce",
				"service cordum-agentd username cordum_api_key",
			},
		},
		{
			name:    "windows installer",
			relPath: "tools/scripts/agentd-install/install.ps1",
			want: []string{
				"/delete:cordum-agentd:cordum_agentd_nonce",
				"/delete:cordum-agentd:cordum_api_key",
				"Invoke-Cmdkey -Target 'cordum-agentd:cordum_agentd_nonce' -User 'cordum_agentd_nonce'",
				"Invoke-Cmdkey -Target 'cordum-agentd:cordum_api_key'      -User 'cordum_api_key'",
			},
		},
		{
			name:    "operator docs",
			relPath: "docs/security/agentd-keychain.md",
			want: []string{
				`go-keyring.Get("cordum-agentd", <key>)`,
				"service=cordum-agentd, account=<key>",
				"service=cordum-agentd, username=<key>",
				"target `cordum-agentd:<key>`",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertFileContains(t, filepath.Join(root, tc.relPath), tc.want)
			assertFileOmitsOldTuple(t, filepath.Join(root, tc.relPath))
		})
	}
}

func TestServiceTemplatesDocumentGoKeyringTuple(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	cases := map[string][]string{
		"tools/scripts/launchd/com.cordum.agentd.plist": {
			"security add-generic-password -a cordum_agentd_nonce -s cordum-agentd",
			"security add-generic-password -a cordum_api_key -s cordum-agentd",
		},
		"tools/scripts/systemd/cordum-agentd.service": {
			"service cordum-agentd username cordum_agentd_nonce",
			"service cordum-agentd username cordum_api_key",
		},
		"tools/scripts/windows/cordum-agentd-service.xml": {
			"cmdkey /generic:cordum-agentd:cordum_agentd_nonce /user:cordum_agentd_nonce",
			"cmdkey /generic:cordum-agentd:cordum_api_key      /user:cordum_api_key",
		},
		"tools/scripts/agentd-install/synthetic-test/run.sh": {
			"grep -F -q 'BOOTSTRAP-FAIL:'",
			"security delete-generic-password -a cordum_agentd_nonce -s cordum-agentd",
			"secret-tool clear service cordum-agentd username cordum_agentd_nonce",
		},
	}
	for relPath, want := range cases {
		relPath, want := relPath, want
		t.Run(relPath, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(root, relPath)
			assertFileContains(t, path, want)
			assertFileOmitsOldTuple(t, path)
		})
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func assertFileContains(t *testing.T, path string, want []string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := string(body)
	for _, needle := range want {
		if !strings.Contains(text, needle) {
			t.Fatalf("%s missing required tuple fragment %q", path, needle)
		}
	}
}

func assertFileOmitsOldTuple(t *testing.T, path string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	forbidden := []string{
		`-a "$USER" -s cordum_agentd_nonce`,
		`-a "$USER" -s cordum_api_key`,
		"service cordum-agentd account cordum_agentd_nonce",
		"service cordum-agentd account cordum_api_key",
		"/generic:cordum_agentd_nonce",
		"/generic:cordum_api_key",
		"/delete:cordum_agentd_nonce",
		"/delete:cordum_api_key",
		"/list:cordum_agentd_nonce",
	}
	text := string(body)
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Fatalf("%s still contains old keyring tuple %q", path, needle)
		}
	}
}
