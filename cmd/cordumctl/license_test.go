package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/licensing"
)

func TestRunLicenseInstallCopiesToPreferredPath(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("APPDATA", base)
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("USERPROFILE", filepath.Join(base, "home"))
	t.Setenv(licenseFileEnvName, "")

	source := filepath.Join(t.TempDir(), "license.json")
	const content = `{"payload":{"org_id":"org-1","license_id":"lic-123","plan":"team"},"signature":"sig"}`
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatalf("write source license: %v", err)
	}

	stdout := captureStdout(t, func() {
		if err := runLicenseInstallE([]string{source}); err != nil {
			t.Fatalf("runLicenseInstallE() error = %v", err)
		}
	})

	destination, err := licensing.PreferredLicenseFilePath()
	if err != nil {
		t.Fatalf("PreferredLicenseFilePath() error = %v", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("read installed license: %v", err)
	}
	if string(data) != content {
		t.Fatalf("installed license content mismatch: %q", string(data))
	}
	if !strings.Contains(stdout, destination) {
		t.Fatalf("expected install output to mention %q, got %q", destination, stdout)
	}
}

func TestRunLicenseInfoReadsInstalledLicense(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("APPDATA", base)
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("USERPROFILE", filepath.Join(base, "home"))
	t.Setenv(licenseFileEnvName, "")

	destination, err := licensing.PreferredLicenseFilePath()
	if err != nil {
		t.Fatalf("PreferredLicenseFilePath() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatalf("create destination dir: %v", err)
	}
	if err := os.WriteFile(destination, []byte(`{"payload":{"org_id":"org-1","license_id":"lic-123","plan":"team","deployment_type":"self-hosted"},"signature":"sig"}`), 0o600); err != nil {
		t.Fatalf("write installed license: %v", err)
	}

	stdout := captureStdout(t, func() {
		if err := runLicenseInfoE(nil); err != nil {
			t.Fatalf("runLicenseInfoE() error = %v", err)
		}
	})

	for _, want := range []string{"Plan", "Team", "License ID", "lic-123", "Commercial rights"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected license info output to contain %q, got %q", want, stdout)
		}
	}
}

func TestRunStatusShowsTierExpiryAndUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"time":"2026-04-09T12:00:00Z",
				"uptime_seconds":3600,
				"build":{"version":"dev","commit":"abc123","date":"2026-04-09"},
				"nats":{"connected":true,"status":"CONNECTED"},
				"redis":{"ok":true},
				"workers":{"count":4},
				"license":{"plan":"enterprise","status":"warning","expires_at":"2026-05-01T00:00:00Z"}
			}`))
		case "/api/v1/license/usage":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"tenant_id":"default",
				"plan":"enterprise",
				"license":{"plan":"enterprise","status":"warning","expires_at":"2026-05-01T00:00:00Z"},
				"usage":{
					"workers":{"current":4,"allowed":25,"registered":4,"connected":3},
					"concurrent_jobs":{"current":2,"allowed":25},
					"active_workflows":{"current":1,"allowed":25},
					"workflow_steps":{"allowed":200},
					"schemas":{"current":3,"allowed":50},
					"policy_bundles":{"current":2,"allowed":10},
					"requests_per_second":{"allowed":2000},
					"prompt_chars":{"allowed":250000},
					"body_bytes":{"allowed":1048576},
					"approval_mode":{"allowed":"multi"}
				}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	stdout := captureStdout(t, func() {
		if err := runStatusCmdE([]string{"--gateway", srv.URL}); err != nil {
			t.Fatalf("runStatusCmdE() error = %v", err)
		}
	})

	for _, want := range []string{"Tier", "Enterprise", "Expiry", "2026-05-01T00:00:00Z", "Usage vs limits", "Workers", "25"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("expected status output to contain %q, got %q", want, stdout)
		}
	}
}
