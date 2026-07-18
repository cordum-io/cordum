// Package copilot generates the GitHub Copilot (VS Code agent mode) hook
// configuration that routes Copilot's governance events through Cordum.
//
// Copilot's Agent Hooks contract matches Claude Code's (same stdin payload and
// the same hookSpecificOutput.permissionDecision / exit-code response), so the
// generated config simply points Copilot's hook commands at `cordum-hook
// copilot <event>`, which reuses the existing Edge runner + agentd + safety
// kernel path. The produced JSON is written to a Copilot hooks config location
// (e.g. .github/hooks/cordum.json or ~/.copilot/hooks/cordum.json).
package copilot

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// HookSettingsOptions configures the generated Copilot agent-hooks document.
type HookSettingsOptions struct {
	// HookCommand is the cordum-hook executable (path or name). Default "cordum-hook".
	HookCommand string
	// AgentdURL is the loopback URL of the local cordum-agentd the hook posts to.
	AgentdURL string
	// PolicyMode is observe | enforce | enterprise-strict. Default "enforce".
	PolicyMode string
	// FailClosed denies the action when agentd errors/times out (enforce posture).
	FailClosed bool
	// TimeoutMS bounds each hook; kept under Copilot's per-hook budget. Default 4500.
	TimeoutMS int
}

type hookCommandEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Timeout int               `json:"timeout,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type hooksDocument struct {
	Hooks map[string][]hookCommandEntry `json:"hooks"`
}

// copilotGovernanceEvents maps Copilot hook event names to the cordum-hook
// subcommand. These three carry the governance surface: every chat
// (UserPromptSubmit) and every tool action (PreToolUse can block; PostToolUse
// records the outcome). Lifecycle events (SessionStart/Stop/PreCompact/
// Subagent*) are intentionally NOT wired — they are not gateable actions and
// the cordum-hook copilot adapter treats them as observe-only no-ops.
var copilotGovernanceEvents = []struct{ event, sub string }{
	{"UserPromptSubmit", "user-prompt-submit"},
	{"PreToolUse", "pre-tool-use"},
	{"PostToolUse", "post-tool-use"},
}

// GenerateHookSettings returns the Copilot agent-hooks JSON document. Secrets
// (the agentd loopback nonce) are intentionally NOT baked in — they are
// supplied to the hook at runtime by the agentd process environment, so this
// document is safe to write to a workspace/user config file.
func GenerateHookSettings(opts HookSettingsOptions) ([]byte, error) {
	cmd := strings.TrimSpace(opts.HookCommand)
	if cmd == "" {
		cmd = "cordum-hook"
	}
	if containsSensitiveValue(cmd) {
		return nil, fmt.Errorf("hook command contains a sensitive value")
	}
	// Strip the loopback auth nonce before it can be baked into the persisted
	// config (mirrors the Claude settings generator's agentdURLForSettings).
	agentdURL := stripAgentdURLNonce(opts.AgentdURL)
	if containsSensitiveValue(agentdURL) {
		return nil, fmt.Errorf("agentd url contains a sensitive value")
	}
	mode := strings.TrimSpace(opts.PolicyMode)
	if mode == "" {
		mode = "enforce"
	}
	timeout := timeoutSeconds(opts.TimeoutMS)

	env := map[string]string{
		"CORDUM_AGENT_PRODUCT":      "github-copilot",
		"CORDUM_EDGE_POLICY_MODE":   mode,
		"CORDUM_AGENTD_FAIL_CLOSED": boolEnv(opts.FailClosed),
	}
	if agentdURL != "" {
		env["CORDUM_AGENTD_URL"] = agentdURL
	}

	doc := hooksDocument{Hooks: map[string][]hookCommandEntry{}}
	for _, e := range copilotGovernanceEvents {
		// env is copied per entry so future per-event tweaks don't alias.
		entryEnv := make(map[string]string, len(env))
		for k, v := range env {
			entryEnv[k] = v
		}
		doc.Hooks[e.event] = []hookCommandEntry{{
			Type:    "command",
			Command: quoteCommandPath(cmd) + " copilot " + e.sub,
			Timeout: timeout,
			Env:     entryEnv,
		}}
	}
	return json.MarshalIndent(doc, "", "  ")
}

// timeoutSeconds converts the caller's millisecond hook budget into the SECONDS
// unit the GitHub/Copilot hook `timeout` field expects. Emitting TimeoutMS
// verbatim would turn the 4500 default into a 4500-second (75 minute) window
// during which a stuck hook could block the agent. Defaults to 4500ms (5s) and
// never rounds below 1s.
func timeoutSeconds(ms int) int {
	if ms <= 0 {
		ms = 4500
	}
	sec := (ms + 999) / 1000
	if sec < 1 {
		sec = 1
	}
	return sec
}

// stripAgentdURLNonce removes the loopback auth nonce query param from an agentd
// URL before it is baked into a generated hooks config. The nonce is a local
// agentd auth credential supplied to the hook at runtime via env and must never
// be persisted. Mirrors the Claude settings generator's stripAgentdURLNonce.
func stripAgentdURLNonce(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	q := u.Query()
	if _, ok := q["nonce"]; !ok {
		return trimmed
	}
	q.Del("nonce")
	u.RawQuery = q.Encode()
	return u.String()
}

// quoteCommandPath wraps a hook command path in POSIX single quotes when it
// contains characters bash would interpret unquoted (whitespace, quotes,
// backslash) so an absolute path like `/Applications/Cordum Edge/cordum-hook`
// survives the `<cmd> copilot <event>` concatenation intact. Mirrors the Claude
// settings generator's quoteCommandPath.
func quoteCommandPath(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return trimmed
	}
	if (strings.HasPrefix(trimmed, `"`) && strings.HasSuffix(trimmed, `"`)) ||
		(strings.HasPrefix(trimmed, `'`) && strings.HasSuffix(trimmed, `'`)) {
		return trimmed
	}
	if strings.ContainsAny(trimmed, " \t\"'\\") {
		return `'` + strings.ReplaceAll(trimmed, `'`, `'\''`) + `'`
	}
	return trimmed
}

func boolEnv(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// containsSensitiveValue is a conservative guard so a generated config never
// embeds an obvious secret marker (mirrors the Claude managed-settings check).
func containsSensitiveValue(s string) bool {
	low := strings.ToLower(s)
	for _, marker := range []string{"sk-", "ghp_", "github_pat_", "bearer ", "authorization:", "secret", "password", "api_key", "apikey", "token="} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}
