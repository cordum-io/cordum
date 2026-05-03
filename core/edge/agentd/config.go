package agentd

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

type Config struct {
	GatewayURL                string
	APIKey                    string
	TenantID                  string
	PrincipalID               string
	PolicyMode                edgecore.PolicyMode
	BindURL                   string
	SocketPath                string
	LogLevel                  string
	HookTimeout               time.Duration
	GatewayTimeout            time.Duration
	HeartbeatTTL              time.Duration
	HeartbeatInterval         time.Duration
	FailClosed                bool
	SafeAllowCache            SafeAllowCacheConfig
	InlineApprovalWaitEnabled bool
	InlineApprovalWaitTimeout time.Duration
	StateDir                  string
	// TLSCAFile, when non-empty, is forwarded to the Gateway HTTP client so
	// agentd can validate Gateway TLS against a locally-issued CA. Required
	// on Windows when Go's default trust store doesn't include the local CA.
	// Read from CORDUM_TLS_CA env var.
	TLSCAFile string
	// BindSessionID + BindExecutionID, when both non-empty, instruct agentd
	// to skip Gateway CreateSession at startup and bind to an EdgeSession +
	// AgentExecution that an external owner (cordumctl wrapper, integration
	// test) already created. The Gateway records must exist; agentd writes
	// hook events under those IDs. Read from CORDUM_EDGE_SESSION_ID and
	// CORDUM_EDGE_EXECUTION_ID env vars; both are required when binding.
	BindSessionID   string
	BindExecutionID string
}

func LoadConfig(env map[string]string) (Config, error) {
	cfg := Config{
		GatewayURL:        strings.TrimRight(envString(env, "CORDUM_GATEWAY"), "/"),
		APIKey:            strings.TrimSpace(envString(env, "CORDUM_API_KEY")),
		TenantID:          strings.TrimSpace(envString(env, "CORDUM_TENANT_ID")),
		PrincipalID:       strings.TrimSpace(envString(env, "CORDUM_PRINCIPAL_ID")),
		PolicyMode:        edgecore.PolicyModeObserve,
		BindURL:           defaultAgentdBindURL,
		LogLevel:          strings.TrimSpace(envString(env, "CORDUM_AGENTD_LOG_LEVEL")),
		HookTimeout:       defaultHookTimeout,
		GatewayTimeout:    defaultGatewayTimeout,
		HeartbeatTTL:      defaultHeartbeatTTL,
		HeartbeatInterval: defaultHeartbeatTTL / 2,
		FailClosed:        parseBool(envString(env, "CORDUM_AGENTD_FAIL_CLOSED")),
		SafeAllowCache: SafeAllowCacheConfig{
			Enabled:    false,
			TTL:        defaultSafeAllowCacheTTL,
			MaxEntries: defaultSafeAllowCacheMaxEntries,
		},
		InlineApprovalWaitEnabled: false,
		InlineApprovalWaitTimeout: defaultInlineApprovalWaitTimeout,
		StateDir:                  defaultStateDir(),
		TLSCAFile:                 strings.TrimSpace(envString(env, "CORDUM_TLS_CA")),
		BindSessionID:             strings.TrimSpace(envString(env, "CORDUM_EDGE_SESSION_ID")),
		BindExecutionID:           strings.TrimSpace(envString(env, "CORDUM_EDGE_EXECUTION_ID")),
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_EDGE_POLICY_MODE")); raw != "" {
		cfg.PolicyMode = edgecore.PolicyMode(raw)
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_AGENTD_SOCKET")); raw != "" {
		if strings.HasPrefix(strings.ToLower(raw), "http://") || strings.HasPrefix(strings.ToLower(raw), "https://") {
			cfg.BindURL = raw
		} else {
			cfg.SocketPath = raw
			cfg.BindURL = ""
		}
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_AGENTD_HOOK_TIMEOUT")); raw != "" {
		d, err := parseBoundedDuration("CORDUM_AGENTD_HOOK_TIMEOUT", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.HookTimeout = d
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_AGENTD_GATEWAY_TIMEOUT")); raw != "" {
		d, err := parseBoundedDuration("CORDUM_AGENTD_GATEWAY_TIMEOUT", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.GatewayTimeout = d
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_EDGE_HEARTBEAT_TTL")); raw != "" {
		d, err := parseBoundedDuration("CORDUM_EDGE_HEARTBEAT_TTL", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.HeartbeatTTL = d
		cfg.HeartbeatInterval = d / 2
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_EDGE_HEARTBEAT_INTERVAL")); raw != "" {
		d, err := parseBoundedDuration("CORDUM_EDGE_HEARTBEAT_INTERVAL", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.HeartbeatInterval = d
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_AGENTD_STATE_DIR")); raw != "" {
		cfg.StateDir = raw
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_AGENTD_SAFE_ALLOW_CACHE")); raw != "" {
		cfg.SafeAllowCache.Enabled = parseBool(raw)
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_AGENTD_SAFE_ALLOW_CACHE_TTL")); raw != "" {
		d, err := parseBoundedDuration("CORDUM_AGENTD_SAFE_ALLOW_CACHE_TTL", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.SafeAllowCache.TTL = d
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_AGENTD_SAFE_ALLOW_CACHE_MAX_ENTRIES")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("CORDUM_AGENTD_SAFE_ALLOW_CACHE_MAX_ENTRIES invalid integer: %w", err)
		}
		cfg.SafeAllowCache.MaxEntries = n
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_AGENTD_INLINE_APPROVAL_WAIT")); raw != "" {
		cfg.InlineApprovalWaitEnabled = parseBool(raw)
	}
	if raw := strings.TrimSpace(envString(env, "CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT")); raw != "" {
		d, err := parseBoundedDuration("CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.InlineApprovalWaitTimeout = d
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var missing []string
	if strings.TrimSpace(c.GatewayURL) == "" {
		missing = append(missing, "CORDUM_GATEWAY")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		missing = append(missing, "CORDUM_API_KEY")
	}
	if strings.TrimSpace(c.TenantID) == "" {
		missing = append(missing, "CORDUM_TENANT_ID")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required Gateway credentials: %s", strings.Join(missing, ", "))
	}
	switch c.PolicyMode {
	case "", edgecore.PolicyModeObserve, edgecore.PolicyModeEnforce, edgecore.PolicyModeEnterpriseStrict:
	default:
		return fmt.Errorf("invalid CORDUM_EDGE_POLICY_MODE %q", c.PolicyMode)
	}
	hasBindSession := strings.TrimSpace(c.BindSessionID) != ""
	hasBindExecution := strings.TrimSpace(c.BindExecutionID) != ""
	if hasBindSession != hasBindExecution {
		return errors.New("CORDUM_EDGE_SESSION_ID and CORDUM_EDGE_EXECUTION_ID must be set together")
	}
	if strings.TrimSpace(c.SocketPath) != "" {
		return fmt.Errorf("CORDUM_AGENTD_SOCKET socket paths are not supported in this P0 build; use a local http loopback URL such as %s", defaultAgentdBindURL)
	}
	if strings.TrimSpace(c.BindURL) == "" {
		return fmt.Errorf("CORDUM_AGENTD_SOCKET must be a local http loopback URL such as %s; socket paths are not supported in this P0 build", defaultAgentdBindURL)
	}
	if err := validateLocalBindURL(c.BindURL); err != nil {
		return err
	}
	for name, value := range map[string]time.Duration{
		"CORDUM_AGENTD_HOOK_TIMEOUT":     c.HookTimeout,
		"CORDUM_AGENTD_GATEWAY_TIMEOUT":  c.GatewayTimeout,
		"CORDUM_EDGE_HEARTBEAT_TTL":      c.HeartbeatTTL,
		"CORDUM_EDGE_HEARTBEAT_INTERVAL": c.HeartbeatInterval,
	} {
		if value <= 0 || value > maxAgentdDuration {
			return fmt.Errorf("%s must be >0 and <= %s", name, maxAgentdDuration)
		}
	}
	if c.HeartbeatInterval > c.HeartbeatTTL/2 {
		return errors.New("CORDUM_EDGE_HEARTBEAT_INTERVAL must be <= TTL/2")
	}
	if c.SafeAllowCache.Enabled {
		if c.SafeAllowCache.TTL <= 0 || c.SafeAllowCache.TTL > maxAgentdDuration {
			return fmt.Errorf("CORDUM_AGENTD_SAFE_ALLOW_CACHE_TTL must be >0 and <= %s", maxAgentdDuration)
		}
		if c.SafeAllowCache.MaxEntries <= 0 || c.SafeAllowCache.MaxEntries > 10000 {
			return errors.New("CORDUM_AGENTD_SAFE_ALLOW_CACHE_MAX_ENTRIES must be >0 and <= 10000")
		}
	}
	if c.InlineApprovalWaitEnabled {
		if c.InlineApprovalWaitTimeout <= 0 || c.InlineApprovalWaitTimeout > maxAgentdDuration {
			return fmt.Errorf("CORDUM_AGENTD_INLINE_APPROVAL_WAIT_TIMEOUT must be >0 and <= %s", maxAgentdDuration)
		}
	}
	return nil
}

func parseBoundedDuration(name, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s invalid duration: %w", name, err)
	}
	if d <= 0 || d > maxAgentdDuration {
		return 0, fmt.Errorf("%s must be >0 and <= %s", name, maxAgentdDuration)
	}
	return d, nil
}

func validateLocalBindURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid local agentd bind URL: %w", err)
	}
	if u.Scheme != "http" {
		return fmt.Errorf("agentd bind URL must use local http loopback, got %q", u.Scheme)
	}
	if !isLoopbackHost(u.Hostname()) {
		return fmt.Errorf("agentd bind URL must be local-only loopback, got host %q", u.Hostname())
	}
	if strings.TrimSpace(u.Path) == "" {
		return fmt.Errorf("agentd bind URL must include hook path %s", defaultAgentdHookPath)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func envString(env map[string]string, key string) string {
	if env == nil {
		return os.Getenv(key)
	}
	return env[key]
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func defaultStateDir() string {
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".cordum", "edge", "sessions")
	}
	return filepath.Join(os.TempDir(), "cordum", "edge", "sessions")
}
