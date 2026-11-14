package claude

import (
	"encoding/json"
	"testing"
)

type managedBoundaryDriftCase struct {
	name      string
	mutate    func(map[string]any)
	wantField string
}

func TestVerifyManagedSettings_EnforcesProductionBoundary(t *testing.T) {
	t.Parallel()
	valid := mustManagedSettingsMap(t)
	assertManagedSettingsOK(t, valid)

	for _, tc := range managedBoundaryDriftCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := mustManagedSettingsMap(t)
			tc.mutate(doc)
			res := verifyManagedSettingsMap(t, doc)
			if res.OK {
				t.Fatalf("expected production-boundary drift on %s, got OK", tc.wantField)
			}
			if !driftsContainField(res.Drifts, tc.wantField) {
				t.Fatalf("expected drift on field %q; got drifts=%+v", tc.wantField, res.Drifts)
			}
		})
	}
}

func managedBoundaryDriftCases() []managedBoundaryDriftCase {
	return []managedBoundaryDriftCase{
		{
			name: "non_empty_http_hook_urls",
			mutate: func(doc map[string]any) {
				doc["allowedHttpHookUrls"] = []any{"https://hooks.example.invalid/claude"}
			},
			wantField: "allowedHttpHookUrls",
		},
		{
			name: "extra_mcp_server",
			mutate: func(doc map[string]any) {
				doc["allowedMcpServers"] = []any{
					map[string]any{"serverUrl": "https://mcp.cordum.example/mcp"},
					map[string]any{"serverUrl": "https://untrusted.example.invalid/mcp"},
				}
			},
			wantField: "allowedMcpServers",
		},
		{
			// The pre-fix template shape: users pick server names, so a name
			// match is not a security control.
			name: "server_name_entry",
			mutate: func(doc map[string]any) {
				doc["allowedMcpServers"] = []any{map[string]any{"serverName": "cordum-edge"}}
			},
			wantField: "allowedMcpServers[0]",
		},
		{
			name: "server_command_entry",
			mutate: func(doc map[string]any) {
				doc["allowedMcpServers"] = []any{map[string]any{"serverCommand": []any{"/bin/sh", "-c", "echo untrusted"}}}
			},
			wantField: "allowedMcpServers[0]",
		},
		{
			name: "wildcard_server_url",
			mutate: func(doc map[string]any) {
				doc["allowedMcpServers"] = []any{map[string]any{"serverUrl": "https://*"}}
			},
			wantField: "allowedMcpServers[0].serverUrl",
		},
		{
			name: "arbitrary_hook_command",
			mutate: func(doc map[string]any) {
				replaceManagedHookCommands(doc, "/bin/bash -c 'arbitrary'")
			},
			wantField: "hooks.PreToolUse[0].command",
		},
		{
			name: "non_cordum_hook_boundary",
			mutate: func(doc map[string]any) {
				replaceManagedHookCommands(doc, "/usr/local/bin/not-cordum-hook claude pre-tool-use")
			},
			wantField: "hooks.PreToolUse[0].command",
		},
		{
			name:      "pre_tool_use_without_on_failure_block",
			mutate:    func(doc map[string]any) { setManagedHookOnFailure(doc, "PreToolUse", nil) },
			wantField: "hooks.PreToolUse[0].onFailure",
		},
		{
			name:      "user_prompt_submit_without_on_failure_block",
			mutate:    func(doc map[string]any) { setManagedHookOnFailure(doc, "UserPromptSubmit", nil) },
			wantField: "hooks.UserPromptSubmit[0].onFailure",
		},
		{
			name:      "config_change_on_failure_continue",
			mutate:    func(doc map[string]any) { setManagedHookOnFailure(doc, "ConfigChange", "continue") },
			wantField: "hooks.ConfigChange[0].onFailure",
		},
	}
}

// setManagedHookOnFailure sets (or, for nil, deletes) onFailure on the first
// command hook of event.
func setManagedHookOnFailure(doc map[string]any, event string, value any) {
	hooks, _ := doc["hooks"].(map[string]any)
	sets, _ := hooks[event].([]any)
	set, _ := sets[0].(map[string]any)
	commands, _ := set["hooks"].([]any)
	hook, _ := commands[0].(map[string]any)
	if value == nil {
		delete(hook, "onFailure")
		return
	}
	hook["onFailure"] = value
}

func mustManagedSettingsMap(t *testing.T) map[string]any {
	t.Helper()
	bundle := mustGenerateGoldenBundle(t)
	var doc map[string]any
	if err := json.Unmarshal(bundle.ManagedSettingsJSON, &doc); err != nil {
		t.Fatalf("unmarshal generated managed settings: %v", err)
	}
	return doc
}

func verifyManagedSettingsMap(t *testing.T, doc map[string]any) ManagedSettingsVerifyResult {
	t.Helper()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal managed settings: %v", err)
	}
	res, err := VerifyManagedSettings(data)
	if err != nil {
		t.Fatalf("VerifyManagedSettings returned parse error: %v", err)
	}
	return res
}

func assertManagedSettingsOK(t *testing.T, doc map[string]any) {
	t.Helper()
	res := verifyManagedSettingsMap(t, doc)
	if !res.OK {
		t.Fatalf("generated managed settings expected OK; drifts=%+v", res.Drifts)
	}
}

func replaceManagedHookCommands(doc map[string]any, command string) {
	hooks, _ := doc["hooks"].(map[string]any)
	for _, rawSets := range hooks {
		sets, _ := rawSets.([]any)
		for _, rawSet := range sets {
			set, _ := rawSet.(map[string]any)
			rawCommands, _ := set["hooks"].([]any)
			for _, rawCommand := range rawCommands {
				commandHook, _ := rawCommand.(map[string]any)
				commandHook["command"] = command
			}
		}
	}
}
