package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	claudeSettingsSchema = "https://json.schemastore.org/claude-code-settings.json"

	defaultConfigChangeMatcher = "user_settings|project_settings|local_settings|skills"
	defaultFileChangedMatcher  = ".claude/settings.json|.claude/settings.local.json|CLAUDE.md"
)

// DevSettingsOptions controls generation of a local developer Claude
// settings.json fragment. Values are intended to be short-lived session
// metadata; long-lived API keys and tokens are rejected.
type DevSettingsOptions struct {
	SessionID            string
	ExecutionID          string
	AgentdURL            string
	AgentdHookNonce      string
	AgentdSocket         string
	HookCommand          string
	HookTimeout          time.Duration
	PolicyMode           string
	ApprovalWaitTimeout  time.Duration
	FailClosed           bool
	Platform             string
	ExtraEnv             map[string]string
	FileChangedWatchList []string
}

// ManagedSettingsOptions controls enterprise managed-settings template
// generation. The implementation is completed in managed_settings.go.
type ManagedSettingsOptions struct {
	HookCommand                string
	HookTimeout                time.Duration
	AgentdURL                  string
	AgentdHookNonce            string
	MCPGatewayURL              string
	LLMProxyBaseURL            string
	APIKeyHelperCommand        string
	ForceRemoteSettingsRefresh bool
	Platform                   string
}

// ManagedSettingsBundle contains generated enterprise managed settings,
// managed MCP configuration, and human-readable rollout notes.
type ManagedSettingsBundle struct {
	ManagedSettingsJSON []byte
	ManagedMCPJSON      []byte
	Notes               string
}

type claudeSettingsDocument struct {
	Schema string                     `json:"$schema,omitempty"`
	Env    map[string]string          `json:"env,omitempty"`
	Hooks  map[string][]claudeHookSet `json:"hooks,omitempty"`
}

type claudeHookSet struct {
	Matcher string              `json:"matcher,omitempty"`
	Hooks   []claudeCommandHook `json:"hooks"`
}

type claudeCommandHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// GenerateDevSettingsJSON returns a deterministic Claude Code settings.json
// payload for local development. It emits command hooks only; HTTP hook URLs
// are deliberately not part of the production path.
func GenerateDevSettingsJSON(opts DevSettingsOptions) ([]byte, error) {
	if err := validateDevSettingsOptions(opts); err != nil {
		return nil, err
	}
	hookCommand := hookCommandOrDefault(opts.HookCommand)
	env := map[string]string{
		"CORDUM_EDGE_SESSION_ID":            strings.TrimSpace(opts.SessionID),
		"CORDUM_EDGE_EXECUTION_ID":          strings.TrimSpace(opts.ExecutionID),
		"CORDUM_AGENTD_HOOK_TIMEOUT":        durationForEnv(hookTimeoutOrDefault(opts.HookTimeout)),
		"CORDUM_EDGE_MODE":                  strings.TrimSpace(opts.PolicyMode),
		"CORDUM_AGENTD_FAIL_CLOSED":         boolString(opts.FailClosed),
		"CORDUM_EDGE_APPROVAL_WAIT_TIMEOUT": durationForEnv(opts.ApprovalWaitTimeout),
		"CORDUM_EDGE_PLATFORM":              strings.TrimSpace(opts.Platform),
	}
	if strings.TrimSpace(opts.AgentdURL) != "" {
		env["CORDUM_AGENTD_URL"] = agentdURLForSettings(opts.AgentdURL)
	}
	if strings.TrimSpace(opts.AgentdSocket) != "" {
		env["CORDUM_AGENTD_SOCKET"] = strings.TrimSpace(opts.AgentdSocket)
	}
	for _, key := range sortedKeys(opts.ExtraEnv) {
		env[strings.TrimSpace(key)] = strings.TrimSpace(opts.ExtraEnv[key])
	}

	doc := claudeSettingsDocument{
		Schema: claudeSettingsSchema,
		Env:    env,
		Hooks:  commandHookSettings(hookCommand, opts.HookTimeout, opts.FileChangedWatchList),
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal dev settings: %w", err)
	}
	return append(data, '\n'), nil
}

func validateDevSettingsOptions(opts DevSettingsOptions) error {
	required := map[string]string{
		"session_id":   opts.SessionID,
		"execution_id": opts.ExecutionID,
		"policy_mode":  opts.PolicyMode,
		"platform":     opts.Platform,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s required", name)
		}
	}
	if strings.TrimSpace(opts.AgentdURL) == "" && strings.TrimSpace(opts.AgentdSocket) == "" {
		return errors.New("agentd url or socket required")
	}
	if opts.ApprovalWaitTimeout <= 0 {
		return errors.New("approval_wait_timeout required")
	}
	if err := validateNonSecretEnv(opts.ExtraEnv); err != nil {
		return err
	}
	return nil
}

func hookCommandOrDefault(command string) string {
	if strings.TrimSpace(command) == "" {
		return "cordum-hook"
	}
	return strings.TrimSpace(command)
}

func validateNonSecretEnv(env map[string]string) error {
	for key, value := range env {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return errors.New("extra env key required")
		}
		if isManagedReservedEnvKey(trimmedKey) {
			return fmt.Errorf("extra env %s is reserved for managed settings", redactDiagnostic(trimmedKey))
		}
		if isSensitiveEnvKey(trimmedKey) {
			return fmt.Errorf("extra env %s is sensitive and must not be stored in Claude settings", redactDiagnostic(trimmedKey))
		}
		if strings.Contains(redactDiagnostic(value), "[REDACTED]") {
			return fmt.Errorf("extra env %s contains sensitive value", redactDiagnostic(trimmedKey))
		}
	}
	return nil
}

func isSensitiveEnvKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if strings.Contains(k, "api_key") || strings.Contains(k, "apikey") {
		return true
	}
	for _, marker := range []string{"password", "passwd", "secret", "token", "credential", "nonce"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

func isManagedReservedEnvKey(key string) bool {
	return strings.HasPrefix(strings.ToUpper(strings.TrimSpace(key)), "CORDUM_EDGE_MANAGED_")
}

func agentdURLForSettings(raw string) string {
	cleaned, _ := stripAgentdURLNonce(raw)
	return cleaned
}

func stripAgentdURLNonce(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return trimmed, false
	}
	q := u.Query()
	if _, ok := q["nonce"]; !ok {
		return trimmed, false
	}
	q.Del("nonce")
	u.RawQuery = q.Encode()
	return u.String(), true
}

func commandHookSettings(hookCommand string, timeout time.Duration, watchList []string) map[string][]claudeHookSet {
	return map[string][]claudeHookSet{
		"UserPromptSubmit": {
			{Hooks: []claudeCommandHook{commandHook(hookCommand, "user-prompt-submit", timeout)}},
		},
		"PreToolUse": {
			{Matcher: "*", Hooks: []claudeCommandHook{commandHook(hookCommand, "pre-tool-use", timeout)}},
		},
		"PostToolUse": {
			{Matcher: "*", Hooks: []claudeCommandHook{commandHook(hookCommand, "post-tool-use", timeout)}},
		},
		"PostToolUseFailure": {
			{Matcher: "*", Hooks: []claudeCommandHook{commandHook(hookCommand, "post-tool-use-failure", timeout)}},
		},
		"ConfigChange": {
			{Matcher: defaultConfigChangeMatcher, Hooks: []claudeCommandHook{commandHook(hookCommand, "config-change", timeout)}},
		},
		"FileChanged": {
			{Matcher: fileChangedMatcher(watchList), Hooks: []claudeCommandHook{commandHook(hookCommand, "file-changed", timeout)}},
		},
	}
}

func commandHook(hookCommand, subcommand string, timeout time.Duration) claudeCommandHook {
	return claudeCommandHook{
		Type:    "command",
		Command: fmt.Sprintf("%s claude %s", quoteCommandPath(strings.TrimSpace(hookCommand)), subcommand),
		Timeout: hookTimeoutSeconds(timeout),
	}
}

func quoteCommandPath(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return trimmed
	}
	if (strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`)) ||
		(strings.HasPrefix(trimmed, `'`) && strings.HasSuffix(trimmed, `'`)) {
		return trimmed
	}
	// Wrap in POSIX single-quotes when the path contains anything bash would
	// interpret unquoted: whitespace, double-quote, single-quote, or
	// backslash. Single-quote wrapping makes bash treat the contents
	// literally, so Windows paths like `.\bin\cordum-hook` don't get \b
	// interpreted as a backspace escape (the EDGE-045 failure mode that
	// collapsed `.\bin\cordum-hook` to `.bincordum-hook` inside Claude's
	// bash one-liner). Embedded single quotes are escaped via the standard
	// POSIX `'\''` close/escape/reopen idiom.
	if strings.ContainsAny(trimmed, " \t\"'\\") {
		return `'` + strings.ReplaceAll(trimmed, `'`, `'\''`) + `'`
	}
	return trimmed
}

func fileChangedMatcher(watchList []string) string {
	cleaned := make([]string, 0, len(watchList))
	for _, item := range watchList {
		item = strings.TrimSpace(item)
		if item != "" {
			cleaned = append(cleaned, item)
		}
	}
	if len(cleaned) == 0 {
		return defaultFileChangedMatcher
	}
	return strings.Join(cleaned, "|")
}

func hookTimeoutOrDefault(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultHookTimeout
	}
	return d
}

func hookTimeoutSeconds(d time.Duration) int {
	d = hookTimeoutOrDefault(d)
	seconds := d.Seconds()
	if seconds < 1 {
		return 1
	}
	return int(math.Ceil(seconds))
}

func durationForEnv(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return d.String()
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// RenderSettingsPreview returns a bounded, redacted preview suitable for
// terminal output and task evidence. It intentionally includes token-storage
// guidance so local dev metadata is not confused with enterprise credentials.
func RenderSettingsPreview(data []byte, mode string) string {
	const maxPreviewBytes = 4000
	preview := strings.TrimSpace(string(data))
	if len(preview) > maxPreviewBytes {
		preview = preview[:maxPreviewBytes] + "..."
	}
	preview = redactDiagnostic(preview)
	if preview == "" {
		preview = "{}"
	}
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "settings-output"
	}
	return fmt.Sprintf("%s preview: %s\nToken tradeoff: dev settings carry session metadata only; enterprise uses agentd memory/keychain/service bootstrap. Do not store long-lived API keys or tokens in Claude settings.", mode, preview)
}
