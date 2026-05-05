package claude

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGenerateManagedSettingsTemplateIncludesEnterpriseControls(t *testing.T) {
	bundle, err := GenerateManagedSettingsTemplate(ManagedSettingsOptions{
		HookCommand:                "/opt/cordum/bin/cordum-hook",
		HookTimeout:                DefaultHookTimeout,
		AgentdURL:                  "http://127.0.0.1:8765/v1/edge/hooks/claude",
		MCPGatewayURL:              "https://mcp.cordum.example/mcp",
		LLMProxyBaseURL:            "https://llm-proxy.cordum.example",
		APIKeyHelperCommand:        "/opt/cordum/bin/cordum-agentd claude api-key-helper",
		ForceRemoteSettingsRefresh: true,
		Platform:                   "linux",
	})
	if err != nil {
		t.Fatalf("GenerateManagedSettingsTemplate returned error: %v", err)
	}

	settings := decodeJSONMap(t, bundle.ManagedSettingsJSON)
	if got := settings["$schema"]; got != "https://json.schemastore.org/claude-code-settings.json" {
		t.Fatalf("$schema = %v", got)
	}
	if got := settings["allowManagedHooksOnly"]; got != true {
		t.Fatalf("allowManagedHooksOnly = %v, want true", got)
	}
	if got := settings["allowManagedMcpServersOnly"]; got != true {
		t.Fatalf("allowManagedMcpServersOnly = %v, want true", got)
	}
	if got := settings["disableBypassPermissionsMode"]; got != "disable" {
		t.Fatalf("disableBypassPermissionsMode = %v, want disable", got)
	}
	if got := settings["forceRemoteSettingsRefresh"]; got != true {
		t.Fatalf("forceRemoteSettingsRefresh = %v, want true", got)
	}
	if got := settings["apiKeyHelper"]; got != "/opt/cordum/bin/cordum-agentd claude api-key-helper" {
		t.Fatalf("apiKeyHelper = %v", got)
	}

	env := jsonObject(t, settings["env"])
	if got := env["CORDUM_EDGE_MODE"]; got != "enterprise-strict" {
		t.Fatalf("CORDUM_EDGE_MODE = %v", got)
	}
	if got := env["CORDUM_AGENTD_FAIL_CLOSED"]; got != "true" {
		t.Fatalf("CORDUM_AGENTD_FAIL_CLOSED = %v", got)
	}
	if got := env["CORDUM_AGENTD_URL"]; got != "http://127.0.0.1:8765/v1/edge/hooks/claude" {
		t.Fatalf("CORDUM_AGENTD_URL = %v", got)
	}
	if got := env["CORDUM_AGENTD_HOOK_TIMEOUT"]; got != "4.5s" {
		t.Fatalf("CORDUM_AGENTD_HOOK_TIMEOUT = %v, want 4.5s", got)
	}
	if got := env["ANTHROPIC_BASE_URL"]; got != "https://llm-proxy.cordum.example" {
		t.Fatalf("ANTHROPIC_BASE_URL = %v", got)
	}
	if _, ok := env["ANTHROPIC_API_KEY"]; ok {
		t.Fatalf("managed settings must not contain long-lived ANTHROPIC_API_KEY: %#v", env)
	}

	hooks := jsonObject(t, settings["hooks"])
	assertCommandHook(t, hooks, "PreToolUse", "*", "/opt/cordum/bin/cordum-hook claude pre-tool-use", 5)
	assertCommandHook(t, hooks, "PostToolUse", "*", "/opt/cordum/bin/cordum-hook claude post-tool-use", 5)
	assertCommandHook(t, hooks, "PostToolUseFailure", "*", "/opt/cordum/bin/cordum-hook claude post-tool-use-failure", 5)
	assertCommandHook(t, hooks, "UserPromptSubmit", "", "/opt/cordum/bin/cordum-hook claude user-prompt-submit", 5)
	assertCommandHook(t, hooks, "ConfigChange", "user_settings|project_settings|local_settings|skills", "/opt/cordum/bin/cordum-hook claude config-change", 5)
	assertCommandHook(t, hooks, "FileChanged", ".claude/settings.json|.claude/settings.local.json|CLAUDE.md", "/opt/cordum/bin/cordum-hook claude file-changed", 5)

	allowed := jsonArray(t, settings["allowedMcpServers"])
	if len(allowed) != 1 || jsonObject(t, allowed[0])["serverName"] != "cordum-edge" {
		t.Fatalf("allowedMcpServers = %#v", allowed)
	}
	if _, ok := settings["disableAllHooks"]; ok {
		t.Fatalf("managed template must not emit weakening disableAllHooks: %#v", settings)
	}
	if permissions, ok := settings["permissions"].(map[string]any); ok {
		if _, hasAllow := permissions["allow"]; hasAllow {
			t.Fatalf("managed template must not add broad allow rules: %#v", permissions)
		}
	}

	mcp := decodeJSONMap(t, bundle.ManagedMCPJSON)
	servers := jsonObject(t, mcp["mcpServers"])
	cordumEdge := jsonObject(t, servers["cordum-edge"])
	if got := cordumEdge["type"]; got != "http" {
		t.Fatalf("managed MCP type = %v, want http", got)
	}
	if got := cordumEdge["url"]; got != "https://mcp.cordum.example/mcp" {
		t.Fatalf("managed MCP url = %v", got)
	}
	if got := cordumEdge["headersHelper"]; got != "/opt/cordum/bin/cordum-agentd mcp headers" {
		t.Fatalf("managed MCP headersHelper = %v", got)
	}

	combined := string(bundle.ManagedSettingsJSON) + string(bundle.ManagedMCPJSON) + bundle.Notes
	for _, forbidden := range []string{"sk-test-secret", "ghp_testtoken", "Authorization: Bearer", "raw API key"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("managed template leaked forbidden secret marker %q in %s", forbidden, combined)
		}
	}
	if !strings.Contains(bundle.Notes, "Jamf") || !strings.Contains(bundle.Notes, "Intune") || !strings.Contains(bundle.Notes, "keychain") {
		t.Fatalf("notes missing rollout/token storage guidance: %q", bundle.Notes)
	}
}

func TestManagedSettingsDominateMaliciousDevSettings(t *testing.T) {
	devData, err := GenerateDevSettingsJSON(DevSettingsOptions{
		SessionID:           "sess-dev",
		ExecutionID:         "exec-dev",
		AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
		HookCommand:         "cordum-hook",
		HookTimeout:         DefaultHookTimeout,
		PolicyMode:          "observe",
		ApprovalWaitTimeout: 30 * time.Second,
		Platform:            "linux",
	})
	if err != nil {
		t.Fatalf("GenerateDevSettingsJSON returned error: %v", err)
	}
	devSettings := decodeJSONMap(t, devData)
	devSettings["disableAllHooks"] = true
	devSettings["hooks"] = map[string]any{}
	devEnv := jsonObject(t, devSettings["env"])
	devEnv["CORDUM_EDGE_MODE"] = "observe"

	bundle, err := GenerateManagedSettingsTemplate(ManagedSettingsOptions{
		HookCommand:                "/opt/cordum/bin/cordum-hook",
		HookTimeout:                DefaultHookTimeout,
		AgentdURL:                  "http://127.0.0.1:8765/v1/edge/hooks/claude",
		MCPGatewayURL:              "https://mcp.cordum.example/mcp",
		LLMProxyBaseURL:            "https://llm-proxy.cordum.example",
		APIKeyHelperCommand:        "/opt/cordum/bin/cordum-agentd claude api-key-helper",
		ForceRemoteSettingsRefresh: true,
		Platform:                   "linux",
	})
	if err != nil {
		t.Fatalf("GenerateManagedSettingsTemplate returned error: %v", err)
	}
	managedSettings := decodeJSONMap(t, bundle.ManagedSettingsJSON)
	managedEnv := jsonObject(t, managedSettings["env"])
	if got := managedEnv[managedPolicyModeEnv]; got != "enterprise-strict" {
		t.Fatalf("%s = %v, want enterprise-strict", managedPolicyModeEnv, got)
	}
	if got := managedEnv[managedHooksOnlyEnv]; got != "true" {
		t.Fatalf("%s = %v, want true", managedHooksOnlyEnv, got)
	}
	if got := hookPolicyMode(RunOptions{Env: map[string]string{
		"CORDUM_EDGE_MODE":   devEnv["CORDUM_EDGE_MODE"].(string),
		managedPolicyModeEnv: managedEnv[managedPolicyModeEnv].(string),
	}}); got != "enterprise-strict" {
		t.Fatalf("hookPolicyMode with malicious dev observe + managed lock = %q, want enterprise-strict", got)
	}
	if got := managedSettings["allowManagedHooksOnly"]; got != true {
		t.Fatalf("allowManagedHooksOnly = %v, want true", got)
	}
	if got := managedSettings["disableBypassPermissionsMode"]; got != "disable" {
		t.Fatalf("disableBypassPermissionsMode = %v, want disable", got)
	}
	if _, ok := managedSettings["disableAllHooks"]; ok {
		t.Fatalf("managed settings must not emit dev weakening disableAllHooks: %#v", managedSettings)
	}
	hooks := jsonObject(t, managedSettings["hooks"])
	assertCommandHook(t, hooks, "PreToolUse", "*", "/opt/cordum/bin/cordum-hook claude pre-tool-use", 5)
}

func TestManagedSettingsRendersNonceOutsideURL(t *testing.T) {
	bundle, err := GenerateManagedSettingsTemplate(ManagedSettingsOptions{
		HookCommand:                "/opt/cordum/bin/cordum-hook",
		HookTimeout:                DefaultHookTimeout,
		AgentdURL:                  "http://127.0.0.1:8765/v1/edge/hooks/claude?nonce=" + syntheticAgentdHexNonce,
		AgentdHookNonce:            syntheticAgentdHexNonce,
		MCPGatewayURL:              "https://mcp.cordum.example/mcp",
		LLMProxyBaseURL:            "https://llm-proxy.cordum.example",
		APIKeyHelperCommand:        "/opt/cordum/bin/cordum-agentd claude api-key-helper",
		ForceRemoteSettingsRefresh: true,
		Platform:                   "linux",
	})
	if err != nil {
		t.Fatalf("GenerateManagedSettingsTemplate returned error: %v", err)
	}

	settings := decodeJSONMap(t, bundle.ManagedSettingsJSON)
	env := jsonObject(t, settings["env"])
	if got := env["CORDUM_AGENTD_URL"]; got != "http://127.0.0.1:8765/v1/edge/hooks/claude" {
		t.Fatalf("CORDUM_AGENTD_URL = %v, want bare loopback hook URL", got)
	}
	if _, ok := env["CORDUM_AGENTD_HOOK_NONCE"]; ok {
		t.Fatalf("managed settings must not persist CORDUM_AGENTD_HOOK_NONCE: %#v", env)
	}
	assertRenderedSettingsOmitsAgentdNonce(t, bundle.ManagedSettingsJSON, syntheticAgentdHexNonce)
	if combined := string(bundle.ManagedSettingsJSON) + string(bundle.ManagedMCPJSON) + bundle.Notes; strings.Contains(combined, syntheticAgentdHexNonce) {
		t.Fatalf("managed bundle leaked agentd nonce: %s", combined)
	}
}

func TestGenerateManagedSettingsTemplateIsParseableForPlatformPathVariants(t *testing.T) {
	cases := []struct {
		name string
		opts ManagedSettingsOptions
	}{
		{
			name: "linux",
			opts: ManagedSettingsOptions{Platform: "linux", HookCommand: "/usr/local/bin/cordum-hook", AgentdURL: "http://127.0.0.1:8765/v1/edge/hooks/claude"},
		},
		{
			name: "macos path with spaces",
			opts: ManagedSettingsOptions{Platform: "darwin", HookCommand: "/Applications/Cordum Edge/cordum-hook", AgentdURL: "http://127.0.0.1:8765/v1/edge/hooks/claude"},
		},
		{
			name: "windows path with spaces",
			opts: ManagedSettingsOptions{Platform: "windows", HookCommand: `C:\Program Files\Cordum\cordum-hook.exe`, AgentdURL: "http://127.0.0.1:8765/v1/edge/hooks/claude"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.HookTimeout = DefaultHookTimeout
			tc.opts.MCPGatewayURL = "https://mcp.cordum.example/mcp"
			tc.opts.LLMProxyBaseURL = "https://llm-proxy.cordum.example"
			tc.opts.APIKeyHelperCommand = tc.opts.HookCommand + " agentd-key-helper"
			bundle, err := GenerateManagedSettingsTemplate(tc.opts)
			if err != nil {
				t.Fatalf("GenerateManagedSettingsTemplate returned error: %v", err)
			}
			var settings map[string]any
			if err := json.Unmarshal(bundle.ManagedSettingsJSON, &settings); err != nil {
				t.Fatalf("managed settings JSON invalid: %v; raw=%s", err, bundle.ManagedSettingsJSON)
			}
			var mcp map[string]any
			if err := json.Unmarshal(bundle.ManagedMCPJSON, &mcp); err != nil {
				t.Fatalf("managed MCP JSON invalid: %v; raw=%s", err, bundle.ManagedMCPJSON)
			}
		})
	}
}

func TestManagedSettingsFixturesAreSyntheticAndParseable(t *testing.T) {
	for _, path := range []string{
		"testdata/settings/managed-settings.json",
		"testdata/settings/managed-mcp.json",
	} {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			var parsed map[string]any
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("fixture is not valid JSON: %v", err)
			}
			text := string(data)
			for _, forbidden := range []string{"sk-", "ghp_", "Authorization: Bearer", "ANTHROPIC_API_KEY", "CORDUM_API_KEY"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("fixture contains forbidden secret marker %q: %s", forbidden, path)
				}
			}
		})
	}
}
