package claude

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const trustedManagedMCPGatewayURL = "https://mcp.cordum.example/mcp"

func TestManagedSettingsMCPGatewayURLMustBeLiteralEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, url string
		valid     bool
	}{
		{"gateway path", "https://mcp.cordum.example/mcp", true},
		{"private ingress", "https://gateway.internal:8443/team/mcp", true},
		{"local gateway", "http://127.0.0.1:8081/mcp", true},
		{"query preserved", "https://mcp.cordum.example/mcp?tenant=acme", true},
		{"relative", "/mcp", false},
		{"scheme relative", "//mcp.cordum.example/mcp", false},
		{"other scheme", "ftp://mcp.cordum.example/mcp", false},
		{"host only", "https://mcp.cordum.example", false},
		{"root path", "https://mcp.cordum.example/", false},
		{"wildcard path", "https://mcp.cordum.example/*", false},
		{"encoded wildcard", "https://mcp.cordum.example/%2a", false},
		{"backslash traversal", `https://mcp.cordum.example/mcp\..`, false},
		{"encoded backslash traversal", "https://mcp.cordum.example/mcp%5C..", false},
		{"credentials", "https://operator:private-value@mcp.cordum.example/mcp", false},
		{"variable", "https://${GATEWAY}/mcp", false},
		{"invalid port", "https://mcp.cordum.example:invalid/mcp", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle, err := GenerateManagedSettingsTemplate(ManagedSettingsOptions{
				AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
				MCPGatewayURL:       tc.url,
				LLMProxyBaseURL:     "https://llm-proxy.cordum.example",
				APIKeyHelperCommand: "/usr/local/bin/cordum-agentd claude api-key-helper",
			})
			if tc.valid {
				if err != nil {
					t.Fatalf("legitimate endpoint rejected: %v", err)
				}
				settings := decodeJSONMap(t, bundle.ManagedSettingsJSON)
				allowed := jsonArray(t, settings["allowedMcpServers"])
				if got := jsonObject(t, allowed[0])["serverUrl"]; got != tc.url {
					t.Fatalf("allowlist URL = %v, want %q", got, tc.url)
				}
				mcp := decodeJSONMap(t, bundle.ManagedMCPJSON)
				servers := jsonObject(t, mcp["mcpServers"])
				if got := jsonObject(t, servers["cordum-edge"])["url"]; got != tc.url {
					t.Fatalf("managed MCP URL = %v, want %q", got, tc.url)
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe or non-endpoint MCP URL accepted")
			}
			if !strings.Contains(err.Error(), "mcp_gateway_url") || strings.Contains(err.Error(), tc.url) || strings.Contains(err.Error(), "private-value") {
				t.Fatalf("error must identify the field without echoing its value: %v", err)
			}
		})
	}
}

func TestVerifyManagedSettingsWithoutTrustedGatewayCannotReportOK(t *testing.T) {
	data, err := os.ReadFile("testdata/settings/managed-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	result, err := VerifyManagedSettings(data)
	if err != nil {
		t.Fatalf("structurally valid settings returned parse error: %v", err)
	}
	if result.OK {
		t.Fatal("security verification reported OK without an independent trusted gateway baseline")
	}
	for _, drift := range result.Drifts {
		if drift.Field == "trusted.mcpGatewayURL" && drift.Severity == managedSettingsDriftCritical {
			return
		}
	}
	t.Fatal("missing critical trusted-gateway baseline drift")
}

func TestVerifyManagedSettingsComparesIndependentGateway(t *testing.T) {
	data, err := os.ReadFile("testdata/settings/managed-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, actual, expected string
		ok, invalidBaseline    bool
	}{
		{"matching", trustedManagedMCPGatewayURL, trustedManagedMCPGatewayURL, true, false},
		{"trimmed baseline", trustedManagedMCPGatewayURL, " " + trustedManagedMCPGatewayURL + " ", true, false},
		{"different host", "https://untrusted.example/mcp", trustedManagedMCPGatewayURL, false, false},
		{"different path", "https://mcp.cordum.example/other", trustedManagedMCPGatewayURL, false, false},
		{"different port", "https://mcp.cordum.example:8443/mcp", trustedManagedMCPGatewayURL, false, false},
		{"trailing slash", trustedManagedMCPGatewayURL + "/", trustedManagedMCPGatewayURL, false, false},
		{"host only", "https://mcp.cordum.example", trustedManagedMCPGatewayURL, false, false},
		{"wildcard", trustedManagedMCPGatewayURL + "*", trustedManagedMCPGatewayURL, false, false},
		{"query differs", trustedManagedMCPGatewayURL + "?tenant=other", trustedManagedMCPGatewayURL, false, false},
		{"baseline host only", trustedManagedMCPGatewayURL, "https://mcp.cordum.example", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			doc["allowedMcpServers"] = []any{map[string]any{"serverUrl": tc.actual}}
			mutated, _ := json.Marshal(doc)
			result, err := VerifyManagedSettingsWithOptions(mutated, ManagedSettingsVerifyOptions{MCPGatewayURL: tc.expected})
			if tc.invalidBaseline {
				if err == nil {
					t.Fatal("invalid trusted baseline accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.OK != tc.ok {
				t.Fatalf("OK=%v, want %v; drifts=%+v", result.OK, tc.ok, result.Drifts)
			}
			if !tc.ok && !driftsContainField(result.Drifts, "allowedMcpServers[0].serverUrl") {
				t.Fatalf("missing endpoint mismatch drift: %+v", result.Drifts)
			}
		})
	}
	result, err := VerifyManagedSettingsFromPath("testdata/settings/managed-settings.json")
	if err != nil || result.OK || !driftsContainField(result.Drifts, "trusted.mcpGatewayURL") {
		t.Fatalf("path wrapper without baseline: result=%+v err=%v", result, err)
	}
}

func TestVerifyManagedSettingsRejectsManagedMCPAllowlistExceptions(t *testing.T) {
	data, err := os.ReadFile("testdata/settings/managed-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, field string
		value       any
		ok          bool
	}{
		{"inline untrusted server", "managedMcpServers", map[string]any{"untrusted": map[string]any{"type": "http", "url": "https://untrusted.example/mcp"}}, false},
		{"inline gateway still outside file-only contract", "managedMcpServers", map[string]any{"cordum": map[string]any{"type": "http", "url": trustedManagedMCPGatewayURL}}, false},
		{"empty inline object", "managedMcpServers", map[string]any{}, true},
		{"inline null", "managedMcpServers", nil, false},
		{"inline array", "managedMcpServers", []any{}, false},
		{"inline string", "managedMcpServers", "disabled", false},
		{"Chrome enabled", "allowClaudeInChromeWithManagedMcp", true, false},
		{"Chrome disabled", "allowClaudeInChromeWithManagedMcp", false, true},
		{"Chrome string", "allowClaudeInChromeWithManagedMcp", "false", false},
		{"Chrome null", "allowClaudeInChromeWithManagedMcp", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			doc[tc.field] = tc.value
			mutated, _ := json.Marshal(doc)
			result, err := VerifyManagedSettingsWithOptions(mutated, ManagedSettingsVerifyOptions{MCPGatewayURL: trustedManagedMCPGatewayURL})
			if err != nil {
				t.Fatal(err)
			}
			if result.OK != tc.ok {
				t.Fatalf("OK=%v, want %v; drifts=%+v", result.OK, tc.ok, result.Drifts)
			}
			if !tc.ok {
				for _, drift := range result.Drifts {
					if drift.Field == tc.field && drift.Severity == managedSettingsDriftCritical {
						return
					}
				}
				t.Fatalf("missing critical exception drift for %s", tc.field)
			}
		})
	}
}

func TestVerifyManagedSettingsRejectsCaseShadows(t *testing.T) {
	data, err := os.ReadFile("testdata/settings/managed-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]json.RawMessage
	if err := json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	hooksWithPreTool := func(value string) string {
		var hooks map[string]json.RawMessage
		if err := json.Unmarshal(original["hooks"], &hooks); err != nil {
			t.Fatal(err)
		}
		hooks["PreToolUse"] = json.RawMessage(value)
		encoded, err := json.Marshal(hooks)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	for _, tc := range []struct {
		name, field, exact, shadow string
	}{
		{"inline servers", "managedMcpServers", `{"evil":{"type":"http","url":"https://untrusted.example/mcp"}}`, `,"ManagedMcpServers":{}`},
		{"Chrome", "allowClaudeInChromeWithManagedMcp", `true`, `,"AllowClaudeInChromeWithManagedMcp":false`},
		{"allowlist", "allowedMcpServers", `[{"serverUrl":"https://untrusted.example/mcp"}]`, `,"AllowedMcpServers":[{"serverUrl":"https://mcp.cordum.example/mcp"}]`},
		{"URL entry", "allowedMcpServers", `[{"serverUrl":"https://untrusted.example/mcp","ServerUrl":"https://mcp.cordum.example/mcp"}]`, ""},
		{"name entry", "allowedMcpServers", `[{"serverUrl":"https://mcp.cordum.example/mcp","serverName":"other","ServerName":""}]`, ""},
		{"command entry", "allowedMcpServers", `[{"serverUrl":"https://mcp.cordum.example/mcp","serverCommand":["other"],"ServerCommand":[]}]`, ""},
		{"MCP lock", "allowManagedMcpServersOnly", `false`, `,"AllowManagedMcpServersOnly":true`},
		{"hook lock", "allowManagedHooksOnly", `false`, `,"AllowManagedHooksOnly":true`},
		{"bypass mode", "disableBypassPermissionsMode", `"default"`, `,"DisableBypassPermissionsMode":"disable"`},
		{"HTTP hooks", "allowedHttpHookUrls", `["https://untrusted.example/hook"]`, `,"AllowedHttpHookUrls":[]`},
		{"env", "env", `{}`, `,"Env":` + string(original["env"])},
		{"hooks", "hooks", `{}`, `,"Hooks":` + string(original["hooks"])},
		{"hook command", "hooks", hooksWithPreTool(`[{"hooks":[{"type":"command","command":"curl https://untrusted.example","Command":"/usr/local/bin/cordum-hook claude pre-tool-use","onFailure":"block"}]}]`), ""},
		{"hook failure", "hooks", hooksWithPreTool(`[{"hooks":[{"type":"command","command":"/usr/local/bin/cordum-hook claude pre-tool-use","onFailure":"continue","OnFailure":"block"}]}]`), ""},
		{"hook set", "hooks", hooksWithPreTool(`[{"hooks":[],"Hooks":[{"type":"command","command":"/usr/local/bin/cordum-hook claude pre-tool-use","onFailure":"block"}]}]`), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			doc[tc.field] = json.RawMessage(tc.exact)
			payload, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			// Append a distinct, differently cased key last. Go's struct
			// decoder would otherwise overwrite the exact key Claude uses.
			payload = append(payload[:len(payload)-1], []byte(tc.shadow+"}")...)
			result, err := VerifyManagedSettingsWithOptions(payload, ManagedSettingsVerifyOptions{MCPGatewayURL: trustedManagedMCPGatewayURL})
			if err == nil && result.OK {
				t.Fatal("case-shadowed MCP security setting reported OK")
			}
		})
	}
}

func TestVerifyManagedSettingsRejectsDuplicateOrTrailingJSON(t *testing.T) {
	data, err := os.ReadFile("testdata/settings/managed-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSpace(string(data))
	prefix := base[:len(base)-1]
	for name, payload := range map[string]string{
		"duplicate env":          prefix + `,"env":{"UNRELATED":"value"}}`,
		"duplicate hooks":        prefix + `,"hooks":{}}`,
		"nested duplicate":       prefix + `,"future":{"value":1,"value":2}}`,
		"trailing JSON":          base + ` {"managedMcpServers":{"evil":{}}}`,
		"oversized direct input": base + strings.Repeat(" ", int(maxManagedSettingsFileBytes)),
		"excessive nesting":      prefix + `,"future":` + strings.Repeat(`{"nested":`, 140) + `0` + strings.Repeat("}", 140) + "}",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := VerifyManagedSettingsWithOptions([]byte(payload), ManagedSettingsVerifyOptions{MCPGatewayURL: trustedManagedMCPGatewayURL})
			if err == nil && result.OK {
				t.Fatal("ambiguous or unbounded settings JSON reported OK")
			}
		})
	}
}

func TestVerifyManagedSettingsPreservesUnknownFieldsAndEnvCase(t *testing.T) {
	data, err := os.ReadFile("testdata/settings/managed-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeJSONMap(t, data)
	doc["futureOptionalSetting"] = map[string]any{"Command": "value"}
	env := jsonObject(t, doc["env"])
	env["COMMAND"], env["Command"] = "one", "two"
	payload, _ := json.Marshal(doc)
	result, err := VerifyManagedSettingsWithOptions(payload, ManagedSettingsVerifyOptions{MCPGatewayURL: trustedManagedMCPGatewayURL})
	if err != nil || !result.OK {
		t.Fatalf("unrelated unknown fields or case-sensitive env keys rejected: result=%+v err=%v", result, err)
	}
}
