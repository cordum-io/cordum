package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// configSource records which precedence layer produced a given resolved
// field. Surfaced via `cordumctl edge claude --print-config` so an operator
// can see at a glance whether a value came from a flag, env, YAML file, or
// the built-in default.
type configSource string

const (
	sourceDefault      configSource = "default"
	sourceUserYAML     configSource = "user_yaml"
	sourceProjectYAML  configSource = "project_yaml"
	sourceEnv          configSource = "env"
	sourceAutoDetected configSource = "auto"
	sourceFlag         configSource = "flag"
)

// EdgeClaudeConfig is the resolved configuration for `cordumctl edge claude`.
// The field set is intentionally a subset of the full flag matrix at
// `cmd/cordumctl/edge_claude.go`: only knobs that belong in a checked-in
// config file are exposed here. Per-session details (cwd, git-*, host-id,
// device-id, state-dir, etc.) stay flag/env-only.
type EdgeClaudeConfig struct {
	Gateway             string        `yaml:"gateway,omitempty"`
	APIKey              string        `yaml:"api_key,omitempty"`
	Tenant              string        `yaml:"tenant,omitempty"`
	Principal           string        `yaml:"principal,omitempty"`
	PolicyMode          string        `yaml:"policy_mode,omitempty"`
	CACert              string        `yaml:"cacert,omitempty"`
	DashboardURL        string        `yaml:"dashboard_url,omitempty"`
	AgentdPath          string        `yaml:"agentd_path,omitempty"`
	HookCommand         string        `yaml:"hook_command,omitempty"`
	ApprovalWaitTimeout time.Duration `yaml:"-"`
}

// rawEdgeClaudeYAML mirrors EdgeClaudeConfig but keeps ApprovalWaitTimeout as
// a string so YAML can use the human-readable `30s` / `2m` form. yaml.v3 does
// not parse time.Duration directly.
type rawEdgeClaudeYAML struct {
	Gateway             string `yaml:"gateway,omitempty"`
	APIKey              string `yaml:"api_key,omitempty"`
	Tenant              string `yaml:"tenant,omitempty"`
	Principal           string `yaml:"principal,omitempty"`
	PolicyMode          string `yaml:"policy_mode,omitempty"`
	CACert              string `yaml:"cacert,omitempty"`
	DashboardURL        string `yaml:"dashboard_url,omitempty"`
	AgentdPath          string `yaml:"agentd_path,omitempty"`
	HookCommand         string `yaml:"hook_command,omitempty"`
	ApprovalWaitTimeout string `yaml:"approval_wait_timeout,omitempty"`
}

// LoadEdgeClaudeConfig resolves the config from layered sources:
//
//	built-in default → ~/.cordum/config.yaml → ./cordum.yaml → env vars
//
// Flags layer on top via the caller (see runEdgeClaudeCmd in edge_claude.go),
// which uses the resolved values as flag defaults. The returned source map
// records which layer produced each non-default field, suitable for
// --print-config diagnostics.
func LoadEdgeClaudeConfig(env []string, cwd, homeDir string) (EdgeClaudeConfig, map[string]configSource, error) {
	cfg := defaultEdgeClaudeConfig()
	sources := initialEdgeClaudeSources()

	if homeDir != "" {
		userPath := filepath.Join(homeDir, ".cordum", "config.yaml")
		if err := layerEdgeClaudeYAMLFile(&cfg, sources, userPath, sourceUserYAML); err != nil {
			return EdgeClaudeConfig{}, nil, err
		}
	}
	if cwd != "" {
		projectPath := filepath.Join(cwd, "cordum.yaml")
		if err := layerEdgeClaudeYAMLFile(&cfg, sources, projectPath, sourceProjectYAML); err != nil {
			return EdgeClaudeConfig{}, nil, err
		}
	}
	expanded, err := expandAPIKeyReference(cfg.APIKey, env)
	if err != nil {
		return EdgeClaudeConfig{}, nil, err
	}
	cfg.APIKey = expanded
	layerEdgeClaudeEnv(&cfg, sources, env)
	autoDetectEdgeClaudeCACert(&cfg, sources, cwd)
	return cfg, sources, nil
}

func defaultEdgeClaudeConfig() EdgeClaudeConfig {
	return EdgeClaudeConfig{
		Gateway:             defaultGateway,
		Tenant:              "default",
		PolicyMode:          "enforce",
		HookCommand:         "cordum-hook",
		ApprovalWaitTimeout: 30 * time.Second,
	}
}

func initialEdgeClaudeSources() map[string]configSource {
	return map[string]configSource{
		"gateway":               sourceDefault,
		"api_key":               sourceDefault,
		"tenant":                sourceDefault,
		"principal":             sourceDefault,
		"policy_mode":           sourceDefault,
		"cacert":                sourceDefault,
		"dashboard_url":         sourceDefault,
		"agentd_path":           sourceDefault,
		"hook_command":          sourceDefault,
		"approval_wait_timeout": sourceDefault,
	}
}

func layerEdgeClaudeYAMLFile(cfg *EdgeClaudeConfig, sources map[string]configSource, path string, source configSource) error {
	// #nosec G304 -- path comes from caller-supplied home/cwd, not user input.
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	var raw rawEdgeClaudeYAML
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("parse %s: %w", path, err)
	}
	layer, err := raw.toEdgeClaudeConfig(path)
	if err != nil {
		return err
	}
	applyEdgeClaudeLayer(cfg, sources, layer, source)
	return nil
}

func (r rawEdgeClaudeYAML) toEdgeClaudeConfig(path string) (EdgeClaudeConfig, error) {
	var wait time.Duration
	if s := strings.TrimSpace(r.ApprovalWaitTimeout); s != "" {
		parsed, err := time.ParseDuration(s)
		if err != nil {
			return EdgeClaudeConfig{}, fmt.Errorf("parse %s: approval_wait_timeout: %w", path, err)
		}
		wait = parsed
	}
	return EdgeClaudeConfig{
		Gateway:             strings.TrimSpace(r.Gateway),
		APIKey:              strings.TrimSpace(r.APIKey),
		Tenant:              strings.TrimSpace(r.Tenant),
		Principal:           strings.TrimSpace(r.Principal),
		PolicyMode:          strings.TrimSpace(r.PolicyMode),
		CACert:              strings.TrimSpace(r.CACert),
		DashboardURL:        strings.TrimSpace(r.DashboardURL),
		AgentdPath:          strings.TrimSpace(r.AgentdPath),
		HookCommand:         strings.TrimSpace(r.HookCommand),
		ApprovalWaitTimeout: wait,
	}, nil
}

func applyEdgeClaudeLayer(cfg *EdgeClaudeConfig, sources map[string]configSource, layer EdgeClaudeConfig, source configSource) {
	if layer.Gateway != "" {
		cfg.Gateway = layer.Gateway
		sources["gateway"] = source
	}
	if layer.APIKey != "" {
		cfg.APIKey = layer.APIKey
		sources["api_key"] = source
	}
	if layer.Tenant != "" {
		cfg.Tenant = layer.Tenant
		sources["tenant"] = source
	}
	if layer.Principal != "" {
		cfg.Principal = layer.Principal
		sources["principal"] = source
	}
	if layer.PolicyMode != "" {
		cfg.PolicyMode = layer.PolicyMode
		sources["policy_mode"] = source
	}
	if layer.CACert != "" {
		cfg.CACert = layer.CACert
		sources["cacert"] = source
	}
	if layer.DashboardURL != "" {
		cfg.DashboardURL = layer.DashboardURL
		sources["dashboard_url"] = source
	}
	if layer.AgentdPath != "" {
		cfg.AgentdPath = layer.AgentdPath
		sources["agentd_path"] = source
	}
	if layer.HookCommand != "" {
		cfg.HookCommand = layer.HookCommand
		sources["hook_command"] = source
	}
	if layer.ApprovalWaitTimeout > 0 {
		cfg.ApprovalWaitTimeout = layer.ApprovalWaitTimeout
		sources["approval_wait_timeout"] = source
	}
}

func layerEdgeClaudeEnv(cfg *EdgeClaudeConfig, sources map[string]configSource, env []string) {
	envMap := envMapFromSlice(env)
	setIfEnv := func(field *string, sourceKey string, keys ...string) {
		for _, key := range keys {
			if v := strings.TrimSpace(envMap[key]); v != "" {
				*field = v
				sources[sourceKey] = sourceEnv
				return
			}
		}
	}
	setIfEnv(&cfg.Gateway, "gateway", "CORDUM_GATEWAY")
	setIfEnv(&cfg.APIKey, "api_key", "CORDUM_API_KEY")
	setIfEnv(&cfg.Tenant, "tenant", "CORDUM_TENANT_ID")
	setIfEnv(&cfg.Principal, "principal", "CORDUM_PRINCIPAL_ID", "CORDUM_EDGE_PRINCIPAL_ID")
	setIfEnv(&cfg.PolicyMode, "policy_mode", "CORDUM_EDGE_POLICY_MODE")
	setIfEnv(&cfg.CACert, "cacert", "CORDUM_TLS_CA")
	setIfEnv(&cfg.DashboardURL, "dashboard_url", "CORDUM_EDGE_DASHBOARD_URL", "CORDUM_DASHBOARD_URL")
	setIfEnv(&cfg.AgentdPath, "agentd_path", "CORDUM_AGENTD_PATH")
	setIfEnv(&cfg.HookCommand, "hook_command", "CORDUM_HOOK_COMMAND")
}

func envMapFromSlice(env []string) map[string]string {
	if env == nil {
		env = os.Environ()
	}
	out := make(map[string]string, len(env))
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out[k] = v
		}
	}
	return out
}

var apiKeyEnvRefPattern = regexp.MustCompile(`^\$\{([A-Z_][A-Z0-9_]*)\}$`)

// expandAPIKeyReference enforces the security rail: api_key in a checked-in
// YAML file must be either empty or a `${ENV_VAR_NAME}` reference. Plaintext
// values are rejected so a real key never lands in source control. The error
// never echoes the offending value.
func expandAPIKeyReference(value string, env []string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	match := apiKeyEnvRefPattern.FindStringSubmatch(trimmed)
	if match == nil {
		return "", errors.New("api_key in cordum.yaml/~/.cordum/config.yaml must be empty or a ${ENV_VAR} reference; plaintext rejected to prevent accidental commit of real keys")
	}
	envMap := envMapFromSlice(env)
	return envMap[match[1]], nil
}

func autoDetectEdgeClaudeCACert(cfg *EdgeClaudeConfig, sources map[string]configSource, cwd string) {
	if cfg.CACert != "" {
		return
	}
	if !isLocalhostHTTPSGateway(cfg.Gateway) {
		return
	}
	if cwd == "" {
		return
	}
	candidate := filepath.Join(cwd, "certs", "ca", "ca.crt")
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return
	}
	cfg.CACert = candidate
	sources["cacert"] = sourceAutoDetected
}

func isLocalhostHTTPSGateway(gateway string) bool {
	g := strings.ToLower(strings.TrimSpace(gateway))
	if g == "https://localhost" {
		return true
	}
	return strings.HasPrefix(g, "https://localhost:") || strings.HasPrefix(g, "https://localhost/")
}

// RenderRedactedYAML emits the resolved config as YAML with api_key replaced
// by `<redacted>`. Used by `cordumctl edge claude --print-config` so an
// operator can review what flags + env + yaml resolved to without ever
// surfacing the actual API key.
func (c EdgeClaudeConfig) RenderRedactedYAML() string {
	view := redactedView{
		Gateway:      c.Gateway,
		Tenant:       c.Tenant,
		Principal:    c.Principal,
		PolicyMode:   c.PolicyMode,
		CACert:       c.CACert,
		DashboardURL: c.DashboardURL,
		AgentdPath:   c.AgentdPath,
		HookCommand:  c.HookCommand,
	}
	if strings.TrimSpace(c.APIKey) != "" {
		view.APIKey = "<redacted>"
	}
	if c.ApprovalWaitTimeout > 0 {
		view.ApprovalWaitTimeout = c.ApprovalWaitTimeout.String()
	}
	data, err := yaml.Marshal(view)
	if err != nil {
		return fmt.Sprintf("# render error: %v\n", err)
	}
	return string(data)
}

type redactedView struct {
	Gateway             string `yaml:"gateway,omitempty"`
	APIKey              string `yaml:"api_key,omitempty"`
	Tenant              string `yaml:"tenant,omitempty"`
	Principal           string `yaml:"principal,omitempty"`
	PolicyMode          string `yaml:"policy_mode,omitempty"`
	CACert              string `yaml:"cacert,omitempty"`
	DashboardURL        string `yaml:"dashboard_url,omitempty"`
	AgentdPath          string `yaml:"agentd_path,omitempty"`
	HookCommand         string `yaml:"hook_command,omitempty"`
	ApprovalWaitTimeout string `yaml:"approval_wait_timeout,omitempty"`
}
