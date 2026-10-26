// Package copilot generates the GitHub Copilot (VS Code agent mode) hook
// configuration that routes Copilot's governance events through Cordum.
//
// PascalCase Copilot hook events send Claude Code's stdin payload, so the
// generated config points Copilot's hook commands at `cordum-hook copilot
// <event>`, which reuses the existing Edge runner + agentd + safety kernel path
// and re-encodes its answer for Copilot. The produced JSON is written to a
// Copilot hooks config location (e.g. .github/hooks/cordum.json or
// ~/.copilot/hooks/cordum.json).
//
// Unlike Claude Code, Copilot has no "onFailure": "block" for the hook process
// itself: a hook timeout is always fail-open, and the Local harness also lets
// exits other than 2 through. FailClosed covers only failures cordum-hook
// handles itself. See docs/edge/cordum-hook.md#github-copilot.
package copilot

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cordum/cordum/core/edge/claude"
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
	// Copilot has no onFailure equivalent, so a hook that itself times out or
	// crashes is still handled by Copilot's own (mostly fail-open) rules.
	FailClosed bool
	// TimeoutMS is Copilot's per-hook timeout, rounded up to whole seconds and
	// never below claude.ClaudeHookDeadline (5s). Default 5s.
	TimeoutMS int
}

type hookCommandEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Timeout int               `json:"timeout,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// hooksDocument is one hooks file both Copilot harnesses read. The GitHub
// Copilot hooks reference requires "version": 1 ("Structural errors ... a bad
// version ... reject the entire file"); VS Code's Local hooks reference says
// "a numeric version does not select a different Local payload schema", so
// the field is safe there too.
type hooksDocument struct {
	Version int                           `json:"version"`
	Hooks   map[string][]hookCommandEntry `json:"hooks"`
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

	doc := hooksDocument{Version: 1, Hooks: map[string][]hookCommandEntry{}}
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
// verbatim would turn a 4500 value into a 4500-second (75 minute) window
// during which a stuck hook could block the agent. Copilot times a hook out
// fail-open, so the result never drops below claude.ClaudeHookDeadline: the
// deadline cordum-hook's own 4.5s budget is built to finish under, which lets
// the hook decide (and deny, when fail-closed) before Copilot gives up.
func timeoutSeconds(ms int) int {
	return max((ms+999)/1000, int(claude.ClaudeHookDeadline/time.Second))
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
