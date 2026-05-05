package claude

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

const syntheticAgentdHexNonce = "f00ddeadbeefcafe0123456789abcdef"

var agentdURLNoncePattern = regexp.MustCompile(`http://127\.0\.0\.1:\d+/[^"\s?]*\?[^"\s]*nonce=[0-9a-fA-F]{32}`)

func TestGenerateDevSettingsJSONIncludesCordumCommandHooks(t *testing.T) {
	data, err := GenerateDevSettingsJSON(DevSettingsOptions{
		SessionID:            "sess-123",
		ExecutionID:          "exec-456",
		AgentdURL:            "http://127.0.0.1:8765/v1/edge/hooks/claude",
		HookCommand:          "cordum-hook",
		HookTimeout:          DefaultHookTimeout,
		PolicyMode:           "local-dev-enforce",
		FailClosed:           true,
		ApprovalWaitTimeout:  30 * time.Second,
		Platform:             "windows",
		ExtraEnv:             map[string]string{"CORDUM_EDGE_TRACE_ID": "trace-789"},
		FileChangedWatchList: []string{".claude/settings.json", ".claude/settings.local.json", "CLAUDE.md"},
	})
	if err != nil {
		t.Fatalf("GenerateDevSettingsJSON returned error: %v", err)
	}

	settings := decodeJSONMap(t, data)
	if got := settings["$schema"]; got != "https://json.schemastore.org/claude-code-settings.json" {
		t.Fatalf("$schema = %v", got)
	}

	env := jsonObject(t, settings["env"])
	wantEnv := map[string]string{
		"CORDUM_EDGE_SESSION_ID":            "sess-123",
		"CORDUM_EDGE_EXECUTION_ID":          "exec-456",
		"CORDUM_AGENTD_URL":                 "http://127.0.0.1:8765/v1/edge/hooks/claude",
		"CORDUM_AGENTD_HOOK_TIMEOUT":        "4.5s",
		"CORDUM_EDGE_MODE":                  "local-dev-enforce",
		"CORDUM_AGENTD_FAIL_CLOSED":         "true",
		"CORDUM_EDGE_APPROVAL_WAIT_TIMEOUT": "30s",
		"CORDUM_EDGE_PLATFORM":              "windows",
		"CORDUM_EDGE_TRACE_ID":              "trace-789",
	}
	if len(env) != len(wantEnv) {
		t.Fatalf("env len = %d, want %d: %#v", len(env), len(wantEnv), env)
	}
	for key, want := range wantEnv {
		if got := env[key]; got != want {
			t.Fatalf("env[%s] = %v, want %q", key, got, want)
		}
	}

	hooks := jsonObject(t, settings["hooks"])
	assertCommandHook(t, hooks, "UserPromptSubmit", "", "cordum-hook claude user-prompt-submit", 5)
	assertCommandHook(t, hooks, "PreToolUse", "*", "cordum-hook claude pre-tool-use", 5)
	assertCommandHook(t, hooks, "PostToolUse", "*", "cordum-hook claude post-tool-use", 5)
	assertCommandHook(t, hooks, "PostToolUseFailure", "*", "cordum-hook claude post-tool-use-failure", 5)
	assertCommandHook(t, hooks, "ConfigChange", "user_settings|project_settings|local_settings|skills", "cordum-hook claude config-change", 5)
	assertCommandHook(t, hooks, "FileChanged", ".claude/settings.json|.claude/settings.local.json|CLAUDE.md", "cordum-hook claude file-changed", 5)

	compact := string(data)
	if strings.Contains(compact, `"url"`) || strings.Contains(compact, `"type":"http"`) || strings.Contains(compact, "http://localhost") {
		t.Fatalf("dev settings must not generate HTTP hook configuration: %s", compact)
	}
	assertNoSyntheticSecrets(t, compact)
}

func TestDevSettingsRendersNonceOutsideURL(t *testing.T) {
	data, err := GenerateDevSettingsJSON(DevSettingsOptions{
		SessionID:           "sess-123",
		ExecutionID:         "exec-456",
		AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude?nonce=" + syntheticAgentdHexNonce,
		AgentdHookNonce:     syntheticAgentdHexNonce,
		HookCommand:         "cordum-hook",
		HookTimeout:         DefaultHookTimeout,
		PolicyMode:          "local-dev-enforce",
		ApprovalWaitTimeout: 30 * time.Second,
		Platform:            "linux",
	})
	if err != nil {
		t.Fatalf("GenerateDevSettingsJSON returned error: %v", err)
	}

	settings := decodeJSONMap(t, data)
	env := jsonObject(t, settings["env"])
	if got := env["CORDUM_AGENTD_URL"]; got != "http://127.0.0.1:8765/v1/edge/hooks/claude" {
		t.Fatalf("CORDUM_AGENTD_URL = %v, want bare loopback hook URL", got)
	}
	if _, ok := env["CORDUM_AGENTD_HOOK_NONCE"]; ok {
		t.Fatalf("dev settings must not persist CORDUM_AGENTD_HOOK_NONCE: %#v", env)
	}
	assertRenderedSettingsOmitsAgentdNonce(t, data, syntheticAgentdHexNonce)
}

func TestDevSettingsOmitsAgentdNonceVariants(t *testing.T) {
	encodedHex := url.QueryEscape(syntheticAgentdHexNonce)
	cases := []struct {
		name      string
		urlNonce  string
		forbidden []string
	}{
		{name: "lower_hex", urlNonce: syntheticAgentdHexNonce, forbidden: []string{syntheticAgentdHexNonce}},
		{name: "upper_hex", urlNonce: strings.ToUpper(syntheticAgentdHexNonce), forbidden: []string{strings.ToUpper(syntheticAgentdHexNonce)}},
		{name: "mixed_hex", urlNonce: "F00dDeadBeefCafe0123456789abcDEF", forbidden: []string{"F00dDeadBeefCafe0123456789abcDEF"}},
		{name: "base64url", urlNonce: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ", forbidden: []string{"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"}},
		{name: "url_encoded", urlNonce: encodedHex, forbidden: []string{syntheticAgentdHexNonce, encodedHex}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := GenerateDevSettingsJSON(DevSettingsOptions{
				SessionID:           "sess-123",
				ExecutionID:         "exec-456",
				AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude?nonce=" + tc.urlNonce,
				AgentdHookNonce:     tc.urlNonce,
				HookCommand:         "cordum-hook",
				HookTimeout:         DefaultHookTimeout,
				PolicyMode:          "local-dev-enforce",
				ApprovalWaitTimeout: 30 * time.Second,
				Platform:            "linux",
			})
			if err != nil {
				t.Fatalf("GenerateDevSettingsJSON returned error: %v", err)
			}
			env := jsonObject(t, decodeJSONMap(t, data)["env"])
			if got := env["CORDUM_AGENTD_URL"]; got != "http://127.0.0.1:8765/v1/edge/hooks/claude" {
				t.Fatalf("CORDUM_AGENTD_URL = %v, want bare loopback hook URL", got)
			}
			if _, ok := env["CORDUM_AGENTD_HOOK_NONCE"]; ok {
				t.Fatalf("dev settings must not persist CORDUM_AGENTD_HOOK_NONCE: %#v", env)
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(string(data), forbidden) {
					t.Fatalf("rendered settings leaked nonce variant %q in %s", forbidden, data)
				}
			}
			if strings.Contains(string(data), "nonce=") {
				t.Fatalf("rendered settings kept nonce query: %s", data)
			}
		})
	}
}

func TestDevSettingsRejectsPersistedNonceEnv(t *testing.T) {
	_, err := GenerateDevSettingsJSON(DevSettingsOptions{
		SessionID:           "sess-123",
		ExecutionID:         "exec-456",
		AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
		HookCommand:         "cordum-hook",
		HookTimeout:         DefaultHookTimeout,
		PolicyMode:          "local-dev-enforce",
		ApprovalWaitTimeout: 30 * time.Second,
		Platform:            "linux",
		ExtraEnv:            map[string]string{"CORDUM_AGENTD_HOOK_NONCE": syntheticAgentdHexNonce},
	})
	if err == nil {
		t.Fatalf("GenerateDevSettingsJSON accepted persisted CORDUM_AGENTD_HOOK_NONCE")
	}
	if strings.Contains(err.Error(), syntheticAgentdHexNonce) {
		t.Fatalf("error leaked nonce value: %v", err)
	}
	if !isSensitiveEnvKey("CORDUM_AGENTD_HOOK_NONCE") {
		t.Fatalf("CORDUM_AGENTD_HOOK_NONCE must remain classified as sensitive")
	}
}

func TestGenerateDevSettingsJSONRejectsRawSecretEnv(t *testing.T) {
	_, err := GenerateDevSettingsJSON(DevSettingsOptions{
		SessionID:           "sess-123",
		ExecutionID:         "exec-456",
		AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
		HookCommand:         "cordum-hook",
		HookTimeout:         DefaultHookTimeout,
		PolicyMode:          "local-dev-enforce",
		ApprovalWaitTimeout: 30 * time.Second,
		ExtraEnv:            map[string]string{"ANTHROPIC_API_KEY": "sk-test-secret"},
	})
	if err == nil {
		t.Fatalf("GenerateDevSettingsJSON accepted raw API key env")
	}
	if strings.Contains(err.Error(), "sk-test-secret") {
		t.Fatalf("error leaked raw secret: %v", err)
	}
}

func TestGenerateDevSettingsJSONRejectsManagedReservedEnv(t *testing.T) {
	_, err := GenerateDevSettingsJSON(DevSettingsOptions{
		SessionID:           "sess-123",
		ExecutionID:         "exec-456",
		AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
		HookCommand:         "cordum-hook",
		HookTimeout:         DefaultHookTimeout,
		PolicyMode:          "local-dev-enforce",
		ApprovalWaitTimeout: 30 * time.Second,
		Platform:            "linux",
		ExtraEnv:            map[string]string{"CORDUM_EDGE_MANAGED_POLICY_MODE": "observe"},
	})
	if err == nil {
		t.Fatalf("GenerateDevSettingsJSON accepted managed-reserved env")
	}
	if strings.Contains(err.Error(), "observe") {
		t.Fatalf("error leaked env value: %v", err)
	}
	if !isManagedReservedEnvKey("CORDUM_EDGE_MANAGED_POLICY_MODE") {
		t.Fatalf("CORDUM_EDGE_MANAGED_POLICY_MODE must remain managed-reserved")
	}
}

func TestGenerateDevSettingsJSONNormalizesHookCommandPaths(t *testing.T) {
	cases := []struct {
		name        string
		platform    string
		hookCommand string
		want        string
	}{
		// EDGE-045 — switched from double-quote to POSIX single-quote wrapping
		// so Windows backslash paths don't get \b/\c/etc. interpreted as bash
		// escape sequences inside Claude's hook one-liner.
		{
			name:        "windows program files",
			platform:    "windows",
			hookCommand: `C:\Program Files\Cordum\cordum-hook.exe`,
			want:        `'C:\Program Files\Cordum\cordum-hook.exe' claude pre-tool-use`,
		},
		{
			name:        "windows backslash no spaces (EDGE-045 regression)",
			platform:    "windows",
			hookCommand: `.\bin\cordum-hook.exe`,
			want:        `'.\bin\cordum-hook.exe' claude pre-tool-use`,
		},
		{
			name:        "msys path with spaces",
			platform:    "msys",
			hookCommand: `/c/Program Files/Cordum/cordum-hook.exe`,
			want:        `'/c/Program Files/Cordum/cordum-hook.exe' claude pre-tool-use`,
		},
		{
			name:        "wsl linux path",
			platform:    "wsl",
			hookCommand: `/usr/local/bin/cordum-hook`,
			want:        `/usr/local/bin/cordum-hook claude pre-tool-use`,
		},
		{
			name:        "macos path with spaces",
			platform:    "darwin",
			hookCommand: `/Applications/Cordum Edge/cordum-hook`,
			want:        `'/Applications/Cordum Edge/cordum-hook' claude pre-tool-use`,
		},
		{
			name:        "relative dev path",
			platform:    "linux",
			hookCommand: `tools/bin/cordum-hook`,
			want:        `tools/bin/cordum-hook claude pre-tool-use`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := GenerateDevSettingsJSON(DevSettingsOptions{
				SessionID:           "sess-123",
				ExecutionID:         "exec-456",
				AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
				HookCommand:         tc.hookCommand,
				HookTimeout:         DefaultHookTimeout,
				PolicyMode:          "local-dev-enforce",
				ApprovalWaitTimeout: 30 * time.Second,
				Platform:            tc.platform,
			})
			if err != nil {
				t.Fatalf("GenerateDevSettingsJSON returned error: %v", err)
			}
			hooks := jsonObject(t, decodeJSONMap(t, data)["hooks"])
			assertCommandHook(t, hooks, "PreToolUse", "*", tc.want, 5)
		})
	}
}

func TestGenerateDevSettingsJSONDefaultsDevHookCommandToPathBinary(t *testing.T) {
	data, err := GenerateDevSettingsJSON(DevSettingsOptions{
		SessionID:           "sess-123",
		ExecutionID:         "exec-456",
		AgentdURL:           "http://127.0.0.1:8765/v1/edge/hooks/claude",
		HookTimeout:         DefaultHookTimeout,
		PolicyMode:          "local-dev-enforce",
		ApprovalWaitTimeout: 30 * time.Second,
		Platform:            "linux",
	})
	if err != nil {
		t.Fatalf("GenerateDevSettingsJSON returned error: %v", err)
	}
	hooks := jsonObject(t, decodeJSONMap(t, data)["hooks"])
	assertCommandHook(t, hooks, "PreToolUse", "*", "cordum-hook claude pre-tool-use", 5)
	assertCommandHook(t, hooks, "UserPromptSubmit", "", "cordum-hook claude user-prompt-submit", 5)
}

func TestSettingsPreviewRedactsSecretsAndExplainsTokenTradeoff(t *testing.T) {
	preview := RenderSettingsPreview([]byte(`{
		"env": {
			"ANTHROPIC_API_KEY": "sk-test-secret",
			"CORDUM_API_KEY": "ghp_testtoken",
			"CORDUM_EDGE_SESSION_ID": "sess-123"
		}
	}`), "dev")
	assertNoSyntheticSecrets(t, preview)
	if !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("preview did not redact sensitive values: %s", preview)
	}
	for _, want := range []string{"dev settings carry session metadata", "enterprise uses agentd memory/keychain/service bootstrap", "Do not store long-lived API keys"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing token tradeoff text %q: %s", want, preview)
		}
	}
}

func decodeJSONMap(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("invalid JSON: %v; raw=%s", err, data)
	}
	return out
}

func jsonObject(t *testing.T, v any) map[string]any {
	t.Helper()
	out, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("value is %T, want object: %#v", v, v)
	}
	return out
}

func jsonArray(t *testing.T, v any) []any {
	t.Helper()
	out, ok := v.([]any)
	if !ok {
		t.Fatalf("value is %T, want array: %#v", v, v)
	}
	return out
}

func assertCommandHook(t *testing.T, hooks map[string]any, eventName, matcher, command string, timeout float64) {
	t.Helper()
	entries := jsonArray(t, hooks[eventName])
	if len(entries) != 1 {
		t.Fatalf("%s entries len = %d, want 1", eventName, len(entries))
	}
	group := jsonObject(t, entries[0])
	if matcher == "" {
		if _, ok := group["matcher"]; ok {
			t.Fatalf("%s matcher present for matcherless event: %#v", eventName, group["matcher"])
		}
	} else if got := group["matcher"]; got != matcher {
		t.Fatalf("%s matcher = %v, want %q", eventName, got, matcher)
	}
	commands := jsonArray(t, group["hooks"])
	if len(commands) != 1 {
		t.Fatalf("%s hooks len = %d, want 1", eventName, len(commands))
	}
	hook := jsonObject(t, commands[0])
	if got := hook["type"]; got != "command" {
		t.Fatalf("%s hook type = %v, want command", eventName, got)
	}
	if got := hook["command"]; got != command {
		t.Fatalf("%s hook command = %v, want %q", eventName, got, command)
	}
	if got := hook["timeout"]; got != timeout {
		t.Fatalf("%s hook timeout = %v, want %v", eventName, got, timeout)
	}
	if _, ok := hook["url"]; ok {
		t.Fatalf("%s hook unexpectedly contains url: %#v", eventName, hook)
	}
}

func assertRenderedSettingsOmitsAgentdNonce(t *testing.T, data []byte, nonce string) {
	t.Helper()
	text := string(data)
	if strings.Contains(text, nonce) {
		t.Fatalf("rendered settings leaked agentd nonce %q in %s", nonce, text)
	}
	if agentdURLNoncePattern.Match(data) {
		t.Fatalf("rendered settings leaked nonce in CORDUM_AGENTD_URL: %s", text)
	}
}

func TestQuoteCommandPathHandlesQuotesAndSpaces(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain", "/usr/local/bin/cordum-hook", "/usr/local/bin/cordum-hook"},
		{"already double-quoted", `"/path with space/cordum-hook"`, `"/path with space/cordum-hook"`},
		{"already single-quoted", `'/path with space/cordum-hook'`, `'/path with space/cordum-hook'`},
		// EDGE-045: switched to POSIX single-quote wrapping. Bash treats
		// single-quoted content verbatim, so `\b`/`\c`/etc. don't become
		// escape sequences. Double-quote wrapping (the old behavior) still
		// left `\b` interpretable, which is the EDGE-045 failure mode.
		{"contains space", `/Program Files/cordum/cordum-hook.exe`, `'/Program Files/cordum/cordum-hook.exe'`},
		{"contains tab", "/path\twith\ttab/cordum-hook", "'/path\twith\ttab/cordum-hook'"},
		{"embedded double quote no space", `/odd"path/cordum-hook`, `'/odd"path/cordum-hook'`},
		{"embedded double quote with space", `/Program Files/odd"path/cordum-hook`, `'/Program Files/odd"path/cordum-hook'`},
		// EDGE-045 primary regression case: Windows backslash path. Pre-fix
		// `quoteCommandPath` did NOT include `\` in its trigger set, so the
		// path returned unwrapped and bash collapsed `.\bin\cordum-hook` to
		// `.bincordum-hook` (`\b` parsed as backspace). Post-fix the path is
		// wrapped in single-quotes so backslashes are literal.
		{"windows backslash path", `.\bin\cordum-hook`, `'.\bin\cordum-hook'`},
		{"windows absolute backslash path", `D:\Cordum\cordum\bin\cordum-hook.exe`, `'D:\Cordum\cordum\bin\cordum-hook.exe'`},
		{"backslash with space", `D:\Program Files\cordum\cordum-hook.exe`, `'D:\Program Files\cordum\cordum-hook.exe'`},
		// Embedded single quote — POSIX `'\''` close/escape/reopen idiom.
		{"embedded single quote", `/path/with'apostrophe/cordum-hook`, `'/path/with'\''apostrophe/cordum-hook'`},
		{"empty", "", ""},
		{"whitespace-only", "   ", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := quoteCommandPath(tc.in); got != tc.want {
				t.Fatalf("quoteCommandPath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
