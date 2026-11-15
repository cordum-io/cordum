package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/cordum/cordum/core/edge"
)

const (
	managedPolicyModeEnv = "CORDUM_EDGE_MANAGED_POLICY_MODE"
	managedHooksOnlyEnv  = "CORDUM_EDGE_MANAGED_HOOKS_ONLY"
)

type managedSettingsDocument struct {
	Schema                     string                     `json:"$schema,omitempty"`
	AllowManagedHooksOnly      bool                       `json:"allowManagedHooksOnly"`
	AllowManagedMcpServersOnly bool                       `json:"allowManagedMcpServersOnly"`
	AllowedHTTPHookURLs        []string                   `json:"allowedHttpHookUrls"`
	AllowedMcpServers          []managedMCPAllow          `json:"allowedMcpServers"`
	ManagedMCPServers          json.RawMessage            `json:"managedMcpServers,omitempty"`
	AllowChromeWithManagedMCP  json.RawMessage            `json:"allowClaudeInChromeWithManagedMcp,omitempty"`
	DisableBypassPermissions   string                     `json:"disableBypassPermissionsMode"`
	ForceRemoteRefresh         *bool                      `json:"forceRemoteSettingsRefresh,omitempty"`
	APIKeyHelper               string                     `json:"apiKeyHelper,omitempty"`
	Env                        map[string]string          `json:"env,omitempty"`
	Hooks                      map[string][]claudeHookSet `json:"hooks,omitempty"`
}

// managedMCPAllow is one allowedMcpServers entry. The template emits serverUrl
// only: a serverName entry is not a security control (users pick names) and
// would also admit any stdio server given that name while no serverCommand
// entry exists. ServerName and ServerCommand are decoded so verify can flag
// them.
type managedMCPAllow struct {
	ServerURL     string   `json:"serverUrl,omitempty"`
	ServerName    string   `json:"serverName,omitempty"`
	ServerCommand []string `json:"serverCommand,omitempty"`
}

type managedMCPDocument struct {
	MCPServers map[string]managedMCPServer `json:"mcpServers"`
}

type managedMCPServer struct {
	Type          string `json:"type"`
	URL           string `json:"url"`
	HeadersHelper string `json:"headersHelper,omitempty"`
}

// GenerateManagedSettingsTemplate returns enterprise managed-settings.json and
// managed-mcp.json templates. The templates intentionally contain placeholders
// and helper commands rather than long-lived tokens.
func GenerateManagedSettingsTemplate(opts ManagedSettingsOptions) (ManagedSettingsBundle, error) {
	llmProxyBaseURL, mcpURL, err := validateManagedSettingsOptions(opts)
	if err != nil {
		return ManagedSettingsBundle{}, err
	}
	hookCommand := hookCommandOrDefault(opts.HookCommand)
	timeout := hookTimeoutOrDefault(opts.HookTimeout)
	env := map[string]string{
		"CORDUM_EDGE_MODE":           "enterprise-strict",
		managedPolicyModeEnv:         "enterprise-strict",
		managedHooksOnlyEnv:          "true",
		"CORDUM_AGENTD_FAIL_CLOSED":  "true",
		"CORDUM_AGENTD_URL":          agentdURLForSettings(opts.AgentdURL),
		"CORDUM_AGENTD_HOOK_TIMEOUT": durationForEnv(timeout),
		"ANTHROPIC_BASE_URL":         llmProxyBaseURL,
	}
	if strings.TrimSpace(opts.Platform) != "" {
		env["CORDUM_EDGE_PLATFORM"] = strings.TrimSpace(opts.Platform)
	}
	var forceRefresh *bool
	if opts.ForceRemoteSettingsRefresh {
		v := true
		forceRefresh = &v
	}
	settings := managedSettingsDocument{
		Schema:                     claudeSettingsSchema,
		AllowManagedHooksOnly:      true,
		AllowManagedMcpServersOnly: true,
		AllowedHTTPHookURLs:        []string{},
		AllowedMcpServers:          []managedMCPAllow{{ServerURL: mcpURL}},
		DisableBypassPermissions:   "disable",
		ForceRemoteRefresh:         forceRefresh,
		APIKeyHelper:               strings.TrimSpace(opts.APIKeyHelperCommand),
		Env:                        env,
		Hooks:                      commandHookSettings(hookCommand, timeout, nil, true),
	}
	settingsJSON, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return ManagedSettingsBundle{}, fmt.Errorf("marshal managed settings: %w", err)
	}
	mcp := managedMCPDocument{MCPServers: map[string]managedMCPServer{
		"cordum-edge": {
			Type:          "http",
			URL:           mcpURL,
			HeadersHelper: managedHeadersHelper(opts),
		},
	}}
	mcpJSON, err := json.MarshalIndent(mcp, "", "  ")
	if err != nil {
		return ManagedSettingsBundle{}, fmt.Errorf("marshal managed mcp: %w", err)
	}
	return ManagedSettingsBundle{
		ManagedSettingsJSON: append(settingsJSON, '\n'),
		ManagedMCPJSON:      append(mcpJSON, '\n'),
		Notes:               managedSettingsNotes(),
	}, nil
}

func validateManagedSettingsOptions(opts ManagedSettingsOptions) (string, string, error) {
	// hook_command is intentionally absent: GenerateManagedSettingsTemplate
	// fills in the built-in default via hookCommandOrDefault when the caller
	// leaves opts.HookCommand empty, so requiring it here would make the
	// happy path unreachable. The default is still validated for sensitive
	// values via the loop below.
	required := map[string]string{
		"agentd_url":             agentdURLForSettings(opts.AgentdURL),
		"mcp_gateway_url":        opts.MCPGatewayURL,
		"llm_proxy_base_url":     opts.LLMProxyBaseURL,
		"api_key_helper_command": opts.APIKeyHelperCommand,
	}
	if opts.HookCommand != "" && containsSensitiveValue(opts.HookCommand) {
		return "", "", fmt.Errorf("hook_command contains sensitive value")
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return "", "", fmt.Errorf("%s required", name)
		}
		if containsSensitiveValue(value) {
			return "", "", fmt.Errorf("%s contains sensitive value", name)
		}
	}
	mcpURL, err := validateManagedMCPGatewayURL(opts.MCPGatewayURL)
	if err != nil {
		return "", "", err
	}
	llmProxyBaseURL, err := edge.ValidateProviderBaseURL(context.Background(), opts.LLMProxyBaseURL)
	if err != nil {
		return "", "", fmt.Errorf("llm_proxy_base_url: %w", err)
	}
	return llmProxyBaseURL, mcpURL, nil
}

// A host-only Claude serverUrl allowlist pattern admits every path on that
// host. Require a literal endpoint, while allowing private gateways and custom
// ingress paths without DNS or SSRF checks on this local configuration input.
func validateManagedMCPGatewayURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		return "", fmt.Errorf("mcp_gateway_url must be an absolute HTTP(S) URL")
	}
	if u.User != nil {
		return "", fmt.Errorf("mcp_gateway_url must not contain credentials; use headersHelper")
	}
	if strings.Contains(raw, "*") || strings.Contains(u.Path, "*") || strings.Contains(raw, "${") {
		return "", fmt.Errorf("mcp_gateway_url must be a literal URL without wildcards or variables")
	}
	if strings.ContainsRune(u.Path, '\\') {
		return "", fmt.Errorf("mcp_gateway_url path must not contain ambiguous backslashes")
	}
	if u.Path == "" || path.Clean(u.Path) == "/" {
		return "", fmt.Errorf("mcp_gateway_url requires a non-root endpoint path")
	}
	return raw, nil
}

func containsSensitiveValue(value string) bool {
	return strings.Contains(redactDiagnostic(value), "[REDACTED]")
}

func managedHeadersHelper(opts ManagedSettingsOptions) string {
	helper := strings.TrimSpace(opts.APIKeyHelperCommand)
	if helper != "" {
		if base, ok := strings.CutSuffix(helper, " claude api-key-helper"); ok {
			return base + " mcp headers"
		}
	}
	// Mirror GenerateManagedSettingsTemplate's default-fill so an empty
	// HookCommand still produces a usable headersHelper. Returning "" here
	// would silently omit headersHelper from managed-mcp.json even though
	// the hook command path itself is otherwise valid.
	command := strings.TrimSpace(hookCommandOrDefault(opts.HookCommand))
	if command == "" {
		return ""
	}
	return quoteCommandPath(command) + " mcp headers"
}

func managedSettingsNotes() string {
	return strings.Join([]string{
		"Cordum Edge managed settings template.",
		"Deploy managed-settings.json through Jamf/macOS managed preferences (com.anthropic.claudecode), Intune or Windows policy (HKLM\\SOFTWARE\\Policies\\ClaudeCode), the system file (macOS /Library/Application Support/ClaudeCode, Windows C:\\Program Files\\ClaudeCode, Linux/WSL /etc/claude-code), or server-managed settings.",
		"Deploy managed-mcp.json as a file in that same system directory; it is a standalone file with no managed-preferences domain and cannot be delivered via server-managed settings.",
		"Token tradeoff: dev settings may carry local generated session metadata; enterprise uses agentd memory/keychain/service bootstrap and apiKeyHelper/headersHelper commands.",
		"Do not store long-lived API keys, raw prompts, raw tool payloads, or bearer tokens in Claude settings or managed MCP files.",
		"These files are templates only and do not perform end-to-end deployment automation.",
	}, "\n")
}
