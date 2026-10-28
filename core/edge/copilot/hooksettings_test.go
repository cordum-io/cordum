package copilot

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGenerateHookSettings_ShapeAndGovernanceEvents(t *testing.T) {
	out, err := GenerateHookSettings(HookSettingsOptions{
		HookCommand: "/opt/cordum/bin/cordum-hook",
		AgentdURL:   "http://127.0.0.1:8765",
		PolicyMode:  "enforce",
		FailClosed:  true,
	})
	if err != nil {
		t.Fatalf("GenerateHookSettings: %v", err)
	}
	var doc struct {
		Version int `json:"version"`
		Hooks   map[string][]struct {
			Type    string            `json:"type"`
			Command string            `json:"command"`
			Timeout int               `json:"timeout"`
			Env     map[string]string `json:"env"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	// The GitHub Copilot hooks reference rejects a file without version 1.
	if doc.Version != 1 {
		t.Fatalf("version = %d, want 1", doc.Version)
	}
	// Every chat + every action must be wired.
	for _, ev := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		entries, ok := doc.Hooks[ev]
		if !ok || len(entries) != 1 {
			t.Fatalf("event %s missing/duplicated: %+v", ev, doc.Hooks[ev])
		}
		e := entries[0]
		if e.Type != "command" {
			t.Fatalf("%s type = %q, want command", ev, e.Type)
		}
		if !strings.HasPrefix(e.Command, "/opt/cordum/bin/cordum-hook copilot ") {
			t.Fatalf("%s command = %q, want cordum-hook copilot prefix", ev, e.Command)
		}
		// timeout is emitted in SECONDS (Copilot/GitHub hook unit); the 4500ms
		// default converts to 5s, not a 4500s (75 min) window.
		if e.Timeout != 5 {
			t.Fatalf("%s timeout = %d, want 5s (from 4500ms default)", ev, e.Timeout)
		}
		if e.Env["CORDUM_AGENT_PRODUCT"] != "github-copilot" {
			t.Fatalf("%s missing product attribution: %v", ev, e.Env)
		}
		if e.Env["CORDUM_AGENTD_FAIL_CLOSED"] != "true" {
			t.Fatalf("%s fail-closed not set: %v", ev, e.Env)
		}
		if e.Env["CORDUM_AGENTD_URL"] != "http://127.0.0.1:8765" {
			t.Fatalf("%s agentd url missing: %v", ev, e.Env)
		}
	}
	// PreToolUse must map to the blocking governance subcommand.
	if !strings.Contains(doc.Hooks["PreToolUse"][0].Command, "pre-tool-use") {
		t.Fatalf("PreToolUse not mapped to pre-tool-use: %q", doc.Hooks["PreToolUse"][0].Command)
	}
	// Lifecycle events are intentionally NOT wired.
	if _, ok := doc.Hooks["SessionStart"]; ok {
		t.Fatalf("SessionStart should not be wired as a governance hook")
	}
}

func TestGenerateHookSettings_NoSecretsBaked(t *testing.T) {
	// The nonce is a runtime/agentd concern — the generator must reject a
	// command/url carrying an obvious secret and never emit one.
	if _, err := GenerateHookSettings(HookSettingsOptions{HookCommand: "cordum-hook --token=sk-abc"}); err == nil {
		t.Fatal("expected rejection of a sensitive hook command")
	}
	out, err := GenerateHookSettings(HookSettingsOptions{HookCommand: "cordum-hook"})
	if err != nil {
		t.Fatalf("GenerateHookSettings: %v", err)
	}
	if strings.Contains(strings.ToLower(string(out)), "nonce") || strings.Contains(string(out), "sk-") {
		t.Fatalf("generated config must not bake secrets: %s", out)
	}
}

func TestGenerateHookSettings_StripsAgentdNonce(t *testing.T) {
	out, err := GenerateHookSettings(HookSettingsOptions{
		AgentdURL: "http://127.0.0.1:8765/v1/edge/hooks/copilot?nonce=super-secret-xyz",
	})
	if err != nil {
		t.Fatalf("GenerateHookSettings: %v", err)
	}
	s := string(out)
	if strings.Contains(s, "nonce") || strings.Contains(s, "super-secret-xyz") {
		t.Fatalf("agentd nonce must be stripped from generated config: %s", s)
	}
	if !strings.Contains(s, "http://127.0.0.1:8765/v1/edge/hooks/copilot") {
		t.Fatalf("stripped agentd url should be preserved: %s", s)
	}
}

func TestGenerateHookSettings_QuotesCommandPathWithSpaces(t *testing.T) {
	out, err := GenerateHookSettings(HookSettingsOptions{
		HookCommand: "/Applications/Cordum Edge/cordum-hook",
	})
	if err != nil {
		t.Fatalf("GenerateHookSettings: %v", err)
	}
	var doc struct {
		Hooks map[string][]struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	want := `'/Applications/Cordum Edge/cordum-hook' copilot pre-tool-use`
	if got := doc.Hooks["PreToolUse"][0].Command; got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
}

func TestGenerateHookSettings_Defaults(t *testing.T) {
	out, err := GenerateHookSettings(HookSettingsOptions{})
	if err != nil {
		t.Fatalf("GenerateHookSettings: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "cordum-hook copilot pre-tool-use") {
		t.Fatalf("default hook command missing: %s", s)
	}
	if !strings.Contains(s, `"CORDUM_EDGE_POLICY_MODE": "enforce"`) {
		t.Fatalf("default policy mode should be enforce: %s", s)
	}
}

// Copilot times a hook out fail-open, so the generated timeout must stay above
// cordum-hook's 4.5s budget: a shorter request is raised to 5s.
func TestTimeoutSecondsNeverBelowHookDeadline(t *testing.T) {
	for ms, want := range map[int]int{0: 5, 3000: 5, 4500: 5, 5000: 5, 5001: 6, 10000: 10} {
		if got := timeoutSeconds(ms); got != want {
			t.Errorf("timeoutSeconds(%d) = %d, want %d", ms, got, want)
		}
	}
}

func TestVerifyHookSettings(t *testing.T) {
	generated, err := GenerateHookSettings(HookSettingsOptions{HookCommand: "/Applications/Cordum Edge/cordum-hook", FailClosed: true})
	if err != nil {
		t.Fatal(err)
	}
	g := string(generated)
	cases := []struct{ name, file, wantErr string }{
		{"generated", g, ""},
		{"sibling hook", strings.Replace(g, `"PreToolUse": [`, `"PreToolUse": [{"type": "command", "command": "./lint.sh"},`, 1), ""},
		{"not JSON", "{", "cannot parse"},
		{"no version", strings.Replace(g, `"version": 1,`, "", 1), `"version" is not 1`},
		{"version 2", strings.Replace(g, `"version": 1`, `"version": 2`, 1), `"version" is not 1`},
		// camelCase selects Copilot's camelCase payload, which cordum-hook cannot read.
		{"camelCase event", strings.Replace(g, `"PreToolUse"`, `"preToolUse"`, 1), "PreToolUse has no hook"},
		{"wrong subcommand", strings.Replace(g, "copilot pre-tool-use", "copilot post-tool-use", 1), "PreToolUse has no hook"},
		{"platform override", strings.ReplaceAll(g, `"type": "command",`, `"type": "command", "powershell": "echo hi",`), "has no hook"},
		{"short timeout", strings.ReplaceAll(g, `"timeout": 5`, `"timeout": 2`), "timeout is 2s"},
		{"short timeoutSec", strings.ReplaceAll(g, `"timeout": 5`, `"timeout": 5, "timeoutSec": 3`), "timeoutSec is 3s"},
	}
	for _, tc := range cases {
		if tc.name != "generated" && tc.name != "not JSON" && tc.file == g {
			t.Fatalf("%s: replacement did not apply", tc.name)
		}
		err := VerifyHookSettings([]byte(tc.file))
		if (tc.wantErr == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.wantErr)
		}
	}
}
