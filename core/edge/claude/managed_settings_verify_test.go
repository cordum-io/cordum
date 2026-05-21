package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const goldenManagedSettingsPath = "testdata/settings/managed-settings.json"

func TestVerifyManagedSettingsPassesGolden(t *testing.T) {
	data, err := os.ReadFile(goldenManagedSettingsPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	res, err := VerifyManagedSettings(data)
	if err != nil {
		t.Fatalf("VerifyManagedSettings returned error on golden: %v", err)
	}
	if !res.OK {
		t.Fatalf("golden fixture must verify OK; drifts=%+v", res.Drifts)
	}
	if len(res.Drifts) != 0 {
		t.Fatalf("golden fixture must report 0 drifts; got %d: %+v", len(res.Drifts), res.Drifts)
	}
}

func TestVerifyManagedSettingsDetectsAllInvariantViolations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		mutate    func(map[string]any)
		wantField string
	}{
		{
			name:      "allowManagedHooksOnly_false",
			mutate:    func(m map[string]any) { m["allowManagedHooksOnly"] = false },
			wantField: "allowManagedHooksOnly",
		},
		{
			name:      "allowManagedMcpServersOnly_false",
			mutate:    func(m map[string]any) { m["allowManagedMcpServersOnly"] = false },
			wantField: "allowManagedMcpServersOnly",
		},
		{
			name:      "disableBypassPermissionsMode_wrong_literal",
			mutate:    func(m map[string]any) { m["disableBypassPermissionsMode"] = "warn" },
			wantField: "disableBypassPermissionsMode",
		},
		{
			name: "missing_PreToolUse_hook",
			mutate: func(m map[string]any) {
				hooks, _ := m["hooks"].(map[string]any)
				delete(hooks, "PreToolUse")
			},
			wantField: "hooks.PreToolUse",
		},
		{
			name: "missing_PostToolUse_hook",
			mutate: func(m map[string]any) {
				hooks, _ := m["hooks"].(map[string]any)
				delete(hooks, "PostToolUse")
			},
			wantField: "hooks.PostToolUse",
		},
		{
			name: "missing_PostToolUseFailure_hook",
			mutate: func(m map[string]any) {
				hooks, _ := m["hooks"].(map[string]any)
				delete(hooks, "PostToolUseFailure")
			},
			wantField: "hooks.PostToolUseFailure",
		},
		{
			name: "missing_UserPromptSubmit_hook",
			mutate: func(m map[string]any) {
				hooks, _ := m["hooks"].(map[string]any)
				delete(hooks, "UserPromptSubmit")
			},
			wantField: "hooks.UserPromptSubmit",
		},
		{
			name: "missing_ConfigChange_hook",
			mutate: func(m map[string]any) {
				hooks, _ := m["hooks"].(map[string]any)
				delete(hooks, "ConfigChange")
			},
			wantField: "hooks.ConfigChange",
		},
		{
			name: "missing_FileChanged_hook",
			mutate: func(m map[string]any) {
				hooks, _ := m["hooks"].(map[string]any)
				delete(hooks, "FileChanged")
			},
			wantField: "hooks.FileChanged",
		},
		{
			name: "env_fail_closed_not_true",
			mutate: func(m map[string]any) {
				env, _ := m["env"].(map[string]any)
				env["CORDUM_AGENTD_FAIL_CLOSED"] = "false"
			},
			wantField: "env.CORDUM_AGENTD_FAIL_CLOSED",
		},
		{
			name: "env_managed_policy_mode_wrong",
			mutate: func(m map[string]any) {
				env, _ := m["env"].(map[string]any)
				env["CORDUM_EDGE_MANAGED_POLICY_MODE"] = "observe"
			},
			wantField: "env.CORDUM_EDGE_MANAGED_POLICY_MODE",
		},
		{
			name: "env_managed_hooks_only_not_true",
			mutate: func(m map[string]any) {
				env, _ := m["env"].(map[string]any)
				env["CORDUM_EDGE_MANAGED_HOOKS_ONLY"] = "false"
			},
			wantField: "env.CORDUM_EDGE_MANAGED_HOOKS_ONLY",
		},
		{
			name: "env_agentd_url_empty",
			mutate: func(m map[string]any) {
				env, _ := m["env"].(map[string]any)
				env["CORDUM_AGENTD_URL"] = ""
			},
			wantField: "env.CORDUM_AGENTD_URL",
		},
		{
			name: "env_agentd_url_carries_query",
			mutate: func(m map[string]any) {
				env, _ := m["env"].(map[string]any)
				env["CORDUM_AGENTD_URL"] = "http://127.0.0.1:8765/v1/edge/hooks/claude?token=abc"
			},
			wantField: "env.CORDUM_AGENTD_URL",
		},
		{
			name: "env_carries_hook_nonce",
			mutate: func(m map[string]any) {
				env, _ := m["env"].(map[string]any)
				env["CORDUM_AGENTD_HOOK_NONCE"] = "deadbeef"
			},
			wantField: "env.CORDUM_AGENTD_HOOK_NONCE",
		},
		{
			name: "env_carries_anthropic_api_key",
			mutate: func(m map[string]any) {
				env, _ := m["env"].(map[string]any)
				env["ANTHROPIC_API_KEY"] = "sk-test-not-real"
			},
			wantField: "env.ANTHROPIC_API_KEY",
		},
		{
			name: "serialized_form_carries_bearer_marker",
			mutate: func(m map[string]any) {
				env, _ := m["env"].(map[string]any)
				env["CUSTOM_DEBUG"] = "Authorization: Bearer leaked-token"
			},
			wantField: "serialized.sensitive_marker",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bundle := mustGenerateGoldenBundle(t)
			var doc map[string]any
			if err := json.Unmarshal(bundle.ManagedSettingsJSON, &doc); err != nil {
				t.Fatalf("unmarshal generated managed settings: %v", err)
			}
			tc.mutate(doc)
			mutated, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				t.Fatalf("marshal mutated doc: %v", err)
			}
			res, err := VerifyManagedSettings(mutated)
			if err != nil {
				t.Fatalf("VerifyManagedSettings returned parse error: %v", err)
			}
			if res.OK {
				t.Fatalf("expected drift for %q but result was OK", tc.wantField)
			}
			if !driftsContainField(res.Drifts, tc.wantField) {
				t.Fatalf("expected drift on field %q; got drifts=%+v", tc.wantField, res.Drifts)
			}
		})
	}
}

func TestVerifyManagedSettingsCrossPlatformPathVariants(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opts ManagedSettingsOptions
	}{
		{
			name: "linux",
			opts: ManagedSettingsOptions{Platform: "linux", HookCommand: "/usr/local/bin/cordum-hook"},
		},
		{
			name: "macos path with spaces",
			opts: ManagedSettingsOptions{Platform: "darwin", HookCommand: "/Applications/Cordum Edge/cordum-hook"},
		},
		{
			name: "windows path with spaces",
			opts: ManagedSettingsOptions{Platform: "windows", HookCommand: `C:\Program Files\Cordum\cordum-hook.exe`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.opts.HookTimeout = DefaultHookTimeout
			tc.opts.AgentdURL = "http://127.0.0.1:8765/v1/edge/hooks/claude"
			tc.opts.MCPGatewayURL = "https://mcp.cordum.example/mcp"
			tc.opts.LLMProxyBaseURL = "https://llm-proxy.cordum.example"
			tc.opts.APIKeyHelperCommand = "/opt/cordum/bin/cordum-agentd claude api-key-helper"
			bundle, err := GenerateManagedSettingsTemplate(tc.opts)
			if err != nil {
				t.Fatalf("generate %s bundle: %v", tc.name, err)
			}
			res, err := VerifyManagedSettings(bundle.ManagedSettingsJSON)
			if err != nil {
				t.Fatalf("verify %s bundle: %v", tc.name, err)
			}
			if !res.OK {
				t.Fatalf("verify %s expected OK; drifts=%+v", tc.name, res.Drifts)
			}
		})
	}
}

func TestVerifyManagedSettingsFromPathRejectsMissingFile(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")
	if _, err := VerifyManagedSettingsFromPath(missing); err == nil {
		t.Fatalf("expected error for missing path %q, got nil", missing)
	}
}

func TestVerifyManagedSettingsFromPathRejectsHugeFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.json")
	// 2 MiB of bytes — exceeds the 1 MiB io.LimitReader cap.
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 2*1024*1024), 0o600); err != nil {
		t.Fatalf("write huge fixture: %v", err)
	}
	if _, err := VerifyManagedSettingsFromPath(path); err == nil {
		t.Fatalf("expected error for oversized file; got nil")
	}
}

func TestVerifyManagedSettingsRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	if _, err := VerifyManagedSettings([]byte("{ not valid json")); err == nil {
		t.Fatalf("expected parse error for malformed JSON; got nil")
	}
}

// TestValidateLoopbackAgentdURL is the PR #276 Sub-G #20 regression. Every
// invalid input is asserted against a typed sentinel (errors.Is) so a future
// refactor of error wording cannot regress the violation taxonomy — and the
// raw URL bytes (which may carry userinfo credentials) are NEVER substring-
// matched against expected text. The full VerifyManagedSettings round-trip is
// also exercised per input so the drift reported to operators stays scoped to
// env.CORDUM_AGENTD_URL without echoing userinfo into the Got field.
func TestValidateLoopbackAgentdURL(t *testing.T) {
	t.Parallel()

	const (
		validV4    = "http://127.0.0.1:8765/v1/edge/hooks/claude"
		validV6    = "http://[::1]:8765/v1/edge/hooks/claude"
		validHost  = "http://localhost:8765/v1/edge/hooks/claude"
		validHTTPS = "https://127.0.0.1:8765/v1/edge/hooks/claude"
	)
	cases := []struct {
		name    string
		raw     string
		wantErr error // nil = expect valid
	}{
		// VALID inputs — production contract accepts http+https loopback.
		{name: "valid_loopback_v4", raw: validV4, wantErr: nil},
		{name: "valid_loopback_v6", raw: validV6, wantErr: nil},
		{name: "valid_loopback_localhost", raw: validHost, wantErr: nil},
		{name: "valid_loopback_https", raw: validHTTPS, wantErr: nil},
		// INVALID — one row per violation class, typed sentinel.
		{name: "empty_string", raw: "", wantErr: errAgentdURLEmpty},
		{name: "whitespace_only", raw: "   ", wantErr: errAgentdURLEmpty},
		{name: "missing_scheme", raw: "127.0.0.1:8765/v1/edge/hooks/claude", wantErr: errAgentdURLScheme},
		{name: "scheme_ftp", raw: "ftp://127.0.0.1:8765/v1/edge/hooks/claude", wantErr: errAgentdURLScheme},
		{name: "scheme_file", raw: "file:///v1/edge/hooks/claude", wantErr: errAgentdURLScheme},
		{name: "remote_host_dns", raw: "http://example.com:8765/v1/edge/hooks/claude", wantErr: errAgentdURLHost},
		{name: "remote_host_v4", raw: "http://10.0.0.1:8765/v1/edge/hooks/claude", wantErr: errAgentdURLHost},
		{name: "non_canonical_loopback_v4_alias", raw: "http://127.0.0.5:8765/v1/edge/hooks/claude", wantErr: errAgentdURLHost},
		{name: "non_canonical_loopback_v4_subnet", raw: "http://127.1.2.3:8765/v1/edge/hooks/claude", wantErr: errAgentdURLHost},
		{name: "remote_host_unspecified", raw: "http://0.0.0.0:8765/v1/edge/hooks/claude", wantErr: errAgentdURLHost},
		{name: "remote_host_link_local_v6", raw: "http://[fe80::1]:8765/v1/edge/hooks/claude", wantErr: errAgentdURLHost},
		{name: "userinfo_password", raw: "http://user:pass@127.0.0.1:8765/v1/edge/hooks/claude", wantErr: errAgentdURLUserinfo},
		{name: "userinfo_username_only", raw: "http://user@127.0.0.1:8765/v1/edge/hooks/claude", wantErr: errAgentdURLUserinfo},
		{name: "missing_port", raw: "http://127.0.0.1/v1/edge/hooks/claude", wantErr: errAgentdURLPort},
		{name: "port_zero", raw: "http://127.0.0.1:0/v1/edge/hooks/claude", wantErr: errAgentdURLPort},
		{name: "port_out_of_range_high", raw: "http://127.0.0.1:65536/v1/edge/hooks/claude", wantErr: errAgentdURLPort},
		{name: "wrong_path_admin", raw: "http://127.0.0.1:8765/admin", wantErr: errAgentdURLPath},
		{name: "wrong_path_traversal", raw: "http://127.0.0.1:8765/v1/edge/hooks/claude/../admin", wantErr: errAgentdURLPath},
		{name: "query_present", raw: validV4 + "?token=abc", wantErr: errAgentdURLQuery},
		{name: "fragment_present", raw: validV4 + "#frag", wantErr: errAgentdURLFragment},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotErr := validateLoopbackAgentdURL(tc.raw)
			if tc.wantErr == nil {
				if gotErr != nil {
					t.Fatalf("validateLoopbackAgentdURL(%q) returned %v, want nil", tc.raw, gotErr)
				}
				return
			}
			if !errors.Is(gotErr, tc.wantErr) {
				t.Fatalf("validateLoopbackAgentdURL(%q): errors.Is(%v, %v) = false", tc.raw, gotErr, tc.wantErr)
			}
			// Round-trip through VerifyManagedSettings: every invalid URL
			// must surface as a critical drift on env.CORDUM_AGENTD_URL
			// without ever echoing userinfo (the password is the canonical
			// secret-shaped substring credentials could carry).
			drifts := agentdURLDrifts(map[string]string{"CORDUM_AGENTD_URL": tc.raw})
			if len(drifts) == 0 {
				t.Fatalf("agentdURLDrifts(%q) = empty; want 1 critical drift", tc.raw)
			}
			d := drifts[0]
			if d.Field != "env.CORDUM_AGENTD_URL" {
				t.Fatalf("drift field = %q, want env.CORDUM_AGENTD_URL", d.Field)
			}
			if d.Severity != managedSettingsDriftCritical {
				t.Fatalf("drift severity = %q, want %q", d.Severity, managedSettingsDriftCritical)
			}
			if strings.Contains(d.Got, "pass") || strings.Contains(d.Got, "user:") {
				t.Fatalf("drift.Got %q leaked userinfo from raw %q", d.Got, tc.raw)
			}
		})
	}
}

func mustGenerateGoldenBundle(t *testing.T) ManagedSettingsBundle {
	t.Helper()
	bundle, err := GenerateManagedSettingsTemplate(ManagedSettingsOptions{
		HookCommand:         "/opt/cordum/bin/cordum-hook",
		HookTimeout:         5 * time.Second,
		AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
		MCPGatewayURL:       "https://mcp.cordum.example/mcp",
		LLMProxyBaseURL:     "https://llm-proxy.cordum.example",
		APIKeyHelperCommand: "/opt/cordum/bin/cordum-agentd claude api-key-helper",
		Platform:            "linux",
	})
	if err != nil {
		t.Fatalf("generate golden bundle: %v", err)
	}
	return bundle
}

func driftsContainField(drifts []ManagedSettingsDrift, field string) bool {
	for _, d := range drifts {
		if d.Field == field {
			return true
		}
		// Allow callers to assert a prefix (e.g. "hooks.PreToolUse"
		// matches "hooks.PreToolUse[0].command") so future invariants
		// can use sub-paths without breaking existing assertions.
		if strings.HasPrefix(d.Field, field+".") || strings.HasPrefix(d.Field, field+"[") {
			return true
		}
	}
	return false
}
