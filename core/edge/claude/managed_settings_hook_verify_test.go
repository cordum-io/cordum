package claude

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVerifyManagedSettingsRejectsUnsupportedHookExecutionFields(t *testing.T) {
	for _, tc := range []struct {
		name, scope, key string
		value            any
	}{
		{"conditional command", "command", "if", "Bash(rm *)"},
		{"async command", "command", "async", true},
		{"malformed condition", "command", "if", map[string]any{"private-marker": true}},
		{"unknown command modifier", "command", "futureExecutionMode", "private-marker"},
		{"unknown hook set modifier", "set", "futureMatcherMode", "private-marker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := decodeJSONMap(t, mustGenerateGoldenBundle(t).ManagedSettingsJSON)
			set := doc["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
			target := set
			if tc.scope == "command" {
				target = set["hooks"].([]any)[0].(map[string]any)
			}
			target[tc.key] = tc.value
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			res, err := VerifyManagedSettingsWithOptions(data, ManagedSettingsVerifyOptions{MCPGatewayURL: trustedManagedMCPGatewayURL})
			if err == nil {
				t.Fatalf("unsupported %s field must be rejected, result=%+v", tc.scope, res)
			}
			if strings.Contains(err.Error(), tc.key) || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("error must not echo unsupported fields or values: %v", err)
			}
		})
	}
}

func TestVerifyManagedSettingsRequiresCommandHookType(t *testing.T) {
	for _, event := range requiredManagedHookEvents {
		for _, kind := range []string{"missing", "http", "Command", " command"} {
			t.Run(event+"/"+kind, func(t *testing.T) {
				doc := decodeJSONMap(t, mustGenerateGoldenBundle(t).ManagedSettingsJSON)
				sets := doc["hooks"].(map[string]any)[event].([]any)
				hook := sets[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
				if kind == "missing" {
					delete(hook, "type")
				} else {
					hook["type"] = kind
				}
				data, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				res, err := VerifyManagedSettingsWithOptions(data, ManagedSettingsVerifyOptions{MCPGatewayURL: trustedManagedMCPGatewayURL})
				if err != nil {
					t.Fatal(err)
				}
				if res.OK || !driftsContainField(res.Drifts, "hooks."+event) {
					t.Fatalf("type %q must not satisfy command hook for %s: %+v", kind, event, res)
				}
			})
		}
	}
}

func TestVerifyManagedSettingsRequiresUniversalToolMatchers(t *testing.T) {
	for _, event := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure"} {
		for _, matcher := range []string{"Bash", "Edit|Write", "^Bash$", " * ", " ", ".* "} {
			t.Run(event+"/"+matcher, func(t *testing.T) {
				var doc managedSettingsDocument
				if err := json.Unmarshal(mustGenerateGoldenBundle(t).ManagedSettingsJSON, &doc); err != nil {
					t.Fatal(err)
				}
				doc.Hooks[event][0].Matcher = matcher
				data, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				res, err := VerifyManagedSettingsWithOptions(data, ManagedSettingsVerifyOptions{MCPGatewayURL: trustedManagedMCPGatewayURL})
				if err != nil {
					t.Fatal(err)
				}
				if res.OK || !driftsContainField(res.Drifts, "hooks."+event) {
					t.Fatalf("matcher %q must not claim all-tool coverage for %s: %+v", matcher, event, res)
				}
			})
		}
	}
}

func TestVerifyManagedSettingsAcceptsLaterUniversalCommandHook(t *testing.T) {
	for _, event := range []string{"PreToolUse", "PostToolUse", "PostToolUseFailure"} {
		for _, matcher := range []string{"", "*", ".*"} {
			t.Run(event+"/"+matcher, func(t *testing.T) {
				var doc managedSettingsDocument
				if err := json.Unmarshal(mustGenerateGoldenBundle(t).ManagedSettingsJSON, &doc); err != nil {
					t.Fatal(err)
				}
				valid := doc.Hooks[event][0]
				valid.Matcher = matcher
				invalid := valid.Hooks[0]
				invalid.Type = "http"
				doc.Hooks[event] = []claudeHookSet{
					{Matcher: "Bash", Hooks: valid.Hooks},
					{Matcher: matcher, Hooks: []claudeCommandHook{invalid}},
					valid,
				}
				// These events intentionally select configuration sources and watched
				// filenames, rather than all tools. Their coverage needs fleet review.
				doc.Hooks["ConfigChange"][0].Matcher = "project_settings"
				doc.Hooks["FileChanged"][0].Matcher = "custom-policy.yaml"
				data, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				res, err := VerifyManagedSettingsWithOptions(data, ManagedSettingsVerifyOptions{MCPGatewayURL: trustedManagedMCPGatewayURL})
				if err != nil {
					t.Fatal(err)
				}
				if !res.OK {
					t.Fatalf("later universal command hook %q must verify for %s: %+v", matcher, event, res.Drifts)
				}
			})
		}
	}
}
