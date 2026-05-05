package claude

import (
	"encoding/json"
	"fmt"
	"strings"
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
	DisableBypassPermissions   string                     `json:"disableBypassPermissionsMode"`
	ForceRemoteRefresh         *bool                      `json:"forceRemoteSettingsRefresh,omitempty"`
	APIKeyHelper               string                     `json:"apiKeyHelper,omitempty"`
	Env                        map[string]string          `json:"env,omitempty"`
	Hooks                      map[string][]claudeHookSet `json:"hooks,omitempty"`
}

type managedMCPAllow struct {
	ServerName string `json:"serverName"`
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
	if err := validateManagedSettingsOptions(opts); err != nil {
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
		"ANTHROPIC_BASE_URL":         strings.TrimSpace(opts.LLMProxyBaseURL),
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
		AllowedMcpServers:          []managedMCPAllow{{ServerName: "cordum-edge"}},
		DisableBypassPermissions:   "disable",
		ForceRemoteRefresh:         forceRefresh,
		APIKeyHelper:               strings.TrimSpace(opts.APIKeyHelperCommand),
		Env:                        env,
		Hooks:                      commandHookSettings(hookCommand, timeout, nil),
	}
	settingsJSON, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return ManagedSettingsBundle{}, fmt.Errorf("marshal managed settings: %w", err)
	}
	mcp := managedMCPDocument{MCPServers: map[string]managedMCPServer{
		"cordum-edge": {
			Type:          "http",
			URL:           strings.TrimSpace(opts.MCPGatewayURL),
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

func validateManagedSettingsOptions(opts ManagedSettingsOptions) error {
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
		return fmt.Errorf("hook_command contains sensitive value")
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s required", name)
		}
		if containsSensitiveValue(value) {
			return fmt.Errorf("%s contains sensitive value", name)
		}
	}
	return nil
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
		"Deploy managed-settings.json and managed-mcp.json through Jamf/macOS managed preferences, Intune or Windows policy/Program Files managed settings, Linux/WSL /etc/claude-code, or server-managed settings.",
		"Token tradeoff: dev settings may carry local generated session metadata; enterprise uses agentd memory/keychain/service bootstrap and apiKeyHelper/headersHelper commands.",
		"Do not store long-lived API keys, raw prompts, raw tool payloads, or bearer tokens in Claude settings or managed MCP files.",
		"These files are templates only and do not perform end-to-end deployment automation.",
	}, "\n")
}
