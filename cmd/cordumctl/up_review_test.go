package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

func TestDevBootstrapAdminPasswordPassesValidation(t *testing.T) {
	t.Setenv("CORDUM_ADMIN_PASSWORD", "")
	result, err := ensureDevBootstrapEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.ValidatePassword(readEnvFile(result.EnvPath, "CORDUM_ADMIN_PASSWORD")); err != nil {
		t.Fatalf("generated password fails gateway validation: %v", err)
	}
}

func TestDevBootstrapCredentialReadersAgree(t *testing.T) {
	for _, tc := range []struct{ name, content, want string }{
		{"generated with empty export", "", ""},
		{"canonical before fallback", "CORDUM_API_KEY=canonical\nAPI_KEY=legacy\n", "canonical"},
		{"canonical after fallback", "API_KEY=legacy\nCORDUM_API_KEY=canonical\n", "canonical"},
		{"last canonical definition", "CORDUM_API_KEY=old\nAPI_KEY=legacy\nCORDUM_API_KEY=canonical\n", "canonical"},
		{"blank exported interpolation input", "REDIS_PASSWORD=redis-secret\nCORDUM_API_KEY=${REDIS_PASSWORD:-fallback-key}\n", "redis-secret"},
		{"generated interpolation input", "REDIS_PASSWORD=${DOTENV_REVIEW_EMPTY}\nCORDUM_API_KEY=${REDIS_PASSWORD:-fallback-key}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("USERPROFILE", t.TempDir())
			t.Setenv("CORDUM_API_KEY", "")
			t.Setenv("API_KEY", "")
			t.Setenv("REDIS_PASSWORD", "")
			t.Setenv("DOTENV_REVIEW_EMPTY", "")
			if err := os.WriteFile(".env", []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := ensureDevBootstrapEnv(dir)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != "" && result.APIKey != tc.want {
				t.Fatalf("bootstrap selected %q, want %q", result.APIKey, tc.want)
			}
			checkAPIKeyDefaults(t, "empty after bootstrap", result.APIKey)
			if got := readEnvFile(result.EnvPath, "CORDUM_API_KEY", "API_KEY"); got != result.APIKey {
				t.Fatalf("dotenv reader differs from bootstrap: %q != %q", got, result.APIKey)
			}
			second, err := ensureDevBootstrapEnv(dir)
			if err != nil {
				t.Fatal(err)
			}
			if second.APIKey != result.APIKey || len(second.AddedKeys) != 0 {
				t.Fatalf("second bootstrap changed credentials: key=%q added=%v", second.APIKey, second.AddedKeys)
			}
		})
	}
}

func TestDevBootstrapFillsEffectivelyEmptyDefinitions(t *testing.T) {
	for _, content := range []string{
		"CORDUM_API_KEY=${DOTENV_TEST_EMPTY}\n",
		"export CORDUM_API_KEY=\"${DOTENV_TEST_EMPTY}\" # keep comment\n",
		"CORDUM_API_KEY=\nCORDUM_API_KEY=${DOTENV_TEST_EMPTY}\n",
		"CORDUM_API_KEY=preserved\nCORDUM_API_KEY=${DOTENV_TEST_EMPTY}\n",
		"CORDUM_API_KEY: ${DOTENV_TEST_EMPTY}\n",
	} {
		t.Run(content, func(t *testing.T) {
			t.Setenv("CORDUM_API_KEY", "")
			t.Setenv("API_KEY", "")
			t.Setenv("DOTENV_TEST_EMPTY", "")
			dir := t.TempDir()
			path := filepath.Join(dir, ".env")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := ensureDevBootstrapEnv(dir)
			if err != nil {
				t.Fatal(err)
			}
			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(written), "${DOTENV_TEST_EMPTY}") {
				t.Error("effectively empty API key definition was not filled in place")
			}
			if strings.Contains(content, "=preserved") && !strings.Contains(string(written), "CORDUM_API_KEY=preserved\n") {
				t.Error("nonempty definition was changed")
			}
			if got := readEnvFile(path, "CORDUM_API_KEY"); got != result.APIKey {
				t.Fatalf("written API key differs from bootstrap: %q != %q", got, result.APIKey)
			}
		})
	}
}

func TestSeedDevMCPConfigPreservesNonObject(t *testing.T) {
	for _, raw := range []string{"null", "false", "0", `"disabled"`, "[]"} {
		t.Run(raw, func(t *testing.T) {
			var posted bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posted = true
					w.WriteHeader(http.StatusNoContent)
					return
				}
				var value any
				if err := json.Unmarshal([]byte(raw), &value); err != nil {
					t.Error(err)
				}
				_ = json.NewEncoder(w).Encode(configDoc{Data: map[string]any{"mcp": value}})
			}))
			defer srv.Close()
			if err := seedDevMCPConfig(context.Background(), newRestClient(srv.URL, "test", "default", nil), true); err == nil {
				t.Error("expected explicit error for non-object MCP configuration")
			}
			if posted {
				t.Error("non-object MCP configuration was overwritten")
			}
		})
	}
}
